package gateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

type wireIngestOffer struct {
	token, id, generation uint64
	sdp, target           string
}
type wireIngestRelay struct {
	fakeRelay
	bindingMu sync.Mutex
	token     uint64
	entered   chan wireIngestOffer
	negotiate func(context.Context, wireIngestOffer) (string, error)
}

func (r *wireIngestRelay) BeginIngestBinding(ctx context.Context) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	r.bindingMu.Lock()
	defer r.bindingMu.Unlock()
	r.token += 11
	return r.token, nil
}
func (r *wireIngestRelay) HandleIngestOfferForBinding(ctx context.Context, token, id uint64, sdp string, generation uint64, target string) (string, error) {
	call := wireIngestOffer{token, id, generation, sdp, target}
	r.entered <- call
	if r.negotiate != nil {
		return r.negotiate(ctx, call)
	}
	return "qualified-answer", nil
}
func ingestWireFixture(t *testing.T) (*browser.CaptureSession, *wireIngestRelay, string) {
	return ingestWireFixtureWithValidation(t, true)
}

// With inbound schema validation off, the production admission guards in
// serveBoundIngest are the only rejection layer a malformed offer faces.
func ingestWireFixtureWithValidation(t *testing.T, validate bool) (*browser.CaptureSession, *wireIngestRelay, string) {
	t.Helper()
	_, al := newBrowserWSTestHandler(t, func(cfg *config.Config) { cfg.Gateway.ValidateInbound = validate })
	relay := &wireIngestRelay{token: 60, entered: make(chan wireIngestOffer, 16)}
	var starts int32
	cs, err := browser.NewCaptureSessionWithDeps(nil, "wire-fixture", relay, fakeEncoderStarter(&starts, nil), nil)
	require.NoError(t, err)
	t.Cleanup(cs.Stop)
	_, err = cs.BeginFrameTransition("page-a", 800, 600, 1)
	require.NoError(t, err)
	registry := newCaptureRegistry()
	registry.set("wire-fixture", cs)
	handler := newCaptureIngestWSHandler(al, registry)
	srv := httptest.NewServer(handler)
	t.Cleanup(func() { srv.Close(); handler.Wait() })
	return cs, relay, "ws" + srv.URL[len("http"):]
}
func ingestWireConnect(t *testing.T, cs *browser.CaptureSession, url string) *websocket.Conn {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	if resp != nil {
		resp.Body.Close()
	}
	t.Cleanup(func() { conn.Close() })
	require.NoError(t, conn.WriteJSON(generated.BrowserCaptureHelloFrame{Type: "browser_capture_hello", Token: cs.TokenHex(), ExtVersion: "test"}))
	require.Eventually(t, func() bool { return !cs.LastPingAt().IsZero() }, time.Second, time.Millisecond)
	return conn
}
func ingestWireSendOffer(t *testing.T, conn *websocket.Conn, id, generation int, target string) {
	t.Helper()
	require.NoError(t, conn.WriteJSON(generated.BrowserCaptureOfferFrame{Type: "browser_capture_offer", Sdp: "v=0\r\n", OfferId: &id, CaptureGeneration: &generation, TargetId: &target}))
}
func ingestWireRead(t *testing.T, conn *websocket.Conn, want string) map[string]any {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	for {
		_, data, err := conn.ReadMessage()
		require.NoError(t, err)
		var frame map[string]any
		require.NoError(t, json.Unmarshal(data, &frame))
		if frame["type"] == want {
			return frame
		}
		require.Equal(t, "browser_capture_control", frame["type"], "unexpected frame while awaiting %s", want)
	}
}
func ingestWireAwaitOffer(t *testing.T, r *wireIngestRelay) wireIngestOffer {
	t.Helper()
	select {
	case call := <-r.entered:
		return call
	case <-time.After(time.Second):
		t.Fatal("authenticated offer never reached context-bound negotiation")
		return wireIngestOffer{}
	}
}

func TestCaptureIngestWireReplaysLatestConfirmedState(t *testing.T) {
	cs, _, url := ingestWireFixture(t)
	_, err := cs.BeginFrameTransition("page-b", 756, 413, 1.25)
	require.NoError(t, err)
	conn := ingestWireConnect(t, cs, url)
	want := map[string]any{"type": "browser_capture_control", "action": "recapture", "capture_generation": float64(2), "target_id": "page-b", "expected_width": float64(756), "expected_height": float64(413), "capture_scale": 1.25}
	require.Equal(t, want, ingestWireRead(t, conn, "browser_capture_control"))
	cs.RecaptureAt(9, 9)
	require.Equal(t, want, ingestWireRead(t, conn, "browser_capture_control"), "old call arguments must not mix with current generation metadata")
}

func TestCaptureIngestWireEchoesAcceptedOfferIdentity(t *testing.T) {
	cs, r, url := ingestWireFixture(t)
	conn := ingestWireConnect(t, cs, url)
	ingestWireSendOffer(t, conn, 7, 1, "page-a")
	require.Equal(t, map[string]any{"type": "browser_capture_answer", "sdp": "qualified-answer", "offer_id": float64(7), "capture_generation": float64(1), "target_id": "page-a"}, ingestWireRead(t, conn, "browser_capture_answer"))
	require.Equal(t, wireIngestOffer{71, 7, 1, "v=0\r\n", "page-a"}, ingestWireAwaitOffer(t, r))
}

// The safe-integer ceiling applies to the offer and generation counters with
// schema validation disabled, so the production admission guard itself is the
// layer under test: the boundary value negotiates, one past it is refused
// before negotiation and closes only its own socket.
func TestCaptureIngestWireSafeIntegerBound(t *testing.T) {
	const maximum = 9007199254740991
	t.Run("boundary offer negotiates", func(t *testing.T) {
		cs, r, url := ingestWireFixtureWithValidation(t, false)
		conn := ingestWireConnect(t, cs, url)
		ingestWireSendOffer(t, conn, maximum, 1, "page-a")
		require.Equal(t, map[string]any{"type": "browser_capture_answer", "sdp": "qualified-answer", "offer_id": float64(maximum), "capture_generation": float64(1), "target_id": "page-a"}, ingestWireRead(t, conn, "browser_capture_answer"))
		require.Equal(t, wireIngestOffer{71, maximum, 1, "v=0\r\n", "page-a"}, ingestWireAwaitOffer(t, r))
	})
	t.Run("offer overflow is refused", func(t *testing.T) {
		cs, r, url := ingestWireFixtureWithValidation(t, false)
		conn := ingestWireConnect(t, cs, url)
		ingestWireSendOffer(t, conn, maximum+1, 1, "page-a")
		frame := ingestWireRead(t, conn, "error")
		require.Equal(t, "capture ingest offer requires capture generation, target and offer ID", frame["message"])
		_, _, err := conn.ReadMessage()
		require.Error(t, err)
		select {
		case <-r.entered:
			t.Fatal("offer above the safe-integer bound reached negotiation")
		default:
		}
	})
	t.Run("generation overflow is refused", func(t *testing.T) {
		cs, _, url := ingestWireFixtureWithValidation(t, false)
		conn := ingestWireConnect(t, cs, url)
		ingestWireSendOffer(t, conn, 1, maximum+1, "page-a")
		frame := ingestWireRead(t, conn, "error")
		require.Equal(t, "capture ingest offer requires capture generation, target and offer ID", frame["message"])
		_, _, err := conn.ReadMessage()
		require.Error(t, err)
	})
}

func TestCaptureIngestWireRejectsMissingIdentity(t *testing.T) {
	cs, _, url := ingestWireFixture(t)
	conn := ingestWireConnect(t, cs, url)
	require.NoError(t, conn.WriteJSON(generated.BrowserCaptureOfferFrame{Type: "browser_capture_offer", Sdp: "v=0\r\n"}))
	frame := ingestWireRead(t, conn, "error")
	require.Equal(t, "capture ingest offer requires capture generation, target and offer ID", frame["message"])
	_, _, err := conn.ReadMessage()
	require.Error(t, err)
}

func TestCaptureIngestWireRemoteCloseCancelsPendingNegotiation(t *testing.T) {
	cs, r, url := ingestWireFixture(t)
	canceled := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	r.negotiate = func(ctx context.Context, _ wireIngestOffer) (string, error) {
		select {
		case <-ctx.Done():
			close(canceled)
			return "", ctx.Err()
		case <-release:
			return "released", nil
		}
	}
	conn := ingestWireConnect(t, cs, url)
	ingestWireSendOffer(t, conn, 1, 1, "page-a")
	ingestWireAwaitOffer(t, r)
	require.NoError(t, conn.Close())
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("remote socket close did not cancel pending negotiation")
	}
}

func TestCaptureIngestWireHeartbeatsStayFreshDuringNegotiation(t *testing.T) {
	cs, r, url := ingestWireFixture(t)
	release := make(chan struct{})
	defer close(release)
	r.negotiate = func(ctx context.Context, _ wireIngestOffer) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-release:
			return "released", nil
		}
	}
	conn := ingestWireConnect(t, cs, url)
	ingestWireSendOffer(t, conn, 1, 1, "page-a")
	ingestWireAwaitOffer(t, r)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"browser_capture_control","action":"ping","capture_generation":1,"target_id":"page-a","capture_health":{"generation":1,"track_state":"live","track_muted":false,"peer_state":"connected","source_frames":21,"encoded_frames":20,"packets_sent":30,"sample_timestamp_ms":120}}`)))
	require.Eventually(t, func() bool { return cs.CaptureHealth().SampleTimestampMS == 120 }, time.Second, time.Millisecond)
	require.Equal(t, uint64(1), cs.CaptureHealth().BindingEpoch)
}

func TestCaptureIngestWireNewFrameSupersedesPendingOfferWithoutReconnect(t *testing.T) {
	cs, r, url := ingestWireFixture(t)
	release := make(chan struct{})
	defer close(release)
	r.negotiate = func(ctx context.Context, call wireIngestOffer) (string, error) {
		if call.generation == 1 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-release:
				return "released", nil
			}
		}
		return "new-frame-answer", nil
	}
	conn := ingestWireConnect(t, cs, url)
	ingestWireSendOffer(t, conn, 1, 1, "page-a")
	ingestWireAwaitOffer(t, r)
	_, err := cs.BeginFrameTransition("page-b", 800, 600, 1)
	require.NoError(t, err)
	cs.RecaptureAt(800, 600)
	ingestWireSendOffer(t, conn, 2, 2, "page-b")
	require.Equal(t, map[string]any{"type": "browser_capture_answer", "sdp": "new-frame-answer", "offer_id": float64(2), "capture_generation": float64(2), "target_id": "page-b"}, ingestWireRead(t, conn, "browser_capture_answer"))
}

func TestCaptureIngestWireOfferOverflowClosesOnlyItsSocket(t *testing.T) {
	cs, r, url := ingestWireFixture(t)
	release := make(chan struct{})
	defer close(release)
	r.negotiate = func(ctx context.Context, _ wireIngestOffer) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-release:
			return "released", nil
		}
	}
	conn := ingestWireConnect(t, cs, url)
	ingestWireSendOffer(t, conn, 1, 1, "page-a")
	ingestWireAwaitOffer(t, r)
	// One active negotiation plus four queued offers is the bounded contract.
	for id := 2; id <= 6; id++ {
		ingestWireSendOffer(t, conn, id, 1, "page-a")
	}
	frame := ingestWireRead(t, conn, "error")
	require.Equal(t, "capture ingest offer queue full", frame["message"])
	_, _, err := conn.ReadMessage()
	require.Error(t, err)
	// A new authenticated socket must survive cleanup of the overloaded one.
	replacement := ingestWireConnect(t, cs, url)
	require.Equal(t, "recapture", ingestWireRead(t, replacement, "browser_capture_control")["action"])
}

func TestCaptureIngestWireNegotiationHasBoundedLifetime(t *testing.T) {
	cs, relay, url := ingestWireFixture(t)
	deadlines := make(chan time.Time, 1)
	relay.negotiate = func(ctx context.Context, _ wireIngestOffer) (string, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			deadlines <- time.Time{}
		} else {
			deadlines <- deadline
		}
		return "bounded-answer", nil
	}
	conn := ingestWireConnect(t, cs, url)
	before := time.Now()
	ingestWireSendOffer(t, conn, 1, 1, "page-a")
	require.Equal(t, "bounded-answer", ingestWireRead(t, conn, "browser_capture_answer")["sdp"])
	deadline := <-deadlines
	require.False(t, deadline.IsZero(), "continuous heartbeats must not permit an unbounded negotiation")
	require.False(t, deadline.Before(before.Add(20*time.Second)), "the offer receives its full protocol budget at receipt")
	require.False(t, deadline.After(time.Now().Add(20*time.Second)), "the negotiation budget is at most 20 seconds")
}

func TestCaptureIngestWireRejectsMalformedIdentity(t *testing.T) {
	cs, relay, url := ingestWireFixture(t)
	conn := ingestWireConnect(t, cs, url)
	// Zero is invalid under the schema as well as production offer admission.
	ingestWireSendOffer(t, conn, 0, 1, "page-a")
	require.Equal(t, "invalid capture ingest offer", ingestWireRead(t, conn, "error")["message"])
	_, _, err := conn.ReadMessage()
	require.Error(t, err)
	select {
	case <-relay.entered:
		t.Fatal("malformed identity reached negotiation")
	default:
	}
}

// A stale offer must not be answered with silence. The encoder's only
// response to silence is its 10s offer-answer timeout, whose escape is a WS
// reconnect — and every reconnect re-issues a recapture, so a stale window
// became a teardown loop restarting the whole capture (ui-browser shard,
// 2026-09-18: two-to-five encoder re-inits per test). The committed frame
// must go out on the SAME socket so the encoder re-offers for the right
// generation without a reconnect cycle.
func TestCaptureIngestWireStaleOfferReceivesCommittedFrameRecapture(t *testing.T) {
	cs, relay, url := ingestWireFixture(t)
	conn := ingestWireConnect(t, cs, url)
	// Consume the connect-path resync recapture so the assertion below
	// observes only the stale offer's own correction.
	require.Equal(t, "recapture", ingestWireRead(t, conn, "browser_capture_control")["action"])
	// Generation 2 is committed while the encoder still offers generation 1.
	_, err := cs.BeginFrameTransition("page-b", 756, 413, 1.25)
	require.NoError(t, err)
	ingestWireSendOffer(t, conn, 3, 1, "page-a")
	// The stale offer is rejected by admission before any negotiation; give
	// the handler a moment to process it, then prove it never got through.
	// (Waiting for the corrective recapture below is what synchronizes the
	// assertion — the stale offer's fate is decided before it is sent.)
	select {
	case <-relay.entered:
		t.Fatal("stale offer reached negotiation")
	default:
	}
	want := map[string]any{"type": "browser_capture_control", "action": "recapture", "capture_generation": float64(2), "target_id": "page-b", "expected_width": float64(756), "expected_height": float64(413), "capture_scale": 1.25}
	require.Equal(t, want, ingestWireRead(t, conn, "browser_capture_control"))
	// The socket stays open for the corrective offer the recapture elicits;
	// a close here would resurrect the reconnect loop this test pins down.
	ingestWireSendOffer(t, conn, 4, 2, "page-b")
	require.Equal(t, map[string]any{"type": "browser_capture_answer", "sdp": "qualified-answer", "offer_id": float64(4), "capture_generation": float64(2), "target_id": "page-b"}, ingestWireRead(t, conn, "browser_capture_answer"))
}
