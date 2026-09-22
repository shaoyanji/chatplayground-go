package api_test

import (
	"testing"

	"github.com/shaoyanji/chatplayground-go/pkg/api"
)

func TestMultiTurnThreadRetention(t *testing.T) {
	client := api.NewClient()

	// Turn 1:
	prompt1 := "My secret word is 'hyperdrive'. Remember this."
	resp1, err := client.StreamQuery("gemini-3.8-flash-l", prompt1, "", nil)
	if err != nil {
		t.Fatalf("Turn 1 failed: %v", err)
	}
	t.Logf("Turn 1 response: %s", resp1)

	if client.ChatID == "" {
		t.Fatalf("Expected ChatID to be set after turn 1, got empty")
	}
	t.Logf("Captured ChatID: %s", client.ChatID)

	if len(client.Messages) != 2 {
		t.Fatalf("Expected 2 messages in client history, got %d", len(client.Messages))
	}

	// Turn 2:
	prompt2 := "What is my secret word? Reply in 1 word only."
	resp2, err := client.StreamQuery("gemini-3.8-flash-l", prompt2, "", nil)
	if err != nil {
		t.Fatalf("Turn 2 failed: %v", err)
	}
	t.Logf("Turn 2 response: %s", resp2)

	if len(client.Messages) != 4 {
		t.Fatalf("Expected 4 messages in client history, got %d", len(client.Messages))
	}
}
