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
		seedMark1  = "SEEDMARK-Q33-ALPHA-7f3a"
		seedMark2  = "SEEDMARK-Q33-BETA-7f3a"
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
	provider := &recordingFailFirstProvider{
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
	// Plain-history recovery fixture, repaired per the 2026-10-01 ruling §3
	// row 1 + caveat E14 (same shape as the eventbus test): the old ~93-byte
	// pair could never overcome the ≥254-byte breadcrumb framing. Two
	// completed old plain exchanges with multi-KB deterministic text, seeded
	// in THIS test's exact active scope (sessionKey is both the seed key and
	// the runAgentLoop invocation key); each carries a unique seed marker the
	// request assertions trace. Only exchange 1 is older than the newest
	// plain assistant (exchange 2's assistant is the floor), so exactly one
	// eligible eviction endpoint exists.
	seedExchange := func(mark, word string) []providers.Message {
		filler := strings.Repeat(word, 240) // ~2.6KB deterministic removable text per message
		return []providers.Message{
			// Marker AFTER the filler, beyond rune 80 — the eviction
			// breadcrumb quotes the 80-rune head snippet of every evicted
			// line (breadcrumb_archive.go::archiveBreadcrumbEntry), and a
			// head-positioned marker would ride that snippet into the retry
			// request even after its exchange legitimately slid away.
			{Role: "user", Content: filler + " " + mark},
			{Role: "assistant", Content: filler},
		}
	}
	seed := append(seedExchange(seedMark1, "alpha lore "), seedExchange(seedMark2, "beta lore ")...)
	seed = append(seed, providers.Message{Role: "user", Content: "Continue the task"})
	defaultAgent.Sessions.SetHistory(sessionKey, seed)

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
	if response != answer || provider.calls() != 2 {
		t.Fatalf("fixture: want recovered answer %q after exactly 2 calls; got %q after %d", answer, response, provider.calls())
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

	// Positive control (ruling §3 row 1): the first recorded provider request
	// must actually carry both seed markers — the seeded history sat in the
	// active scope and was really sent.
	req1 := provider.request(1)
	if !requestContains(req1, seedMark1) || !requestContains(req1, seedMark2) {
		t.Fatalf("fixture positive control failed: the first provider request must carry both seed markers")
	}
	// MAJ-CW-012's serialization instrument: the retry's normalized retained
	// payload must be STRICTLY smaller than the rejected one, and only the
	// eligible prefix may leave (exchange 1's marker gone; exchange 2's
	// marker still present — its assistant is the newest-plain floor).
	req2 := provider.request(2)
	size1, sizeErr := retainedPayloadSize(nil, req1)
	if sizeErr != nil {
		t.Fatalf("fixture: normalize request 1: %v", sizeErr)
	}
	size2, sizeErr := retainedPayloadSize(nil, req2)
	if sizeErr != nil {
		t.Fatalf("fixture: normalize request 2: %v", sizeErr)
	}
	if size2 >= size1 {
		t.Fatalf("FR-032/MAJ-CW-012: the retry's retained payload must shrink strictly; req1=%d req2=%d normalized bytes", size1, size2)
	}
	if requestContains(req2, seedMark1) {
		t.Fatal("fixture: the eligible oldest prefix (exchange 1) must be gone from the retry request")
	}
	if !requestContains(req2, seedMark2) {
		t.Fatal("fixture: exchange 2 must survive the relief (newest plain assistant is the floor)")
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
