package repl

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/shaoyanji/chatplayground-go/pkg/api"
)

func Start(defaultModel string) error {
	if defaultModel == "" {
		defaultModel = "gemini-3.8-flash-l"
	}

	client := api.NewClient()

	fmt.Println("\033[1;36m=== ChatPlayground Interactive CLI ===\033[0m")
	fmt.Printf("🤖 Model: \033[1;32m%s\033[0m\n", defaultModel)
	fmt.Println("💡 Commands: \033[35m/model <id>\033[0m, \033[35m/models\033[0m, \033[35m/new\033[0m, \033[35m/status\033[0m, \033[35m/clear\033[0m, \033[35m/exit\033[0m\n")

	scanner := bufio.NewScanner(os.Stdin)

	for {
		threadInfo := ""
		if client.ChatID != "" {
			id := client.ChatID
			if len(id) > 12 {
				id = id[:12] + "..."
			}
			threadInfo = fmt.Sprintf(" [%s]", id)
		}
		fmt.Printf("\033[1;32mYou%s>\033[0m ", threadInfo)

		if !scanner.Scan() {
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "/") {
			parts := strings.Fields(line)
			cmd := strings.ToLower(parts[0])

			switch cmd {
			case "/exit", "/quit", "/q":
				fmt.Println("Goodbye!")
				return nil

			case "/new", "/clear", "/reset":
				client.ResetSession()
				fmt.Println("\033[1;33mConversation cleared and started new thread.\033[0m\n")
				continue

			case "/status":
				fmt.Println("\033[1;32m● Active Session\033[0m")
				fmt.Printf("Model:   %s\n", defaultModel)
				fmt.Printf("Chat ID: %s\n", client.ChatID)
				fmt.Printf("Turns:   %d\n\n", len(client.Messages)/2)
				continue

			case "/model":
				if len(parts) > 1 {
					defaultModel = parts[1]
					fmt.Printf("\033[1;32mSwitched active model to: %s\033[0m\n\n", defaultModel)
				} else {
					fmt.Printf("Current model: %s\n\n", defaultModel)
				}
				continue

			case "/models":
				models, err := client.GetModels()
				if err != nil {
					fmt.Printf("Error fetching models: %v\n\n", err)
				} else {
					fmt.Printf("\nAvailable models (%d total):\n", len(models))
					for _, m := range models {
						fmt.Printf("  %-26s %-28s [%s]\n", m.BotID, m.DisplayName, m.Provider)
					}
					fmt.Println()
				}
				continue

			default:
				fmt.Printf("Unknown command: %s. Type /new, /model <name>, /status, or /exit\n\n", cmd)
				continue
			}
		}

		// Stream assistant reply in real-time
		fmt.Printf("\n\033[1;36m%s>\033[0m ", defaultModel)
		_, err := client.StreamQuery(defaultModel, line, "", func(chunk string) {
			fmt.Print(chunk)
		})
		if err != nil {
			fmt.Printf("\n\033[1;31mError: %v\033[0m\n", err)
		}
		fmt.Println("\n")
	}

	return nil
}
