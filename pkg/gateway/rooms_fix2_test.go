package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/rooms"
	"github.com/elicify-ai/omnipus/pkg/session"
)

type fix2CancelFaultJournal struct {
	rooms.Journal
	fail atomic.Bool
	hits atomic.Int64
}

func (j *fix2CancelFaultJournal) Append(id, stream string, value any) error {
	if j.fail.Load() && stream == rooms.StreamDeliveries {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		var record map[string]any
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		if record["state"] == "cancelled" {
			j.hits.Add(1)
			return errors.New("injected cancellation write failure")
		}
	}
	return j.Journal.Append(id, stream, value)
}

func TestRoomsFix2_WorkspacePUTReportsUnrepairedCancellation(t *testing.T) {
	h := wu15NewHarness(t)
	channel, found := h.rs.ChannelManager.GetChannel("webchat")
	require.True(t, found)
	web, ok := channel.(*webchatChannel)
	require.True(t, ok)
	hub := web.wsHandler.rooms
	hub.svc.Close()
	j := &fix2CancelFaultJournal{Journal: rooms.NewFileJournal(rooms.DefaultDir(h.home))}
	hub.svc = rooms.New(j, rooms.DepsFrom(&gatewayRoomDeps{h: web.wsHandler, home: h.home, hub: hub}))
	h.al.SetRoomRuntime(agentRoomRuntime{svc: hub.svc})
	room := h.create("squad", "Launch", "mia", "jim", "researcher2")
	h.post(room.Id, "queued", "@Jim work")
	d := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "queued"), "jim")
	h.waitState(room.Id, d.RequestId, "admitted")
	status, body := h.http(http.MethodGet, "/api/v1/workspaces/sales", nil)
	require.Equal(t, http.StatusOK, status)
	var ws gen.Workspace
	require.NoError(t, json.Unmarshal(body, &ws))
	team := []string{"mia", "researcher2", "gp"}
	j.fail.Store(true)
	status, body = h.http(http.MethodPut, "/api/v1/workspaces/sales", gen.WorkspaceUpdateRequest{Revision: ws.Revision, CoreTeam: &team})
	require.Equal(t, http.StatusInternalServerError, status, "saved workspace with failed cancellation must not report success: %s", body)
	require.Contains(t, string(body), "workspace changes were saved")
	require.Positive(t, j.hits.Load(), "instrument: cancellation fault really fired")
	status, body = h.http(http.MethodGet, "/api/v1/rooms/"+room.Id, nil)
	require.Equal(t, http.StatusInternalServerError, status, "stale labels cannot be served as healthy: %s", body)
	j.fail.Store(false)
	require.Equal(t, []string{"mia", "researcher2"}, h.room(room.Id).Members)
	state := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "queued"), "jim")
	require.Equal(t, "cancelled", state.State)
	require.NotNil(t, state.Reason)
	require.Equal(t, "removed", *state.Reason)
}

// A/F02/NTA-01: the existing no-model gate runs before the first steering
// poll. No injected production branch is needed to reach this failure.
func TestRoomsFix2_HeldRequestFailsBeforeFirstSteeringPoll(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	h.stopMain("mia")
	h.post(room.Id, "held", "@Mia HELD_EARLY_FAILURE")
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "held"), "mia")
	h.waitState(room.Id, request.RequestId, "held")
	p := &wu15ScriptProvider{answer: "not called"}
	h.provider("mia", p)
	inst, ok := h.al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	inst.Model = "" // natural pre-poll ErrAgentModelUnassigned gate
	conn := h.connect()
	require.NoError(t, conn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
	wu15ReadFrame(t, conn, "catch_up_complete")
	h.sendMain(conn, "mia", "WEB_REVIVE")
	snap := h.waitState(room.Id, request.RequestId, "failed")
	d := wu15Delivery(t, wu15FindEntry(t, snap, "held"), "mia")
	require.NotNil(t, d.Reason)
	require.Equal(t, "turn_failed", *d.Reason, "the owning turn settles even before its first poll")
	require.Empty(t, p.inputs(), "instrument: no provider call preceded the failure")
}

// SF-N2: an ordinary main's system wake consumes the same real held capture,
// with the same allocation/settlement as a web revival. Only the paid model
// and its known per-call price are substituted.
func TestRoomsFix2_SystemWakeOwnsHeldInputs(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "priced_answer", true: "provider_failure"}[fail], func(t *testing.T) {
			h := wu15NewHarness(t)
			room := h.create("squad", "Launch", "mia", "jim")
			h.stopMain("mia")
			h.post(room.Id, "held", "@Mia SYSTEM_HELD_TASK")
			request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "held"), "mia")
			h.waitState(room.Id, request.RequestId, "held")
			// Fixture: the earlier ordinary turn has finished and the chat is
			// runnable. This is NOT permission for a system wake to lift Stop.
			require.NoError(t, h.al.GetSessionLifecycleStore().Mutate(h.main("mia"), func(r *session.LifecycleRecord) error {
				r.State = session.LifecycleCompleted
				r.Stop, r.StopNote = nil, nil
				return nil
			}))
			p := &wu15UsageProvider{&wu15ScriptProvider{requestID: request.RequestId, answer: "system answer"}}
			if fail {
				p.fail = errors.New("injected provider failure")
			}
			h.pricedProvider("mia", p)
			h.al.SetRoomCallPricer(func(string, providers.UsageInfo) float64 { return .03 })
			rec, err := h.al.GetSessionLifecycleStore().Load(h.main("mia"))
			require.NoError(t, err)
			require.NoError(t, h.bus.PublishInbound(context.Background(), bus.InboundMessage{
				Channel: "system", Content: "helper finished", AsyncOriginAgentID: "mia",
				AsyncTranscriptSessionID: h.main("mia"),
				Metadata:                 map[string]string{"steer_message_id": "fix2-helper-final", "steer_generation": strconv.Itoa(rec.Generation)},
			}))
			if fail {
				snap := h.waitState(room.Id, request.RequestId, "failed")
				d := wu15Delivery(t, wu15FindEntry(t, snap, "held"), "mia")
				require.NotNil(t, d.Reason)
				require.Equal(t, "turn_failed", *d.Reason)
			} else {
				h.waitState(room.Id, request.RequestId, "answered")
				// One priced call (.03); one room + one helper input -> .015.
				require.Eventually(t, func() bool { return h.room(room.Id).AllocatedCostMicroUsd == 15_000 }, 10*time.Second, 10*time.Millisecond)
			}
			wu15AssertProviderSaw(t, p.wu15ScriptProvider, "SYSTEM_HELD_TASK")
		})
	}
}

type fix2ReadBarrierJournal struct {
	rooms.Journal
	mu               sync.Mutex
	armed            bool
	entered, release chan struct{}
}

func (j *fix2ReadBarrierJournal) ReadRecord(id string, out any) error {
	j.mu.Lock()
	block := j.armed
	j.armed = false
	j.mu.Unlock()
	if block {
		close(j.entered)
		<-j.release
	}
	return j.Journal.ReadRecord(id, out)
}

// D/S5: pause workspace synchronization at its journal read, after the
// workspace became visible, and race a real authenticated room read.
func TestRoomsFix2_WorkspaceAuditActorWinsConcurrentRead(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim", "researcher2")
	channel, found := h.rs.ChannelManager.GetChannel("webchat")
	require.True(t, found)
	web, ok := channel.(*webchatChannel)
	require.True(t, ok)
	hub := web.wsHandler.rooms
	hub.svc.Close()
	j := &fix2ReadBarrierJournal{Journal: rooms.NewFileJournal(rooms.DefaultDir(h.home)), entered: make(chan struct{}), release: make(chan struct{})}
	hub.svc = rooms.New(j, rooms.DepsFrom(&gatewayRoomDeps{h: web.wsHandler, home: h.home, hub: hub}))
	h.al.SetRoomRuntime(agentRoomRuntime{svc: hub.svc})
	_, err := hub.svc.List(wu15Workspace)
	require.NoError(t, err)
	before := wu15ReadAuditLines(t, h.home)
	j.mu.Lock()
	j.armed = true
	j.mu.Unlock()
	putDone := make(chan struct{})
	go func() { defer close(putDone); h.team("mia", "jim", "gp") }()
	select {
	case <-j.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("instrument: workspace room-sync cut was not reached")
	}
	readDone := make(chan struct{})
	go func() { defer close(readDone); h.room(room.Id) }()
	// The old implementation completes this read (and wrongly audits system)
	// while the initiating save is paused. The fix holds it behind that save.
	select {
	case <-readDone:
	case <-time.After(200 * time.Millisecond):
	}
	close(j.release)
	select {
	case <-putDone:
	case <-time.After(10 * time.Second):
		t.Fatal("workspace PUT did not finish")
	}
	select {
	case <-readDone:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent room GET did not finish")
	}
	requireRoomAudit(t, wu15NewAuditRows(t, before, wu15ReadAuditLines(t, h.home)), room.Id,
		"members_left_workspace", "wu15-tester", "researcher2")
	status, body := h.http(http.MethodGet, "/api/v1/rooms/"+room.Id, nil)
	require.Equal(t, http.StatusOK, status, "%s", body)
}
