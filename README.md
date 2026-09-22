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
Perfect for Git hooks, shell scripts, and hotkey automation where Node startup overhead is noticeable:
```bash
# Instant code review on your current git diff
git diff | chatplayground-go "Review this diff for memory leaks and edge cases"

# Piping logs
tail -n 50 /var/log/app.log | chatplayground-go "Diagnose the fatal crash"
```

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
# Force a manual REST token refresh:
chatplayground-go refresh
```

### 4. 🖥️ Interactive Terminal UI (TUI)
Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Lip Gloss](https://github.com/charmbracelet/lipgloss), and [Glamour](https://github.com/charmbracelet/glamour):
```bash
chatplayground-go tui
```
- Real-time syntax-highlighted markdown rendering
- <kbd>Tab</kbd> to cycle between models on the fly
- Smooth viewport scrolling with conversation memory

### 5. 🎨 GPT Image 2 Generation
Generate images and download them directly:
```bash
chatplayground-go image --size 1536x1024 -o city.png "A peaceful cyberpunk tea shop in Kyoto"
```

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
  compare, benchmark <p>  Run parallel benchmark across Claude Sonnet 5, DeepSeek R1 & GPT-5.6
  image <prompt>          Generate image with GPT Image 2
  tui                     Launch interactive Bubble Tea terminal UI
  status                  Check authentication credentials and expiry
  refresh                 Perform browserless pure-REST Clerk token renewal

OPTIONS:
  -m, --model <id>        Target model (default: claude-sonnet-5-l)
  -i, --image <path/url>  Input image for multimodal vision
  -s, --size <size>       Image size: 1024x1024, 1536x1024, 1024x1536
  -o, --output <file>     Download path for image generation
  -h, --help              Show help
  -v, --version           Show version
```

---

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
