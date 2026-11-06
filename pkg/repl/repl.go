// Package repl is the self-contained chat client: one process, one binary, no
// server and no helper daemon. It talks to the chatplayground chat endpoints
// directly, keeps the thread keyed by chatId on the server side, and mirrors
// the transcript to ~/.chatplayground/sessions/<name>.json so a conversation
// survives a restart.
package repl

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/shaoyanji/chatplayground-go/pkg/api"
	"github.com/shaoyanji/chatplayground-go/pkg/auth"
)

// DefaultModel is the fast free-tier model the CLI has always defaulted to.
const DefaultModel = "gemini-3.8-flash-l"

// Options is what the caller decided on the command line.
type Options struct {
	Model   string // empty means: the session's model, else DefaultModel
	Session string // session file name; empty means "default"
	Prompt  string // non-empty means one-shot: send, print, save, exit
}

// Session is the on-disk state: the server thread id plus the local transcript.
// The endpoint keeps the conversation itself — only the new prompt is ever sent
// (docs: "Send only the new prompt") — so ChatID is what makes turns 2..n a
// conversation rather than n unrelated questions.
type Session struct {
	Name     string        `json:"name"`
	ChatID   string        `json:"chatId"`
	Model    string        `json:"model"`
	Messages []api.Message `json:"messages"`
	Updated  time.Time     `json:"updatedAt"`
}

func sessionsDir() string { return filepath.Join(auth.GetConfigDir(), "sessions") }

// Trimmed, not rejected: a session name comes from a shell argument and ends up
// in a path.
func SessionPath(name string) string {
	clean := regexp.MustCompile(`[^a-zA-Z0-9._-]+`).ReplaceAllString(strings.TrimSpace(name), "-")
	clean = strings.Trim(clean, "-.")
	if clean == "" {
		clean = "default"
	}
	return filepath.Join(sessionsDir(), clean+".json")
}

// LoadSession returns a session with defaults filled in, so the caller never
// has to distinguish "new file" from "old file missing a field".
func LoadSession(name string) *Session {
	if name == "" {
		name = "default"
	}
	s := &Session{Name: name}
	if data, err := os.ReadFile(SessionPath(name)); err == nil {
		_ = json.Unmarshal(data, s)
		s.Name = name
	}
	if s.Model == "" {
		s.Model = DefaultModel
	}
	return s
}

// Save writes the transcript 0600: it is whatever the user typed at a model.
func (s *Session) Save() error {
	if err := os.MkdirAll(sessionsDir(), 0o700); err != nil {
		return err
	}
	s.Updated = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(SessionPath(s.Name), data, 0o600)
}

// staleThread is the endpoint's answer when a chatId is unknown to it — a
// session file restored after the server forgot the thread, most often.
func staleThread(err error) bool {
	return err != nil && strings.Contains(err.Error(), "Conversation history is not accepted")
}

func Start(opts Options) error {
	if opts.Session == "" {
		opts.Session = "default"
	}
	sess := LoadSession(opts.Session)
	if opts.Model != "" {
		sess.Model = opts.Model
	}

	client := api.NewClient()
	client.ChatID = sess.ChatID
	client.Messages = sess.Messages

	if opts.Prompt != "" {
		return turn(client, sess, opts.Prompt, os.Stdout, true)
	}

	fmt.Println("\033[1;36m=== chatplayground — self-contained chat ===\033[0m")
	fmt.Printf("🤖 model: \033[1;32m%s\033[0m   🗂  session: \033[35m%s\033[0m\n", sess.Model, sess.Name)
	if len(client.Messages) > 0 {
		fmt.Printf("↩️  resumed: %d stored turns, thread %s\n", len(client.Messages)/2, shortID(client.ChatID))
	}
	fmt.Print("💡 \033[35m/help\033[0m for commands, \033[35m/new\033[0m to start over, \033[35m/exit\033[0m to quit\n\n")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 15_000)

	for {
		fmt.Printf("\033[1;32mcpg\033[0m \033[2m[%s]\033[0m> ", shortModel(sess.Model))
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if !strings.HasPrefix(line, "/") {
			_ = turn(client, sess, line, os.Stdout, false)
			continue
		}

		parts := strings.Fields(line)
		switch cmd := strings.ToLower(parts[0]); cmd {
		case "/exit", "/quit", "/q":
			fmt.Println("bye")
			return nil

		case "/new", "/clear", "/reset":
			client.ResetSession()
			sess.ChatID, sess.Messages = "", nil
			if err := sess.Save(); err != nil {
				fmt.Printf("\033[1;31mcould not save session: %v\033[0m\n", err)
			}
			fmt.Print("\033[1;33mnew thread\033[0m\n\n")

		case "/status":
			fmt.Printf("\033[1;32m● session\033[0m  %s\n", sess.Name)
			fmt.Printf("model    %s\nthread   %s\nstored   %d messages\nfile     %s\n\n",
				sess.Model, orDash(sess.ChatID), len(sess.Messages), SessionPath(sess.Name))

		case "/model":
			if len(parts) > 1 {
				sess.Model = parts[1]
				fmt.Printf("\033[1;32mmodel → %s\033[0m\n\n", sess.Model)
			} else {
				fmt.Printf("current model: %s\n\n", sess.Model)
			}

		case "/models":
			models, err := client.GetModels()
			if err != nil {
				fmt.Printf("\033[1;31m%v\033[0m\n\n", err)
				continue
			}
			fmt.Printf("\n%d models:\n", len(models))
			for _, m := range models {
				fmt.Printf("  \033[33m%-26s\033[0m %-28s [%s]\n", m.BotID, m.DisplayName, m.Provider)
			}
			fmt.Println()

		case "/help":
			fmt.Print(`
  /new              start a fresh thread (forget server context)
  /model <id>       switch model (keeps the thread)
  /models           list model ids
  /status           session file, thread id, stored turns
  /exit             quit

Everything else is sent to the model. Context lives server side under the
thread id; the transcript here is a local mirror (0600) for resume and status.
`)
		default:
			fmt.Printf("unknown command %s — /help\n\n", cmd)
		}
	}
	return nil
}

// turn sends one prompt and prints the reply. A thread the server no longer
// knows is retried once on a fresh chatId: losing context beats a dead session.
func turn(client *api.Client, sess *Session, prompt string, out io.Writer, oneShot bool) error {
	reply, err := send(client, sess, prompt, out)
	if staleThread(err) {
		fmt.Print("\n\033[1;33m↻ server forgot this thread; starting a new one\033[0m\n\n")
		client.ResetSession()
		reply, err = send(client, sess, prompt, out)
	}
	if oneShot {
		fmt.Fprintln(out)
	}
	if err != nil {
		fmt.Printf("\n\033[1;31merror: %v\033[0m\n\n", err)
		return err
	}
	sess.ChatID, sess.Messages = client.ChatID, client.Messages
	if err := sess.Save(); err != nil {
		fmt.Printf("\n\033[1;31mcould not save session: %v\033[0m\n", err)
	}
	if !oneShot {
		fmt.Print("\n\n")
	}
	_ = reply
	return nil
}

func send(client *api.Client, sess *Session, prompt string, out io.Writer) (string, error) {
	fmt.Fprintf(out, "\n\033[1;36m%s>\033[0m ", shortModel(sess.Model))
	return client.StreamQuery(sess.Model, prompt, "", func(chunk string) {
		fmt.Fprint(out, chunk)
	})
}

func shortModel(m string) string { return m }

func shortID(id string) string {
	if id == "" {
		return "-"
	}
	if len(id) > 12 {
		return id[:12] + "…"
	}
	return id
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
