package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type b3ToolInvocation struct {
	ctx                    context.Context
	id, sid, channel, chat string
	owner                  *executionDisposition
	readErr                error
	release, done          chan struct{}
	once                   sync.Once
}

func (i *b3ToolInvocation) open() { i.once.Do(func() { close(i.release) }) }

type b3AsyncTool struct {
	tools.BaseTool
	al    *AgentLoop
	name  string
	mu    sync.Mutex
	calls []*b3ToolInvocation
}

func (p *b3AsyncTool) Name() string      { return p.name }
func (*b3AsyncTool) Description() string { return "B3 controlled external completion edge" }
func (*b3AsyncTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (*b3AsyncTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (*b3AsyncTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	return tools.ErrorResult("B3 fixture requires async dispatch")
}
func (p *b3AsyncTool) ExecuteAsync(ctx context.Context, _ map[string]any, cb tools.AsyncCallback) *tools.ToolResult {
	i := &b3ToolInvocation{ctx: ctx, id: tools.ToolCallID(ctx), sid: tools.ToolTranscriptSessionID(ctx), channel: tools.ToolChannel(ctx), chat: tools.ToolChatID(ctx), release: make(chan struct{}), done: make(chan struct{})}
	rec, err := p.al.GetSessionLifecycleStore().Load(i.sid)
	i.readErr = err
	if err == nil {
		i.owner = p.al.executionDispositionFor(p.al.executionClaimFor(rec))
	}
	p.mu.Lock()
	p.calls = append(p.calls, i)
	p.mu.Unlock()
	go func() { defer close(i.done); <-i.release; cb(ctx, &tools.ToolResult{ForLLM: b3PrivateBody}) }()
	return &tools.ToolResult{ForLLM: "B3 asynchronous operation started", Async: true}
}
func (p *b3AsyncTool) invocations() []*b3ToolInvocation {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*b3ToolInvocation(nil), p.calls...)
}
func b3JoinTool(t *testing.T, i *b3ToolInvocation) {
	t.Helper()
	select {
	case <-i.done:
	case <-time.After(b3JoinWatchdog):
		t.Fatal("INVALID fixture: actual async callback did not join")
	}
}

// b3ShortenPublishRetryTimers replaces the shipped 5s per-attempt publish
// timeout and 1s/2s retry backoff (pkg/agent/async_notify_retry.go) with
// millisecond-scale values, so the delivery-budget oracles below — three REAL
// attempts through the real finished-tool callback against a full inbound
// queue — stop blocking ~18s per callback on wall-clock constants. The oracles
// are unchanged: only the two timer values are scaled, the attempt budget,
// backoff arithmetic (1 unit after attempt 1, 2 units after attempt 2) and
// failure mode (ctx-bounded full-queue send) are untouched. Restores the
// shipped values via t.Cleanup; LIFO ordering runs this restore AFTER the
// test's own drain/join cleanups, so no in-flight publish survives into it.
// RetryContract keeps the real timers and remains the owner of the
// real-timer arithmetic.
func b3ShortenPublishRetryTimers(t *testing.T) {
	t.Helper()
	prevTimeout, prevUnit := asyncNotifyPublishTimeout, asyncNotifyBackoffUnit
	asyncNotifyPublishTimeout = 20 * time.Millisecond
	asyncNotifyBackoffUnit = time.Millisecond
	t.Cleanup(func() {
		asyncNotifyPublishTimeout = prevTimeout
		asyncNotifyBackoffUnit = prevUnit
	})
}

func TestAsyncNotifierB3ProducerContract(t *testing.T) {
	t.Run("LateForLLMCallbackActualIdentityAndOrigin", func(t *testing.T) { b3RealProducer(t, "b3_finished_probe", 1, false) })
	t.Run("SameToolReverseCallbacksKeepTheirActualCallIDs", func(t *testing.T) { b3RealProducer(t, "b3_finished_probe", 2, false) })
	t.Run("InvalidActualToolNameUsesSafeFallback", func(t *testing.T) { b3RealProducer(t, "3b_invalid", 1, false) })
	t.Run("SuccessfulLateCallbackQueuesOnceWithoutWarning", func(t *testing.T) { b3RealProducer(t, "b3_finished_probe", 1, true) })
}

func b3RealProducer(t *testing.T, name string, count int, success bool) {
	b3ShortenPublishRetryTimers(t)
	calls := make([]providers.ToolCall, count)
	for index := range calls {
		calls[index] = providers.ToolCall{ID: fmt.Sprintf("b3-real-call-%d", index), Function: &providers.FunctionCall{Name: name, Arguments: "{}"}}
	}
	provider := testutil.NewScenario().WithToolCalls(calls).WithText("B3 parent completed naturally")
	al, mb, _ := newAsyncChildPublicationTestLoop(t, provider, false)
	p := &b3AsyncTool{al: al, name: name}
	al.RegisterTool(p)
	setAskPolicyForAllAgents(t, al, name, config.ToolPolicyAllow)
	path := b3CaptureNative(t)
	t.Cleanup(func() {
		b3DrainInbound(mb)
		for _, i := range p.invocations() {
			i.open()
			b3DrainInbound(mb)
			b3JoinTool(t, i)
		}
	})
	// Public external-channel entry creates the real transcript session itself.
	answer, err := al.ProcessDirectWithChannelAs(context.Background(), testDefaultAgentID, "Run the controlled async operation", "agent:"+testDefaultAgentID+":b3-producer", "telegram.b3-origin", "b3-origin-chat")
	require.NoError(t, err)
	require.Equal(t, "B3 parent completed naturally", answer)
	actual := p.invocations()
	require.Len(t, actual, count, "SETUP: actual registered tool must have been invoked")
	sid := actual[0].sid
	require.NotEmpty(t, sid)
	for _, i := range actual {
		require.NotEmpty(t, i.id)
		require.NoError(t, i.readErr, "SETUP: real producing lifecycle must be readable at actual dispatch")
		require.Equal(t, sid, i.sid)
		require.NotNil(t, i.owner)
		select {
		case <-i.owner.done:
		case <-time.After(b3JoinWatchdog):
			t.Fatal("INVALID fixture: real producing parent owner did not join")
		}
		i.owner.mu.Lock()
		settlement := i.owner.result
		i.owner.mu.Unlock()
		require.NoError(t, settlement)
		require.Nil(t, al.executionDispositionFor(i.owner.claim))
		require.Nil(t, al.ownLiveTurn(sid))
	}
	rec, err := session.NewLifecycleStore(al.GetSessionLifecycleStore().Dir()).Load(sid)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleCompleted, rec.State)
	require.NoError(t, al.RecordLastChannel("telegram.b3-foreign"))
	require.NoError(t, al.RecordLastChatID("b3-foreign-chat"))
	sub := al.SubscribeEvents(256)
	defer al.UnsubscribeEvents(sub.ID)
	if !success {
		b3FillInbound(t, mb)
	}
	for index := len(actual) - 1; index >= 0; index-- {
		actual[index].open()
		b3JoinTool(t, actual[index])
		if !success {
			rows := b3Rows(t, al, sid)
			expectedCount := len(actual) - index
			assert.Len(t, rows, expectedCount, "each real callback needs its own one saved identity; missing notice is behavioral RED")
			out := b3DrainOutbound(mb)
			assert.Len(t, out, 1, "finished callback warning must reach exactly its own originating conversation")
			safeName := name
			if stringsInvalidB3ToolName(name) {
				safeName = "background tool"
			}
			want := fmt.Sprintf("Tool %s [%s] finished, but the agent was not woken by this notification.", safeName, b3Ref(actual[index].id))
			if len(rows) >= expectedCount {
				assert.Equal(t, want, rows[expectedCount-1].Content)
				assert.Empty(t, rows[expectedCount-1].Status)
			}
			if len(out) == 1 {
				assert.Equal(t, actual[index].channel, out[0].Channel)
				assert.Equal(t, actual[index].chat, out[0].ChatID)
				assert.Equal(t, sid, out[0].SessionID)
				assert.Equal(t, want, out[0].Content)
				assert.Empty(t, out[0].AgentID)
				assert.Empty(t, out[0].WorkspaceID)
			}
		}
	}
	queued := 0
	for {
		select {
		case e := <-sub.C:
			if e.Kind == EventKindFollowUpQueued {
				queued++
			}
		default:
			goto drained
		}
	}
drained:
	if success {
		require.Len(t, b3DrainInbound(mb), 1)
		assert.Equal(t, 1, queued)
		assert.Empty(t, b3Rows(t, al, sid))
		assert.Empty(t, b3DrainOutbound(mb))
		assert.Empty(t, b3Logs(t, path, b3AttemptMessage))
	} else {
		assert.Equal(t, 0, queued)
		logs := b3Logs(t, path, b3AttemptMessage)
		assert.Len(t, logs, 3*count, "one actual tool completion must not become tool reruns; three delivery attempts per callback only")
		if len(logs) > 0 {
			valid := name
			if stringsInvalidB3ToolName(name) {
				valid = ""
			}
			for _, record := range logs {
				actualID := actual[0].id
				if ref, present := record["tool_ref"]; present {
					matched := false
					for _, invocation := range actual {
						if ref == b3Ref(invocation.id) {
							actualID = invocation.id
							matched = true
						}
					}
					assert.True(t, matched, "diagnostic reference must belong to one of the actual callbacks")
				}
				b3AssertDiagnostic(t, []map[string]any{record}, false, "asynchronous update", sid, valid, actualID)
			}
		}
	}
	b3DrainInbound(mb)
	t.Logf("producer controls: actual_callbacks=%d actual_parent_completed=true actual_owner_joined=true callbacks_joined=true", len(actual))
}
func stringsInvalidB3ToolName(name string) bool {
	if len(name) < 1 || len(name) > 96 {
		return true
	}
	letter := func(b byte) bool { return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' }
	if !letter(name[0]) {
		return true
	}
	for _, b := range []byte(name) {
		if !letter(b) && (b < '0' || b > '9') && b != '_' && b != '.' && b != ':' && b != '-' {
			return true
		}
	}
	return false
}
