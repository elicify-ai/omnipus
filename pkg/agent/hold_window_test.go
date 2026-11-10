package agent

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// windowDeps runs a callback at the one point of admitRequest that sits between
// recording the capture and MarkHeld (the live publish of the request entry),
// so a test can place a fault or a competing turn in that window
// deterministically.
type windowDeps struct {
	fakeAddressDeps
	mu       sync.Mutex
	inWindow func(sessionID string, e session.TranscriptEntry)
}

func (d *windowDeps) PublishUserEntry(sessionID string, e session.TranscriptEntry) {
	d.mu.Lock()
	fn := d.inWindow
	d.mu.Unlock()
	if fn != nil {
		fn(sessionID, e)
	}
}

func admitInWindow(t *testing.T, f *addrFixture, senderMessageID string, inWindow func(sessionID string, e session.TranscriptEntry)) (held bool, id, recv string) {
	t.Helper()
	deps := &windowDeps{fakeAddressDeps: fakeAddressDeps{eligible: f.deps.eligible}, inWindow: inWindow}
	f.al.SetAddressDeps(deps)
	f.bus.Close() // the wake cannot be published
	var err error
	id, recv, held, err = f.al.admitRequest(context.Background(), RequestAdmission{
		Receiver: addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver}, Content: "are you there",
		Sender: addressing.Sender{Principal: "p"}, SenderLabel: "a person",
		Source:          addressing.Source{Kind: addressing.SourceConversation, SessionID: "src-session"},
		SenderMessageID: senderMessageID,
	})
	require.NoError(t, err, "the request is durable: it is never refused")
	return held, id, recv
}

// R4-SRV-CR-02: a turn of the receiver admitted between recording the request
// and MarkHeld delivers it with Held=false (no tick). The sender must then NOT
// be told "held": no later tick would ever clear it.
func TestWakeFailedRequest_DeliveredInTheMarkHeldWindow_IsNotReportedHeld(t *testing.T) {
	f := newAddrFixture(t)
	wireLifecycle(t, f)
	rec := &deliveredRecorder{}
	f.al.SetRequestDeliveredHook(rec.hook)

	held, id, recv := admitInWindow(t, f, "cm-window", func(sid string, e session.TranscriptEntry) {
		f.al.markRequestDelivered(sid, e.ID) // the competing turn delivers the request
	})

	require.False(t, held, "an already-delivered request is not waiting: the sender must not be told 'held'")
	c, ok, err := f.al.RequestLedger().Get(recv, id)
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, c.Delivered)
	require.False(t, c.Held, "MarkHeld must leave a delivered request alone")
	require.Empty(t, rec.snapshot(), "no tick was owed: the capture was not held when it was delivered")
}

// R4-SRV-SFH-02 / QA-03: MarkHeld itself fails. The capture then says Held=false,
// so delivery will not move the sender's row; reporting 'held' would leave it
// waiting for ever. The reported state must match the capture.
func TestWakeFailedRequest_MarkHeldFailure_ReportsTheCapturesState(t *testing.T) {
	f := newAddrFixture(t)
	wireLifecycle(t, f)

	var ledgerFile string
	held, id, recv := admitInWindow(t, f, "cm-markheld-fail", func(sid string, _ session.TranscriptEntry) {
		ledgerFile = filepath.Join(f.al.GetSessionStore().BaseDir(), sid, "requests.jsonl")
		require.NoError(t, os.Remove(ledgerFile))
		require.NoError(t, os.Mkdir(ledgerFile, 0o755)) // every read and append now fails, on every OS and uid
	})

	require.False(t, held, "the capture could not be marked held: the sender's row keeps its ordinary look")
	require.NotEmpty(t, id)
	require.NotEmpty(t, recv)
}
