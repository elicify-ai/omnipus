//go:build goolm && stdjson

package gateway

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestCrashRecovery_RoomWorkRetiredBeforeRoomIsOpened(t *testing.T) {
	rh, _, _, path := gatewayCrashRoomAtBoot(t, false)
	p := newCrashRoomProvider(false, path)
	gatewayCrashRoomPolicies(t, rh, p)
	mainID, err := session.MainSessionID("sales", "mia")
	require.NoError(t, err)
	rh.h.handleChatMessage(context.Background(), mainID, mainID, "NEW_J_WITHOUT_OPENING_ROOM", "mia", nil, "", "sales", makeTestConn())
	cancel, done := gatewayCrashRun(t, rh)
	defer func() { p.open(); gatewayCrashStopLoop(t, cancel, done) }()
	input := gatewayCrashAwait(t, p.entered)
	gatewayCrashAssertRetired(t, input)
	p.open()
	gatewayCrashAwait(t, p.entered)
	bytes, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "G1\n", string(bytes), "opening the room is not a prerequisite for retiring old work")
}

func TestCrashRecovery_IndependentHeldPeerInputSurvivesRoomRetirement(t *testing.T) {
	rh, _, _, path := gatewayCrashRoomAfterBoot(t)
	jim, err := rh.api.agentLoop.GetSessionStore().GetOrCreateMainSession("sales", "jim")
	require.NoError(t, err)
	_, err = rh.api.agentLoop.NewAddressRouter().SendPeer(context.Background(), tools.PeerRequest{RecipientWorkspaceID: "sales", RecipientAgentID: "mia", Content: "INDEPENDENT_HELD_PEER", Sender: tools.SendOrigin{WorkspaceID: "sales", AgentID: "jim"}, SenderSessionID: jim.ID})
	require.NoError(t, err)
	mainID, err := session.MainSessionID("sales", "mia")
	require.NoError(t, err)
	pending, err := rh.api.agentLoop.RequestLedger().Undelivered(mainID)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.True(t, pending[0].Held)
	p := newCrashRoomProvider(false, path)
	gatewayCrashRoomPolicies(t, rh, p)
	rh.h.handleChatMessage(context.Background(), mainID, mainID, "NEW_J_WITH_INDEPENDENT_HELD_INPUT", "mia", nil, "", "sales", makeTestConn())
	cancel, done := gatewayCrashRun(t, rh)
	defer func() { p.open(); gatewayCrashStopLoop(t, cancel, done) }()
	input := gatewayCrashAwait(t, p.entered)
	gatewayCrashAssertRetired(t, input)
	var independent int
	for _, m := range input {
		if m.Role == "user" && strings.Contains(m.Content, "INDEPENDENT_HELD_PEER") {
			independent++
		}
	}
	assert.Equal(t, 1, independent, "selective retirement cannot erase an unrelated independently addressed held request")
}

func TestCrashRecovery_InFlightStopCannotGainHumanRoomRevival(t *testing.T) {
	rh, room, _, _ := gatewayCrashRoomAfterBoot(t, func(live *roomsHarness, id string) {
		_, err := agent.NewSteerCanceller(live.api.agentLoop.GetSessionLifecycleStore()).CancelSubtree(context.Background(), id, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "owner"})
		require.NoError(t, err)
		rec, err := live.api.agentLoop.GetSessionLifecycleStore().Load(id)
		require.NoError(t, err)
		require.NotNil(t, rec.Stop, "instrument: Stop is stamped while the original execution is still held")
	})
	mainID, err := session.MainSessionID("sales", "mia")
	require.NoError(t, err)
	before, err := rh.api.agentLoop.GetSessionLifecycleStore().Load(mainID)
	require.NoError(t, err)
	require.NotNil(t, before.Stop)
	w := rh.do(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", `{"client_message_id":"in-flight-H","text":"fresh H","mentions":["mia"]}`, rh.api.HandleRooms)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	rh.hub.svc.WaitIdleForTest()
	pending, err := rh.api.agentLoop.RequestLedger().Undelivered(mainID)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.True(t, pending[0].Held)
	after, err := rh.api.agentLoop.GetSessionLifecycleStore().Load(mainID)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	select {
	case msg := <-rh.h.msgBus.InboundChan():
		t.Fatalf("in-flight Stop gained a room wake: %+v", msg)
	default:
	}
}

type crashApprovalGate struct {
	entered chan agent.PolicyApprovalReq
	release chan struct{}
	once    sync.Once
}

func (g *crashApprovalGate) open() { g.once.Do(func() { close(g.release) }) }
func (g *crashApprovalGate) RequestApproval(ctx context.Context, req agent.PolicyApprovalReq) (bool, string, bool) {
	g.entered <- req
	select {
	case <-g.release:
		return true, "", false
	case <-ctx.Done():
		return false, "cancelled", false
	}
}

func TestCrashRecovery_PauseOrRemovalDuringApprovalCannotReviveMain(t *testing.T) {
	for _, action := range []string{"pause", "remove"} {
		t.Run(action, func(t *testing.T) {
			rh, room, _, _ := gatewayCrashRoomAfterBoot(t)
			gate := &crashApprovalGate{entered: make(chan agent.PolicyApprovalReq, 1), release: make(chan struct{})}
			t.Cleanup(gate.open)
			rh.api.agentLoop.SetToolApprover(gate)
			inst, ok := rh.api.agentLoop.GetRegistry().GetAgent("mia")
			require.True(t, ok)
			inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"send_message": config.ToolPolicyAsk}})
			mainID, err := session.MainSessionID("sales", "mia")
			require.NoError(t, err)
			before, err := rh.api.agentLoop.GetSessionLifecycleStore().Load(mainID)
			require.NoError(t, err)
			w := rh.do(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", `{"client_message_id":"approval-H","text":"fresh H","mentions":["mia"]}`, rh.api.HandleRooms)
			require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
			approval := gatewayCrashAwait(t, gate.entered)
			if action == "pause" {
				w = rh.do(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", `{"client_message_id":"pause-during-approval","text":"/pause","mentions":[]}`, rh.api.HandleRooms)
				require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
			} else {
				require.NoError(t, writeWorkspaceFile(rh.api.homePath, storedWorkspace{ID: "sales", Name: "Sales", CoreTeam: []string{"jim"}}))
				require.NoError(t, rh.hub.svc.SyncWorkspace("sales", "owner"))
			}
			gate.open()
			rh.hub.svc.WaitIdleForTest()
			_, found, err := rh.api.agentLoop.RequestLedger().Get(mainID, approval.ToolCallID)
			require.NoError(t, err)
			assert.False(t, found, "approval granted after pause/removal cannot cross the admission-generation/membership fence")
			after, err := rh.api.agentLoop.GetSessionLifecycleStore().Load(mainID)
			require.NoError(t, err)
			assert.Equal(t, before, after)
			select {
			case msg := <-rh.h.msgBus.InboundChan():
				t.Fatalf("stale approved request gained a wake after %s: %+v", action, msg)
			default:
			}
		})
	}
}

func TestCrashRecovery_DuplicateFreshHumanRoomMessageHasOneWake(t *testing.T) {
	rh, room, _, _ := gatewayCrashRoomAfterBoot(t)
	for range 2 {
		w := rh.do(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", `{"client_message_id":"same-H","text":"fresh H","mentions":["mia"]}`, rh.api.HandleRooms)
		require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
		rh.hub.svc.WaitIdleForTest()
	}
	mainID, err := session.MainSessionID("sales", "mia")
	require.NoError(t, err)
	pending, err := rh.api.agentLoop.RequestLedger().Undelivered(mainID)
	require.NoError(t, err)
	require.Len(t, pending, 1, "one request identity, not one per HTTP retry")
	var wakes int
	for {
		select {
		case <-rh.h.msgBus.InboundChan():
			wakes++
		default:
			assert.Equal(t, 1, wakes, "one fresh H has exactly one real wake, not zero or duplicate revival")
			return
		}
	}
}
