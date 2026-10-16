// Omnipus — #1329 RED Addition 2: real stale-target web Stop transport
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SFH F2 / team-lead Q1=A, explicitly assigned in Addition 2 (2026-10-17).
// The full sample was adopted before this test: it is NOT production output.
const qaStaleStopMessage = "The question changed before Stop reached it. No replacement card was cancelled. Press Stop again if you want to cancel it."

// TestWebSocketStopRetryStaleTargetUsesFixedMessage drives the REAL web
// transport after a real card-save failure has left A/B pending on a durably
// stopped owner root. The existing canceller seam schedules a REAL answer of
// A after the NEW Stop captures A, before its already-landed cancellation
// re-drive. B survives; the actual error bytes explain that safe no-op.
// No StopSession, registry, file writer, card sink or resume dispatcher is
// faked. GREEN, mutations and the proof-of-failability gate defer to CHECK.
func TestWebSocketStopRetryStaleTargetUsesFixedMessage(t *testing.T) {
	f := qaNewStaleStopWireFixture(t)
	pendingPath := filepath.Join(f.store.BaseDir(), f.sid, "pending_ask.json")
	before, err := os.ReadFile(pendingPath)
	require.NoError(t, err)
	restore, osCause := qaBlockQuestionStorage(t, pendingPath, false)

	// Real first explicit Stop: settlement can land before the card save
	// fails. This is a required, asserted precondition, never a hand-written
	// already-stopped record or an observed state silently accepted as valid.
	first, err := f.al.StopSession(context.Background(), agent.StopRequest{
		SessionID: f.sid, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "qa-stale-stop"},
		Channel: "web", Canceller: f.stopper,
	})
	require.NoError(t, err)
	require.True(t, first.Durable)
	require.ErrorContains(t, first.RootErr, osCause, "instrument: the first Stop must ACTUALLY encounter the pending-card save failure")
	require.ErrorContains(t, first.RootErr, "pending_ask.json")
	landed, err := f.lifecycle.Load(f.sid)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleStopped, landed.State, "instrument: durable Stop must have landed despite the card-save error")
	require.Nil(t, landed.Stop, "instrument: a spent fence, not an unfinished stamped Stop")
	require.NotNil(t, landed.StopNote)
	require.Equal(t, session.StopCauseStop, landed.StopNote.Cause)
	retained, exists := f.reg.PendingForSession(f.sid)
	require.True(t, exists)
	require.Equal(t, f.cardA.CardID, retained.CardID)
	require.Equal(t, 2, f.reg.PendingCount(), "failed save must retain A AND queued B")
	_, queuedVisible := f.reg.Get(f.cardB.CardID)
	require.False(t, queuedVisible)
	restore()
	after, err := os.ReadFile(pendingPath)
	require.NoError(t, err)
	require.Equal(t, before, after, "instrument: first failed save must leave the durable card queue unchanged")
	select {
	case msg := <-f.mb.InboundChan():
		t.Fatalf("the failed first Stop must dispatch no resume: %+v", msg)
	default:
	}

	decorator := &qaStaleStopCanceller{
		Canceller: gatewaySteerCanceller(f.al), inner: f.stopper,
		reg: f.reg, sid: f.sid, cardA: f.cardA.CardID,
		observed: make(chan qaStaleStopObservation, 1),
	}
	setGatewaySteerCanceller(f.al, decorator) // existing composition seam; no production setter is added.
	srv := httptest.NewServer(f.h)
	t.Cleanup(srv.Close)
	conn := dialTestWS(t, srv)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	sendWSAuthFrameDevMode(t, conn)
	snapshot := awaitStopScopeWireFrame(t, conn, "session_state", "SETUP: real web authentication and retained-card snapshot")
	require.Contains(t, string(snapshot), f.cardA.CardID, "instrument: the web client must actually receive retained A before retry")

	qaWriteStaleStopFrame(t, conn, f.sid)
	raw, observedFrames := qaReadStaleStopError(t, conn)
	var observation qaStaleStopObservation
	select {
	case observation = <-decorator.observed:
	default:
		t.Fatal("instrument: the real gateway canceller decorator was never exercised; this is not the requested capture/re-drive scenario")
	}
	require.NoError(t, observation.err, "instrument: independent real Submit(A) and real cascade must succeed")
	require.Equal(t, f.cardA.CardID, observation.before)
	require.Equal(t, f.cardB.CardID, observation.after, "instrument: B must promote AFTER capture but BEFORE the Stop re-drive")
	require.Empty(t, observation.reached, "instrument: already-landed durable Stop must have no live target to fire")

	var frame generated.ErrorFrame
	require.NoError(t, json.Unmarshal(raw, &frame))
	assert.Equal(t, "error", frame.Type)
	require.NotNil(t, frame.SessionId)
	assert.Equal(t, f.sid, *frame.SessionId, "the transport correlation is the submitted session id")
	assert.Equal(t, qaStaleStopMessage, frame.Message, "SFH F2: a safe stale-target no-op must not become a generic storage failure")
	assert.Nil(t, frame.AskUserCardId, "this is a Stop error, not an answer/cancel card-action correlation")
	assert.Nil(t, frame.ClientMessageId)
	assert.Nil(t, frame.FirstMessageError)
	r5AssertClean(t, frame.Message, f.sid, f.cardA.CardID, f.cardB.CardID, f.store.BaseDir(), f.lifecycle.Dir(), osCause,
		"pending_ask.json", "pending_user_messages", "owner_scope_kind")
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	delete(fields, "session_id") // Q1=A: ONLY this parsed structured member may carry the id.
	withoutCorrelation, err := json.Marshal(fields)
	require.NoError(t, err)
	r5AssertClean(t, string(withoutCorrelation), f.sid, f.cardA.CardID, f.cardB.CardID, f.store.BaseDir(), f.lifecycle.Dir(), osCause)

	active, exists := f.reg.PendingForSession(f.sid)
	require.True(t, exists, "a stale Stop must not cancel replacement B")
	assert.Equal(t, f.cardB.CardID, active.CardID)
	assert.Equal(t, askuser.StatusPending, active.Status)
	assert.Equal(t, 1, f.reg.PendingCount())
	assert.ElementsMatch(t, []string{f.cardA.CardID + ":answered", f.cardB.CardID + ":pending"}, observedFrames,
		"only the independent human answer and its one promotion may be emitted; never B:cancelled")
	current, err := f.lifecycle.Load(f.sid)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleStopped, current.State)
	assert.Equal(t, landed.Generation, current.Generation)
	assert.Equal(t, landed.StopNote, current.StopNote, "already-landed retry must not fabricate a new durable Stop")
	resume := awaitInboundMessage(t, f.mb, "the independent real answer of A must resume exactly once")
	assert.Equal(t, f.sid, resume.SessionID)
	assert.Contains(t, resume.Content, "card_id="+f.cardA.CardID)

	// Positive recovery control: obey the advertised instruction. A deliberate
	// NEW web Stop captures B and may cancel B; the stale request above may not.
	qaWriteStaleStopFrame(t, conn, f.sid)
	ack := awaitStopScopeWireFrame(t, conn, "cancel_stage", "a deliberate next Stop must cancel the replacement card")
	var stage generated.CancelStageFrame
	require.NoError(t, json.Unmarshal(ack, &stage))
	assert.Equal(t, f.sid, stage.SessionId)
	assert.Zero(t, f.reg.PendingCount(), "the deliberate next Stop, and only that Stop, may consume B")
	select {
	case extra := <-f.mb.InboundChan():
		t.Errorf("a stale or deliberate session Stop must add no resume: %+v", extra)
	default:
	}
}

type qaStaleStopWireFixture struct {
	h         *WSHandler
	al        *agent.AgentLoop
	mb        *bus.MessageBus
	store     *session.UnifiedStore
	lifecycle *session.LifecycleStore
	stopper   agent.StopTurnsCanceller
	reg       *askuser.Registry
	sid       string
	cardA     *askuser.PendingSet
	cardB     *askuser.PendingSet
}

func qaNewStaleStopWireFixture(t *testing.T) *qaStaleStopWireFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	h, mb, al := newTestWSHandler(t)
	boot := session.NewBootEpochStore(home)
	epoch, err := boot.Mint()
	require.NoError(t, err)
	require.NotZero(t, epoch, "instrument: a real boot epoch must exist")
	al.SetBootEpochStore(boot)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lifecycle)
	setGatewaySteerCanceller(al, agent.NewSteerCanceller(lifecycle, al.SteerGenerationCancel))
	stopper, ok := gatewaySteerCanceller(al).(agent.StopTurnsCanceller)
	require.True(t, ok, "instrument: the existing gateway canceller must forward the real StopTurns API")
	store := al.GetSessionStore()
	require.NotNil(t, store)
	sink := &askUserCardSink{h: h}
	reg := askuser.NewRegistry(store, &askUserResumeDispatcher{msgBus: mb}, askuser.Options{Sink: sink})
	sink.delayFn = reg.EffectiveDefaultSafeDelay
	al.SetAskUserRegistry(reg)
	h.askUserReg = reg
	t.Cleanup(func() {
		h.Wait()      // caller registers socket/server Close later, so those run first.
		reg.Quiesce() // neither fixture card arms a default-safe timer.
		gatewaySteerCancellers.Delete(al)
		require.NoError(t, store.Close())
		mb.Close()
	})
	root, err := agent.NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		TargetAgentID: "mia", Task: "Keep this owner question waiting until the human stops or answers it",
		WorkspaceID: testHarnessWorkspaceMembershipID, Origin: steer.Origin{Kind: steer.OriginKindChat},
	})
	require.NoError(t, err, "instrument: root identity must be minted by the REAL launcher")
	meta, err := store.GetMeta(root.SessionID)
	require.NoError(t, err)
	require.Empty(t, meta.ParentSessionID)
	record, err := lifecycle.Load(root.SessionID)
	require.NoError(t, err)
	require.Nil(t, record.SteeredBy, "instrument: this must be an owner root, never a delegated child's card")
	cardA := qaLifecycleCard(root.SessionID, "PRIVATE-STALE-HEAD-QUESTION-1329", false)
	cardB := qaLifecycleCard(root.SessionID, "PRIVATE-STALE-QUEUED-QUESTION-1329", false)
	require.NoError(t, reg.CreatePending(cardA))
	require.NoError(t, reg.CreatePending(cardB))
	return &qaStaleStopWireFixture{h: h, al: al, mb: mb, store: store, lifecycle: lifecycle, stopper: stopper,
		reg: reg, sid: root.SessionID, cardA: cardA, cardB: cardB}
}

// The decorator schedules a lawful concurrent answer at an existing INPUT
// boundary and then forwards the REAL durable cascade unchanged. It does not
// fabricate a report, error, Stop result, lifecycle transition or card state.
type qaStaleStopCanceller struct {
	steer.Canceller
	inner    agent.StopTurnsCanceller
	reg      *askuser.Registry
	sid      string
	cardA    string
	once     sync.Once
	observed chan qaStaleStopObservation
}

type qaStaleStopObservation struct {
	before, after string
	reached       []string
	err           error
}

func (d *qaStaleStopCanceller) StopTurnsWithCause(ctx context.Context, id string, by steer.Principal, tree bool,
	cause session.StopCause, stopTurn agent.GenerationCancelFunc) (steer.CancelReport, error) {
	var observation qaStaleStopObservation
	answered := false
	d.once.Do(func() {
		answered = true
		before, exists := d.reg.PendingForSession(d.sid)
		if !exists || before.CardID != d.cardA || id != d.sid {
			observation.err = fmt.Errorf("fixture: the newly captured head is not A")
			return
		}
		observation.before = before.CardID
		observation.err = d.reg.Submit(d.cardA, d.sid, "", []askuser.SubmittedAnswer{{Header: "Scope", Selected: []string{"Backend"}}})
		if after, exists := d.reg.PendingForSession(d.sid); exists {
			observation.after = after.CardID
		}
	})
	if observation.err != nil {
		d.observed <- observation
		return steer.CancelReport{}, observation.err
	}
	report, err := d.inner.StopTurnsWithCause(ctx, id, by, tree, cause, stopTurn)
	if answered {
		observation.reached = append([]string(nil), report.Reached...)
		observation.err = err
		d.observed <- observation
	}
	return report, err
}

func (d *qaStaleStopCanceller) ContinueAcceptedStop(ctx context.Context, intent session.UnfinishedStopIntent,
	stopTurn agent.GenerationCancelFunc) (steer.CancelReport, error) {
	return d.inner.ContinueAcceptedStop(ctx, intent, stopTurn)
}

func qaWriteStaleStopFrame(t *testing.T, conn *websocket.Conn, sid string) {
	t.Helper()
	raw, err := json.Marshal(generated.CancelFrame{Type: "cancel", SessionId: sid})
	require.NoError(t, err)
	t.Logf("WIRE client->server fresh Stop: %s", raw)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, raw))
}

func qaReadStaleStopError(t *testing.T, conn *websocket.Conn) ([]byte, []string) {
	t.Helper()
	// Same bounded delivery failsafe as the existing wire harness, never a
	// latency assertion or a sleep used to pretend an interleaving happened.
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(busDeliveryTimeout)))
	var cards []string
	for {
		kind, raw, err := conn.ReadMessage()
		require.NoError(t, err, "the real web Stop must emit an error frame")
		require.Equal(t, websocket.TextMessage, kind)
		t.Logf("WIRE server->client stale Stop: %s", raw)
		var envelope map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &envelope))
		var frameType string
		require.NoError(t, json.Unmarshal(envelope["type"], &frameType))
		if frameType == "ask_user_question" {
			var update generated.AskUserQuestionFrame
			require.NoError(t, json.Unmarshal(raw, &update))
			cards = append(cards, update.Card.CardId+":"+update.Card.Status)
		}
		if frameType == "error" {
			return raw, cards
		}
	}
}
