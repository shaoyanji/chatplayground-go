package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/shaoyanji/chatplayground-go/pkg/auth"
)

// DefaultBaseURL is the web app that serves both the model catalog and the
// chat endpoints. Overridable for tests and for relays.
const DefaultBaseURL = "https://app.chatplayground.ai"

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"

// BaseURL returns the ChatPlayground origin to talk to.
func BaseURL() string {
	if v := strings.TrimRight(os.Getenv("CHATPLAYGROUND_BASE_URL"), "/"); v != "" {
		return v
	}
	return DefaultBaseURL
}

// Target is what one chat request needs: the bot id the web app routes on, the
// vendor-qualified model name it records, and the chat endpoint for that vendor.
type Target struct {
	BotID    string
	Model    string
	Endpoint string
}

var (
	catalogMu     sync.Mutex
	catalog       []ModelInfo
	catalogExpiry time.Time
)

// FetchCatalog reads the public model list. It needs no token: the web app
// serves it to anyone, which is why discovery works before login.
func FetchCatalog(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, BaseURL()+"/api/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model catalog: status %d", resp.StatusCode)
	}
	var models []ModelInfo
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		return nil, err
	}
	return models, nil
}

func cachedCatalog(ctx context.Context) []ModelInfo {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	if len(catalog) > 0 && time.Now().Before(catalogExpiry) {
		return catalog
	}
	models, err := FetchCatalog(ctx)
	if err != nil {
		return catalog
	}
	catalog = models
	catalogExpiry = time.Now().Add(10 * time.Minute)
	return catalog
}

// ResolveTarget maps an OpenAI-style model id onto a chat endpoint. Exact ids
// from the catalog win, then a substring match, then the fast default.
func ResolveTarget(ctx context.Context, model string) Target {
	want := strings.ToLower(strings.TrimSpace(model))
	models := cachedCatalog(ctx)

	for _, m := range models {
		if strings.ToLower(m.BotID) == want || strings.ToLower(m.ModelName) == want {
			return targetFor(m)
		}
	}
	if want != "" {
		for _, m := range models {
			if strings.Contains(strings.ToLower(m.BotID), want) || strings.Contains(strings.ToLower(m.ModelName), want) {
				return targetFor(m)
			}
		}
	}

	// Catalog unreachable or id unknown: keep the vendor routing the CLI used.
	botID, vendor, formatted := "gemini-3.8-flash-l", "azure", "google/gemini-3.8-flash"
	switch {
	case strings.Contains(want, "glm"), strings.Contains(want, "kimi"), strings.Contains(want, "deepseek"),
		strings.Contains(want, "qwen"), strings.Contains(want, "grok"), strings.Contains(want, "llama"),
		strings.Contains(want, "sonar"), strings.Contains(want, "command"):
		botID, vendor, formatted = want, "lmsys", want
	case strings.Contains(want, "claude"):
		botID, vendor, formatted = "claude-haiku-5-5", "azure", "anthropic/claude-haiku-5-5"
	case strings.Contains(want, "gpt"):
		botID, vendor, formatted = "gpt-6-luna", "azure", "openai/gpt-6-luna"
	case want != "":
		botID, vendor, formatted = want, "azure", want
	}
	return Target{BotID: botID, Model: formatted, Endpoint: BaseURL() + "/api/chat/" + vendor}
}

func targetFor(m ModelInfo) Target {
	vendor := m.Endpoint
	if vendor == "" {
		vendor = "azure"
	}
	model := m.ModelName
	if model == "" {
		model = m.BotID
	}
	return Target{BotID: m.BotID, Model: model, Endpoint: BaseURL() + "/api/chat/" + vendor}
}

var (
	chatIDExtract = regexp.MustCompile(`CHAT_ID:([a-zA-Z0-9_-]{15,50})`)
	chatIDStrip   = regexp.MustCompile(`CHAT_ID:[a-zA-Z0-9_-]*\n?`)
)

// Chat sends a full conversation and streams the reply text back. Unlike
// StreamQuery it does not own the history: the caller passes the messages it
// wants sent, so an OpenAI-compatible client keeps its own transcript.
func Chat(ctx context.Context, c *Client, model string, messages []Message, onChunk func(string)) (string, error) {
	token := c.Token
	if token == "" {
		var err error
		if token, err = auth.GetValidToken(); err != nil {
			return "", err
		}
	}

	target := ResolveTarget(ctx, model)
	payload := map[string]interface{}{
		"messages":       messages,
		"model":          target.Model,
		"botId":          target.BotID,
		"chatId":         c.ChatID,
		"isRegenerate":   false,
		"promptTemplate": nil,
		"fileUrl":        nil,
		"submissionId":   nil,
		"noSave":         true,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	resp, err := c.post(ctx, target.Endpoint, body, token)
	if err != nil {
		return "", err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		if fresh, rerr := refreshToken(c, token); rerr == nil && fresh != "" {
			c.Token = fresh
			if resp, err = c.post(ctx, target.Endpoint, body, fresh); err != nil {
				return "", err
			}
		} else {
			return "", fmt.Errorf("authentication expired (401); run 'chatplayground-go login'")
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("chatplayground error %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	var text strings.Builder
	reader := newChunkReader(resp.Body)
	for {
		chunk, err := reader.next()
		if chunk != "" {
			if m := chatIDExtract.FindStringSubmatch(chunk); len(m) > 1 {
				c.ChatID = m[1]
			}
			chunk = chatIDStrip.ReplaceAllString(chunk, "")
			if chunk != "" {
				text.WriteString(chunk)
				if onChunk != nil {
					onChunk(chunk)
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return text.String(), err
		}
	}
	return text.String(), nil
}

func (c *Client) post(ctx context.Context, url string, body []byte, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Cookie", "__session="+token)
	req.Header.Set("Origin", "https://web.chatplayground.ai")
	req.Header.Set("Referer", "https://web.chatplayground.ai/")
	req.Header.Set("User-Agent", userAgent)
	return c.HTTPClient.Do(req)
}

// refreshToken uses the pure-REST Clerk refresh. The browser helper is a Node
// script with a hardcoded Windows path, so the server never falls back to it:
// on a headless host it cannot work, and a missing runtime must not be the
// difference between a clear 401 and a hang.
func refreshToken(c *Client, current string) (string, error) {
	data := &auth.AuthData{Token: current, SessionCookie: current}
	if stored, err := auth.ReadAuth(); err == nil {
		data = stored
	}
	return auth.RefreshClerkTokenREST(data)
}

type chunkReader struct {
	r    io.Reader
	buf  []byte
	tail string
}

func newChunkReader(r io.Reader) *chunkReader { return &chunkReader{r: r, buf: make([]byte, 4096)} }

// next returns one chunk of text. A read can split a multi-byte rune, so an
// incomplete trailing rune is held back and prepended to the next read instead
// of being emitted as U+FFFD into an agent's transcript.
func (cr *chunkReader) next() (string, error) {
	n, err := cr.r.Read(cr.buf)
	if n > 0 {
		chunk := cr.tail + string(cr.buf[:n])
		cr.tail = ""
		if !utf8.ValidString(chunk) {
			for i := len(chunk) - 1; i >= 0 && i >= len(chunk)-utf8.UTFMax; i-- {
				if utf8.RuneStart(chunk[i]) {
					if !utf8.FullRuneInString(chunk[i:]) {
						cr.tail = chunk[i:]
						chunk = chunk[:i]
					}
					break
				}
			}
		}
		if err == io.EOF && cr.tail != "" {
			// End of stream: nothing will complete the rune, so send it as-is.
			chunk += cr.tail
			cr.tail = ""
		}
		return chunk, err
	}
	return cr.tail, err
}
