package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Q33 (#1081), founder Q4: context management must be invisible in normal
// chat. The ordinary outbound bus is real; only the external LLM is faked.
// Positive classified/verbose delivery is covered separately in the SPA pack.
func TestContextOverflowRetryDoesNotPublishNormalChatNotice_Q33(t *testing.T) {
	const (
		notice     = "Context window exceeded. Compressing history and retrying..."
		answer     = "Recovered answer for Q33"
		sessionKey = "q33-context-overflow-session"
		chatID     = "q33-context-overflow-chat"
	)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	// Same wide-window fixture as TestAgentLoop_EmitsContextCompressEventOnRetry:
	// prevent proactive trimming from pre-empting the reactive overflow path.
	cfg.Context.DefaultContextWindow = intPtr(131072)
	provider := &failFirstMockProvider{
		failures:    1, // One provider rejection, then one recovered answer.
		failError:   stringError("InvalidParameter: Total tokens of image and text exceed max message tokens"),
		successResp: answer,
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("fixture: expected the default agent")
	}
	defaultAgent.Sessions.SetHistory(sessionKey, []providers.Message{
		{Role: "user", Content: "Old question one"},
		{Role: "assistant", Content: "Old answer one"},
		{Role: "user", Content: "Old question two"},
		{Role: "assistant", Content: "Old answer two"},
		{Role: "user", Content: "Continue the task"},
	})

	var retryMu sync.Mutex
	var retries []Event
	al.SetEventSyncTap(func(evt Event) {
		if evt.Kind == EventKindLLMRetry {
			retryMu.Lock()
			retries = append(retries, evt)
			retryMu.Unlock()
		}
	})
	response, err := al.runAgentLoop(context.Background(), defaultAgent, processOptions{
		SessionKey: sessionKey, Channel: "webchat", ChatID: chatID,
		UserMessage: "Continue the task", DefaultResponse: defaultResponse,
		SendResponse: true,
	})
	if err != nil {
		t.Fatalf("fixture: overflow retry did not recover: %v", err)
	}
	if response != answer || provider.currentCall != 2 {
		t.Fatalf("fixture: want recovered answer %q after exactly 2 calls; got %q after %d", answer, response, provider.currentCall)
	}
	retryMu.Lock()
	recordedRetries := append([]Event(nil), retries...)
	retryMu.Unlock()
	if len(recordedRetries) != 1 {
		t.Fatalf("fixture: want exactly one context-overflow retry event, got %d", len(recordedRetries))
	}
	retry, ok := recordedRetries[0].Payload.(LLMRetryPayload)
	if !ok || retry.Reason != "context_limit" || retry.Attempt != 1 {
		t.Fatalf("fixture: want context_limit attempt 1, got %#v", recordedRetries[0].Payload)
	}

	// Publications complete synchronously before runAgentLoop returns. No
	// consumer is attached. Drain BEFORE Close, which discards queued messages.
	var outbound []bus.OutboundMessage
drainOutbound:
	for {
		select {
		case msg := <-msgBus.OutboundChan():
			outbound = append(outbound, msg)
		default:
			break drainOutbound
		}
	}
	var deliveredAnswers int
	for _, msg := range outbound {
		// Contains also catches the forbidden exact sentence if it is merged
		// into an otherwise normal answer rather than sent as its own bubble.
		if strings.Contains(msg.Content, notice) {
			t.Errorf("Q33: context-management notice must be absent from ordinary outbound chat; got channel=%q chat=%q content=%q", msg.Channel, msg.ChatID, msg.Content)
		}
		if msg.Channel == "webchat" && msg.ChatID == chatID && msg.Content == answer {
			deliveredAnswers++
		}
	}
	if deliveredAnswers != 1 {
		t.Fatalf("fixture: normal answer must still reach webchat exactly once; got %d in %#v", deliveredAnswers, outbound)
	}
	t.Logf("instrument: context_limit attempt=1, provider_calls=2, normal_answers=%d, outbound_messages=%d", deliveredAnswers, len(outbound))
}
