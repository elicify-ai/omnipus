package browser

// Tests for the bounded automatic video recovery and the health signal it
// emits (issue #674).
//
// The behaviours pinned here are the ones the operator asked for by name —
// "it should always work and be reliable" — plus the two ways a naive
// self-healing loop makes things worse: retrying forever, and stacking a
// second capture on top of a recapture that was already in flight.

import (
	"sync"
	"testing"
	"time"
)

// healthRecorder collects every VideoHealthEvent a session emits.
type healthRecorder struct {
	mu     sync.Mutex
	events []VideoHealthEvent
}

func (r *healthRecorder) observe(ev VideoHealthEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *healthRecorder) states() []VideoHealthState {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]VideoHealthState, 0, len(r.events))
	for _, ev := range r.events {
		out = append(out, ev.State)
	}
	return out
}

func (r *healthRecorder) count(state VideoHealthState) int {
	n := 0
	for _, s := range r.states() {
		if s == state {
			n++
		}
	}
	return n
}

func (r *healthRecorder) last(state VideoHealthState) (VideoHealthEvent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.events) - 1; i >= 0; i-- {
		if r.events[i].State == state {
			return r.events[i], true
		}
	}
	return VideoHealthEvent{}, false
}

// shrinkRecoveryTiming compresses the recovery schedule so a test can watch
// the WHOLE bounded sequence run in well under a second instead of the ~48s it
// takes in production. Restores the production values on cleanup.
func shrinkRecoveryTiming(t *testing.T, settle, step time.Duration) {
	t.Helper()
	oldSettle, oldStep := ingestRecoverySettle, ingestRecoveryBackoffStep
	ingestRecoverySettle, ingestRecoveryBackoffStep = settle, step
	t.Cleanup(func() {
		ingestRecoverySettle, ingestRecoveryBackoffStep = oldSettle, oldStep
	})
}

// newRecoveryTestSession builds a capture session with a health recorder
// attached and guarantees it is stopped (and its recovery timer disarmed)
// before the test returns, so no pending evaluation can leak into a later test.
func newRecoveryTestSession(t *testing.T, relay *fakeRelay) (*CaptureSession, *healthRecorder) {
	t.Helper()
	cs := newTestCaptureSession(t, relay, nil)
	rec := &healthRecorder{}
	cs.SetOnVideoHealth(rec.observe)
	t.Cleanup(cs.Stop)
	return cs, rec
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

// TestIngestLoss_RepeatedNotificationsProduceExactlyOneRecapture — the relay
// can legitimately report a loss more than once around a single teardown (a
// terminal PeerConnection state and a disconnect-grace expiry can both fire).
// Each of those must not buy its own capture: a recapture is the most
// expensive operation in this pipeline, and stacking them is what turned one
// blip into a self-feeding storm before the grace period was introduced.
func TestIngestLoss_RepeatedNotificationsProduceExactlyOneRecapture(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour) // no second attempt during this test
	relay := &fakeRelay{}
	cs, rec := newRecoveryTestSession(t, relay)
	_ = cs

	before := relay.recaptureCount()
	for i := 0; i < 5; i++ {
		relay.triggerIngestLost()
	}

	if got := relay.recaptureCount(); got != before+1 {
		t.Fatalf("recaptures = %d after 5 ingest-loss notifications, want %d. "+
			"One episode of video loss must buy exactly one recapture — a notification per capture is "+
			"the storm this bound exists to prevent", got, before+1)
	}
	if n := rec.count(VideoHealthLost); n != 1 {
		t.Fatalf("browser_video_health lost events = %d, want exactly 1", n)
	}
}

// TestIngestLoss_DuringAnInFlightRecaptureDoesNotStackAnother — a normal
// recapture (viewport resize, tab change, or one this machinery issued) tears
// the ingest connection down on its way to rebuilding it. That teardown is not
// a death, and must not consume the recovery budget or launch a competing
// capture.
func TestIngestLoss_DuringAnInFlightRecaptureDoesNotStackAnother(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	relay := &fakeRelay{}
	cs, rec := newRecoveryTestSession(t, relay)

	cs.Recapture() // an ordinary recapture, e.g. the panel was resized
	afterExplicit := relay.recaptureCount()

	// Its own teardown arrives as an ingest loss.
	relay.triggerIngestLost()

	if got := relay.recaptureCount(); got != afterExplicit {
		t.Fatalf("recaptures = %d after a loss caused by an in-flight recapture, want %d unchanged. "+
			"A recapture's own teardown must not be read as a death — doing so stacks a second "+
			"capture on the first and each one then feeds the next", got, afterExplicit)
	}
	if n := rec.count(VideoHealthLost); n != 0 {
		t.Fatalf("browser_video_health lost events = %d during a legitimate recapture window, want 0 — "+
			"telling the panel video died while a recapture it asked for is still running is a false alarm", n)
	}
}

// TestIngestRecovery_GivesUpAfterTheAttemptBudget is the bound itself. A video
// path that never comes back must end in a named, reported failure — never an
// endless retry loop, which is a worse bug than the frozen panel it is trying
// to fix.
func TestIngestRecovery_GivesUpAfterTheAttemptBudget(t *testing.T) {
	shrinkRecoveryTiming(t, 20*time.Millisecond, 5*time.Millisecond)
	relay := &fakeRelay{}
	_, rec := newRecoveryTestSession(t, relay)

	before := relay.recaptureCount()
	relay.triggerIngestLost() // video never comes back: no triggerIngestLive follows

	waitFor(t, "the recovery budget to be exhausted", 5*time.Second, func() bool {
		return rec.count(VideoHealthUnrecoverable) > 0
	})

	if got, want := relay.recaptureCount()-before, maxIngestRecoveryAttempts; got != want {
		t.Fatalf("recaptures = %d before giving up, want exactly %d (maxIngestRecoveryAttempts)", got, want)
	}
	if n := rec.count(VideoHealthRecovering); n != maxIngestRecoveryAttempts {
		t.Fatalf("browser_video_health recovering events = %d, want %d — the panel must be able to say "+
			"which attempt this is, not show an unbounded spinner", n, maxIngestRecoveryAttempts)
	}

	ev, ok := rec.last(VideoHealthUnrecoverable)
	if !ok {
		t.Fatal("no unrecoverable event")
	}
	if ev.Detail == "" {
		t.Fatal("the unrecoverable event carries no detail — ADR-061 deleted the silent fallback so a " +
			"failure would be visible AND specific; a named state with no cause gives that back")
	}
	if ev.MaxAttempts != maxIngestRecoveryAttempts {
		t.Fatalf("unrecoverable event MaxAttempts = %d, want %d", ev.MaxAttempts, maxIngestRecoveryAttempts)
	}

	// And it must STAY given up: the loop is over, not merely paused.
	settled := relay.recaptureCount()
	relay.triggerIngestLost()
	time.Sleep(80 * time.Millisecond)
	if got := relay.recaptureCount(); got != settled {
		t.Fatalf("recaptures = %d after giving up (was %d) — a spent budget must stop the loop dead, "+
			"otherwise 'bounded' means nothing", got, settled)
	}
}

// TestIngestRecovery_RecoversAndRearmsWhenVideoComesBack — the success path.
// Video returning must end the episode, tell the panel, and leave the session
// fully re-armed for the NEXT failure rather than half-spent.
func TestIngestRecovery_RecoversAndRearmsWhenVideoComesBack(t *testing.T) {
	shrinkRecoveryTiming(t, 30*time.Millisecond, 10*time.Millisecond)
	relay := &fakeRelay{}
	_, rec := newRecoveryTestSession(t, relay)

	relay.triggerIngestLost()
	waitFor(t, "the first automatic recapture", time.Second, func() bool {
		return rec.count(VideoHealthRecovering) >= 1
	})

	// The recapture worked.
	relay.triggerIngestLive()
	waitFor(t, "a recovered event", time.Second, func() bool {
		return rec.count(VideoHealthRecovered) == 1
	})

	// Nothing further may be retried for the episode that just ended.
	settled := relay.recaptureCount()
	time.Sleep(120 * time.Millisecond) // several shrunk settle windows
	if got := relay.recaptureCount(); got != settled {
		t.Fatalf("recaptures = %d after video recovered (was %d) — a recovered feed must cancel the "+
			"pending evaluation, or the machinery keeps recapturing a healthy stream", got, settled)
	}

	// A brand-new failure starts from attempt 1, with the full budget.
	relay.triggerIngestLost()
	waitFor(t, "a fresh recovery episode", time.Second, func() bool {
		return rec.count(VideoHealthRecovering) >= 2
	})
	ev, ok := rec.last(VideoHealthRecovering)
	if !ok {
		t.Fatal("no recovering event")
	}
	if ev.Attempt != 1 {
		t.Fatalf("the first attempt of a NEW episode reported Attempt = %d, want 1 — a session that "+
			"recovered must not carry the previous episode's spent attempts into the next one", ev.Attempt)
	}
}

// TestIngestRecovery_StoppedSessionIsInert — a death notification racing
// teardown must not resurrect work, and a pending evaluation must not outlive
// the session it would recapture.
func TestIngestRecovery_StoppedSessionIsInert(t *testing.T) {
	shrinkRecoveryTiming(t, 20*time.Millisecond, 5*time.Millisecond)
	relay := &fakeRelay{}
	cs, _ := newRecoveryTestSession(t, relay)

	relay.triggerIngestLost()
	before := relay.recaptureCount()
	cs.Stop()

	time.Sleep(120 * time.Millisecond) // would cover several evaluations
	if got := relay.recaptureCount(); got != before {
		t.Fatalf("recaptures = %d after Stop() (was %d), want no further recaptures", got, before)
	}
}

// TestCaptureSession_WiresIngestLiveHook pins the WIRING, exactly as
// TestCaptureSession_WiresIngestLossHook does for the other half. Without it
// the bounded recovery could never observe a success, so it would drain its
// budget against a stream that had already come back — the "feature present
// but never connected" failure class this package has shipped before.
func TestCaptureSession_WiresIngestLiveHook(t *testing.T) {
	relay := &fakeRelay{}
	_ = newTestCaptureSession(t, relay, nil)

	relay.mu.Lock()
	registered := relay.onIngestLive != nil
	relay.mu.Unlock()

	if !registered {
		t.Fatal("CaptureSession must register an ingest-LIVE callback on a relay that supports it — " +
			"without it a successful recapture is indistinguishable from a failed one")
	}
}

// TestEnsureCaptureSession_InstallsTheManagerVideoHealthObserver pins the
// wiring between the gateway's registration point and the session that
// actually emits. The gateway registers the observer on the MANAGER at
// browser_attach — well before any viewer offer creates a CaptureSession — so
// if EnsureCaptureSession failed to hand it down, every health event would be
// dropped on the floor and the panel would be exactly as blind as before.
func TestEnsureCaptureSession_InstallsTheManagerVideoHealthObserver(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	mgr := newCaptureTestManager(t)
	rec := &healthRecorder{}
	mgr.SetVideoHealthObserver(rec.observe)

	relay := &fakeRelay{}
	cs, err := mgr.EnsureCaptureSession(func() (*CaptureSession, error) {
		return NewCaptureSessionWithDeps(mgr, "agent-1", relay, nil, nil)
	})
	if err != nil {
		t.Fatalf("EnsureCaptureSession: %v", err)
	}
	t.Cleanup(cs.Stop)

	relay.triggerIngestLost()

	if n := rec.count(VideoHealthLost); n != 1 {
		t.Fatalf("observer saw %d lost events, want 1 — an observer registered on the manager must "+
			"reach the CaptureSession the manager creates, or the panel learns nothing", n)
	}
}

// TestSetVideoHealthObserver_ReachesAnAlreadyRunningSession — the gateway
// re-registers on every attach, including a SECOND viewer joining a capture
// that is already running. That registration must land on the live session,
// not only on sessions created afterwards.
func TestSetVideoHealthObserver_ReachesAnAlreadyRunningSession(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	mgr := newCaptureTestManager(t)

	relay := &fakeRelay{}
	cs, err := mgr.EnsureCaptureSession(func() (*CaptureSession, error) {
		return NewCaptureSessionWithDeps(mgr, "agent-1", relay, nil, nil)
	})
	if err != nil {
		t.Fatalf("EnsureCaptureSession: %v", err)
	}
	t.Cleanup(cs.Stop)

	rec := &healthRecorder{}
	mgr.SetVideoHealthObserver(rec.observe) // registered AFTER the session exists

	relay.triggerIngestLost()

	if n := rec.count(VideoHealthLost); n != 1 {
		t.Fatalf("observer saw %d lost events, want 1 — a late registration must reach the session "+
			"that is already running, or a second viewer's attach silently unhooks nothing", n)
	}
}
