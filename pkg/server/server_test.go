package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// stubChatPlayground stands in for the web app: a public catalog and a chat
// endpoint that streams raw text with the CHAT_ID marker the real one emits.
func stubChatPlayground(t *testing.T, reply func(w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `[{"botId":"test-bot","modelName":"vendor/test","displayName":"Test","provider":"test","endpoint":"azure","group":"chat"}]`)
	})
	mux.HandleFunc("/api/chat/azure", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		reply(w)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("CHATPLAYGROUND_BASE_URL", srv.URL)
	t.Setenv("CHATPLAYGROUND_TOKEN", "stub-token")
	// Isolate HOME: without a session on disk the client reads the real
	// ~/.chatplayground/auth.json, which would make these tests machine-dependent.
	t.Setenv("HOME", t.TempDir())
	return srv
}

func post(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestModels(t *testing.T) {
	stubChatPlayground(t, func(http.ResponseWriter) {})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	Handler(Config{}).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"test-bot"`) {
		t.Fatalf("catalog not passed through: %s", rec.Body)
	}
}

func TestChatNonStream(t *testing.T) {
	stubChatPlayground(t, func(w http.ResponseWriter) {
		io.WriteString(w, "CHAT_ID:abcdefghijklmnopqrst\nHello there.")
	})
	rec := post(t, Handler(Config{}), `{"model":"test-bot","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content *string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad json: %v (%s)", err, rec.Body)
	}
	if got := *resp.Choices[0].Message.Content; got != "Hello there." {
		t.Fatalf("content %q", got)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish %q", resp.Choices[0].FinishReason)
	}
	if strings.Contains(rec.Body.String(), "CHAT_ID:") {
		t.Fatal("CHAT_ID leaked into the reply")
	}
}

func TestChatStream(t *testing.T) {
	stubChatPlayground(t, func(w http.ResponseWriter) {
		io.WriteString(w, "first ")
		w.(http.Flusher).Flush()
		io.WriteString(w, "second")
	})
	rec := post(t, Handler(Config{}), `{"model":"test-bot","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	body := rec.Body.String()
	if !strings.Contains(body, "first ") || !strings.Contains(body, "second") {
		t.Fatalf("missing content chunks: %s", body)
	}
	if !strings.Contains(body, "chat.completion.chunk") || !strings.Contains(body, "[DONE]") {
		t.Fatalf("not an openai stream: %s", body)
	}
}

const toolReply = "```tool_call\n{\"name\":\"read_file\",\"arguments\":{\"path\":\"go.mod\"}}\n```"

func toolRequest() string {
	return `{"model":"test-bot","messages":[{"role":"user","content":"read go.mod"}],
	  "tools":[{"type":"function","function":{"name":"read_file","description":"Read a file","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}}]}`
}

func TestToolsEmulated(t *testing.T) {
	stubChatPlayground(t, func(w http.ResponseWriter) { io.WriteString(w, toolReply) })
	rec := post(t, Handler(Config{ToolsEmulate: true}), toolRequest())
	body := rec.Body.String()
	if !strings.Contains(body, `"tool_calls"`) || !strings.Contains(body, `"read_file"`) {
		t.Fatalf("tool call not surfaced: %s", body)
	}
	if !strings.Contains(body, `"finish_reason":"tool_calls"`) {
		t.Fatalf("wrong finish reason: %s", body)
	}
	if strings.Contains(body, "```") {
		t.Fatalf("fence leaked into the reply: %s", body)
	}
}

func TestToolsEmulatedStream(t *testing.T) {
	stubChatPlayground(t, func(w http.ResponseWriter) { io.WriteString(w, toolReply) })
	rec := post(t, Handler(Config{ToolsEmulate: true}), strings.Replace(toolRequest(), `"model":"test-bot"`, `"model":"test-bot","stream":true`, 1))
	body := rec.Body.String()
	if !strings.Contains(body, `"tool_calls"`) || !strings.Contains(body, "[DONE]") {
		t.Fatalf("streamed tool call missing: %s", body)
	}
}

func TestToolsPassThroughWhenDisabled(t *testing.T) {
	stubChatPlayground(t, func(w http.ResponseWriter) { io.WriteString(w, toolReply) })
	rec := post(t, Handler(Config{}), toolRequest())
	if !strings.Contains(rec.Body.String(), "```tool_call") {
		t.Fatalf("expected the raw reply untouched: %s", rec.Body.String())
	}
}

func TestUnauthorizedIsReported(t *testing.T) {
	os.Unsetenv("CHATPLAYGROUND_TOKEN")
	stubChatPlayground(t, func(http.ResponseWriter) {})
	os.Unsetenv("CHATPLAYGROUND_TOKEN")
	rec := post(t, Handler(Config{}), `{"model":"test-bot","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code == http.StatusOK {
		t.Fatalf("expected a failure, got %s", rec.Body)
	}
}
