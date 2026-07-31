package browser

import (
	"errors"
	"testing"
)

// TestNotControllerInputError_IsDistinguishable is the 2026-07-30 UAT
// regression. The "viewer does not hold control" rejection was built with the
// same generic benignInputError constructor as the rate-limit rejection, so
// callers could only see "some benign error" and had no way to react to this
// one specifically. It was therefore logged at debug and dropped: the panel
// showed "You're driving" while 448 consecutive inputs were rejected, and
// clicks/keystrokes silently did nothing.
//
// It must stay (a) benign — it is high-frequency and must not spam warnings
// per event — while ALSO being (b) individually identifiable, so the gateway
// can push the authoritative control state back and let the client's UI
// correct itself.
func TestNotControllerInputError_IsDistinguishable(t *testing.T) {
	err := &LiveInputError{Kind: LiveInputErrorBenign, err: ErrViewerNotController}

	if !IsBenignLiveInputError(err) {
		t.Error("not-controller must remain BENIGN — it is high-frequency and must not log a warning per event")
	}
	if !IsNotControllerLiveInputError(err) {
		t.Error("not-controller must be individually identifiable so the gateway can correct the client's control state")
	}
	if !errors.Is(err, ErrViewerNotController) {
		t.Error("errors.Is must see through LiveInputError.Unwrap to the sentinel")
	}

	// The OTHER benign rejection (rate limit) must NOT be mistaken for it —
	// rate limiting is self-correcting and must not trigger a control-state
	// correction frame.
	rateLimited := benignInputError("browser live: input rate limit exceeded (%d/s)", 60)
	if IsNotControllerLiveInputError(rateLimited) {
		t.Error("a rate-limit rejection must not be classified as not-controller")
	}
	if !IsBenignLiveInputError(rateLimited) {
		t.Error("rate limit must still be benign")
	}
}

// TestEnsureControlForInput_UserDrivesByDefault pins the operator's model
// (2026-07-30): the user drives by default, so a human's input acquires the
// lock instead of being discarded — EXCEPT when a different, still-attached
// viewer genuinely holds it.
func TestEnsureControlForInput_UserDrivesByDefault(t *testing.T) {
	t.Run("grants when uncontrolled", func(t *testing.T) {
		lv := &LiveView{viewers: map[string]FrameSink{}}
		if !lv.ensureControlForInput("v1") {
			t.Fatal("input from a viewer must acquire a free lock — the user drives by default")
		}
		if lv.controller != "v1" {
			t.Errorf("controller = %q, want v1", lv.controller)
		}
	})

	t.Run("idempotent for the existing holder", func(t *testing.T) {
		lv := &LiveView{viewers: map[string]FrameSink{}, controller: "v1"}
		if !lv.ensureControlForInput("v1") {
			t.Fatal("the current holder must keep control")
		}
	})

	t.Run("steals a STALE lock from a detached holder", func(t *testing.T) {
		// The 2026-07-30 failure: a viewer that vanished without a clean
		// close still owns the lock (detach never ran), so every later
		// connection was locked out of a browser nobody was driving.
		lv := &LiveView{viewers: map[string]FrameSink{}, controller: "ghost"}
		if !lv.ensureControlForInput("v2") {
			t.Fatal("a lock held by a no-longer-attached viewer must be stealable, else it wedges forever")
		}
		if lv.controller != "v2" {
			t.Errorf("controller = %q, want v2", lv.controller)
		}
	})

	t.Run("refuses when another LIVE viewer holds it", func(t *testing.T) {
		lv := &LiveView{
			viewers:    map[string]FrameSink{"other": func(LiveFrame) {}},
			controller: "other",
		}
		if lv.ensureControlForInput("v2") {
			t.Fatal("must NOT steal from a genuinely attached, driving viewer")
		}
		if lv.controller != "other" {
			t.Errorf("controller = %q, want it left as other", lv.controller)
		}
	})
}
