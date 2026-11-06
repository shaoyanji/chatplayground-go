# ChatPlayground Go (`chatplayground-go`)

[![Go Version](https://img.shields.io/badge/Go-%3E%3D1.22-blue.svg)](https://golang.org/)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![Bubble Tea](https://img.shields.io/badge/TUI-Bubble%20Tea-purple.svg)](https://github.com/charmbracelet/bubbletea)
[![Glamour](https://img.shields.io/badge/Markdown-Glamour-pink.svg)](https://github.com/charmbracelet/glamour)

> A high-performance, single-binary Go client for [ChatPlayground AI](https://web.chatplayground.ai/). Engineered for sub-millisecond cold starts, browserless pure-REST token renewal, parallel multi-model benchmarking with goroutines, and an interactive Bubble Tea terminal UI.

---

## ✨ Why Go?

| Metric / Feature | Node.js CLI | `chatplayground-go` |
| :--- | :--- | :--- |
| **Cold Start Startup** | ~150–300 ms | **< 5 ms** (Instant) |
| **Runtime Dependency** | Node.js + NPM | **Zero** (Single static binary) |
| **Clerk Token Refresh** | Headless Chromium | **Pure REST** (Direct FAPI HTTP call) |
| **Multi-Model Query** | Sequential | **Concurrent Fan-Out / Fan-In** |
| **Terminal Experience** | Text REPL | **Bubble Tea TUI + Glamour Markdown** |

---

## 🚀 Key Capabilities

### 1. ⚡ Instant Cold Start & Pipeline Workflows
Perfect for Git hooks, shell scripts, and hotkey automation where Node startup overhead is noticeable.
Piped stdin is used when no prompt argument is given, so a task that needs both
carries its material in the prompt:

```bash
chatplayground-go "Review this diff for memory leaks and edge cases:
$(git diff)"

# A prompt is not required: the piped text is the prompt
tail -n 50 /var/log/app.log | chatplayground-go "Diagnose the fatal crash"
```

When stdout is not a TTY the reply is written bare — no prompt glyph, no colour —
so it can be piped straight into another tool.

### 2. 🏁 Parallel Multi-Model Benchmarking
Query **Claude Sonnet 5**, **DeepSeek R1**, and **ChatGPT-5.6 Sol** simultaneously via goroutines and channels:
```bash
chatplayground-go compare "Explain the fundamental trade-offs between B-Trees and LSM-Trees"
```
*Streams and compares latency, reasoning steps, and technical explanations across models in comparative terminal cards.*

### 3. 🔄 Browserless Pure-REST Clerk Token Renewal
Eliminates the Playwright / Chromium requirement completely. Calls Clerk's Frontend API directly:
```
POST https://clerk.chatplayground.ai/v1/client/sessions/{sessionId}/tokens
Cookie: __session=...
```
```bash
# Mint a token now; --json is the form a timer or a health probe reads
chatplayground-go refresh --json
```
`refresh --json` exits non-zero when the stored session is dead, so an hourly
timer turns a silent credential expiry into a failed unit instead of a surprise
later.

### 4. 🖥️ Interactive Terminal UI (TUI)
Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Lip Gloss](https://github.com/charmbracelet/lipgloss), and [Glamour](https://github.com/charmbracelet/glamour):
```bash
chatplayground-go tui
```
- Real-time syntax-highlighted markdown rendering
- <kbd>Tab</kbd> to cycle between models on the fly
- Smooth viewport scrolling with conversation memory

### 5. 🎨 Image Generation
The generator is resolved from the live catalog (`gpt-image-2.5-sunburst` and
`-flare` on 2026-11-06); a retired id answers `400 The model is not supported`,
which is why nothing is hardcoded.
```bash
chatplayground-go image --size 1536x1024 -o city.png "A peaceful cyberpunk tea shop in Kyoto"
chatplayground-go image -m gpt-image-2.5-flare --json -o icon.png "a red panda librarian"
```
`--json` prints `{id, url, prompt, model, size, path}`, and `{"error": …}` with a
non-zero exit when generation fails.

### 6. 💬 Self-Contained Chat
One process, no server, no daemon: the thread lives on the server under a
`chatId`, the transcript is mirrored to `~/.chatplayground/sessions/<name>.json`
(0600), and a second run with the same `-s` name resumes where you left off.
```bash
chatplayground-go chat                      # interactive, session "default"
chatplayground-go chat -m gpt-6-luna -s work
chatplayground-go chat -s work "and what did I just ask?"   # one-shot, same thread
```
Inside the prompt: `/new` (fresh thread), `/model <id>`, `/models`, `/status`,
`/exit`. Only the new prompt is ever sent — the endpoint keeps the conversation
and rejects a resent transcript — so a long chat costs one message per turn.

---

## 📦 Building & Installation

### Build from Source
```bash
git clone https://github.com/shaoyanji/chatplayground-go.git
cd chatplayground-go
go build -o chatplayground-go.exe .
```

### Install Globally via Go
```bash
go install github.com/shaoyanji/chatplayground-go@latest
```

---

## 🛠️ Command Reference

```text
USAGE:
  chatplayground-go [options] [prompt]
  chatplayground-go <command> [options]

COMMANDS:
  chat, repl [-m id] [-s name] [prompt]
                          Self-contained streaming chat (server-side thread)
  models [--json]         Catalog; --json adds active/premium_only/tier/credit_weight
  status [--json]         Auth state, user, credential age
  refresh [--json]        Browserless Clerk token renewal; --json exits 1 on failure
  image [-m id] [--json]  Generate an image, optionally downloading it
  compare, benchmark <p>  Parallel comparison across three models
  tui                     Interactive Bubble Tea terminal UI
  serve [--addr] [--tools-emulate]
                          OpenAI-compatible /v1 endpoint

OPTIONS:
  -m, --model <id>        Target model
  -i, --image <path/url>  Input image for multimodal vision
  -s, --size <size>       Image size: 1024x1024, 1536x1024, 1024x1536
  -o, --output <file>     Download path for image generation
  -h, --help              Show help
  -v, --version           Show version
```

`--json` is the machine surface: one document on stdout and nothing decorative.
`models --json` is the entitlement view — `premium_only` ids answer
`403 only available to active subscribers`:

```bash
chatplayground-go models --json | jq -r '.[] | select(.premium_only | not) | .id'
```

---

## 🛰️ OpenAI-compatible API

`chatplayground-go serve` exposes the same account as a standard OpenAI endpoint,
so an OpenAI-compatible client or an agent harness can use a base URL instead of
a CLI.

```bash
chatplayground-go serve                                    # http://127.0.0.1:8787/v1
chatplayground-go serve --addr 0.0.0.0:8787 --tools-emulate
```

| Endpoint | What it does |
| :--- | :--- |
| `GET /v1/models` | Model ids from the live catalog. Public, no token needed. |
| `POST /v1/chat/completions` | Chat, streamed or not. Accepts OpenAI `messages`. |
| `GET /healthz` | Liveness. |

Authentication is the session the CLI already keeps in `~/.chatplayground/auth.json`
(`chatplayground-go login`); `CHATPLAYGROUND_TOKEN` overrides it. Renewal is the
pure-REST Clerk refresh, so the server needs no Node, Chromium or Playwright.

**Tool calls.** The chat endpoint returns plain text and has no `tools` support,
so tool calling is emulated and off by default. With `--tools-emulate` the tool
schemas are sent in a system message, the model answers with a fenced
`tool_call` block, and that block is returned as an OpenAI `tool_calls` field with
`finish_reason: "tool_calls"`. Replies are buffered when the flag is on, because a
half-parsed call would be delivered as prose. Without the flag, `tools` are
ignored and the model's text comes back untouched.

Point a client at it with the base URL and any key:

```bash
export OPENAI_BASE_URL=http://127.0.0.1:8787/v1
export OPENAI_API_KEY=local
```

Model ids are the catalog's `botId` values (`gemini-3.8-flash-l`,
`claude-haiku-5-5`, `deepseek-v4.1-flash`, …); `GET /v1/models` lists them.

## 🏗️ Architecture

```
chatplayground-go/
├── main.go               # Command dispatcher & flag parser
├── pkg/
│   ├── api/              # Pure-REST streaming engine & multipart uploader
│   ├── auth/             # RESTful Clerk session token refresh
│   ├── benchmark/        # Parallel fan-out/fan-in goroutine benchmarker
│   └── tui/              # Bubble Tea TUI with Glamour rendering
├── go.mod
├── go.sum
├── LICENSE
└── README.md
```

---

## 📄 License

[MIT License](LICENSE)
