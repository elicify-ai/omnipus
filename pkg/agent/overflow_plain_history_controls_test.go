package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func plainHistoryStore(t *testing.T, agent *AgentInstance) session.ContextWindowStore {
	t.Helper()
	store, ok := agent.Sessions.(session.ContextWindowStore)
	require.True(t, ok, "fixture: use real atomic window storage")
	return store
}

func observePlainHistoryRelief(al *AgentLoop) func() []EventKind {
	var mu sync.Mutex
	var kinds []EventKind
	al.SetEventSyncTap(func(e Event) {
		if e.Kind == EventKindLLMRetry || e.Kind == EventKindContextCompress {
			mu.Lock()
			kinds = append(kinds, e.Kind)
			mu.Unlock()
		}
	})
	return func() []EventKind {
		mu.Lock()
		defer mu.Unlock()
		return append([]EventKind(nil), kinds...)
	}
}

func runPlainHistoryNoProgress(t *testing.T, history []providers.Message, rejection error) {
	t.Helper()
	const key, trigger = "agent:mia:plain-no-progress", "current user cannot be evicted"
	p := plainHistoryRejectOnce(rejection, "UNAUTHORIZED unchanged recovery")
	al, agent, _ := newPlainHistoryLoop(t, p)
	seedPlainHistory(t, agent, key, history)
	store := plainHistoryStore(t, agent)
	var sentState memory.WindowSnapshot
	p.observe = func(call int, _ plainHistoryCapture) {
		if call == 1 {
			var err error
			sentState, err = store.SnapshotWindow(context.Background(), key)
			require.NoError(t, err)
		}
	}
	reliefEvents := observePlainHistoryRelief(al)
	// Preserve the fresh Anthropic fixture's real inbound routing path too.
	_, routed, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel: "test", ChatID: "plain-no-progress-chat", Sender: bus.SenderInfo{CanonicalID: "user1"}, SessionKey: key, Content: trigger,
	})
	require.Same(t, agent, routed, "fixture: the exact seeded scope must execute under Mia")
	assertPlainHistoryFirstAttempt(t, agent, p, trigger)
	assertPlainHistoryRejection(t, err, rejection)
	require.Len(t, p.recorded(), 1, "§2/3: no changed candidate means no unchanged retry")
	require.Empty(t, reliefEvents(), "§4: no recovery/compression event without real progress")
	after, snapshotErr := store.SnapshotWindow(context.Background(), key)
	require.NoError(t, snapshotErr)
	require.Equal(t, sentState, after, "a rejected no-progress staging operation must not install a cursor, projection, anchor or archive change")
}

func TestContextOverflowPlain_NoPriorCompletedAssistantDoesNotRetry(t *testing.T) {
	cases := []struct {
		name    string
		history []providers.Message
	}{
		{"fresh", nil},
		{"user-only", []providers.Message{{Role: "user", Content: strings.Repeat("uncompleted user text ", 256)}}},
		{"newest-assistant-only", plainHistoryExchange(1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runPlainHistoryNoProgress(t, tc.history, errors.New("context_length_exceeded "+tc.name))
		})
	}
}

func TestContextOverflowPlain_TinyFramingGrowthDoesNotCommitOrRetry(t *testing.T) {
	// §3 derives a >=254-byte system-framing floor BEFORE snippets/ranges.
	// Both older exchanges plus their JSON roles fit below that floor, so
	// even staging every eligible endpoint cannot yield a smaller request.
	for _, source := range []string{"", "x", "old"} {
		t.Run("source-"+source, func(t *testing.T) {
			history := []providers.Message{
				{Role: "user", Content: source}, {Role: "assistant", Content: source},
				{Role: "user", Content: source}, {Role: "assistant", Content: source},
				{Role: "assistant", Content: "newest floor"},
			}
			runPlainHistoryNoProgress(t, history, errors.New("context_length_exceeded tiny source"))
		})
	}
}

func plainHistoryCheckpointFixture(t *testing.T, key string, history []providers.Message, current providers.Message) (*AgentLoop, *AgentInstance, *turnState, []providers.Message) {
	t.Helper()
	p := plainHistoryRejectOnce(errors.New("context_length_exceeded"), "unused")
	al, agent, _ := newPlainHistoryLoop(t, p)
	seedPlainHistory(t, agent, key, history)
	ts := newTurnState(agent, processOptions{SessionKey: key, UserMessage: current.Content, Media: current.Media}, turnEventScope{turnID: key})
	ts.ctx = context.Background()
	require.NoError(t, ts.contextWindowError())
	_, appendErr := ts.appendWindowMessage(current, windowProducerUser)
	require.NoError(t, appendErr)
	messages := append([]providers.Message{{Role: "system", Content: "PINNED instruction must remain"}}, agent.Sessions.GetHistory(key)...)
	return al, agent, ts, messages
}

func TestContextOverflowPlain_ProtectedOrUnmappedPrefixBlocksCut(t *testing.T) {
	for _, blocker := range []string{"protected", "unmapped"} {
		t.Run(blocker, func(t *testing.T) {
			const key = "agent:mia:plain-blocker"
			control := providers.Message{Role: "user", Content: "CONTROL do not consume after rejection", Media: []string{"media://control-identity"}}
			history := append([]providers.Message{control}, plainHistoryExchange(1)...)
			history = append(history, plainHistoryExchange(2)...)
			al, agent, ts, messages := plainHistoryCheckpointFixture(t, key, history, providers.Message{Role: "user", Content: "initiating user"})
			if blocker == "protected" {
				ts.protectWindowControls([]providers.Message{control})
			} else {
				// A real request-only message precedes every endpoint, not an
				// invented archive index. Mapping must not jump around it.
				messages = append(messages[:1], append([]providers.Message{{Role: "user", Content: "UNMAPPED request hook"}}, messages[1:]...)...)
			}
			before, err := plainHistoryStore(t, agent).SnapshotWindow(context.Background(), key)
			require.NoError(t, err)
			rejection := errors.New("context_length_exceeded blocked prefix")
			p := plainHistoryRejectOnce(rejection, "unused")
			rt := &agentLoopRunTurn{al: al, ts: ts, turnCtx: context.Background(), activeProvider: p, llmModel: "test-model"}
			_, err = rt.callProviderOnce(messages, nil)
			require.ErrorIs(t, err, rejection, "failed send must expose context rejection")
			require.Len(t, p.recorded(), 1)
			if blocker == "protected" {
				require.Equal(t, []providers.Message{control}, ts.windowControls, "a rejected send is not a control-consumption receipt")
			}
			candidate, changed, err := al.checkpointWindow(context.Background(), ts, messages, nil, true)
			require.NoError(t, err)
			require.False(t, changed, "FR-030: never cross a prefix blocker to reach a later plain island")
			require.Equal(t, messages, candidate)
			after, err := plainHistoryStore(t, agent).SnapshotWindow(context.Background(), key)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestContextOverflowPlain_InvalidToolStructuresRejectedBeforeSend(t *testing.T) {
	call := providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("owned", "exact-argument")}}
	result := providers.Message{Role: "tool", ToolCallID: "owned", Content: "owned result"}
	cases := []struct {
		name   string
		group  []providers.Message
		reason string
	}{
		{"missing", []providers.Message{call}, "incomplete or invalid tool-result group"},
		{"duplicate", []providers.Message{call, result, result}, "incomplete or invalid tool-result group"},
		{"undeclared", []providers.Message{call, {Role: "tool", ToolCallID: "wrong", Content: "wrong owner"}}, "incomplete or invalid tool-result group"},
		{"orphan", []providers.Message{result}, "orphan tool result"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			history := append(plainHistoryExchange(1), tc.group...)
			history = append(history, providers.Message{Role: "assistant", Content: "newest floor"})
			al, agent, ts, messages := plainHistoryCheckpointFixture(t, "agent:mia:invalid-"+tc.name, history, providers.Message{Role: "user", Content: "trigger"})
			before, err := plainHistoryStore(t, agent).SnapshotWindow(context.Background(), ts.sessionKey)
			require.NoError(t, err)
			p := plainHistoryRejectOnce(errors.New("context_length_exceeded"), "MUST NOT SEND")
			rt := &agentLoopRunTurn{al: al, ts: ts, turnCtx: context.Background(), activeProvider: p, llmModel: "test-model"}
			rf := &agentLoopRunTurnFallbacks{rt: rt, callMessages: messages}
			rq := &agentLoopRunTurnRequest{ri: &agentLoopRunTurnIteration{rf: rf, messages: messages}}
			err = rq.checkpointRequest(false)
			require.ErrorContains(t, err, "context request: "+tc.reason, "invalidity must be refused before a plain cut can hide it")
			_, err = rt.callProviderOnce(messages, nil)
			require.ErrorContains(t, err, "context request: "+tc.reason, "send boundary must independently reject the invalid structure")
			require.Empty(t, p.recorded(), "zero serialization/provider sends")
			after, err := plainHistoryStore(t, agent).SnapshotWindow(context.Background(), ts.sessionKey)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestContextOverflowPlain_TrailingNarrationKeepsToolFallbackClassification(t *testing.T) {
	const key = "agent:mia:plain-fallback"
	control := providers.Message{Role: "user", Content: "protected prefix prevents group eviction"}
	oldSource, newestSource := strings.Repeat("古🙂é", 2048), strings.Repeat("新界🙂é", 2049)
	history := []providers.Message{
		control,
		{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("old", "old")}},
		{Role: "tool", ToolCallID: "old", Content: oldSource},
		{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("z-first", "z-first"), toolCallFor("a-second", "a-second")}},
		// Response order differs from declaration order. Newest fallback must
		// still halve z-first first, not sort IDs or follow result position.
		{Role: "tool", ToolCallID: "a-second", Content: "a-second exact untouched source"},
		{Role: "tool", ToolCallID: "z-first", Content: newestSource},
		{Role: "assistant", Content: "newest plain assistant floor"},
	}
	al, agent, ts, messages := plainHistoryCheckpointFixture(t, key, history, providers.Message{Role: "user", Content: "current"})
	ts.protectWindowControls([]providers.Message{control})
	rejection := errors.New("context_length_exceeded fallback control")
	p := &plainHistoryProvider{reply: func(int) (*providers.LLMResponse, error) { return nil, rejection }}
	rt := &agentLoopRunTurn{al: al, ts: ts, turnCtx: context.Background(), activeProvider: p, llmModel: "test-model"}
	_, sendErr := rt.callProviderOnce(messages, nil)
	require.ErrorIs(t, sendErr, rejection)
	original, err := plainHistoryStore(t, agent).SnapshotWindow(context.Background(), key)
	require.NoError(t, err)
	first, changed, err := al.checkpointWindow(context.Background(), ts, messages, nil, true)
	require.NoError(t, err)
	require.True(t, changed, "blocked eviction falls through to older-result emptying")
	_, sendErr = rt.callProviderOnce(first, nil)
	require.ErrorIs(t, sendErr, rejection)
	firstSnap, err := plainHistoryStore(t, agent).SnapshotWindow(context.Background(), key)
	require.NoError(t, err)
	oldKey := memory.ProjectionKey{ToolCallID: "old", ArchiveLine: 2}
	require.Equal(t, memory.ProjectionEmptied, firstSnap.State.Projection.Entries[oldKey])
	require.Zero(t, firstSnap.State.Projection.SourceRunes[oldKey])
	require.Equal(t, 0, firstSnap.State.Skip)
	second, changed, err := al.checkpointWindow(context.Background(), ts, first, nil, true)
	require.NoError(t, err)
	require.True(t, changed, "next fallback is newest TOOL group despite trailing plain assistant")
	_, sendErr = rt.callProviderOnce(second, nil)
	require.ErrorIs(t, sendErr, rejection)
	captures := p.recorded()
	require.Len(t, captures, 3, "instrument: original and each manually checkpointed candidate reach the normalized provider seam")
	for i := 1; i < len(captures); i++ {
		require.Less(t, len(captures[i].body), len(captures[i-1].body), "fallback has real serialized shrink including request-only notices and all required framing")
	}
	t.Logf("instrument: normalized fallback requests %d -> %d -> %d bytes", len(captures[0].body), len(captures[1].body), len(captures[2].body))
	secondSnap, err := plainHistoryStore(t, agent).SnapshotWindow(context.Background(), key)
	require.NoError(t, err)
	newKey := memory.ProjectionKey{ToolCallID: "z-first", ArchiveLine: 5}
	require.Equal(t, memory.ProjectionCapped, secondSnap.State.Projection.Entries[newKey])
	kept := utf8.RuneCountInString(newestSource) / 2
	require.Equal(t, kept, secondSnap.State.Projection.SourceRunes[newKey], "halve original Unicode runes, not UTF-8 bytes or mark text")
	require.NotContains(t, secondSnap.State.Projection.Entries, memory.ProjectionKey{ToolCallID: "a-second", ArchiveLine: 4})
	// Check source structure separately from request-only system notices.
	// The whole forced-payload comparison above still includes those notices.
	body := plainHistoryNonSystem(second)
	require.Len(t, body, len(history)+1)
	require.Equal(t, history[4], body[4], "declared second call's result is untouched")
	runes := []rune(newestSource)
	require.True(t, strings.HasPrefix(body[5].Content, string(runes[:(kept+1)/2])+"\n"))
	require.True(t, strings.HasSuffix(body[5].Content, "\n"+string(runes[len(runes)-kept/2:])))
	require.True(t, utf8.ValidString(body[5].Content))
	require.Contains(t, body[5].Content, "z-first", "addressed recall mark survives halving")
	require.Contains(t, body[2].Content, "old", "older result retains its addressed recall mark")
	require.NotContains(t, body[2].Content, "古", "older source, unlike newest source, is completely emptied")
	expected := append(append([]providers.Message(nil), history...), providers.Message{Role: "user", Content: "current"})
	expected[2].Content, expected[5].Content = body[2].Content, body[5].Content
	require.Equal(t, expected, body, "only the two declared result contents may change; control, calls, arguments, ordering, newest narration and user remain exact")
	require.Equal(t, original.Archive, secondSnap.Archive, "fallback never rewrites original archived source")
}
