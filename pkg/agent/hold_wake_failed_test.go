package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// R3 N1 (R3-SRV-CR-02, SFH-01, SFH-02): a request that is recorded but whose wake
// could not be published is treated EXACTLY like a held request: the capture says
// Held, the sender's row moves on when it is finally delivered, and the sending
// agent is told it was queued, never "refused" (a "refused" invites a re-send and
// a duplicate request). ORACLE: the F1 ruling (held = a status tick, delivered
// later) and the PeerRouter contract "a nil error means the request was admitted".

func admitWakeFailed(t *testing.T, f *addrFixture, senderMessageID string) (id, recvMain string) {
	t.Helper()
	f.bus.Close() // every later PublishInbound fails: the wake cannot be published
	id, sid, held, err := f.al.admitRequest(context.Background(), RequestAdmission{
		Receiver: addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver}, Content: "are you there",
		Sender: addressing.Sender{Principal: "p"}, SenderLabel: "a person",
		Source:          addressing.Source{Kind: addressing.SourceConversation, SessionID: "src-session"},
		SenderMessageID: senderMessageID,
	})
	require.NoError(t, err, "a durable request whose wake failed is queued, not refused")
	require.True(t, held, "the sender is told the request is waiting")
	return id, sid
}

func TestWakeFailedRequest_IsHeld_AndItsDeliveryMovesTheSendersRowOnce(t *testing.T) {
	f := newAddrFixture(t) // a RUNNING receiver: the only fault is the lost wake
	wireLifecycle(t, f)
	rec := &deliveredRecorder{}
	f.al.SetRequestDeliveredHook(rec.hook)

	id, recv := admitWakeFailed(t, f, "cm-wake-failed-1")

	c, ok, err := f.al.RequestLedger().Get(recv, id)
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, c.Held, "the capture must record that the sender was told 'held'")
	require.Empty(t, rec.snapshot(), "nothing is reported before the request is delivered")

	f.al.markRequestDelivered(recv, id)
	f.al.markRequestDelivered(recv, id) // a repeat must not tick again
	require.Equal(t, [][2]string{{"src-session", "cm-wake-failed-1"}}, rec.snapshot(),
		"delivery moves the sender's row on exactly once")
}

func TestWakeFailedRequest_PeerSendIsQueuedNotRefused(t *testing.T) {
	f := newAddrFixture(t)
	wireLifecycle(t, f)
	f.bus.Close()

	receipt, err := f.r.SendPeer(context.Background(), f.peerReq())
	require.NoError(t, err, "the request is durable: telling the model 'refused' makes it re-send a duplicate")
	require.True(t, receipt.Held, "the sending agent is told the request is queued for delivery")
	require.NotEmpty(t, receipt.RequestID)

	pending, perr := f.al.RequestLedger().Undelivered(receipt.SessionID)
	require.NoError(t, perr)
	require.Len(t, pending, 1, "exactly one durable request: nothing was duplicated or lost")
	require.True(t, pending[0].Held)
}

// R3-SRV-CR-01 / SFH-03: the request's OWN wake is being admitted, so the
// held-request republish that runs at admission must not publish the same
// request again, even when its in-flight marker is older than the TTL (the wake
// sat behind a long system turn). Real worker, real admission.
func TestHeldRequestWake_TheRequestBeingAdmittedIsNeverRepublished(t *testing.T) {
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	provider := &r1CompletionProvider{
		answers: []string{"seen it"},
		entered: make(chan int, 4),
		release: []chan struct{}{make(chan struct{})},
	}
	provider.openAll()
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	inst.Provider = provider

	deps := &fakeAddressDeps{eligible: map[string]bool{addrWS + "/" + addrReceiver: true}}
	al.SetAddressDeps(deps)
	setSendMessagePolicy(t, al, addrReceiver, config.ToolPolicyAllow)

	store := al.GetSessionStore()
	asker, err := store.NewSession(session.SessionTypeChat, "webchat", "ray")
	require.NoError(t, err)
	ws := addrWS
	require.NoError(t, store.SetMeta(asker.ID, session.MetaPatch{WorkspaceID: &ws}))
	_, err = store.GetOrCreateMainSession(addrWS, addrReceiver)
	require.NoError(t, err)

	got, err := al.NewAddressRouter().SendPeer(context.Background(), tools.PeerRequest{
		RecipientWorkspaceID: addrWS, RecipientAgentID: addrReceiver, Content: "please review X",
		Sender: tools.SendOrigin{WorkspaceID: addrWS, AgentID: "ray"}, SenderSessionID: asker.ID,
	})
	require.NoError(t, err)
	require.False(t, got.Held, "SETUP: the receiver is running")

	wake := <-al.bus.InboundChan()
	require.Equal(t, got.RequestID, wake.TranscriptEntryID)
	scope, _, routed := al.resolveSteeringTarget(wake)
	require.True(t, routed)

	// The wake waited longer than the in-flight marker lives.
	al.heldRepublished.Store(got.RequestID, time.Now().Add(-2*heldWakeInFlightTTL))

	w := newSessionWorker(scope, al, func() {})
	t.Cleanup(w.cancel)
	done := make(chan struct{})
	go func() { defer close(done); w.processTurn(context.Background(), wake) }()
	select {
	case <-provider.entered: // the model has the request: admission (and any republish) is behind us
	case <-time.After(15 * time.Second):
		t.Fatal("the wake never reached the model")
	}
	<-done

	// The turn's end may publish an unrelated "still unanswered" reminder; only a
	// second WAKE OF THIS REQUEST is the duplicate.
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case m := <-al.bus.InboundChan():
			if m.ChatID == "request:"+got.RequestID {
				t.Fatalf("the request being admitted was published a second time (the model would see it twice): %+v", m)
			}
		case <-deadline:
			return
		}
	}
}

// R3-SRV-SFH-07: the delivery mark reads the capture and marks it in ONE ledger
// operation. A failure of that operation marks nothing, so the request stays
// undelivered, the next delivery retries both, and the sender's row is moved on
// then. It never marks the request delivered while losing the tick.
func TestMarkRequestDelivered_LedgerFailureLeavesItRetryableAndTheTickIntact(t *testing.T) {
	f := newAddrFixture(t)
	wireLifecycle(t, f)
	rec := &deliveredRecorder{}
	f.al.SetRequestDeliveredHook(rec.hook)
	id, recv := admitWakeFailed(t, f, "cm-ledger-fail")

	ledgerFile := filepath.Join(f.al.GetSessionStore().BaseDir(), recv, "requests.jsonl")
	saved, err := os.ReadFile(ledgerFile)
	require.NoError(t, err)
	require.NoError(t, os.Remove(ledgerFile))
	require.NoError(t, os.Mkdir(ledgerFile, 0o755)) // reading it now fails (a directory), on every OS and uid

	f.al.markRequestDelivered(recv, id)
	require.Empty(t, rec.snapshot(), "no tick while the delivery could not be recorded")

	require.NoError(t, os.Remove(ledgerFile)) // the fault passes
	require.NoError(t, os.WriteFile(ledgerFile, saved, 0o600))
	f.al.markRequestDelivered(recv, id)
	require.Equal(t, [][2]string{{"src-session", "cm-ledger-fail"}}, rec.snapshot(),
		"the retry moves the row on: the failed attempt did not consume the held-to-delivered transition")
	left, err := f.al.RequestLedger().Undelivered(recv)
	require.NoError(t, err)
	require.Empty(t, left)
}
