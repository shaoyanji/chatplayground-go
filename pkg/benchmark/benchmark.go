package benchmark

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/shaoyanji/chatplayground-go/pkg/api"
)

type Result struct {
	Model    string
	Duration time.Duration
	Output   string
	Error    error
}

func RunComparison(prompt string, models []string) []Result {
	if len(models) == 0 {
		models = []string{"claude-sonnet-5-l", "deepseek-r1", "gpt-5.6-sol"}
	}

	resultsChan := make(chan Result, len(models))
	var wg sync.WaitGroup

	fmt.Printf("\n\033[1;36m🏁 Starting Parallel Multi-Model Benchmark (%d models)\033[0m\n", len(models))
	fmt.Printf("Prompt: %s\n\n", prompt)

	client := api.NewClient()

	for _, m := range models {
		wg.Add(1)
		go func(modelName string) {
			defer wg.Done()
			start := time.Now()
			out, err := client.StreamQuery(modelName, prompt, "", nil)
			duration := time.Since(start)

			resultsChan <- Result{
				Model:    modelName,
				Duration: duration,
				Output:   out,
				Error:    err,
			}
		}(m)
	}

	wg.Wait()
	close(resultsChan)

	var results []Result
	for r := range resultsChan {
		results = append(results, r)
	}

	// Display results in styled comparative cards
	for _, res := range results {
		fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		if res.Error != nil {
			fmt.Printf("🤖 \033[1;31m%s\033[0m (Failed in %v)\n", res.Model, res.Duration.Round(time.Millisecond))
			fmt.Printf("Error: %v\n\n", res.Error)
		} else {
			fmt.Printf("🤖 \033[1;32m%s\033[0m (Latency: \033[1;33m%v\033[0m)\n", res.Model, res.Duration.Round(time.Millisecond))
			trimmed := strings.TrimSpace(res.Output)
			fmt.Printf("%s\n\n", trimmed)
		}
	}
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

	return results
}
