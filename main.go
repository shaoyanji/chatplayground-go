package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/shaoyanji/chatplayground-go/pkg/api"
	"github.com/shaoyanji/chatplayground-go/pkg/auth"
	"github.com/shaoyanji/chatplayground-go/pkg/benchmark"
	"github.com/shaoyanji/chatplayground-go/pkg/repl"
	"github.com/shaoyanji/chatplayground-go/pkg/server"
	"github.com/shaoyanji/chatplayground-go/pkg/tui"
)

const Version = "1.2.1"

// stdoutIsTTY decides whether the decorations are for a human. A pipe gets the
// answer and nothing else: `cpg chat ... | jev-decide classify --text-file -`
// must not see a prompt glyph in the text it classifies.
func stdoutIsTTY() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// emitJSON is the machine surface: one document on stdout, nothing decorative.
func emitJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func printHelp() {
	fmt.Printf(`
ChatPlayground Go (v%s) - High-Performance Pure-Go Client

FEATURES:
  ⚡ <5ms Cold Start (zero-overhead Go binary)
  🔄 Pure-REST & Background Token Refresh (seamless auto-renewal)
  🏁 Parallel Multi-Model Benchmarking (Goroutines & Channels)
  🎨 GPT Image 2 Generation
  🖥️  Bubble Tea Interactive TUI with Glamour Markdown Rendering

USAGE:
  chatplayground-go [options] [prompt]
  chatplayground-go <command> [options]

COMMANDS:
  chat, repl [-m id] [-s name] [prompt]
                                Self-contained streaming chat: no server, no
                                daemon; the thread is kept server side and the
                                transcript in ~/.chatplayground/sessions/
  tui                           Launch interactive Bubble Tea terminal UI
  login [token]                 Log in via stealth browser or save session token
  models, list [--json]         List all available models on ChatPlayground
                                (--json adds entitlement fields: premium_only …)
  status [--json]               Auth state, user and credential age
  compare, benchmark <prompt>   Run parallel benchmark across Claude Sonnet 5, DeepSeek R1 & Gemini Flash
  image <prompt>                Generate image with GPT Image 2
  tui                           Launch interactive Bubble Tea terminal UI
  status                        Check authentication credentials and active user
  refresh                       Perform token renewal
  serve [--addr host:port]      Serve an OpenAI-compatible API at /v1 (--tools-emulate for tool calls)

OPTIONS:
  -m, --model <id>              Target model (default: gemini-3.8-flash-l)
  -i, --image <path/url>        Input image for multimodal vision
  -s, --size <size>             Image generation size: 1024x1024, 1536x1024, 1024x1536
  -o, --output <file>           Output file for image generation
  -h, --help                    Show help
  -v, --version                 Show version

EXAMPLES:
  # 1. Instant cold-start query
  chatplayground-go "Explain Raft consensus"

  # 2. Pipeline from Git
  git diff | chatplayground-go "Review this diff for bugs"

  # 3. Parallel multi-model comparison
  chatplayground-go compare "Explain the difference between B-Trees and LSM-Trees"

  # 4. Generate image
  chatplayground-go image --size 1536x1024 -o landscape.png "A peaceful cyberpunk tea shop in Tokyo"

  # 5. Machine-readable catalog and image result
  chatplayground-go models --json | jq '[.[] | select(.premium_only == false) | .id]'
  chatplayground-go image --json -o out.png "a red panda librarian"

  # 5. Interactive Bubble Tea TUI
  chatplayground-go tui
`, Version)
}

func readPipedStdin() string {
	stat, err := os.Stdin.Stat()
	if err != nil || (stat.Mode()&os.ModeCharDevice) != 0 || (stat.Mode()&os.ModeNamedPipe) == 0 {
		return ""
	}
	bytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(bytes))
}

func main() {
	var (
		modelFlag   string
		imageFlag   string
		sizeFlag    string
		outputFlag  string
		helpFlag    bool
		versionFlag bool
	)

	flag.StringVar(&modelFlag, "m", "gemini-3.8-flash-l", "Model ID")
	flag.StringVar(&modelFlag, "model", "gemini-3.8-flash-l", "Model ID")
	flag.StringVar(&imageFlag, "i", "", "Input image")
	flag.StringVar(&imageFlag, "image", "", "Input image")
	flag.StringVar(&sizeFlag, "s", "1024x1024", "Image size")
	flag.StringVar(&sizeFlag, "size", "1024x1024", "Image size")
	flag.StringVar(&outputFlag, "o", "", "Output file")
	flag.StringVar(&outputFlag, "output", "", "Output file")
	flag.BoolVar(&helpFlag, "h", false, "Help")
	flag.BoolVar(&helpFlag, "help", false, "Help")
	flag.BoolVar(&versionFlag, "v", false, "Version")
	flag.BoolVar(&versionFlag, "version", false, "Version")

	flag.Parse()

	if helpFlag {
		printHelp()
		return
	}
	if versionFlag {
		fmt.Printf("chatplayground-go v%s\n", Version)
		return
	}

	args := flag.Args()
	firstArg := ""
	if len(args) > 0 {
		firstArg = args[0]
	}

	// Command: login
	if firstArg == "login" {
		if len(args) > 1 {
			tokenArg := args[1]
			if err := auth.SaveTokenDirectly(tokenArg); err != nil {
				fmt.Printf("\033[1;31mError saving token: %v\033[0m\n", err)
			} else {
				fmt.Println("\033[1;32m✅ Token saved successfully!\033[0m")
			}
			return
		}
		// Interactive stealth login
		fmt.Println("\033[1;36mLaunching browser login via stealth runner...\033[0m")
		cmd := exec.Command("chatplayground", "login")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err != nil {
			fmt.Printf("Login failed or cancelled: %v\n", err)
		}
		return
	}

	// Command: status
	if firstArg == "status" {
		fs := flag.NewFlagSet("status", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "Emit one JSON object instead of text")
		_ = fs.Parse(args[1:])

		authData, err := auth.ReadAuth()
		loggedIn := err == nil && authData.Token != ""

		if *asJSON {
			out := map[string]any{
				"authenticated": loggedIn,
				"config_file":   auth.GetAuthFilePath(),
			}
			if loggedIn {
				if id, ok := authData.User["id"].(string); ok {
					out["user_id"] = id
				}
				if email, ok := authData.User["email"].(string); ok {
					out["email"] = email
				}
				out["session_id"] = authData.SessionID
				out["has_client_cookie"] = authData.ClientCookie != ""
				out["credential_age_seconds"] = (time.Now().UnixMilli() - authData.UpdatedAt) / 1000
			}
			emitJSON(out)
			return
		}

		if !loggedIn {
			fmt.Println("\033[1;33mNot logged in.\033[0m Run 'chatplayground-go login' first.")
			return
		}
		fmt.Println("\033[1;32m● Authenticated with ChatPlayground (Go Client)\033[0m")
		if authData.User != nil {
			if email, ok := authData.User["email"]; ok {
				fmt.Printf("User: %v\n", email)
			}
		}
		if authData.SessionID != "" {
			fmt.Printf("Session ID: %s\n", authData.SessionID)
		}
		fmt.Printf("Config file: %s\n", auth.GetAuthFilePath())
		return
	}

	// Command: refresh
	//
	// This is the keepalive a timer runs, so it reports in the machine surface:
	// `--json` plus a non-zero exit on failure. It never prints the token or a
	// prefix of it — the journal is not a credential store.
	if firstArg == "refresh" {
		fs := flag.NewFlagSet("refresh", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "Emit one JSON object instead of text")
		_ = fs.Parse(args[1:])

		fail := func(err error) {
			if *asJSON {
				emitJSON(map[string]any{"refreshed": false, "error": err.Error()})
			} else {
				fmt.Printf("\033[1;31mToken refresh failed: %v\033[0m\n", err)
			}
			os.Exit(1)
		}

		authData, err := auth.ReadAuth()
		if err != nil {
			fail(fmt.Errorf("read %s: %w", auth.GetAuthFilePath(), err))
		}
		newToken, err := auth.RefreshClerkTokenREST(authData)
		if err != nil {
			// The background helper needs a Windows browser install; it is a
			// last resort, not the second try on this fleet.
			newToken, err = auth.RefreshViaBackgroundHelper()
			if err != nil {
				fail(err)
			}
		}
		if *asJSON {
			emitJSON(map[string]any{
				"refreshed":       true,
				"token_present":   newToken != "",
				"config_file":     auth.GetAuthFilePath(),
				"session_id":      authData.SessionID,
				"refreshed_at_ms": time.Now().UnixMilli(),
			})
			return
		}
		fmt.Println("\033[1;32m✅ Successfully refreshed Clerk token!\033[0m")
		return
	}

	// Command: models / list
	if firstArg == "models" || firstArg == "list" {
		fs := flag.NewFlagSet("models", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "Emit the catalog as JSON, entitlement fields included")
		_ = fs.Parse(args[1:])
		client := api.NewClient()
		models, err := client.GetModels()
		if err != nil {
			fmt.Printf("\033[1;31mError fetching models: %v\033[0m\n", err)
			return
		}
		if *asJSON {
			out := make([]map[string]any, 0, len(models))
			for _, m := range models {
				out = append(out, map[string]any{
					"id":            m.BotID,
					"display_name":  m.DisplayName,
					"provider":      m.Provider,
					"group":         m.Group,
					"endpoint":      m.Endpoint,
					"vision":        m.SupportImage,
					"active":        m.Active,
					"premium_only":  m.PremiumOnly,
					"lifetime_only": m.LifetimeOnly,
					"tier":          m.Tier,
					"credit_weight": m.CreditWeight,
					"default":       m.BotID == repl.DefaultModel,
				})
			}
			emitJSON(out)
			return
		}
		fmt.Printf("\033[1mAvailable models on ChatPlayground (%d total):\033[0m\n\n", len(models))
		for _, m := range models {
			vision := ""
			if m.SupportImage {
				vision = " \033[36m[Vision]\033[0m"
			}
			imgGen := ""
			if m.Group == "image" {
				imgGen = " \033[35m[Image Generator]\033[0m"
			}
			mark := ""
			if m.BotID == "gemini-3.8-flash-l" {
				mark = " \033[32m(default)\033[0m"
			}
			if m.PremiumOnly {
				mark += " \033[31m[premium: 403]\033[0m"
			}
			fmt.Printf("  \033[33m%-28s\033[0m %-30s [%s]%s%s%s\n", m.BotID, m.DisplayName, m.Provider, vision, imgGen, mark)
		}
		fmt.Println("\nChat:  chatplayground-go -m <botId> \"prompt\"")
		fmt.Println("Image: chatplayground-go image \"prompt\"")
		return
	}

	// Command: chat / repl
	//
	// Its own flag set, because flag.Parse stops at the first non-flag argument:
	// `chat -m gpt-6-luna` used to keep the default model and silently ignore the
	// flag. Same for --session.
	if firstArg == "chat" || firstArg == "repl" {
		chatModel, chatSession := "", "default"
		fs := flag.NewFlagSet("chat", flag.ContinueOnError)
		fs.StringVar(&chatModel, "m", "", "Model id")
		fs.StringVar(&chatModel, "model", "", "Model id")
		fs.StringVar(&chatSession, "s", "default", "Session name (resumes if it exists)")
		fs.StringVar(&chatSession, "session", "default", "Session name (resumes if it exists)")
		if err := fs.Parse(args[1:]); err != nil {
			fmt.Printf("Chat error: %v\n", err)
			return
		}
		opts := repl.Options{
			Model:   chatModel,
			Session: chatSession,
			Prompt:  strings.TrimSpace(strings.Join(fs.Args(), " ")),
			Color:   stdoutIsTTY(),
		}
		if err := repl.Start(opts); err != nil {
			fmt.Printf("Chat error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Command: tui
	if firstArg == "tui" {
		if err := tui.Start(); err != nil {
			fmt.Printf("TUI error: %v\n", err)
		}
		return
	}

	// Command: serve
	if firstArg == "serve" {
		addr := "127.0.0.1:8787"
		emulateTools := false
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		fs.StringVar(&addr, "addr", addr, "Address to listen on")
		fs.BoolVar(&emulateTools, "tools-emulate", false, "Convert fenced tool_call replies into OpenAI tool_calls")
		_ = fs.Parse(args[1:])
		if err := server.Start(server.Config{Addr: addr, ToolsEmulate: emulateTools}); err != nil {
			fmt.Printf("serve error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Command: compare / benchmark
	if firstArg == "compare" || firstArg == "benchmark" {
		prompt := strings.Join(args[1:], " ")
		if prompt == "" {
			prompt = readPipedStdin()
		}
		if prompt == "" {
			fmt.Println("Usage: chatplayground-go compare <prompt>")
			return
		}
		benchmark.RunComparison(prompt, nil)
		return
	}

	// Command: image
	//
	// Its own flag set for the same reason `chat` has one: the global parse
	// stops at the subcommand, so `image --size 1536x1024 -o out.png` used to
	// send the flags to the model as the prompt.
	if firstArg == "image" {
		imgSize, imgOut, imgModel, asJSON := sizeFlag, outputFlag, "", false
		fs := flag.NewFlagSet("image", flag.ContinueOnError)
		fs.StringVar(&imgSize, "s", imgSize, "Image size: 1024x1024, 1536x1024, 1024x1536")
		fs.StringVar(&imgSize, "size", imgSize, "Image size")
		fs.StringVar(&imgModel, "m", imgModel, "Image model (default: the free one in the catalog)")
		fs.StringVar(&imgModel, "model", imgModel, "Image model")
		fs.StringVar(&imgOut, "o", imgOut, "Write the PNG here")
		fs.StringVar(&imgOut, "output", imgOut, "Write the PNG here")
		fs.BoolVar(&asJSON, "json", false, "Emit url, path, size and prompt as JSON")
		if err := fs.Parse(args[1:]); err != nil {
			fmt.Printf("Image error: %v\n", err)
			return
		}
		prompt := strings.Join(fs.Args(), " ")
		if prompt == "" {
			prompt = readPipedStdin()
		}
		if prompt == "" {
			fmt.Println("Usage: chatplayground-go image [--size 1024x1024] [-o file.png] [--json] <prompt>")
			return
		}

		client := api.NewClient()
		if !asJSON {
			fmt.Printf("🎨 Generating image with GPT Image 2...\n")
		}
		res, err := client.GenerateImage(prompt, imgModel, imgSize, imgOut)
		if err != nil {
			if asJSON {
				emitJSON(map[string]any{"error": err.Error()})
			} else {
				fmt.Printf("\033[1;31mImage generation error: %v\033[0m\n", err)
			}
			os.Exit(1)
		}
		if asJSON {
			emitJSON(res)
			return
		}
		fmt.Printf("\033[1;32m✅ Image generated with %s!\033[0m\n", res.Model)
		fmt.Printf("🖼️ URL: %s\n", res.URL)
		if res.Path != "" {
			fmt.Printf("💾 Saved to: %s\n", res.Path)
		}
		return
	}

	// Standard query
	prompt := strings.Join(args, " ")
	if prompt == "" {
		prompt = readPipedStdin()
	}

	if prompt == "" {
		// Launch TUI if interactive terminal
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) != 0 {
			if err := tui.Start(); err != nil {
				printHelp()
			}
			return
		}
		printHelp()
		return
	}

	client := api.NewClient()
	_, err := client.StreamQuery(modelFlag, prompt, imageFlag, func(chunk string) {
		fmt.Print(chunk)
	})
	if err != nil {
		fmt.Printf("\n\033[1;31mError: %v\033[0m\n", err)
		os.Exit(1)
	}
	fmt.Println()
}
