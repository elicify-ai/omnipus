// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Founder model: after /clear "the sliding window moves ahead, the next turn
// sends system prompt + instructions only"; the history stays and
// recall_conversation reaches it. These tests drive the REAL turn path and read
// what the provider is actually sent.

type clearScriptProvider struct {
	mu       sync.Mutex
	requests [][]providers.Message
	// behave decides call n (1-based); nil means answer "ok".
	behave func(n int, ctx context.Context) (*providers.LLMResponse, error)
}

func (p *clearScriptProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	n := len(p.requests)
	p.mu.Unlock()
	if p.behave != nil {
		return p.behave(n, ctx)
	}
	return &providers.LLMResponse{Content: "ok"}, nil
}
func (p *clearScriptProvider) GetDefaultModel() string { return "scripted" }
func (p *clearScriptProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}
func (p *clearScriptProvider) request(n int) []providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests[n-1]
}

func clearMessageText(m providers.Message) string {
	var b strings.Builder
	b.WriteString(m.Content)
	for _, part := range m.SystemParts {
		b.WriteString("\n")
		b.WriteString(part.Text)
	}
	for _, tc := range m.ToolCalls {
		b.WriteString("\n")
		if tc.Function != nil {
			b.WriteString(tc.Function.Name + tc.Function.Arguments)
		}
	}
	return b.String()
}

// requireNothingFromBeforeTheClear asserts a request is exactly the system
// prompt plus the new user message: no earlier user, assistant or tool message,
// no archive breadcrumb or summary, no recalled span.
func requireNothingFromBeforeTheClear(t *testing.T, req []providers.Message, newUser string, forbidden ...string) {
	t.Helper()
	require.GreaterOrEqual(t, len(req), 2)
	require.Equal(t, "system", req[0].Role, "the system prompt and instructions lead the request")
	rest := req[1:]
	require.Len(t, rest, 1, "only the new user message follows the system prompt; got %d messages", len(rest))
	require.Equal(t, "user", rest[0].Role)
	require.Contains(t, rest[0].Content, newUser)
	for _, m := range req {
		text := clearMessageText(m)
		for _, f := range forbidden {
			require.NotContains(t, text, f, "a %s message carries pre-clear content", m.Role)
		}
		require.NotContains(t, text, "Earlier in this conversation", "no archive breadcrumb survives the clear")
	}
}

type clearCtxFixture struct {
	*busyClearFixture
	prov *clearScriptProvider
}

func (f *clearCtxFixture) waitIdle(t *testing.T) {
	t.Helper()
	waitUntilClear(t, "the worker to go idle", 15*time.Second, func() bool { return !f.w.inTurn.Load() })
}

// seedPreClear runs one real turn and adds a real tool call + result to the
// model window, then makes a recall_conversation call (which freezes a recall
// span for this session).
func (f *clearCtxFixture) seedPreClear(t *testing.T) {
	t.Helper()
	require.True(t, f.w.enqueue(f.human("PRECLEAR-USER-ALPHA please remember this")))
	waitUntilClear(t, "the first answer", 15*time.Second, func() bool { return f.prov.count() >= 1 })
	f.waitIdle(t)

	key := "agent:" + testDefaultAgentID + ":session:" + f.sid
	ctx := context.Background()
	asst, _, err := f.store.AppendModelMessage(ctx, key, session.ModelAppend{
		Message: providers.Message{Role: "assistant", Content: "PRECLEAR-ASSISTANT-TOOLCALL", ToolCalls: []providers.ToolCall{{
			ID: "call_pre", Type: "function", Function: &providers.FunctionCall{Name: "noop", Arguments: "{}"}}}},
		ViewMembership: session.ViewMembershipModel, Source: session.EntrySource{Kind: "agent"},
	})
	require.NoError(t, err)
	_, _, err = f.store.AppendModelMessage(ctx, key, session.ModelAppend{
		Message:        providers.Message{Role: "tool", ToolCallID: "call_pre", Content: "PRECLEAR-TOOL-RESULT"},
		ViewMembership: session.ViewMembershipModel, Source: session.EntrySource{Kind: "agent"}, ToolResultFor: &asst.Addr,
	})
	require.NoError(t, err)

	inst, ok := f.al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	recall := NewRecallConversationTool(inst.Sessions, f.al)
	res := recall.Execute(tools.WithSessionKey(ctx, key), map[string]any{"query": "PRECLEAR-USER-ALPHA"})
	require.False(t, res.IsError, res.ForLLM)
	require.NotNil(t, f.al.activeRecallSpan(key), "precondition: the recall froze a span for this session")
}

func (f *clearCtxFixture) recallStillReaches(t *testing.T) {
	t.Helper()
	key := "agent:" + testDefaultAgentID + ":session:" + f.sid
	inst, ok := f.al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	recall := NewRecallConversationTool(inst.Sessions, f.al)
	res := recall.Execute(tools.WithSessionKey(context.Background(), key), map[string]any{"query": "PRECLEAR-USER-ALPHA"})
	require.False(t, res.IsError, res.ForLLM)
	span := f.al.activeRecallSpan(key)
	require.NotNil(t, span, "recall_conversation reached the pre-clear entries")
	found := false
	for _, m := range span.Messages() {
		if strings.Contains(m.Content, "PRECLEAR-USER-ALPHA") {
			found = true
		}
	}
	require.True(t, found, "the pre-clear user message is recalled verbatim")
}

func newClearCtxFixture(t *testing.T, behave func(int, context.Context) (*providers.LLMResponse, error)) *clearCtxFixture {
	t.Helper()
	prov := &clearScriptProvider{behave: behave}
	return &clearCtxFixture{busyClearFixture: newBusyClearFixture(t, prov), prov: prov}
}

var preClearWords = []string{"PRECLEAR-USER-ALPHA", "PRECLEAR-ASSISTANT-TOOLCALL", "PRECLEAR-TOOL-RESULT", "PRECLEAR-ASSISTANT-REPLY"}

func TestClearThenNextRequest_Idle(t *testing.T) {
	f := newClearCtxFixture(t, func(n int, _ context.Context) (*providers.LLMResponse, error) {
		if n == 1 {
			return &providers.LLMResponse{Content: "PRECLEAR-ASSISTANT-REPLY"}, nil
		}
		return &providers.LLMResponse{Content: "fresh"}, nil
	})
	f.seedPreClear(t)

	require.True(t, f.w.enqueue(f.persistClearRow(t)))
	waitUntilClear(t, "the /clear marker", 15*time.Second, func() bool { return f.markerCount(t) == 1 })
	f.waitIdle(t)

	before := f.prov.count()
	require.True(t, f.w.enqueue(f.human("POSTCLEAR-USER-GAMMA what now?")))
	waitUntilClear(t, "the post-clear request", 15*time.Second, func() bool { return f.prov.count() > before })
	requireNothingFromBeforeTheClear(t, f.prov.request(before+1), "POSTCLEAR-USER-GAMMA", preClearWords...)

	f.recallStillReaches(t)
}

func TestClearThenNextRequest_WhileBusy(t *testing.T) {
	f := newClearCtxFixture(t, func(n int, ctx context.Context) (*providers.LLMResponse, error) {
		switch n {
		case 1:
			return &providers.LLMResponse{Content: "PRECLEAR-ASSISTANT-REPLY"}, nil
		case 2: // the turn that is running when /clear arrives
			<-ctx.Done()
			return nil, ctx.Err()
		default:
			return &providers.LLMResponse{Content: "fresh"}, nil
		}
	})
	f.seedPreClear(t)

	require.True(t, f.w.enqueue(f.human("BUSY-TURN-DELTA work on something long")))
	waitUntilClear(t, "the busy turn to start", 15*time.Second, func() bool { return f.prov.count() >= 2 && f.w.inTurn.Load() })

	require.True(t, f.w.enqueue(f.persistClearRow(t)))
	waitUntilClear(t, "the /clear marker", 20*time.Second, func() bool { return f.markerCount(t) == 1 })
	f.waitIdle(t)

	before := f.prov.count()
	require.True(t, f.w.enqueue(f.human("POSTCLEAR-USER-GAMMA what now?")))
	waitUntilClear(t, "the post-clear request", 15*time.Second, func() bool { return f.prov.count() > before })
	requireNothingFromBeforeTheClear(t, f.prov.request(before+1), "POSTCLEAR-USER-GAMMA",
		append(preClearWords, "BUSY-TURN-DELTA")...)

	f.recallStillReaches(t)
}

func TestWithExpiryNote(t *testing.T) {
	require.Equal(t, "crumb", withExpiryNote("crumb", ""))
	require.Equal(t, "## Retention notice\nnote", withExpiryNote("", "note"))
	require.Equal(t, "crumb\n\n## Retention notice\nnote", withExpiryNote("crumb", "note"))
}

// FR-007 end to end on the real turn path: an expiry notice stored with the
// window reaches the next request's system prompt.
func TestExpiryNoticeReachesTheNextRequest(t *testing.T) {
	f := newClearCtxFixture(t, nil)
	require.True(t, f.w.enqueue(f.human("first")))
	waitUntilClear(t, "the first answer", 15*time.Second, func() bool { return f.prov.count() >= 1 })
	f.waitIdle(t)

	metaPath := filepath.Join(f.store.BaseDir(), f.sid, "backend_meta.json")
	raw, err := os.ReadFile(metaPath)
	require.NoError(t, err)
	var meta map[string]any
	require.NoError(t, json.Unmarshal(raw, &meta))
	meta["expiry_note"] = "EXPIRY-NOTE-SENTINEL"
	raw, err = json.Marshal(meta)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(metaPath, raw, 0o600))

	before := f.prov.count()
	require.True(t, f.w.enqueue(f.human("second")))
	waitUntilClear(t, "the second request", 15*time.Second, func() bool { return f.prov.count() > before })
	req := f.prov.request(before + 1)
	require.Equal(t, "system", req[0].Role)
	require.Contains(t, clearMessageText(req[0]), "## Retention notice\nEXPIRY-NOTE-SENTINEL")
}
