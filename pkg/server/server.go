// Package server exposes ChatPlayground's chat models as an OpenAI-compatible
// HTTP API, so an agentic harness can use the account through a base URL
// instead of a CLI.
//
// The upstream endpoint streams plain text, not SSE, and knows nothing about
// tools. Text passes through untouched. Tools are emulated: the schemas go into
// a system message, the model answers with a fenced tool_call block, and that
// block is converted into a real tool_calls field before the client sees it.
// Emulation is opt-in because it is a prompt contract, not a guarantee.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/shaoyanji/chatplayground-go/pkg/api"
)

type Config struct {
	Addr         string
	ToolsEmulate bool
}

// Start runs the server until the process exits.
func Start(cfg Config) error {
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           Handler(cfg),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("chatplayground openai api on http://%s/v1 (tools emulation: %v)", cfg.Addr, cfg.ToolsEmulate)
	return srv.ListenAndServe()
}

// Handler builds the mux, exported so tests can drive it without a listener.
func Handler(cfg Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", handleModels)
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		handleChat(cfg, w, r)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	})
	return mux
}

func handleModels(w http.ResponseWriter, r *http.Request) {
	models, err := api.FetchCatalog(r.Context())
	if err != nil {
		http.Error(w, "model catalog unavailable: "+err.Error(), http.StatusBadGateway)
		return
	}
	type model struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}
	out := struct {
		Object string  `json:"object"`
		Data   []model `json:"data"`
	}{Object: "list"}
	for _, m := range models {
		if m.Group != "" && m.Group != "chat" {
			continue
		}
		out.Data = append(out.Data, model{ID: m.BotID, Object: "model", OwnedBy: m.Provider})
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- request ----

type completionRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Tools    []tool        `json:"tools"`
}

type chatMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id"`
	Name       string          `json:"name"`
}

type tool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

// text flattens OpenAI content: a bare string, or an array of content parts.
func (m chatMessage) text() string {
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(m.Content, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Text != "" {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return ""
}

func toUpstream(req completionRequest, emulate bool) []api.Message {
	// The chat endpoint rejects a message list that carries history: it wants
	// "only the new prompt", and history that it cannot tie to a chatId. Since
	// an OpenAI client resends the whole conversation every turn, the transcript
	// is flattened into one prompt with role labels.
	var parts []string
	for _, m := range req.Messages {
		switch m.Role {
		case "system", "developer":
			parts = append(parts, "SYSTEM:\n"+m.text())
		case "tool", "function":
			name := m.Name
			if name == "" {
				name = "tool"
			}
			parts = append(parts, fmt.Sprintf("TOOL RESULT (%s):\n%s", name, m.text()))
		case "assistant":
			parts = append(parts, "ASSISTANT:\n"+m.text())
		default:
			parts = append(parts, "USER:\n"+m.text())
		}
	}
	if emulate && len(req.Tools) > 0 {
		schemas, _ := json.Marshal(req.Tools)
		parts = append([]string{toolProtocol + "\n\n" + string(schemas)}, parts...)
	}

	// A lone user message goes through verbatim: wrapping it would change what
	// the model sees for the most common request shape.
	if len(req.Messages) == 1 && len(parts) == 1 && req.Messages[0].Role == "user" {
		return []api.Message{{Role: "user", Content: req.Messages[0].text()}}
	}
	return []api.Message{{Role: "user", Content: strings.Join(parts, "\n\n")}}
}

const toolProtocol = `You can call tools.

To call a tool, reply with exactly one fenced block and nothing else:

` + "```tool_call" + `
{"name": "<tool name>", "arguments": {<arguments>}}
` + "```" + `

Use only the tools listed below and satisfy each one's JSON schema. If no tool is
needed, answer normally and do not emit a block.`

// ---- tool call parsing ----

type parsedCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// extractToolCall pulls a tool call out of a model reply. It accepts the fenced
// form the protocol asks for, a fence holding a list of calls, and a bare JSON
// object as a last resort, because models drift.
func extractToolCall(text string) (parsedCall, string, bool) {
	for _, fence := range []string{"```tool_call", "```json", "```"} {
		idx := strings.Index(text, fence)
		if idx < 0 {
			continue
		}
		rest := text[idx+len(fence):]
		end := strings.Index(rest, "```")
		if end < 0 {
			continue
		}
		body := strings.TrimSpace(rest[:end])
		remainder := strings.TrimSpace(text[:idx] + rest[end+3:])
		if call, ok := decodeCall(body); ok {
			return call, remainder, true
		}
	}
	return parsedCall{}, text, false
}

func decodeCall(body string) (parsedCall, bool) {
	var one parsedCall
	if err := json.Unmarshal([]byte(body), &one); err == nil && one.Name != "" {
		return one, true
	}
	var wrapped struct {
		ToolCalls []parsedCall `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(body), &wrapped); err == nil && len(wrapped.ToolCalls) > 0 {
		return wrapped.ToolCalls[0], true
	}
	return parsedCall{}, false
}

// ---- response ----

type responseMessage struct {
	Role      string     `json:"role"`
	Content   *string    `json:"content"`
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type choice struct {
	Index        int             `json:"index"`
	Message      responseMessage `json:"message"`
	FinishReason string          `json:"finish_reason"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type completionResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []choice `json:"choices"`
	Usage   usage    `json:"usage"`
}

func handleChat(cfg Config, w http.ResponseWriter, r *http.Request) {
	var req completionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages is required")
		return
	}

	emulate := cfg.ToolsEmulate && len(req.Tools) > 0
	upstream := toUpstream(req, emulate)
	id := "chatcmpl-" + randomID()
	created := time.Now().Unix()

	// Tool replies are parsed, not streamed: a tool_call must be complete before
	// it can be emitted, and a partial block would be read as prose by the client.
	if req.Stream && !emulate {
		streamChat(w, r, req.Model, id, created, upstream)
		return
	}

	text, err := api.Chat(r.Context(), api.NewClient(), req.Model, upstream, nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	msg := responseMessage{Role: "assistant", Content: &text}
	finish := "stop"
	if emulate {
		if call, remainder, ok := extractToolCall(text); ok {
			arguments := string(call.Arguments)
			if arguments == "" || arguments == "null" {
				arguments = "{}"
			}
			tc := toolCall{ID: "call_" + randomID(), Type: "function"}
			tc.Function.Name = call.Name
			tc.Function.Arguments = arguments
			msg.ToolCalls = []toolCall{tc}
			if strings.TrimSpace(remainder) == "" {
				msg.Content = nil
			} else {
				msg.Content = &remainder
			}
			finish = "tool_calls"
		}
	}

	resp := completionResponse{
		ID: id, Object: "chat.completion", Created: created, Model: req.Model,
		Choices: []choice{{Index: 0, Message: msg, FinishReason: finish}},
		Usage:   estimateUsage(upstream, text),
	}
	if req.Stream {
		writeStreamed(w, resp)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func streamChat(w http.ResponseWriter, r *http.Request, model, id string, created int64, upstream []api.Message) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	var collected strings.Builder
	flushed := 0
	text, err := api.Chat(r.Context(), api.NewClient(), model, upstream, func(chunk string) {
		collected.WriteString(chunk)
		sendDelta(w, id, created, model, chunk)
		flushed++
		if flushed%8 == 0 {
			flusher.Flush()
		}
	})
	if err != nil && collected.Len() == 0 {
		sendDelta(w, id, created, model, "error: "+err.Error())
		finishStream(w, id, created, model, "stop")
		return
	}
	if text == "" {
		text = collected.String()
	}
	finishStream(w, id, created, model, "stop")
}

func sendDelta(w http.ResponseWriter, id string, created int64, model, content string) {
	chunk := map[string]interface{}{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []map[string]interface{}{{
			"index": 0, "delta": map[string]interface{}{"content": content}, "finish_reason": nil,
		}},
	}
	payload, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", payload)
}

func finishStream(w http.ResponseWriter, id string, created int64, model, reason string) {
	chunk := map[string]interface{}{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []map[string]interface{}{{
			"index": 0, "delta": map[string]interface{}{}, "finish_reason": reason,
		}},
	}
	payload, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", payload)
}

// writeStreamed replays a buffered completion as a stream, used for tool calls.
func writeStreamed(w http.ResponseWriter, resp completionResponse) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	msg := resp.Choices[0].Message
	if msg.Content != nil && *msg.Content != "" {
		sendDelta(w, resp.ID, resp.Created, resp.Model, *msg.Content)
	}
	if len(msg.ToolCalls) > 0 {
		delta := map[string]interface{}{
			"id": resp.ID, "object": "chat.completion.chunk", "created": resp.Created, "model": resp.Model,
			"choices": []map[string]interface{}{{
				"index":         0,
				"delta":         map[string]interface{}{"role": "assistant", "tool_calls": numbered(msg.ToolCalls)},
				"finish_reason": nil,
			}},
		}
		payload, _ := json.Marshal(delta)
		fmt.Fprintf(w, "data: %s\n\n", payload)
	}
	finishStream(w, resp.ID, resp.Created, resp.Model, resp.Choices[0].FinishReason)
}

func numbered(calls []toolCall) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(calls))
	for i, c := range calls {
		out = append(out, map[string]interface{}{"index": i, "id": c.ID, "type": c.Type, "function": c.Function})
	}
	return out
}

// estimateUsage reports character-based estimates. The upstream sends no token
// accounting, and a harness that shows zero cost is worse than one that shows a
// rough number.
func estimateUsage(in []api.Message, out string) usage {
	inChars := 0
	for _, m := range in {
		if s, ok := m.Content.(string); ok {
			inChars += len(s)
		}
	}
	prompt := inChars / 4
	completion := len(out) / 4
	return usage{PromptTokens: prompt, CompletionTokens: completion, TotalTokens: prompt + completion}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]interface{}{
		"error": map[string]interface{}{"message": msg, "type": "invalid_request_error"},
	})
}

func randomID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

var _ = context.Background
