package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// promptEchoErrProvider returns the provider's own error body. That body
// repeats the prompt. No network.
type promptEchoErrProvider struct {
	prompt string
}

func (p *promptEchoErrProvider) Chat(
	context.Context,
	[]providers.Message,
	[]providers.ToolDefinition,
	string,
	map[string]any,
) (*providers.LLMResponse, error) {
	return nil, fmt.Errorf("provider 400 body echoes the prompt %s", p.prompt)
}

func (p *promptEchoErrProvider) GetDefaultModel() string { return "mock-model" }

// TestLogLine_ErrorBodyDropsPrompt is TDD row 17's error half. A provider
// error that echoes the prompt must not put that text on the caching line,
// and the turn must surface the error instead of a success with empty usage.
func TestLogLine_ErrorBodyDropsPrompt(t *testing.T) {
	const prompt = "SECRET-PROMPT-DO-NOT-LOG"
	tmp := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmp,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmp}},
		},
	}
	provider := &promptEchoErrProvider{prompt: prompt}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(al.Close)
	meta, err := al.sharedSessionStore.NewSession(session.SessionTypeChat, "webchat", "mia")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	readLog := captureLogFile(t, logger.INFO)
	_, _, err = al.processMessageAsPrimaryForTest(context.Background(), bus.InboundMessage{
		Channel:   "webchat",
		SessionID: meta.ID,
		Sender:    bus.SenderInfo{CanonicalID: "user-web-1"},
		ChatID:    "chat-err-1",
		Content:   prompt,
	})
	if err == nil {
		t.Fatal("turn returned success; a provider error must surface, not an empty usage success")
	}
	if !strings.Contains(err.Error(), "provider 400") && !strings.Contains(err.Error(), "LLM call failed") {
		t.Fatalf("turn error = %v, want the provider error", err)
	}
	for _, line := range strings.Split(readLog(), "\n") {
		if strings.Contains(line, prompt) && cachingLine(line) {
			t.Errorf("caching line contains the prompt: %s", line)
		}
	}
}

// cachingLine reports whether a log record is the per-call accounting line:
// an info line that carries the cache counters. The generic "LLM call failed"
// record is not that line.
func cachingLine(line string) bool {
	if !strings.Contains(line, "INFO") {
		return false
	}
	return strings.Contains(line, "cache read") || strings.Contains(line, "cache_read") || strings.Contains(line, "uncached")
}
