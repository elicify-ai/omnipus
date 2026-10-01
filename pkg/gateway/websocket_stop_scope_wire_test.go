// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// TestWSStop_NeverRanSteeredChildEmitsInterruptedSubagentEndOnWire extends
// websocket_stop_scope_test.go's never-ran regression to the REAL socket.
// Oracle: that file's case-1 header, CancelFrame/SubagentEndFrame contracts,
// and steer_frames.go's documented generation-1 span identity and
// OutcomeInterrupted -> "interrupted" mapping. Expectations are fixed before
// execution, not copied from the current gateway's output.
//
// Plan: reuse the durable parent + Launch-only child setup, establish a real
// parent hub subscription with attach_session, receive a live subagent_start
// as the transport control, and send the browser's scope-omitted cancel on a
// fresh socket. No Dispatch or AgentLoop.Start: the child NEVER runs a turn.
// The real launcher, canceller, deliverer, sync tap, hub and write pump are
// all exercised. The existing idle provider is unused. UI consumption,
// disk-seeded E2E fixture equivalence and reload/replay are deliberate gaps.
// Mutation proof (remove end emission, misroute session, change status/span)
// is deferred to an independent CHECK instance; no production code is edited.
func TestWSStop_NeverRanSteeredChildEmitsInterruptedSubagentEndOnWire(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{
		Gateway: config.GatewayConfig{DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: home, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096,
			},
			List: []config.AgentConfig{{ID: "mia", Home: home}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, adr093IdleProvider{})
	lifecycle := session.NewLifecycleStore(t.TempDir())
	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lifecycle)
	setGatewaySteerCanceller(al, agent.NewSteerCanceller(lifecycle, al.SteerGenerationCancel))
	t.Cleanup(func() { gatewaySteerCancellers.Delete(al) })
	classifier := agent.NewSteerRecordClassifier(lifecycle, al.GetSessionStore())
	al.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(classifier), steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())

	// Same seeding operations as the existing never-ran RED test, not a
	// hand-written child record or an internally injected end event.
	parent, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	const workspaceID = "ws-stop-scope-never-ran-wire"
	require.NoError(t, al.GetSessionStore().SetMeta(parent.ID, session.MetaPatch{WorkspaceID: strPtr(workspaceID)}))
	require.NoError(t, lifecycle.Persist(&session.LifecycleRecord{
		SessionID: parent.ID, Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: workspaceID,
		AgentID: "mia", Origin: &session.Origin{Kind: session.OriginKindChat},
	}))

	// newWSHandler installs the production hub sync tap itself. There is no
	// test hook, fake wsConn, manual hub binding or direct Stop-handler call.
	h := newWSHandler(msgBus, al, "")
	t.Cleanup(h.Wait)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	parentConn := dialTestWS(t, srv) // websocket.Dialer.Dial to /api/v1/chat/ws.
	t.Cleanup(func() { _ = parentConn.Close() })
	sendWSAuthFrameDevMode(t, parentConn)
	awaitStopScopeWireFrame(t, parentConn, "session_state", "SETUP: parent connection authentication")
	require.NoError(t, parentConn.WriteJSON(generated.AttachSessionFrame{Type: "attach_session", SessionId: parent.ID}))
	attached := awaitStopScopeWireFrame(t, parentConn, "catch_up_complete", "SETUP: parent hub subscription")
	var catchUp generated.CatchUpCompleteFrame
	require.NoError(t, json.Unmarshal(attached, &catchUp))
	require.Equal(t, parent.ID, catchUp.SessionId, "SETUP: the real client must be bound to the parent's hub")

	const callID = "call-never-ran-wire"
	const taskLabel = "stopped while queued, never runs"
	// Generation 1 uses the bare span_<call_id>, not a _g1 suffix. The wire
	// contract has no separate generation or child_session_id on an end.
	const wantSpanID = "span_" + callID
	launched, err := agent.NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parent.ID, TargetAgentID: "mia", Task: taskLabel,
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
	})
	require.NoError(t, err, "SETUP: launch the same durable, never-dispatched child as the original RED test")
	childID := launched.SessionID
	before, err := lifecycle.Load(childID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleQueued, before.State, "SETUP: child must still be queued; no turn may run")
	require.Equal(t, 1, before.Generation, "SETUP: this regression stops the originally launched generation")
	require.NotNil(t, before.SteeredBy, "SETUP: the child needs the real durable steering edge")
	require.Equal(t, parent.ID, before.SteeredBy.SteeringSessionID)

	startRaw := awaitStopScopeWireFrame(t, parentConn, "subagent_start", "SETUP: live launcher-to-parent transport control")
	var start generated.SubagentStartFrame
	require.NoError(t, json.Unmarshal(startRaw, &start))
	require.Equal(t, parent.ID, start.SessionId)
	require.Equal(t, strPtr(childID), start.ChildSessionId)
	require.Equal(t, wantSpanID, start.SpanId)
	require.Equal(t, callID, start.ParentCallId)
	require.Equal(t, taskLabel, start.TaskLabel)
	require.NotNil(t, start.Seq, "SETUP: a live frame on the bound hub must be sequenced")
	require.GreaterOrEqual(t, *start.Seq, int64(1), "SubagentStartFrame contract: seq minimum is 1")

	// A fresh authenticated socket sends the identical envelope as
	// outbound-lifecycle.ts::cancelStream and the E2E's sendCancelFrame.
	// dispatchFrame -> handleCancelFrame -> handleCancelWithScope ->
	// requestScopedStop. Omitted scope means session/TurnOnly, not tree.
	stopConn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = stopConn.Close() })
	sendWSAuthFrameDevMode(t, stopConn)
	awaitStopScopeWireFrame(t, stopConn, "session_state", "SETUP: Stop connection authentication")
	cancelRaw, err := json.Marshal(generated.CancelFrame{Type: "cancel", SessionId: childID})
	require.NoError(t, err)
	t.Logf("WIRE client->server Stop: %s", cancelRaw)
	require.NoError(t, stopConn.WriteMessage(websocket.TextMessage, cancelRaw))
	ackRaw := awaitStopScopeWireFrame(t, stopConn, "cancel_stage", "BACKEND STOP ENTRYPOINT GAP: browser cancel was not acknowledged")
	var ack generated.CancelStageFrame
	require.NoError(t, json.Unmarshal(ackRaw, &ack))
	require.Equal(t, childID, ack.SessionId, "BACKEND STOP GAP: acknowledgement must name the targeted child")
	require.Equal(t, []string{childID}, ack.Reached, "BACKEND STOP GAP: TurnOnly must reach the child, not its parent")
	require.Empty(t, ack.Unreachable, "BACKEND STOP GAP: the durable child must be reachable")

	// These are diagnostic facts, NOT a substitute for the socket assertion.
	// The acknowledgement is queued after the scoped Stop returns, so the
	// durable landing and parent transcript can be inspected without sleeps.
	after, err := lifecycle.Load(childID)
	require.NoError(t, err)
	entries, err := al.GetSessionStore().ReadTranscript(parent.ID)
	require.NoError(t, err)
	durableEndCount := 0
	for _, entry := range entries {
		if entry.SubagentEnd != nil && entry.SubagentEnd.SpanId == wantSpanID {
			durableEndCount++
		}
	}
	wireGap := fmt.Sprintf("BACKEND WIRE-EMISSION GAP: Stop acknowledged for child=%s generation=1; durable_state=%s parent_transcript_end_count=%d; expected parent_session=%s span=%s status=interrupted",
		childID, after.State, durableEndCount, parent.ID, wantSpanID)
	endRaw := awaitStopScopeWireFrame(t, parentConn, "subagent_end", wireGap)
	var end generated.SubagentEndFrame
	require.NoError(t, json.Unmarshal(endRaw, &end), "BACKEND WIRE-FORMAT GAP: subagent_end must decode under the generated contract")
	require.Equal(t, "subagent_end", end.Type, "BACKEND WIRE-FORMAT GAP: wrong frame type")
	require.Equal(t, parent.ID, end.SessionId, "BACKEND WIRE-ROUTING GAP: subagent_end belongs to the parent's session, not the child")
	require.Equal(t, wantSpanID, end.SpanId, "BACKEND WIRE-IDENTITY GAP: end must close the original child's generation-1 span")
	require.Equal(t, "interrupted", end.Status, "BACKEND WIRE-STATUS GAP: never-ran Stop must emit interrupted, not running/cancelled/success")
	require.Equal(t, strPtr(callID), end.ParentCallId, "BACKEND WIRE-IDENTITY GAP: wrong originating call")
	require.Equal(t, strPtr("mia"), end.AgentId, "BACKEND WIRE-IDENTITY GAP: wrong child agent")
	require.NotNil(t, end.Seq, "BACKEND HUB GAP: the bound parent must receive the sequenced live end")
	require.Greater(t, *end.Seq, *start.Seq, "BACKEND HUB GAP: end must follow start in the parent's ordered stream")
	require.Equal(t, session.LifecycleStopped, after.State, "BACKEND LANDING GAP: acknowledged Stop must durably land the never-ran child stopped")
	require.Equal(t, 1, after.Generation, "BACKEND LANDING GAP: Stop must not revive the child into a new generation")
	require.Equal(t, 1, durableEndCount, "BACKEND PERSISTENCE GAP: exactly one end must survive in the parent transcript")
	t.Logf("BACKEND WIRE CONFIRMED: actual live subagent_end received on parent socket: %s", endRaw)
}

// awaitStopScopeWireFrame uses the existing package delivery failsafe, not
// a latency assertion. It reads actual network bytes; an internal event or
// transcript entry cannot satisfy it. Every raw frame is retained in -v logs.
// It filters ONLY by type, so a wrong session/span/status is asserted by the
// caller instead of silently discarded until a timeout.
func awaitStopScopeWireFrame(t *testing.T, conn *websocket.Conn, wantType, phase string) []byte {
	t.Helper()
	deadline := time.Now().Add(busDeliveryTimeout)
	require.NoError(t, conn.SetReadDeadline(deadline), "SETUP: arm the bounded wire read")
	var observed []string
	for time.Now().Before(deadline) {
		messageType, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("%s: expected type=%s on the REAL WebSocket within %s; observed_types=%v; read_error=%v. Frontend consumption is not involved in this failure.",
				phase, wantType, busDeliveryTimeout, observed, err)
		}
		require.Equal(t, websocket.TextMessage, messageType, "%s: gateway contract requires JSON text frames", phase)
		var envelope map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &envelope), "%s: malformed JSON on the wire: %s", phase, raw)
		var frameType string
		require.NoError(t, json.Unmarshal(envelope["type"], &frameType), "%s: missing/invalid wire type: %s", phase, raw)
		observed = append(observed, frameType)
		t.Logf("WIRE server->client [%s]: %s", phase, raw)
		if frameType == "error" {
			t.Fatalf("%s: gateway returned an error frame instead of type=%s: %s", phase, wantType, raw)
		}
		if frameType == wantType {
			return raw
		}
	}
	t.Fatalf("%s: expected type=%s on the REAL WebSocket within %s; observed_types=%v. Frontend consumption is not involved in this failure.",
		phase, wantType, busDeliveryTimeout, observed)
	return nil // unreachable: Fatalf stops the test, never a successful fallback.
}
