package browser

// Bounded automatic recovery for a dead live-browser video feed (issue #674),
// plus the health signal that lets the panel say what happened instead of
// waiting out a 45s client-side timer.
//
// The relay tells this file two things and nothing else: video stopped
// (SetOnIngestLost) and video started (SetOnIngestLive). Everything here is
// the policy layered on top of those two facts:
//
//   - a recapture is issued automatically, because the operator chose
//     self-healing over a manual Retry button;
//   - it is BOUNDED, because the thing being retried is the most expensive and
//     most failure-prone operation in this pipeline (a full encoder teardown
//     plus a fresh chrome.tabCapture), and an unbounded retry against a
//     genuinely broken encoder is a worse bug than the frozen panel it is
//     trying to fix — it burns CPU on the box that is already failing and
//     never produces an error anyone can act on;
//   - it gives up into a NAMED, reported failure, which the gateway turns into
//     a browser_video_health frame. ADR-061 deleted the JPEG fallback
//     precisely so a broken video path could not hide; a recovery that quietly
//     retried forever would hide it just as effectively.
//
// It also must not fight the machinery that already exists. A NORMAL recapture
// (viewport resize, tab change, or one this file issued) tears the ingest
// connection down before rebuilding it. The relay's own
// ingestDisconnectGracePeriod absorbs most of that window; recapturePendingUntil
// absorbs the rest, so a teardown that outlives the grace re-arms one evaluation
// instead of immediately stacking another capture on top of the one still
// running.

import (
	"fmt"
	"time"
)

// maxIngestRecoveryAttempts is how many automatic recaptures may be issued
// before this session declares the video path unrecoverable and says so.
//
// Three, because each attempt costs a full encoder teardown + tabCapture
// (budgeted at up to captureStartTimeout = 20s cold) and the failure modes
// split cleanly either side of that number. One or two attempts cover
// everything transient: a CPU stall that starved the encoder's consent checks,
// a tab navigation that dropped the captured stream, a renderer hiccup. A
// cause that survives three full rebuilds is structural — the extension is
// gone, Chrome died, the machine is wedged — and repeating it neither fixes
// anything nor tells the operator anything they did not already know after the
// third try. A var, not a const, purely as a test seam (the established
// pattern in this package for captureGracePeriod and friends).
var maxIngestRecoveryAttempts = 3

// ingestRecoverySettle is how long an ISSUED recapture is given to actually
// produce video before it counts as having failed.
//
// 12s: the live-measured video-track latency after a warm recapture is ~5s
// (VP8 software-encoder warm-up — see waitForTracksTimeout's doc comment in
// pkg/tools/browser/webrtc/ingest.go for the captured timeline), so this is
// well over 2x the observed figure. Undershooting it is the dangerous
// direction: it would declare a recapture failed while it was still working,
// consume an attempt for nothing, and tear down the very connection that was
// about to deliver frames.
var ingestRecoverySettle = 12 * time.Second

// ingestRecoveryBackoffStep is added to the settle window for each successive
// attempt (attempt 1 waits 12s, attempt 2 16s, attempt 3 20s — so the whole
// bounded sequence resolves within roughly 48s of the first loss). The point
// of growing it is that a box slow enough to lose one recapture is more likely
// to need MORE room on the next one, not the same amount; a flat interval
// retries hardest exactly when the machine can least afford it.
var ingestRecoveryBackoffStep = 4 * time.Second

// VideoHealthState is what the live-browser video path is currently doing, as
// reported to the gateway (and from there to the panel over
// browser_video_health).
type VideoHealthState string

const (
	// VideoHealthLost — video stopped and automatic recovery is about to
	// start. Sent immediately on the relay's ingest-loss signal, which is the
	// whole point: the gateway knows within milliseconds, so the panel no
	// longer has to infer it by timing out.
	VideoHealthLost VideoHealthState = "lost"
	// VideoHealthRecovering — an automatic recapture has just been issued.
	// Attempt/MaxAttempts say which one, so the panel can be specific rather
	// than showing an unbounded spinner.
	VideoHealthRecovering VideoHealthState = "recovering"
	// VideoHealthRecovered — video is flowing again. Sent only when something
	// was actually wrong, never on a first, ordinary start.
	VideoHealthRecovered VideoHealthState = "recovered"
	// VideoHealthUnrecoverable — the attempt budget is spent. Terminal for
	// this failure: nothing further is retried automatically until video comes
	// back by some other route.
	VideoHealthUnrecoverable VideoHealthState = "unrecoverable"
)

// VideoHealthEvent is one transition of the live-browser video path, handed to
// the observer the gateway installs (BrowserManager.SetVideoHealthObserver).
// It carries everything the gateway needs to build and address the outbound
// frame without calling back into this package — deliberately, because the
// observer runs on whichever goroutine noticed the transition and a callback
// that had to re-enter CaptureSession to find its own audience would be one
// lock-ordering mistake away from a deadlock.
type VideoHealthEvent struct {
	// AgentID is the agent whose capture this is.
	AgentID string
	// ViewerIDs is a snapshot of the WebRTC viewers attached at the moment of
	// the transition — the exact set the gateway must notify.
	ViewerIDs []string
	// State is the transition itself.
	State VideoHealthState
	// Attempt / MaxAttempts describe where in the bounded sequence this is.
	// Both 0 on a Recovered event, which is not part of a sequence.
	Attempt     int
	MaxAttempts int
	// Detail is a human-readable cause, present on Lost and Unrecoverable.
	// Free text; the gateway redacts and length-bounds it before it goes out.
	Detail string
}

// SetOnVideoHealth registers the observer notified on every video-health
// transition. Installed once per session by BrowserManager.EnsureCaptureSession
// from the observer the gateway registered on the manager; nil unregisters.
// The observer is always invoked with no CaptureSession lock held.
func (cs *CaptureSession) SetOnVideoHealth(fn func(VideoHealthEvent)) {
	cs.mu.Lock()
	cs.onVideoHealth = fn
	cs.mu.Unlock()
}

// emitVideoHealth delivers one event to the observer. MUST be called with
// cs.mu released — it takes the lock itself, and ViewerIDs() takes it again.
func (cs *CaptureSession) emitVideoHealth(state VideoHealthState, attempt int, detail string) {
	cs.mu.Lock()
	fn := cs.onVideoHealth
	cs.mu.Unlock()
	if fn == nil {
		return
	}
	fn(VideoHealthEvent{
		AgentID:     cs.agentID,
		ViewerIDs:   cs.ViewerIDs(),
		State:       state,
		Attempt:     attempt,
		MaxAttempts: maxIngestRecoveryAttempts,
		Detail:      detail,
	})
}

// noteRecaptureIssued records that a recapture has just been asked for, from
// any source. It opens the window inside which an ingest loss is read as that
// recapture's own teardown rather than a fresh death — without it, every
// viewport resize and tab change on a slow box would look like a failure and
// spend an attempt from the recovery budget.
func (cs *CaptureSession) noteRecaptureIssued() {
	cs.mu.Lock()
	cs.recapturePendingUntil = time.Now().Add(ingestRecoverySettle)
	cs.mu.Unlock()
}

// onIngestLost is the relay's SetOnIngestLost callback: the ingest connection
// died and nothing is feeding the shared local tracks any more.
//
// It does not recapture directly. It arms exactly one evaluation of the
// recovery state machine (runIngestRecovery), which is what keeps a burst of
// loss notifications — several of which the relay can legitimately produce
// around one teardown — from becoming a burst of captures.
func (cs *CaptureSession) onIngestLost() {
	cs.mu.Lock()
	if cs.stopped {
		cs.mu.Unlock()
		return
	}
	cs.ingestVideoLive = false
	if cs.ingestRecoveryGaveUp {
		// Already reported unrecoverable. Retrying now would be the unbounded
		// loop this whole file exists to prevent.
		cs.mu.Unlock()
		cs.logf("capture[%s]: ingest lost again after automatic recovery was exhausted — not retrying", cs.agentID)
		return
	}
	if cs.ingestRecoveryTimer != nil {
		// An evaluation is already scheduled; this loss is part of the same
		// episode. One timer, one recapture.
		cs.mu.Unlock()
		return
	}
	// If a recapture is plausibly still in flight, wait out the rest of ITS
	// window before judging anything — this loss is most likely its teardown.
	delay := time.Until(cs.recapturePendingUntil)
	attempt := cs.ingestRecoveryAttempts + 1
	if delay > 0 {
		cs.armIngestRecoveryLocked(delay)
		cs.mu.Unlock()
		cs.logf("capture[%s]: ingest lost while a recapture was still in flight — waiting %s for it rather than stacking another",
			cs.agentID, delay.Round(time.Millisecond))
		return
	}
	cs.mu.Unlock()

	// Tell the panel NOW. This is the whole point of the signal: the gateway
	// has known for microseconds what the SPA would otherwise take its full
	// first-frame timeout to infer.
	cs.emitVideoHealth(VideoHealthLost, attempt,
		"the live browser's video feed stopped — reconnecting automatically")
	// delay <= 0, so run the first evaluation inline rather than through a
	// zero-duration timer: the first automatic recapture is issued on the same
	// goroutine that observed the death, with no scheduling latency.
	cs.runIngestRecovery()
}

// onIngestVideoLive is the relay's SetOnIngestLive callback: a video feed is
// forwarding again. It retires the whole recovery episode — including a
// gave-up latch, so a session that recovers by any other route (an operator
// reopening the panel, a tab change's recapture) is fully re-armed for the
// next failure rather than staying permanently unprotected.
func (cs *CaptureSession) onIngestVideoLive() {
	cs.mu.Lock()
	if cs.stopped {
		cs.mu.Unlock()
		return
	}
	wasFailing := cs.ingestRecoveryAttempts > 0 || cs.ingestRecoveryGaveUp
	cs.ingestVideoLive = true
	cs.ingestRecoveryAttempts = 0
	cs.ingestRecoveryGaveUp = false
	cs.recapturePendingUntil = time.Time{}
	cs.stopIngestRecoveryLocked()
	cs.mu.Unlock()

	if wasFailing {
		cs.logf("capture[%s]: video is flowing again — automatic recovery succeeded", cs.agentID)
		cs.emitVideoHealth(VideoHealthRecovered, 0, "")
	}
}

// runIngestRecovery is one evaluation of the recovery state machine, run
// either inline from onIngestLost or from the armed timer.
//
// Each pass either issues one recapture and arms the next evaluation, or —
// once the budget is spent — latches the failure and reports it. Because every
// pass arms at most one successor and the attempt counter only ever grows
// until video returns, the sequence is guaranteed to terminate.
func (cs *CaptureSession) runIngestRecovery() {
	cs.mu.Lock()
	cs.ingestRecoveryTimer = nil
	if cs.stopped || cs.ingestRecoveryGaveUp || cs.ingestVideoLive {
		cs.mu.Unlock()
		return
	}
	cs.ingestRecoveryAttempts++
	attempt := cs.ingestRecoveryAttempts
	if attempt > maxIngestRecoveryAttempts {
		cs.ingestRecoveryGaveUp = true
		cs.mu.Unlock()
		detail := fmt.Sprintf(
			"video did not come back after %d automatic recapture attempts — the capture encoder is not producing frames",
			maxIngestRecoveryAttempts)
		cs.logf("capture[%s]: %s", cs.agentID, detail)
		cs.emitVideoHealth(VideoHealthUnrecoverable, maxIngestRecoveryAttempts, detail)
		return
	}
	// Arm the NEXT evaluation before issuing this attempt: the settle window
	// this attempt gets, plus a step of backoff per attempt already spent.
	next := ingestRecoverySettle + time.Duration(attempt-1)*ingestRecoveryBackoffStep
	cs.recapturePendingUntil = time.Now().Add(next)
	cs.armIngestRecoveryLocked(next)
	cs.mu.Unlock()

	cs.logf("capture[%s]: automatic recapture attempt %d/%d after ingest loss (next check in %s)",
		cs.agentID, attempt, maxIngestRecoveryAttempts, next)
	cs.emitVideoHealth(VideoHealthRecovering, attempt, "")
	cs.Recapture()
}

// armIngestRecoveryLocked schedules the next evaluation. Caller holds cs.mu.
// Any previously armed timer is stopped first, so there is never more than one
// pending evaluation and therefore never more than one recapture in flight
// from this state machine.
func (cs *CaptureSession) armIngestRecoveryLocked(d time.Duration) {
	cs.stopIngestRecoveryLocked()
	cs.ingestRecoveryTimer = time.AfterFunc(d, cs.runIngestRecovery)
}

// stopIngestRecoveryLocked cancels any pending evaluation. Caller holds cs.mu.
func (cs *CaptureSession) stopIngestRecoveryLocked() {
	if cs.ingestRecoveryTimer != nil {
		cs.ingestRecoveryTimer.Stop()
		cs.ingestRecoveryTimer = nil
	}
}
