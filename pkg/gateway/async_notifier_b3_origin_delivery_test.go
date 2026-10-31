package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const b3GatewayToolName = "b3_gateway_async_probe"
const b3GatewayPrivateBody = "B3_GATEWAY_SYNTHETIC_RESULT_PRIVATE_92 question=fixture-only approval=fixture-only"

type b3GatewayAsyncTool struct {
	tools.BaseTool
	mu                         sync.Mutex
	callID, sid, channel, chat string
	invoked                    bool
	release, done              chan struct{}
	once                       sync.Once
}

func (*b3GatewayAsyncTool) Name() string { return b3GatewayToolName }
func (*b3GatewayAsyncTool) Description() string {
	return "B3 actual callback edge for generated live/history contract"
}
func (*b3GatewayAsyncTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (*b3GatewayAsyncTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (*b3GatewayAsyncTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	return tools.ErrorResult("B3 gateway fixture must dispatch asynchronously")
}
func (p *b3GatewayAsyncTool) ExecuteAsync(ctx context.Context, _ map[string]any, callback tools.AsyncCallback) *tools.ToolResult {
	p.mu.Lock()
	p.callID = tools.ToolCallID(ctx)
	p.sid = tools.ToolTranscriptSessionID(ctx)
	p.channel = tools.ToolChannel(ctx)
	p.chat = tools.ToolChatID(ctx)
	p.invoked = true
	p.mu.Unlock()
	go func() {
		defer close(p.done)
		<-p.release
		callback(ctx, &tools.ToolResult{ForLLM: b3GatewayPrivateBody})
	}()
	return &tools.ToolResult{ForLLM: "B3 asynchronous work started", Async: true}
}
func (p *b3GatewayAsyncTool) open() { p.once.Do(func() { close(p.release) }) }
func b3GatewayJoin(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(45 * time.Second):
		t.Fatal("INVALID fixture: actual gateway completion callback did not join; watchdog is not product RED")
	}
}
func b3GatewayDrain(mb *bus.MessageBus) {
	for {
		select {
		case _, ok := <-mb.InboundChan():
			if !ok {
				return
			}
		default:
			return
		}
	}
}

func TestAsyncNotifierB3OriginLiveReplay(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{Agents: config.AgentsConfig{Defaults: config.AgentDefaults{Home: home, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096, MaxToolIterations: 10}, List: []config.AgentConfig{{ID: "mia", Home: home}}}}
	cfg.Sandbox.ToolPolicies = map[string]string{b3GatewayToolName: "allow"}
	provider := testutil.NewScenario().WithToolCall(b3GatewayToolName, "{}").WithText("B3 gateway parent completed")
	mb := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, mb, provider)
	al.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(agent.NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())), steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())
	probe := &b3GatewayAsyncTool{release: make(chan struct{}), done: make(chan struct{})}
	al.RegisterTool(probe)
	h := newWSHandler(mb, al, "") // Installs the REAL sole synchronous hub tap.
	t.Cleanup(func() {
		al.SetEventSyncTap(nil)
		b3GatewayDrain(mb)
		probe.open()
		probe.mu.Lock()
		invoked := probe.invoked
		probe.mu.Unlock()
		if invoked {
			b3GatewayJoin(t, probe.done)
		}
		mb.Close()
	})
	answer, err := al.ProcessDirectWithChannelAs(context.Background(), "mia", "Run the controlled async tool", "agent:mia:b3-origin-wire", "telegram.b3-origin", "b3-origin-chat")
	require.NoError(t, err)
	require.Equal(t, "B3 gateway parent completed", answer)
	probe.mu.Lock()
	sid, callID, channel, chat, invoked := probe.sid, probe.callID, probe.channel, probe.chat, probe.invoked
	probe.mu.Unlock()
	require.True(t, invoked, "SETUP: an actual registered ForLLM-only tool must reach its real callback")
	require.NotEmpty(t, sid)
	require.NotEmpty(t, callID)
	require.NotEqual(t, "agent:mia:b3-origin-wire", sid, "public external intake must create a real transcript session, never guess it from the routing key")
	life, err := session.NewLifecycleStore(al.GetSessionLifecycleStore().Dir()).Load(sid)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleCompleted, life.State, "SETUP: actual public producer returned through ordinary settlement")
	meta, err := al.GetSessionStore().GetMeta(sid)
	require.NoError(t, err)
	require.Equal(t, "mia", meta.AgentID)
	foreign, err := al.GetSessionStore().NewChannelSession("telegram", "telegram.b3-foreign", chat, "mia", "B3 foreign instance")
	require.NoError(t, err)
	require.NotEqual(t, sid, foreign.ID)
	ownA, a := makeForwarderTestConn(256)
	ownB, b := makeForwarderTestConn(256)
	other, c := makeForwarderTestConn(256)
	bindTestConnToSession(h, "b3-own-a", sid, ownA)
	bindTestConnToSession(h, "b3-own-b", sid, ownB)
	bindTestConnToSession(h, "b3-foreign", foreign.ID, other)
	sub := al.SubscribeEvents(256)
	defer al.UnsubscribeEvents(sub.ID)
	require.Equal(t, 0, len(mb.InboundChan()))
	for i := 0; i < cap(mb.InboundChan()); i++ {
		require.NoError(t, mb.PublishInbound(context.Background(), bus.InboundMessage{Channel: "system", ChatID: fmt.Sprintf("b3-filler-%d", i)}))
	}
	require.NoError(t, al.RecordLastChannel("telegram.b3-foreign"))
	require.NoError(t, al.RecordLastChatID(chat))
	probe.open()
	b3GatewayJoin(t, probe.done)
	queued := 0
	for {
		select {
		case event := <-sub.C:
			if event.Kind == agent.EventKindFollowUpQueued {
				queued++
			}
		default:
			goto eventsDrained
		}
	}
eventsDrained:
	assert.Equal(t, 0, queued, "warning delivery cannot pretend the failed model handoff was queued")
	rows := b3GatewayRows(t, al, sid)
	assert.Len(t, rows, 1, "real producer must save one safe ordinary system warning before live/reload can agree")
	framesA := b3GatewayFrames(t, a)
	framesB := b3GatewayFrames(t, b)
	foreignFrames := b3GatewayFrames(t, c)
	assert.Len(t, framesA, 1, "actual producer → typed error → installed hub must deliver ONE own-session generated replay")
	assert.Len(t, framesB, 1)
	assert.Empty(t, foreignFrames, "same agent is not authority to fan out to another conversation")
	out := []bus.OutboundMessage{}
	for {
		select {
		case m := <-mb.OutboundChan():
			out = append(out, m)
		default:
			goto outboundDrained
		}
	}
outboundDrained:
	assert.Len(t, out, 1, "positive actual connector origin warning required")
	require.Len(t, rows, 1, "B3-5 missing real saved producer warning; do not construct a future ErrorPayload carrier to fabricate it")
	row := rows[0]
	digest := sha256.Sum256([]byte(callID))
	ref := hex.EncodeToString(digest[:])[:12]
	want := "Tool " + b3GatewayToolName + " [" + ref + "] finished, but the agent was not woken by this notification."
	assert.Equal(t, want, row.Content)
	assert.Equal(t, "", row.Status)
	assert.Equal(t, "", row.SystemSubtype)
	assert.NotEmpty(t, row.ID)
	assert.False(t, row.Timestamp.IsZero())
	assert.NotContains(t, row.Content, b3GatewayPrivateBody)
	for _, frames := range [][]generated.ReplayMessageFrame{framesA, framesB} {
		if len(frames) == 1 {
			b3GatewayEqual(t, row, sid, frames[0])
		}
	}
	if len(out) == 1 {
		assert.Equal(t, bus.OutboundMessage{Channel: channel, ChatID: chat, SessionID: sid, Content: row.Content}, out[0])
	}
	fresh, err := session.NewUnifiedStore(al.GetSessionStore().BaseDir())
	require.NoError(t, err)
	view, err := fresh.ReadTranscriptView(sid)
	require.NoError(t, err)
	var replay []generated.ReplayMessageFrame
	_, err = streamReplay(context.Background(), sid, view.Entries, replayStats{}, func(frame any) error {
		if f, ok := frame.(generated.ReplayMessageFrame); ok && f.Id != nil && *f.Id == row.ID {
			replay = append(replay, f)
		}
		return nil
	}, nil, nil, nil, al.GetSessionLifecycleStore())
	require.NoError(t, err)
	require.Len(t, replay, 1)
	b3GatewayEqual(t, row, sid, replay[0])
	reattached, ch := makeForwarderTestConn(256)
	bindTestConnToSession(h, "b3-reattach", sid, reattached)
	assert.Equal(t, sid, h.hubResolveSessionIDForChat("b3-reattach"))
	assert.Empty(t, b3GatewayFrames(t, ch), "reattach alone is not a second publication of the saved notice")
	assert.Empty(t, b3GatewayRows(t, al, foreign.ID))
	t.Log("gateway controls: actual_parent_completed=true real_transcript_bound=true callback_joined=true generated_replay_and_history_compared=true")
}
func b3GatewayRows(t *testing.T, al *agent.AgentLoop, sid string) []session.TranscriptEntry {
	t.Helper()
	store, err := session.NewUnifiedStore(al.GetSessionStore().BaseDir())
	require.NoError(t, err)
	view, err := store.ReadTranscriptView(sid)
	require.NoError(t, err)
	var rows []session.TranscriptEntry
	for _, r := range view.Entries {
		if r.Type == session.EntryTypeSystem && r.Role == "system" && strings.Contains(r.Content, "agent was not woken by this notification") {
			rows = append(rows, r)
		}
	}
	return rows
}
func b3GatewayFrames(t *testing.T, ch <-chan []byte) []generated.ReplayMessageFrame {
	t.Helper()
	var frames []generated.ReplayMessageFrame
	for {
		select {
		case raw := <-ch:
			var head map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &head))
			var typ string
			require.NoError(t, json.Unmarshal(head["type"], &typ))
			assert.NotEqual(t, string(generated.WsFrameTypeError), typ, "saved ordinary system warning must not also produce generic ErrorFrame")
			if typ == string(generated.WsFrameTypeReplayMessage) {
				var f generated.ReplayMessageFrame
				require.NoError(t, json.Unmarshal(raw, &f))
				frames = append(frames, f)
			}
		default:
			return frames
		}
	}
}
func b3GatewayEqual(t *testing.T, row session.TranscriptEntry, sid string, f generated.ReplayMessageFrame) {
	t.Helper()
	assert.Equal(t, string(generated.WsFrameTypeReplayMessage), f.Type)
	assert.Equal(t, sid, f.SessionId)
	assert.Equal(t, "system", f.Role)
	assert.Equal(t, row.Content, f.Content)
	require.NotNil(t, f.Id)
	assert.Equal(t, row.ID, *f.Id)
	require.NotNil(t, f.Timestamp)
	assert.Equal(t, row.Timestamp.Format(time.RFC3339Nano), *f.Timestamp)
}
