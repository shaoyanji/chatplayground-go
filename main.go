package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/shaoyanji/chatplayground-go/pkg/api"
	"github.com/shaoyanji/chatplayground-go/pkg/auth"
	"github.com/shaoyanji/chatplayground-go/pkg/benchmark"
	"github.com/shaoyanji/chatplayground-go/pkg/tui"
)

const Version = "1.0.0"

func printHelp() {
	fmt.Printf(`
ChatPlayground Go (v%s) - High-Performance Pure-Go Client

FEATURES:
  ⚡ <5ms Cold Start (zero-overhead Go binary)
  🔄 Pure-REST Clerk Token Refresh (no browser required)
  🏁 Parallel Multi-Model Benchmarking (Goroutines & Channels)
  🎨 GPT Image 2 Generation
  🖥️  Bubble Tea Interactive TUI with Glamour Markdown Rendering

USAGE:
  chatplayground-go [options] [prompt]
  chatplayground-go <command> [options]

COMMANDS:
  compare, benchmark <prompt>   Run parallel benchmark across Claude Sonnet 5, DeepSeek R1 & GPT-5.6
  image <prompt>                Generate image with GPT Image 2
  tui                           Launch interactive Bubble Tea terminal UI
  status                        Check authentication credentials and expiry
  refresh                       Perform browserless pure-REST Clerk token renewal

OPTIONS:
  -m, --model <id>              Target model (default: claude-sonnet-5-l)
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

  # 5. Interactive Bubble Tea TUI
  chatplayground-go tui
`, Version)
}

func readStdin() string {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) != 0 {
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

	flag.StringVar(&modelFlag, "m", "claude-sonnet-5-l", "Model ID")
	flag.StringVar(&modelFlag, "model", "claude-sonnet-5-l", "Model ID")
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

	// Command: status
	if firstArg == "status" {
		authData, err := auth.ReadAuth()
		if err != nil || authData.Token == "" {
			fmt.Println("\033[1;33mNot logged in.\033[0m Run 'chatplayground login' first.")
			return
		}
		fmt.Println("\033[1;32m● Authenticated with ChatPlayground (Go Client)\033[0m")
		if authData.SessionID != "" {
			fmt.Printf("Session ID: %s\n", authData.SessionID)
		}
		fmt.Printf("Config file: %s\n", auth.GetAuthFilePath())
		return
	}

	// Command: refresh
	if firstArg == "refresh" {
		authData, err := auth.ReadAuth()
		if err != nil {
			fmt.Printf("Failed to read auth: %v\n", err)
			return
		}
		newToken, err := auth.RefreshClerkTokenREST(authData)
		if err != nil {
			fmt.Printf("REST Refresh failed: %v\n", err)
			return
		}
		fmt.Println("\033[1;32m✅ Successfully refreshed Clerk token via pure REST!\033[0m")
		fmt.Printf("Token prefix: %s...\n", newToken[:30])
		return
	}

	// Command: tui
	if firstArg == "tui" {
		if err := tui.Start(); err != nil {
			fmt.Printf("TUI error: %v\n", err)
		}
		return
	}

	// Command: compare / benchmark
	if firstArg == "compare" || firstArg == "benchmark" {
		prompt := strings.Join(args[1:], " ")
		if prompt == "" {
			prompt = readStdin()
		}
		if prompt == "" {
			fmt.Println("Usage: chatplayground-go compare <prompt>")
			return
		}
		benchmark.RunComparison(prompt, nil)
		return
	}

	// Command: image
	if firstArg == "image" {
		prompt := strings.Join(args[1:], " ")
		if prompt == "" {
			prompt = readStdin()
		}
		if prompt == "" {
			fmt.Println("Usage: chatplayground-go image [--size 1024x1024] [-o file.png] <prompt>")
			return
		}

		client := api.NewClient()
		fmt.Printf("🎨 Generating image with GPT Image 2...\n")
		url, err := client.GenerateImage(prompt, sizeFlag, outputFlag)
		if err != nil {
			fmt.Printf("\033[1;31mImage generation error: %v\033[0m\n", err)
			return
		}
		fmt.Printf("\033[1;32m✅ Image generated successfully!\033[0m\n")
		fmt.Printf("🖼️ URL: %s\n", url)
		if outputFlag != "" {
			fmt.Printf("💾 Saved to: %s\n", outputFlag)
		}
		return
	}

	// Standard query
	prompt := strings.Join(args, " ")
	piped := readStdin()
	if piped != "" {
		if prompt != "" {
			prompt = fmt.Sprintf("%s\n\nContext:\n%s", prompt, piped)
		} else {
			prompt = piped
		}
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
