package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

// R4 in docs/plan/browser-input-connection/spec.md requires attachment and
// peer identity refusal before negotiation. These tests keep dispatch and
// manager resolution real; the existing fixture replaces only the CDP endpoint.
// A deliberately invalid SDP makes accidental admission observable as a peer
// allocation/negotiation failure instead of a successful authority refusal.
// Mutation targets: remove the session match, remove stale offer checking,
// or accept a canceled attachment. Live data-channel capture fencing is outside
// this admission suite; a schema-only capture test would not prove that fence.
func dedicatedAdmissionOffer(t *testing.T, f handlerContextFixture, change func(*generated.BrowserInputOfferFrame)) []byte {
	t.Helper()
	frame := generated.BrowserInputOfferFrame{Type: "browser_input_offer", AgentId: f.agentID, SessionId: "chat", InputEpoch: 1, OfferId: 1, ControlEpoch: 0, Sdp: "not-an-sdp"}
	if change != nil {
		change(&frame)
	}
	raw, err := json.Marshal(frame)
	require.NoError(t, err)
	message, _ := ValidateInboundFrameJSON("BrowserInputOfferFrame", raw)
	require.Empty(t, message, "the authority test must pass wire validation")
	return raw
}

func dedicatedAdmissionFailure(t *testing.T, f handlerContextFixture) generated.BrowserInputStateFrame {
	t.Helper()
	select {
	case envelope := <-f.conn.sendCh:
		require.True(t, f.conn.canSendFrame(envelope), "current attachment must receive the refusal")
		var frame generated.BrowserInputStateFrame
		require.NoError(t, json.Unmarshal(envelope.data, &frame))
		require.Equal(t, "browser_input_state", frame.Type)
		require.Equal(t, "failed", frame.State)
		select {
		case extra := <-f.conn.sendCh:
			t.Fatalf("unexpected second admission reply: %s", extra.data)
		default:
		}
		return frame
	default:
		t.Fatal("dedicated offer refusal was not published")
		return generated.BrowserInputStateFrame{}
	}
}

func TestDedicatedOfferRejectsMismatchedAttachment(t *testing.T) {
	for _, identity := range []string{"session", "agent"} {
		t.Run(identity, func(t *testing.T) {
			f := newHandlerContextFixture(t, false)
			f.state.setDedicatedInput(true)
			t.Cleanup(func() { f.state.setDedicatedInput(false) })
			raw := dedicatedAdmissionOffer(t, f, func(frame *generated.BrowserInputOfferFrame) {
				if identity == "session" {
					frame.SessionId = "other-chat"
				} else {
					frame.AgentId = "unregistered-agent"
				}
			})
			f.handler.dispatchDedicatedInputOffer(f.conn, f.state, "fixture-viewer", raw, f.cfg)
			f.handler.Wait()
			frame := dedicatedAdmissionFailure(t, f)
			require.Equal(t, strPtr("Input offer does not match the attached browser."), frame.Reason)
			require.Equal(t, 1, frame.InputEpoch)
			require.Equal(t, 1, frame.OfferId)
			require.Equal(t, 0, frame.ControlEpoch)
			d := f.state.dedicatedInput()
			d.mu.Lock()
			defer d.mu.Unlock()
			require.Nil(t, d.peer, "unauthorized negotiation must not allocate a peer")
		})
	}
}

func TestDedicatedOfferRejectsStaleIdentityWithoutCancelingCurrentOwner(t *testing.T) {
	for _, identity := range []string{"input epoch", "offer", "control", "pending control"} {
		t.Run(identity, func(t *testing.T) {
			f := newHandlerContextFixture(t, false)
			f.state.setDedicatedInput(true)
			t.Cleanup(func() { f.state.setDedicatedInput(false) })
			d := f.state.dedicatedInput()
			owner, cancel := context.WithCancel(f.original)
			defer cancel()
			d.mu.Lock()
			d.epoch, d.offer, d.control, d.applied, d.cancel = 2, 2, 1, 1, cancel
			if identity == "pending control" {
				d.applied = 0
			}
			d.mu.Unlock()
			raw := dedicatedAdmissionOffer(t, f, func(frame *generated.BrowserInputOfferFrame) {
				frame.InputEpoch, frame.OfferId, frame.ControlEpoch = 3, 3, 1
				switch identity {
				case "input epoch":
					frame.InputEpoch = 2
				case "offer":
					frame.OfferId = 2
				case "control":
					frame.ControlEpoch = 0
				}
			})
			f.handler.dispatchDedicatedInputOffer(f.conn, f.state, "fixture-viewer", raw, f.cfg)
			f.handler.Wait()
			frame := dedicatedAdmissionFailure(t, f)
			require.Equal(t, strPtr("Input offer is stale or control is pending. Retry input."), frame.Reason)
			require.NoError(t, owner.Err(), "rejected offer must not cancel the current input owner")
			d.mu.Lock()
			defer d.mu.Unlock()
			require.Equal(t, 2, d.epoch)
			require.Equal(t, 2, d.offer)
			require.Nil(t, d.peer, "stale offer must not allocate a replacement")
		})
	}
}

func TestDedicatedOfferRejectsExpiredOrRetiredAttachment(t *testing.T) {
	for _, lifetime := range []string{"expired", "retired"} {
		t.Run(lifetime, func(t *testing.T) {
			f := newHandlerContextFixture(t, false)
			f.state.setDedicatedInput(true)
			t.Cleanup(func() { f.state.setDedicatedInput(false) })
			if lifetime == "retired" {
				f.state.clearAttachment()
			} else {
				// The clock boundary is fixed before dispatch; no timing race is
				// needed to represent an already-expired attachment lifetime.
				f.state.attachMu.Lock()
				f.state.attachmentCancel()
				f.state.attachmentCtx, f.state.attachmentCancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				f.state.attachMu.Unlock()
			}
			f.handler.dispatchDedicatedInputOffer(f.conn, f.state, "fixture-viewer", dedicatedAdmissionOffer(t, f, nil), f.cfg)
			f.handler.Wait()
			d := f.state.dedicatedInput()
			d.mu.Lock()
			require.Nil(t, d.peer, "dead attachment must not create a dedicated peer")
			d.mu.Unlock()
			select {
			case envelope := <-f.conn.sendCh:
				require.False(t, f.conn.canSendFrame(envelope), "dead attachment must not receive a late response: %s", envelope.data)
			default:
			}
		})
	}
}
