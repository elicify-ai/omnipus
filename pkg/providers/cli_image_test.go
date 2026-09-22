package providers

import (
	"context"
	"strings"
	"testing"
)

func TestCLITransports_RejectImagesBeforeExecution(t *testing.T) {
	msg := []Message{{Role: "tool", ToolCallID: "call-c", Media: []string{"data:image/png;base64,AA=="}}}
	for name, provider := range map[string]LLMProvider{"codex": &CodexCliProvider{command: "missing-codex"}, "copilot": &CopilotCliProvider{command: "missing-copilot"}} {
		_, err := provider.Chat(context.Background(), msg, nil, "vision-labelled", nil)
		if err == nil || !strings.Contains(err.Error(), "does not support image input") {
			t.Fatalf("%s error=%v", name, err)
		}
	}
}
