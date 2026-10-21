package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/rooms"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Storage seams only: room state-machine, agent, transcript and ledger stay real.
type fix4RoomJournal struct {
	rooms.Journal
	state, stream string
	fail          atomic.Bool
	readFault     atomic.Bool
	hits          atomic.Int64
}

func (j *fix4RoomJournal) Append(id, stream string, value any) error {
	if j.fail.Load() {
		if stream == j.stream && j.stream != "" {
			j.hits.Add(1)
			return errors.New("injected stream append failure")
		}
		if stream == rooms.StreamDeliveries && j.state != "" {
			data, err := json.Marshal(value)
			if err != nil {
				return err
			}
			var record map[string]any
			if err := json.Unmarshal(data, &record); err != nil {
				return err
			}
			if record["state"] == j.state {
				j.hits.Add(1)
				return errors.New("injected terminal append failure")
			}
		}
	}
	return j.Journal.Append(id, stream, value)
}
func (j *fix4RoomJournal) ReadRecord(id string, out any) error {
	if j.readFault.Load() {
		j.hits.Add(1)
		return errors.New("injected room read failure")
	}
	return j.Journal.ReadRecord(id, out)
}
func fix4InstallJournal(t *testing.T, h *wu15RoomHarness, j *fix4RoomJournal) *roomsHub {
	t.Helper()
	channel, found := h.rs.ChannelManager.GetChannel("webchat")
	require.True(t, found)
	web, ok := channel.(*webchatChannel)
	require.True(t, ok)
	hub := web.wsHandler.rooms
	hub.svc.Close()
	hub.svc = rooms.New(j, rooms.DepsFrom(&gatewayRoomDeps{h: web.wsHandler, home: h.home, hub: hub}))
	h.al.SetRoomRuntime(agentRoomRuntime{svc: hub.svc})
	return hub
}
func fix4WaitTurnSettled(t *testing.T, h *wu15RoomHarness, agentID string) {
	t.Helper()
	require.Eventually(t, func() bool {
		rec, err := h.al.GetSessionLifecycleStore().Load(h.main(agentID))
		return err == nil && rec.State != session.LifecycleRunning && rec.State != session.LifecycleQueued &&
			h.al.GetActiveTurnBySession("agent:"+agentID+":session:"+h.main(agentID)) == nil
	}, 30*time.Second, 10*time.Millisecond, "instrument: actual execution disposition, not only provider/done, has settled")
}

type fix4MirrorWriter struct {
	*session.UnifiedStore
	fail atomic.Bool
	hits atomic.Int64
}

func (w *fix4MirrorWriter) AppendTranscriptOnce(sid string, entry session.TranscriptEntry) (bool, error) {
	if w.fail.Load() && entry.RoomRef != nil && entry.RoomRef.Role == "answer" {
		w.hits.Add(1)
		return false, errors.New("injected main-chat mirror failure")
	}
	return w.UnifiedStore.AppendTranscriptOnce(sid, entry)
}

type fix4AnswerThenFailProvider struct {
	requestID, content string
	cancel             bool
	calls              atomic.Int64
	afterAnswer        chan struct{}
}

func (p *fix4AnswerThenFailProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	if p.calls.Add(1) == 1 {
		return &providers.LLMResponse{FinishReason: "tool_calls", ToolCalls: []providers.ToolCall{{
			ID: "fix4-answer", Type: "function", Name: "answer_request", Arguments: map[string]any{"request_id": p.requestID, "text": p.content},
		}}}, nil
	}
	select {
	case <-p.afterAnswer:
	default:
		close(p.afterAnswer)
	}
	if p.cancel {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return nil, errors.New("injected provider failure after answer")
}
func (*fix4AnswerThenFailProvider) GetDefaultModel() string { return "wu15-unpriced" }
