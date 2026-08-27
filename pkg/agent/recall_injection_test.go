// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// recall_injection_test.go — ADR-066 D5.4 (FR-041…FR-043, B-48/B-49/B-50/
// B-50c, SC-014): a recall made MID-TURN must reach the provider's very next
// request, or the tool result must say it did not fit.
//
// Before D5.4 the recall span was only read at assembly/trim sites, while
// every mid-turn request was built from the in-memory slice — so 25 live
// recalls reached the model as a receipt string and nothing else. These
// tests drive a REAL turn (runTurn via processTaskDirect) against a
// recording provider and assert on the bytes of the provider's SECOND
// request, which is the only thing that proves the fix.

// recallInjectionProvider is a scripted providers.LLMProvider that records
// every request it receives. Call 1 returns a recall_conversation tool call
// with the configured args; subsequent calls follow `script` (one entry per
// call after the first: either another recall call, an error, or a plain
// text answer).
type recallInjectionProvider struct {
	mu       sync.Mutex
	requests [][]providers.Message
	// script[i] is the response for call i+2 (call 1 is always the first
	// recall). A nil entry means "plain text answer".
	script []func() (*providers.LLMResponse, error)
	first  map[string]any
}

func (p *recallInjectionProvider) Chat(
	_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Deep-copy the request so later in-place mutation of the loop's slice
	// cannot rewrite what we observed.
	snap := make([]providers.Message, len(messages))
	copy(snap, messages)
	p.requests = append(p.requests, snap)
	call := len(p.requests)
	if call == 1 {
		return &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{{
				ID:        "call_recall_1",
				Type:      "function",
				Name:      "recall_conversation",
				Arguments: p.first,
			}},
		}, nil
	}
	idx := call - 2
	if idx < len(p.script) && p.script[idx] != nil {
		return p.script[idx]()
	}
	return &providers.LLMResponse{Content: "done", ToolCalls: []providers.ToolCall{}}, nil
}

func (p *recallInjectionProvider) GetDefaultModel() string { return "recall-injection-model" }

func (p *recallInjectionProvider) request(n int) []providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n < 1 || n > len(p.requests) {
		return nil
	}
	return p.requests[n-1]
}

func (p *recallInjectionProvider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

// recallInjectionFixture boots an AgentLoop with one custom agent whose
// window is `cw`, seeds `turns` into the agent's per-agent session store
// under sessionKey, evicts the oldest turn(s) via windowTrim and grants the
// agent the recall_conversation tool. It returns the loop and the agent.
func recallInjectionFixture(
	t *testing.T, provider providers.LLMProvider, cw, mt int, turns [][]providers.Message,
) (*AgentLoop, *AgentInstance) {
	t.Helper()
	const agentID = "recall-injection-agent"
	// Isolate $OMNIPUS_HOME: an agent with no explicit Home resolves its
	// workspace (and its per-agent session store) under $OMNIPUS_HOME/agents/,
	// which would otherwise be the developer's real ~/.omnipus and leak the
	// archive between tests.
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              filepath.Join(home, "agents", "defaults"),
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         mt,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: agentID, Name: "Recall Injection Agent", Type: config.AgentTypeCustom, Home: filepath.Join(home, "agents", agentID)},
			},
		},
	}
	cfg.Context.DefaultContextWindow = intPtr(cw)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(al.Close)

	agent, ok := al.GetRegistry().GetAgent(agentID)
	require.True(t, ok, "test setup: agent must be registered")
	agent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"recall_conversation": config.ToolPolicyAllow},
	})
	history := make([]providers.Message, 0, 2*len(turns))
	for _, tr := range turns {
		history = append(history, tr...)
	}
	agent.Sessions.SetHistory(recallInjectionSessionKey, history)
	require.NoError(t, agent.Sessions.Save(recallInjectionSessionKey))
	return al, agent
}

const recallInjectionSessionKey = "recall-injection-session"

// countMarkers returns how many messages in req carry the recall marker.
func countMarkers(req []providers.Message) int {
	n := 0
	for _, m := range req {
		if strings.Contains(m.Content, "[Recalled earlier turns") {
			n++
		}
	}
	return n
}

func requestContains(req []providers.Message, needle string) bool {
	for _, m := range req {
		if strings.Contains(m.Content, needle) {
			return true
		}
	}
	return false
}

func toolMessageContent(req []providers.Message, toolCallID string) (string, bool) {
	for _, m := range req {
		if m.Role == "tool" && m.ToolCallID == toolCallID {
			return m.Content, true
		}
	}
	return "", false
}

// TestRunTurn_RecallInjected_NonceInSecondRequest — B-48 / B-50c / SC-014
// (test 49): turn 1 holds a nonce; Skip advanced past it; the provider's
// first call returns recall_conversation(turn_range:"1-1"); the provider's
// SECOND request contains the nonce and the recall marker; the tool result
// says the text is now in context; set + injected are logged at INFO with
// sizes.
func TestRunTurn_RecallInjected_NonceInSecondRequest(t *testing.T) {
	readLog := captureLogFile(t, logger.INFO)
	const nonce = "N-7f3a-RECALL-NONCE"
	filler := strings.Repeat("x", 400)
	turns := [][]providers.Message{makeTurn(filler+" "+nonce, filler)}
	for i := 0; i < 5; i++ {
		turns = append(turns, makeTurn(fmt.Sprintf("turn %d %s", i+2, filler), filler))
	}
	provider := &recallInjectionProvider{first: map[string]any{"turn_range": "1-1"}}
	al, agent := recallInjectionFixture(t, provider, 200000, 1000, turns)

	// Evict turn 1 from the live window by advancing Skip (archive keeps it).
	// The nonce sits AFTER the filler so the breadcrumb's 80-char snippet of
	// the evicted user line (system message) never carries it — only a real
	// splice of the recalled turn can put it in a request.
	agent.Sessions.TruncateHistory(recallInjectionSessionKey, len(turns)*2-2)
	for _, m := range agent.Sessions.GetHistory(recallInjectionSessionKey) {
		require.NotContains(t, m.Content, nonce, "test setup: the nonce must be evicted from the live window")
	}

	_, err := al.processTaskDirect(context.Background(), agent.ID, "what was the nonce?", recallInjectionSessionKey, "chat-recall-1")
	require.NoError(t, err)
	require.GreaterOrEqual(t, provider.calls(), 2, "the provider must be called again after the recall tool ran")

	req1 := provider.request(1)
	require.False(t, requestContains(req1, nonce), "request 1 must not carry the evicted nonce (precondition)")

	req2 := provider.request(2)
	require.True(t, requestContains(req2, nonce),
		"SC-014: the provider's SECOND request must contain the recalled nonce — got %d messages: %+v", len(req2), roles(req2))
	require.Equal(t, 1, countMarkers(req2), "exactly one recall marker in request 2")
	require.True(t, requestContains(req2, "[Recalled earlier turns 1–1"), "marker must name turns 1–1")

	content, ok := toolMessageContent(req2, "call_recall_1")
	require.True(t, ok, "request 2 must carry the recall tool result")
	require.Equal(t, "Recalled 1 turn(s) (turns 1–1); their text is now in your context", content)

	// The span sits at the BuildMessages position: right after the pinned
	// system message, before the window.
	require.Equal(t, "system", req2[0].Role)
	require.Contains(t, req2[1].Content, "[Recalled earlier turns 1–1", "span must follow the pinned core")

	logs := readLog()
	require.Contains(t, logs, "recall span injected", "FR-043: injection must log at INFO")
	require.Contains(t, logs, "span_tokens", "the INFO line must carry sizes")
}

// TestRunTurn_RecallNonFit_ToolResultStatesIt — B-49 (test 50): with a
// window too small for the span, nothing is spliced, the request lacks the
// nonce and the tool result states the non-fit with N and X.
func TestRunTurn_RecallNonFit_ToolResultStatesIt(t *testing.T) {
	readLog := captureLogFile(t, logger.INFO)
	const nonce = "N-7f3a-RECALL-NONCE-NONFIT"
	huge := strings.Repeat("y", 40000) // ~16k estimator tokens: can never fit B below
	small := strings.Repeat("z", 100)
	turns := [][]providers.Message{makeTurn(huge+" "+nonce, huge)}
	for i := 0; i < 3; i++ {
		turns = append(turns, makeTurn(fmt.Sprintf("turn %d %s", i+2, small), small))
	}
	provider := &recallInjectionProvider{first: map[string]any{"turn_range": "1-1"}}
	al, agent := recallInjectionFixture(t, provider, 12000, 1000, turns)
	agent.Sessions.TruncateHistory(recallInjectionSessionKey, len(turns)*2-2)

	_, err := al.processTaskDirect(context.Background(), agent.ID, "what was the nonce?", recallInjectionSessionKey, "chat-recall-2")
	require.NoError(t, err)
	require.GreaterOrEqual(t, provider.calls(), 2)

	req2 := provider.request(2)
	require.False(t, requestContains(req2, nonce), "a span that does not fit must not reach the provider")
	require.Equal(t, 0, countMarkers(req2), "no marker when nothing was spliced")

	content, ok := toolMessageContent(req2, "call_recall_1")
	require.True(t, ok)
	require.Regexp(t, `^1 turn\(s\) found \(\d+ estimator tokens\) but they do not fit the current window; narrow with turn_range or query$`, content)
	require.NotContains(t, content, "into context")
	require.NotContains(t, content, "in your context")
	require.Nil(t, al.activeRecallSpan(recallInjectionSessionKey), "a refused span is dropped, not left to resurface at the next assembly")

	require.Contains(t, readLog(), "recall span refused", "FR-043: refusal must log at INFO")
}

// TestRunTurn_RecallNotDoubledOnReassembly — B-50 (test 51): after
// injection, a trim-site reassembly (Site-4, provider context-overflow
// retry) must carry the marker exactly once and still carry the nonce.
func TestRunTurn_RecallNotDoubledOnReassembly(t *testing.T) {
	const nonce = "N-7f3a-RECALL-NONCE-REASSEMBLY"
	filler := strings.Repeat("x", 400)
	turns := [][]providers.Message{makeTurn(filler+" "+nonce, filler)}
	for i := 0; i < 5; i++ {
		turns = append(turns, makeTurn(fmt.Sprintf("turn %d %s", i+2, filler), filler))
	}
	provider := &recallInjectionProvider{
		first: map[string]any{"turn_range": "1-1"},
		script: []func() (*providers.LLMResponse, error){
			func() (*providers.LLMResponse, error) { return nil, errors.New("context_length_exceeded") },
		},
	}
	al, agent := recallInjectionFixture(t, provider, 200000, 1000, turns)
	agent.Sessions.TruncateHistory(recallInjectionSessionKey, len(turns)*2-2)

	_, err := al.processTaskDirect(context.Background(), agent.ID, "what was the nonce?", recallInjectionSessionKey, "chat-recall-3")
	require.NoError(t, err)
	require.GreaterOrEqual(t, provider.calls(), 3, "call 2 overflowed; call 3 is the post-reassembly retry")

	req3 := provider.request(3)
	require.Equal(t, 1, countMarkers(req3), "the marker must appear exactly once after a trim-site reassembly")
	require.True(t, requestContains(req3, nonce), "the recalled text must survive the reassembly")
}

// TestRunTurn_RecallReplacedInSameTurn_OneMarker — E20 / DS-10 #4: two
// recalls in one turn → the second replaces the first and the replaced
// span's messages are removed from the in-memory slice before the new one
// is spliced, so the third request carries exactly one marker (turns 2–2)
// and no trace of the first span.
func TestRunTurn_RecallReplacedInSameTurn_OneMarker(t *testing.T) {
	const nonce1 = "N-1111-FIRST-SPAN"
	const nonce2 = "N-2222-SECOND-SPAN"
	filler := strings.Repeat("x", 400)
	turns := [][]providers.Message{
		makeTurn(filler+" "+nonce1, filler),
		makeTurn(filler+" "+nonce2, filler),
	}
	for i := 0; i < 4; i++ {
		turns = append(turns, makeTurn(fmt.Sprintf("turn %d %s", i+3, filler), filler))
	}
	provider := &recallInjectionProvider{
		first: map[string]any{"turn_range": "1-1"},
		script: []func() (*providers.LLMResponse, error){
			func() (*providers.LLMResponse, error) {
				return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
					ID: "call_recall_2", Type: "function", Name: "recall_conversation",
					Arguments: map[string]any{"turn_range": "2-2"},
				}}}, nil
			},
		},
	}
	al, agent := recallInjectionFixture(t, provider, 200000, 1000, turns)
	// Evict turns 1 and 2.
	agent.Sessions.TruncateHistory(recallInjectionSessionKey, len(turns)*2-4)

	_, err := al.processTaskDirect(context.Background(), agent.ID, "recall twice", recallInjectionSessionKey, "chat-recall-4")
	require.NoError(t, err)
	require.GreaterOrEqual(t, provider.calls(), 3)

	req2 := provider.request(2)
	require.True(t, requestContains(req2, nonce1))
	require.Equal(t, 1, countMarkers(req2))

	req3 := provider.request(3)
	require.Equal(t, 1, countMarkers(req3), "the replaced span must be removed before the new one is spliced")
	require.True(t, requestContains(req3, "[Recalled earlier turns 2–2"))
	require.True(t, requestContains(req3, nonce2))
	require.False(t, requestContains(req3, nonce1), "the replaced span's text must be gone")
	// Both receipts are still in the window as tool results (history), but
	// only the second one is backed by injected text.
	c2, ok := toolMessageContent(req3, "call_recall_2")
	require.True(t, ok)
	require.Equal(t, "Recalled 1 turn(s) (turns 2–2); their text is now in your context", c2)
	require.NotNil(t, al.activeRecallSpan(recallInjectionSessionKey))
	require.Equal(t, 2, al.activeRecallSpan(recallInjectionSessionKey).FromTurn)
}

// TestRecallSpanFits_ExactBudget — DS-10 #3: a span whose total lands
// exactly on B is injected (<=, not <). The share args are set generously
// slack (0 window share, absoluteShare far above spanShareTokens) so these
// cases isolate the total trigger.
func TestRecallSpanFits_ExactBudget(t *testing.T) {
	require.True(t, recallSpanFits(100, 60, 10, 30, 1000, 0, 0), "total == B must fit")
	require.False(t, recallSpanFits(100, 60, 10, 31, 1000, 0, 0), "total == B+1 must not fit")
	require.True(t, recallSpanFits(100, 0, 0, 1, 1000, 0, 0))
}

// TestRecallSpanFits_ShareTrigger — M3: recallSpanFits must refuse a span
// that fits the total but pushes the tool-result SHARE of the slice past
// absoluteShare — D6's second trigger (midturn_budget.go). Before this fix
// recallSpanFits checked only the total, so a span like this was admitted
// and spliced, its receipt told the model the text was "now in your
// context", and midTurnWindowCheck — which checks BOTH triggers and runs
// immediately after the splice — then dropped it under the share trigger,
// withdrawing exactly what the receipt just promised.
func TestRecallSpanFits_ShareTrigger(t *testing.T) {
	// Total: 60 (window) + 10 (tools) + 5 (span) = 75 <= 100 (budget): fits.
	// Share: 90 (window) + 20 (span) = 110 > 100 (absoluteShare): does not.
	require.False(t, recallSpanFits(100, 60, 10, 5, 100, 90, 20),
		"total fits but share exceeds absoluteShare — must be refused, not admitted then withdrawn")
	// Share at exactly absoluteShare fits (<=, mirrors the total trigger's own <=).
	require.True(t, recallSpanFits(100, 60, 10, 5, 100, 80, 20), "share == absoluteShare must fit")
	require.False(t, recallSpanFits(100, 60, 10, 5, 100, 80, 21), "share == absoluteShare+1 must not fit")
}

// countPageMarkers returns how many messages in req carry the tool_call_id
// recall page marker (T066-14; distinct from the whole-turn recall marker).
func countPageMarkers(req []providers.Message) int {
	n := 0
	for _, m := range req {
		if strings.Contains(m.Content, "[Recalled tool result page") {
			n++
		}
	}
	return n
}

// TestRunTurn_RecallByIdPageInjected — B-50b / FR-043 (per-page injection
// clause), test 52. A `tool_call_id` recall is injected the same way a
// whole-turn recall is, ONE PAGE AT A TIME: the provider's next request
// carries that page's bytes and nothing else of the result, and a second
// recall in the same turn replaces the first page rather than stacking it.
func TestRunTurn_RecallByIdPageInjected(t *testing.T) {
	const (
		callID = "call_page_1"
		nonce1 = "NONCE-PAGE-ONE-7f3a"
		nonce2 = "NONCE-PAGE-TWO-b91c"
	)
	// The nonces live ONLY in the archived tool result, so neither the
	// window nor the eviction breadcrumb (which quotes the user line) can
	// put them in a request — only a real page splice can.
	result := strings.Repeat("a", 50) + nonce1 + strings.Repeat("b", 100) + nonce2 + strings.Repeat("c", 50)
	require.Greater(t, len(result), 200, "test setup")

	firstTurn := []providers.Message{
		{Role: "user", Content: "search the mailbox"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{
			ID: callID, Type: "function", Name: "read_file",
			Function: &providers.FunctionCall{Name: "read_file", Arguments: "{}"},
		}}},
		{Role: "tool", ToolCallID: callID, Content: result},
	}
	filler := strings.Repeat("x", 200)
	turns := [][]providers.Message{firstTurn}
	for i := 0; i < 4; i++ {
		turns = append(turns, makeTurn(fmt.Sprintf("turn %d %s", i+2, filler), filler))
	}

	page1Len := 50 + len(nonce1) + 20 // covers nonce1, stops well before nonce2
	provider := &recallInjectionProvider{
		first: map[string]any{"tool_call_id": callID, "offset": 0, "length": page1Len},
		script: []func() (*providers.LLMResponse, error){
			func() (*providers.LLMResponse, error) {
				return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
					ID: "call_recall_2", Type: "function", Name: "recall_conversation",
					Arguments: map[string]any{
						"tool_call_id": callID,
						"offset":       page1Len,
						"length":       len(result) - page1Len,
					},
				}}}, nil
			},
		},
	}
	al, agent := recallInjectionFixture(t, provider, 200000, 1000, turns)

	total := 0
	for _, tr := range turns {
		total += len(tr)
	}
	agent.Sessions.TruncateHistory(recallInjectionSessionKey, total-len(firstTurn))
	for _, m := range agent.Sessions.GetHistory(recallInjectionSessionKey) {
		require.NotContains(t, m.Content, nonce1, "test setup: the result must be evicted from the window")
		require.NotContains(t, m.Content, nonce2, "test setup: the result must be evicted from the window")
	}

	_, err := al.processTaskDirect(context.Background(), agent.ID, "what did the search return?", recallInjectionSessionKey, "chat-recall-page")
	require.NoError(t, err)
	require.GreaterOrEqual(t, provider.calls(), 3, "two recalls means three provider calls")

	require.False(t, requestContains(provider.request(1), nonce1), "request 1 must not carry the evicted result")

	// Page 1 — and ONLY page 1 — reaches the provider's second request.
	req2 := provider.request(2)
	require.True(t, requestContains(req2, nonce1), "B-50b: page 1 must be in the provider's second request")
	require.False(t, requestContains(req2, nonce2), "only the addressed page may be injected, not the whole result")
	require.Equal(t, 1, countPageMarkers(req2), "exactly one page marker in request 2")
	require.True(t, requestContains(req2, "total="+fmt.Sprint(len(result))+" chars"),
		"the page framing must state the total size")

	content, ok := toolMessageContent(req2, "call_recall_1")
	require.True(t, ok, "request 2 must carry the recall tool result")
	require.Contains(t, content, "this page is now in your context")
	require.Contains(t, content, fmt.Sprintf("next offset %d", page1Len))

	// Page 2 replaces page 1 in the third request — one page at a time.
	req3 := provider.request(3)
	require.True(t, requestContains(req3, nonce2), "B-50b: page 2 must be in the provider's third request")
	require.Equal(t, 1, countPageMarkers(req3), "the replaced page must not stack (FR-043)")
	content2, ok := toolMessageContent(req3, "call_recall_2")
	require.True(t, ok, "request 3 must carry the second recall tool result")
	require.Contains(t, content2, "end of result reached")
}
