package gateway

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const wu22EngineAnswer = "WU22 exact engine-settled answer"
const wu22EnginePrompt = "Complete this WU22 engine settlement probe."

// Script only the provider edge. The real turn loop, model writes, gateway
// streamer, EventBus and ordinary lifecycle admission/settlement all execute.
type wu22EngineProvider struct{ calls int }

func (*wu22EngineProvider) GetDefaultModel() string { return "wu22-engine-model" }
func (p *wu22EngineProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	p.calls++
	return &providers.LLMResponse{Content: wu22EngineAnswer, FinishReason: "stop"}, nil
}
func (p *wu22EngineProvider) ChatStream(ctx context.Context, messages []providers.Message, defs []providers.ToolDefinition, model string, opts map[string]any, chunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	response, err := p.Chat(ctx, messages, defs, model, opts)
	if err == nil {
		chunk(response.Content)
	}
	return response, err
}

// This embeds the REAL concrete streamer, preserving every optional setter.
// The only interception arms a filesystem fault at the final append boundary;
// it does not replace persistence or invent an error, flag, notice or outcome.
type wu22EngineCutStreamer struct {
	*wsStreamer
	cut *wu22EngineCut
}

type wu22EngineCut struct {
	t             *testing.T
	frames        *wu22GatewayFinalizeHarness
	fail          bool
	finalizations int
	modelBefore   []providers.Message
	streamErr     error
	restore       func()
}

func (s *wu22EngineCutStreamer) Finalize(ctx context.Context, content string) error {
	s.cut.finalizations++
	s.cut.modelBefore = s.cut.frames.store.GetHistory(s.cut.frames.sid)
	if s.cut.fail {
		s.cut.restore = wu22GatewayBlockTranscript(s.cut.t, s.cut.frames)
	}
	s.cut.streamErr = s.wsStreamer.Finalize(ctx, content)
	return s.cut.streamErr // the real gateway error is forwarded unchanged
}

type wu22EngineStreamDelegate struct {
	handler *WSHandler
	cut     *wu22EngineCut
}

func (d *wu22EngineStreamDelegate) GetStreamer(ctx context.Context, channel, chatID, sid string) (bus.Streamer, bool) {
	stream, ok := d.handler.GetStreamer(ctx, channel, chatID, sid)
	if !ok {
		return stream, false
	}
	real, ok := stream.(*wsStreamer)
	require.True(d.cut.t, ok, "SETUP: real gateway streamer, not a replacement capture")
	require.Equal(d.cut.t, d.cut.frames.sid, sid)
	d.cut.frames.streamer = real
	return &wu22EngineCutStreamer{wsStreamer: real, cut: d.cut}, true
}

type wu22EngineFixture struct {
	al       *agent.AgentLoop
	provider *wu22EngineProvider
	cut      *wu22EngineCut
	sub      agent.EventSubscription
}

func wu22EngineSettlementFixture(t *testing.T, fail bool) *wu22EngineFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: t.TempDir(), DefaultModel: config.DefaultModel{Model: "wu22-engine-model"},
				MaxTokens: 4096, MaxToolIterations: 4,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	cfg.Sandbox.ToolPolicies = config.DefaultToolPolicyCeiling()
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	provider := &wu22EngineProvider{}
	al := mustAgentLoop(t, cfg, msgBus, provider)
	handler := newWSHandler(msgBus, al, "")
	handler.webchatCh = newWebchatChannel(handler)
	t.Cleanup(handler.Wait)
	store := al.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	conn := makeTestConn()
	conn.sendCh = make(chan []byte, 256)
	bindTestConnToSession(handler, "wu22-engine-chat", meta.ID, conn)
	frames := &wu22GatewayFinalizeHarness{handler: handler, store: store, sid: meta.ID, conn: conn}
	cut := &wu22EngineCut{t: t, frames: frames, fail: fail}
	msgBus.SetStreamDelegate(&wu22EngineStreamDelegate{handler: handler, cut: cut})
	// Subscribe without replacing the real gateway's synchronous frame tap.
	sub := al.SubscribeEvents(256)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })
	return &wu22EngineFixture{al: al, provider: provider, cut: cut, sub: sub}
}

func wu22EngineTurnEnds(t *testing.T, f *wu22EngineFixture) []agent.TurnEndStatus {
	t.Helper()
	var statuses []agent.TurnEndStatus
	for {
		select {
		case event := <-f.sub.C:
			if event.Kind == agent.EventKindTurnEnd {
				payload, ok := event.Payload.(agent.TurnEndPayload)
				require.True(t, ok)
				assert.Equal(t, f.cut.frames.sid, payload.SessionID)
				statuses = append(statuses, payload.Status)
			}
		default:
			return statuses // the synchronous outer entry has already returned
		}
	}
}

// runAgentLoop already keeps this snapshot solely for test observability.
// Read the bool after the single synchronous turn AND settlement have returned:
// no concurrent writer/follow-up exists, no unsafe access or production accessor.
func wu22EngineReturnedFailed(t *testing.T, al *agent.AgentLoop) bool {
	t.Helper()
	result := reflect.ValueOf(al).Elem().FieldByName("lastTurnResult")
	require.True(t, result.IsValid(), "SETUP: engine's existing result observation exists")
	failed := result.FieldByName("turnFailed")
	require.Equal(t, reflect.Bool, failed.Kind())
	return failed.Bool()
}

func wu22EngineRecord(t *testing.T, f *wu22EngineFixture) *session.LifecycleRecord {
	t.Helper()
	lifecycle := f.al.GetSessionLifecycleStore()
	require.NotNil(t, lifecycle)
	record, err := lifecycle.Load(f.cut.frames.sid)
	require.NoError(t, err, "real ordinary admission/settlement must create the record")
	require.Nil(t, record.SteeredBy, "this must exercise settleOrdinaryRoot, not a steered completion")
	require.NotNil(t, record.ExecutionID, "identity must be minted by real admission")
	return record
}

// Amendment §4.1 / founder #1178 and Chief's engine-level F01 ruling. A wire
// failure is not sufficient: returned error/flag, turn.end and durable ordinary
// lifecycle must all agree. ProcessScheduled pins this real chat's store and
// synchronously owns admission -> runAgentLoop/runTurn -> ordinary settlement.
func TestWU22RealStreamerLateWriteFailsEngineAndLifecycle(t *testing.T) {
	f := wu22EngineSettlementFixture(t, true)
	answer, turnErr := f.al.ProcessScheduled(context.Background(), "mia", f.cut.frames.sid, wu22EnginePrompt, "webchat", "wu22-engine-chat")
	require.Equal(t, 1, f.provider.calls, "instrument: one real provider round, no retry")
	require.Equal(t, 1, f.cut.finalizations, "instrument: actual engine reaches real final streamer once")
	require.Equal(t, []providers.Message{{Role: "user", Content: wu22EnginePrompt}, {Role: "assistant", Content: wu22EngineAnswer}}, f.cut.modelBefore,
		"instrument: final model-window write succeeded before the transcript fault")
	var lateCause *os.PathError
	require.ErrorAs(t, f.cut.streamErr, &lateCause, "instrument: real gateway Finalize returns the actual late filesystem failure")
	var returnedCause *os.PathError
	assert.ErrorAs(t, turnErr, &returnedCause, "§4.1: engine caller must receive the late transcript error, not success")
	assert.Empty(t, answer, "failed final persistence must not return a successful answer")
	assert.True(t, wu22EngineReturnedFailed(t, f.al), "the returned turn outcome must carry TurnFailed, not just the wire flag")
	assert.Equal(t, []agent.TurnEndStatus{agent.TurnEndStatusError}, wu22EngineTurnEnds(t, f), "turn.end must be emitted after the late failure is known")
	record := wu22EngineRecord(t, f)
	assert.Equal(t, session.LifecycleFailed, record.State, "canonical ordinary lifecycle settlement must record failure")
	assert.NotEmpty(t, record.FailedReason, "durable failed state must retain its cause")
	live, _ := wu22GatewayFinalFrames(t, f.cut.frames)
	assert.Equal(t, 1, wu22GatewayNoticeCount(t, live), "the existing save notice must appear exactly once across engine/gateway handling")
	done := wu22GatewayDoneFrames(t, live)
	require.Len(t, done, 1)
	require.NotNil(t, done[0].Stats)
	require.NotNil(t, done[0].Stats.TurnFailed)
	assert.True(t, *done[0].Stats.TurnFailed)
	f.cut.restore()
}

func TestWU22RealStreamerHealthyWriteCompletesEngineAndLifecycle(t *testing.T) {
	f := wu22EngineSettlementFixture(t, false)
	answer, turnErr := f.al.ProcessScheduled(context.Background(), "mia", f.cut.frames.sid, wu22EnginePrompt, "webchat", "wu22-engine-chat")
	require.NoError(t, turnErr)
	assert.Equal(t, wu22EngineAnswer, answer)
	assert.Equal(t, 1, f.provider.calls)
	assert.Equal(t, 1, f.cut.finalizations)
	require.NoError(t, f.cut.streamErr)
	assert.False(t, wu22EngineReturnedFailed(t, f.al))
	assert.Equal(t, []agent.TurnEndStatus{agent.TurnEndStatusCompleted}, wu22EngineTurnEnds(t, f))
	record := wu22EngineRecord(t, f)
	assert.Equal(t, session.LifecycleCompleted, record.State)
	assert.Equal(t, "", record.FailedReason)
	live, _ := wu22GatewayFinalFrames(t, f.cut.frames)
	assert.Equal(t, 0, wu22GatewayNoticeCount(t, live))
	done := wu22GatewayDoneFrames(t, live)
	require.Len(t, done, 1)
	require.NotNil(t, done[0].Stats)
	if done[0].Stats.TurnFailed != nil {
		assert.False(t, *done[0].Stats.TurnFailed)
	}
	entries, err := f.cut.frames.store.ReadTranscript(f.cut.frames.sid)
	require.NoError(t, err)
	var final []session.TranscriptEntry
	for _, entry := range entries {
		if entry.Role == "assistant" {
			final = append(final, entry)
		}
	}
	require.Len(t, final, 1)
	assert.Equal(t, wu22EngineAnswer, final[0].Content)
	assert.Equal(t, "mia", final[0].AgentID)
	var tokens []providers.Message
	for _, raw := range framesOfType(t, live, "token") {
		var frame generated.TokenFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		tokens = append(tokens, providers.Message{Role: "assistant", Content: frame.Content})
	}
	assert.Equal(t, []providers.Message{{Role: "assistant", Content: wu22EngineAnswer}}, tokens, "healthy streamed text is not duplicated")
}

var _ providers.StreamingProvider = (*wu22EngineProvider)(nil)
var _ bus.StreamDelegate = (*wu22EngineStreamDelegate)(nil)
var _ bus.Streamer = (*wu22EngineCutStreamer)(nil)
