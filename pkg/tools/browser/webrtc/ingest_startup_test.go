package webrtc

// In-package unit tests for the ingest leg's STARTUP robustness: the keyframe
// request that used to be written to a connection that could not carry it, and
// the eviction policy that used to treat a non-terminal Disconnected as death.
//
// Both were diagnosed from one CI occurrence (run 33943602552) whose whole
// record was:
//
//	[viewer-14/...] PLI send failed: the DTLS transport has not started yet  (x5, ~3s apart)
//	[ingest-15] ICE connection state -> failed
//	[ingest-15] ingest connection failed — cleared; a fresh capture is required
//
// These tests pin the two behaviours that record exposed. They deliberately
// drive the unexported units directly rather than standing up a Go<->Go wire
// flow: the conditions under test are "a connection that has NOT finished
// negotiating" and "a connection that recovers, or is replaced, during a grace
// window", neither of which a real wire flow can produce on demand.

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

// recordingLogger collects every log line a Session emits so a test can assert
// on what an operator would actually have seen.
type recordingLogger struct {
	mu    sync.Mutex
	lines []string
}

func (r *recordingLogger) logf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}

func (r *recordingLogger) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.lines))
	copy(out, r.lines)
	return out
}

// countContaining reports how many recorded lines contain sub.
func (r *recordingLogger) countContaining(sub string) int {
	n := 0
	for _, l := range r.snapshot() {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

// newTestSession returns a Session with a recording logger attached.
func newTestSession(t *testing.T) (*Session, *recordingLogger) {
	t.Helper()
	rec := &recordingLogger{}
	s := NewSession(Config{}, nil, rec.logf)
	t.Cleanup(func() { _ = s.Close() })
	return s, rec
}

// newNegotiatingPC returns a real PeerConnection that has been created but
// never negotiated, i.e. permanently in ConnectionState "new": no ICE, no DTLS,
// no SRTCP session. This is exactly the state the ingest connection is in
// between HandleIngestOffer installing it and ICE completing -- the window the
// 15s pliBurstForNewViewer ticker fires into.
func newNegotiatingPC(t *testing.T, s *Session) *webrtc.PeerConnection {
	t.Helper()
	pc, err := s.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("NewPeerConnection: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	if got := pc.ConnectionState(); got == webrtc.PeerConnectionStateConnected {
		t.Fatalf("precondition: a freshly created PeerConnection must not be connected, got %s", got)
	}
	return pc
}

func setIngestDisconnectGrace(t *testing.T, d time.Duration) {
	t.Helper()
	old := ingestDisconnectGracePeriod
	ingestDisconnectGracePeriod = d
	t.Cleanup(func() { ingestDisconnectGracePeriod = old })
}

// TestSendPLIDefersWhileIngestIsStillNegotiating is the direct regression for
// the "PLI send failed: the DTLS transport has not started yet" lines. A
// keyframe request aimed at a connection that has not finished negotiating
// cannot be delivered and must not be written; it must be remembered instead.
func TestSendPLIDefersWhileIngestIsStillNegotiating(t *testing.T) {
	s, rec := newTestSession(t)
	pc := newNegotiatingPC(t, s)

	// The exact production shape: a videoSSRC left over from an ingest
	// connection that HAS delivered video, and a replacement connection
	// installed by a recapture that is still negotiating.
	s.mu.Lock()
	s.ingestPC = pc
	s.videoSSRC = 0xDEADBEEF
	s.mu.Unlock()

	s.sendPLI("[viewer-14/test]")

	if n := rec.countContaining("PLI send failed"); n != 0 {
		t.Errorf("sendPLI wrote RTCP to a connection with no DTLS transport: %d 'PLI send failed' line(s) in %q", n, rec.snapshot())
	}
	if n := rec.countContaining("PLI deferred"); n != 1 {
		t.Errorf("sendPLI did not report deferring the request: want 1 'PLI deferred' line, got %d in %q", n, rec.snapshot())
	}
	if !s.pliDeferred.Load() {
		t.Error("sendPLI dropped the keyframe request instead of deferring it: pliDeferred is false")
	}
}

// TestSendPLIWritesOnlyWhenTheIngestIsConnected guards the other half of the
// gate: the deferral must be keyed on the connection state, not on some
// incidental property, so it can never suppress a PLI on a healthy connection.
// A connected PeerConnection is unreachable without a full wire flow, so this
// asserts the complementary precondition -- with no ingest installed at all,
// nothing is deferred and nothing is written.
func TestSendPLINoIngestDefersNothing(t *testing.T) {
	s, rec := newTestSession(t)

	s.mu.Lock()
	s.videoSSRC = 0xDEADBEEF
	s.mu.Unlock()

	s.sendPLI("[viewer-1/test]")

	if s.pliDeferred.Load() {
		t.Error("sendPLI deferred a keyframe request with no ingest connection to redeem it against")
	}
	if n := rec.countContaining("PLI"); n != 0 {
		t.Errorf("sendPLI logged about a PLI with no ingest connection: %q", rec.snapshot())
	}
}

// TestFlushDeferredPLIRedeemsExactlyOnce pins flushDeferredPLI's consumption
// semantics: several deferred requests collapse into the single keyframe they
// were all asking for, and a flush with nothing outstanding is silent.
func TestFlushDeferredPLIRedeemsExactlyOnce(t *testing.T) {
	s, rec := newTestSession(t)

	// No ingest connection installed, so the sendPLI that flushDeferredPLI
	// makes returns early -- this test is about the flag's consumption, not
	// about the RTCP write.
	s.pliDeferred.Store(true)

	s.flushDeferredPLI("[ingest-16]")
	if s.pliDeferred.Load() {
		t.Error("flushDeferredPLI left the deferred flag set, so the same keyframe request can be redeemed twice")
	}
	if n := rec.countContaining("deferred while it negotiated"); n != 1 {
		t.Errorf("flushDeferredPLI did not report redeeming the request: want 1 line, got %d in %q", n, rec.snapshot())
	}

	s.flushDeferredPLI("[ingest-16]")
	if n := rec.countContaining("deferred while it negotiated"); n != 1 {
		t.Errorf("flushDeferredPLI redeemed a second time with nothing outstanding: got %d line(s) in %q", n, rec.snapshot())
	}
}

// TestIngestDisconnectedIsNotTreatedAsDeathImmediately is the regression for
// the eviction policy. Pion reaches Disconnected after 5s of failed ICE consent
// checks and leaves it again the moment consent returns; only 25s later does it
// become Failed. Evicting on the first Disconnected spends a full capture
// teardown and renegotiation on a connection that was very likely fine.
func TestIngestDisconnectedIsNotTreatedAsDeathImmediately(t *testing.T) {
	setIngestDisconnectGrace(t, 200*time.Millisecond)
	s, _ := newTestSession(t)
	pc := newNegotiatingPC(t, s)

	lost := make(chan struct{}, 4)
	s.mu.Lock()
	s.ingestPC = pc
	s.onIngestLost = func() { lost <- struct{}{} }
	s.mu.Unlock()

	s.handleIngestStateChange("[ingest-15]", pc, webrtc.PeerConnectionStateDisconnected)

	// Immediately after the state change the connection must still be
	// installed and no recapture must have been requested.
	s.mu.Lock()
	still := s.ingestPC == pc
	s.mu.Unlock()
	if !still {
		t.Fatal("a Disconnected ingest connection was cleared immediately, with no chance to recover")
	}
	select {
	case <-lost:
		t.Fatal("a Disconnected ingest connection requested a fresh capture immediately, with no chance to recover")
	case <-time.After(50 * time.Millisecond):
	}

	// Once the grace elapses without recovery it IS treated as dead, exactly
	// as before -- the grace delays the verdict, it does not remove it.
	select {
	case <-lost:
	case <-time.After(3 * time.Second):
		t.Fatal("an ingest connection that stayed Disconnected past the grace period never requested a fresh capture")
	}
	s.mu.Lock()
	cleared := s.ingestPC == nil
	s.mu.Unlock()
	if !cleared {
		t.Error("an ingest connection that stayed Disconnected past the grace period was never cleared")
	}
}

// TestIngestDisconnectedThenReplacedRequestsNoRecapture pins the loop-breaker.
//
// encoder.js's runCaptureAndOffer tears its PeerConnection down FIRST and only
// then captures, negotiates and offers, so a normal recapture leaves the old
// connection Disconnected for however long the rebuild takes. Acting on that
// asks the encoder for ANOTHER recapture while the first is still in flight;
// encoder.js coalesces it into a rerun, which tears down the connection it just
// built, which produces another Disconnected. Every recapture fed the next one.
func TestIngestDisconnectedThenReplacedRequestsNoRecapture(t *testing.T) {
	setIngestDisconnectGrace(t, 150*time.Millisecond)
	s, _ := newTestSession(t)
	oldPC := newNegotiatingPC(t, s)
	newPC := newNegotiatingPC(t, s)

	lost := make(chan struct{}, 4)
	s.mu.Lock()
	s.ingestPC = oldPC
	s.onIngestLost = func() { lost <- struct{}{} }
	s.mu.Unlock()

	// The encoder tears the old connection down...
	s.handleIngestStateChange("[ingest-15]", oldPC, webrtc.PeerConnectionStateDisconnected)
	// ...and its replacement offer lands inside the grace window.
	s.mu.Lock()
	s.ingestPC = newPC
	s.mu.Unlock()

	select {
	case <-lost:
		t.Fatal("a superseded ingest connection's Disconnected asked for a recapture the live replacement did not need")
	case <-time.After(500 * time.Millisecond):
	}

	s.mu.Lock()
	installed := s.ingestPC
	s.mu.Unlock()
	if installed != newPC {
		t.Errorf("a superseded connection's eviction wiped its healthy successor: ingestPC=%p want %p", installed, newPC)
	}
}

// TestIngestFailedAndClosedStillEvictImmediately guards the states that ARE
// terminal. The grace above must not have slowed down a real death: a Failed or
// Closed connection can never recover, so waiting on it only lengthens the time
// the panel shows a stream nothing is feeding.
func TestIngestFailedAndClosedStillEvictImmediately(t *testing.T) {
	for _, st := range []webrtc.PeerConnectionState{
		webrtc.PeerConnectionStateFailed,
		webrtc.PeerConnectionStateClosed,
	} {
		t.Run(st.String(), func(t *testing.T) {
			setIngestDisconnectGrace(t, time.Hour) // must be irrelevant here
			s, _ := newTestSession(t)
			pc := newNegotiatingPC(t, s)

			lost := make(chan struct{}, 1)
			s.mu.Lock()
			s.ingestPC = pc
			s.onIngestLost = func() { lost <- struct{}{} }
			s.mu.Unlock()

			s.handleIngestStateChange("[ingest-15]", pc, st)

			s.mu.Lock()
			cleared := s.ingestPC == nil
			s.mu.Unlock()
			if !cleared {
				t.Errorf("a %s ingest connection was not cleared", st)
			}
			select {
			case <-lost:
			case <-time.After(2 * time.Second):
				t.Errorf("a %s ingest connection never requested a fresh capture", st)
			}
		})
	}
}

// TestClearIngestIfCurrentIgnoresASupersededConnection pins the identity check
// clearIngestIfCurrent is built on, independently of the timer that calls it.
func TestClearIngestIfCurrentIgnoresASupersededConnection(t *testing.T) {
	s, _ := newTestSession(t)
	oldPC := newNegotiatingPC(t, s)
	newPC := newNegotiatingPC(t, s)

	notified := false
	s.mu.Lock()
	s.ingestPC = newPC
	s.onIngestLost = func() { notified = true }
	s.mu.Unlock()

	if s.clearIngestIfCurrent("[ingest-15]", oldPC, "failed") {
		t.Error("clearIngestIfCurrent cleared on behalf of a connection that is no longer installed")
	}
	s.mu.Lock()
	installed := s.ingestPC
	s.mu.Unlock()
	if installed != newPC {
		t.Errorf("clearIngestIfCurrent wiped the installed connection: got %p want %p", installed, newPC)
	}
	time.Sleep(50 * time.Millisecond)
	if notified {
		t.Error("clearIngestIfCurrent requested a fresh capture for a superseded connection")
	}
}

// TestDescribeSDPCandidates covers the failure-time diagnostic. The mdns split
// is the load-bearing part: an SDP whose host candidates are ".local" names is
// one whose host candidates a container may be completely unable to use, and
// nothing else in the log distinguishes that from a healthy candidate set.
func TestDescribeSDPCandidates(t *testing.T) {
	tests := []struct {
		name string
		sdp  string
		want string
	}{
		{
			name: "no sdp at all",
			sdp:  "",
			want: "none",
		},
		{
			name: "sdp with no candidate lines",
			sdp:  "v=0\r\no=- 1 2 IN IP4 127.0.0.1\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\n",
			want: "none",
		},
		{
			name: "host and server-reflexive",
			sdp: "a=candidate:1 1 udp 2130706431 192.168.1.5 54321 typ host\r\n" +
				"a=candidate:2 1 udp 2130706431 127.0.0.1 54322 typ host\r\n" +
				"a=candidate:3 1 udp 1694498815 203.0.113.7 54323 typ srflx raddr 192.168.1.5 rport 54321\r\n",
			want: "host=2 srflx=1",
		},
		{
			name: "chrome mdns-obfuscated host candidates",
			sdp: "a=candidate:1 1 udp 2130706431 8f2c1f0e-1c2b-4a5d-9f11-0a1b2c3d4e5f.local 54321 typ host\r\n" +
				"a=candidate:2 1 udp 1694498815 203.0.113.7 54323 typ srflx raddr 10.0.0.2 rport 54321\r\n",
			want: "host=1 srflx=1 mdns=1",
		},
		{
			name: "relay only",
			sdp:  "a=candidate:4 1 udp 41885439 198.51.100.9 3478 typ relay raddr 0.0.0.0 rport 0\r\n",
			want: "relay=1",
		},
		{
			name: "malformed candidate lines are skipped, not counted",
			sdp: "a=candidate:1 1 udp 2130706431 192.168.1.5 54321 typ host\r\n" +
				"a=candidate:broken\r\n" +
				"a=candidate:2 1 udp 2130706431 192.168.1.6\r\n",
			want: "host=1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := describeSDPCandidates(tc.sdp); got != tc.want {
				t.Errorf("describeSDPCandidates() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDescriptionSDPHandlesNil guards the nil case the failure log relies on --
// a PeerConnection that failed before SetRemoteDescription has neither
// description, and the diagnostic must still print.
func TestDescriptionSDPHandlesNil(t *testing.T) {
	if got := descriptionSDP(nil); got != "" {
		t.Errorf("descriptionSDP(nil) = %q, want empty", got)
	}
	desc := &webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: "v=0\r\n"}
	if got := descriptionSDP(desc); got != "v=0\r\n" {
		t.Errorf("descriptionSDP() = %q, want the SDP verbatim", got)
	}
}
