package browser

// Bounded automatic recovery for a dead live-browser video feed (issue #674),
// plus the health signal that lets the panel say what happened instead of
// waiting out a 45s client-side timer.
//
// Authenticated loss callbacks retain the installed offer identity. Recovery
// requires a matching received video packet after the episode's loss baseline;
// merely installing a track is not proof. Legacy relay implementations keep
// their direct live/lost callbacks. Retries are bounded and each episode owns a
// cancellation lifetime, so resolved or replaced work cannot restart capture.
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
type VideoHealthEvent struct { // not-wire-format: internal observer claim, serialized by the gateway contract.
	// Version orders internal health claims, including same-frame recovery.
	Version uint64
	// Frame is the immutable capture identity observed while claiming this event.
	Frame CaptureFrameState
	// AgentID is the agent whose capture this is.
	AgentID string
	// ViewerIDs is a snapshot of the WebRTC viewers attached at the moment of
	// the transition — the exact set the gateway must notify.
	ViewerIDs []string
	// State is the transition itself.
	State VideoHealthState
	// Attempt / MaxAttempts describe where in the bounded sequence this is.
	// Attempt is 0 on Recovered; MaxAttempts retains the configured budget.
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

// videoHealthEventLocked captures the claim's identity and audience. Caller holds cs.mu.
func (cs *CaptureSession) videoHealthEventLocked(state VideoHealthState, attempt int, detail string) VideoHealthEvent {
	viewers := make([]string, 0, len(cs.viewers))
	for id := range cs.viewers {
		viewers = append(viewers, id)
	}
	return VideoHealthEvent{Version: cs.nextVideoHealthVersionLocked(), Frame: cs.frameStateLocked(), AgentID: cs.agentID, ViewerIDs: viewers, State: state, Attempt: attempt, MaxAttempts: maxIngestRecoveryAttempts, Detail: detail}
}

// emitVideoHealth delivers an already-claimed immutable event outside cs.mu.
func (cs *CaptureSession) emitVideoHealth(event VideoHealthEvent) {
	cs.mu.Lock()
	fn := cs.onVideoHealth
	cs.mu.Unlock()
	if fn != nil {
		fn(event)
	}
}

// noteRecaptureIssued records that a recapture has just been asked for, from
// any source. It opens the window inside which an ingest loss is read as that
// recapture's own teardown rather than a fresh death — without it, every
// viewport resize and tab change on a slow box would look like a failure and
// spend an attempt from the recovery budget.
func (cs *CaptureSession) noteRecaptureIssued() {
	cs.mu.Lock()
	cs.noteRecaptureIssuedLocked(ingestRecoverySettle)
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
	cs.reportIngestLoss(nil)
}

// reportIngestLoss validates sampled evidence while claiming the loss under the
// same lock. Relay callbacks use nil because they report a live connection
// event rather than a copied watchdog observation.
func (cs *CaptureSession) reportIngestLoss(sample *CaptureHealthObservation) bool {
	return cs.reportIngestLossClaim(sample, nil)
}

func (cs *CaptureSession) reportIngestLossClaim(sample *CaptureHealthObservation, offer *captureLostOffer) bool {
	cs.mu.Lock()
	if cs.stopped || sample != nil && (sample.BindingEpoch == 0 || sample.BindingEpoch != cs.ingestEpoch || cs.ingestSend == nil || (cs.ingestBindingCtx != nil && cs.ingestBindingCtx.Err() != nil) || *sample != cs.captureHealth || !cs.healthMatchesFrameLocked(*sample)) {
		cs.mu.Unlock()
		return false
	}
	var baseline uint64
	if offer != nil {
		var current bool
		baseline, current = cs.captureLostOfferCurrentLocked(*offer)
		if !current {
			cs.mu.Unlock()
			return false
		}
	}
	if offer == nil {
		baseline = cs.captureProgressSerialLocked()
	}
	// A relay snapshot may wait on its state lock while the original socket
	// ends independently of cs.mu. Recheck after that wait, before any claim.
	if cs.ingestContextBound && (cs.ingestBindingCtx == nil || cs.ingestBindingCtx.Err() != nil || cs.ingestSend == nil) {
		cs.mu.Unlock()
		return false
	}
	cs.ingestVideoLive = false
	if cs.ingestRecoveryGaveUp {
		// Already reported unrecoverable. Retrying now would be the unbounded
		// loop this whole file exists to prevent.
		cs.mu.Unlock()
		cs.logf("capture[%s]: ingest lost again after automatic recovery was exhausted — not retrying", cs.agentID)
		return true
	}
	if cs.ingestRecoveryCtx != nil && cs.ingestRecoveryCtx.Err() == nil {
		// The episode is claimed before observer delivery, so simultaneous
		// notifications cannot stack work or absorb a later finite packet.
		cs.mu.Unlock()
		return true
	}
	cs.beginIngestRecoveryEpisodeLocked()
	cs.ingestRecoveryProgressBaseline = baseline
	// If a recapture is plausibly still in flight, wait out the rest of ITS
	// window before judging anything — this loss is most likely its teardown.
	delay := time.Until(cs.recapturePendingUntil)
	attempt := cs.ingestRecoveryAttempts + 1
	if delay > 0 {
		cs.armIngestRecoveryLocked(delay)
		cs.mu.Unlock()
		cs.logf("capture[%s]: ingest lost while a recapture was still in flight — waiting %s for it rather than stacking another",
			cs.agentID, delay.Round(time.Millisecond))
		return true
	}
	event := cs.videoHealthEventLocked(VideoHealthLost, attempt, "the live browser's video feed stopped — reconnecting automatically")
	epoch := cs.ingestRecoveryEpoch
	cs.mu.Unlock()

	// Tell the panel NOW. This is the whole point of the signal: the gateway
	// has known for microseconds what the SPA would otherwise take its full
	// first-frame timeout to infer.
	cs.emitVideoHealth(event)
	// delay <= 0, so run the first evaluation inline rather than through a
	// zero-duration timer: the first automatic recapture is issued on the same
	// goroutine that observed the death, with no scheduling latency.
	cs.runIngestRecoveryForEpoch(epoch)
	return true
}

// onIngestVideoLive is a track-arrival notification. Authenticated captures
// additionally require an unconsumed matching receipt after the loss/recapture
// baseline; the callback normally arrives before that packet and cannot alone
// confirm recovery. Legacy relay implementations retain their direct signal.
func (cs *CaptureSession) onIngestVideoLive() {
	cs.recordIngestVideoLive(false)
}

func (cs *CaptureSession) recordIngestVideoLive(requireProgress bool) {
	cs.mu.Lock()
	if cs.stopped {
		cs.mu.Unlock()
		return
	}
	wasFailing := cs.ingestRecoveryCtx != nil || cs.ingestRecoveryAttempts > 0 || cs.ingestRecoveryGaveUp
	if cs.ingestContextBound {
		receipt := cs.relay.Stats().VideoReceipt
		if !cs.receiptMatchesFrameLocked(receipt) || receipt.Serial <= cs.ingestRecoveryProgressBaseline || receipt.Serial <= cs.ingestConsumedReceiptSerial {
			cs.mu.Unlock()
			return
		}
		cs.ingestConsumedReceiptSerial = receipt.Serial
	} else if requireProgress && (!wasFailing || cs.captureProgressSerialLocked() <= cs.ingestRecoveryProgressBaseline) {
		cs.mu.Unlock()
		return
	}
	cs.ingestVideoLive = true
	cs.ingestRecoveryAttempts = 0
	cs.ingestRecoveryGaveUp = false
	cs.recapturePendingUntil = time.Time{}
	cs.retireIngestRecoveryEpisodeLocked()
	var event VideoHealthEvent
	if wasFailing {
		event = cs.videoHealthEventLocked(VideoHealthRecovered, 0, "")
	}
	cs.mu.Unlock()

	if wasFailing {
		cs.logf("capture[%s]: video is flowing again — automatic recovery succeeded", cs.agentID)
		cs.emitVideoHealth(event)
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
	epoch := cs.ingestRecoveryEpoch
	cs.mu.Unlock()
	cs.runIngestRecoveryForEpoch(epoch)
}

func (cs *CaptureSession) runIngestRecoveryForEpoch(epoch uint64) {
	cs.mu.Lock()
	if epoch != cs.ingestRecoveryEpoch || cs.ingestRecoveryCtx != nil && cs.ingestRecoveryCtx.Err() != nil {
		cs.mu.Unlock()
		return
	}
	cs.ingestRecoveryTimer = nil
	if cs.stopped || cs.ingestRecoveryGaveUp || cs.ingestVideoLive {
		cs.mu.Unlock()
		return
	}
	if cs.ingestRecoveryCtx == nil {
		// Preserve the explicit legacy/manual evaluation entry point.
		cs.beginIngestRecoveryEpisodeLocked()
		epoch = cs.ingestRecoveryEpoch
		cs.ingestRecoveryProgressBaseline = cs.captureProgressSerialLocked()
	}
	cs.ingestRecoveryAttempts++
	attempt := cs.ingestRecoveryAttempts
	if attempt > maxIngestRecoveryAttempts {
		cs.ingestRecoveryGaveUp = true
		cs.retireIngestRecoveryEpisodeLocked()
		detail := fmt.Sprintf(
			"video did not come back after %d automatic recapture attempts — the capture encoder is not producing frames",
			maxIngestRecoveryAttempts)
		event := cs.videoHealthEventLocked(VideoHealthUnrecoverable, maxIngestRecoveryAttempts, detail)
		cs.mu.Unlock()
		cs.logf("capture[%s]: %s", cs.agentID, detail)
		cs.emitVideoHealth(event)
		return
	}
	// Arm the NEXT evaluation before issuing this attempt: the settle window
	// this attempt gets, plus a step of backoff per attempt already spent.
	next := ingestRecoverySettle + time.Duration(attempt-1)*ingestRecoveryBackoffStep
	// Automatic attempts keep the episode's loss baseline. Rebasing here
	// would discard a single packet received during callback delivery.
	cs.recapturePendingUntil = time.Now().Add(next)
	cs.armIngestRecoveryLocked(next)
	event := cs.videoHealthEventLocked(VideoHealthRecovering, attempt, "")
	episode := cs.ingestRecoveryCtx
	cs.mu.Unlock()

	cs.logf("capture[%s]: automatic recapture attempt %d/%d after ingest loss (next check in %s)",
		cs.agentID, attempt, maxIngestRecoveryAttempts, next)
	cs.emitVideoHealth(event)
	cs.mu.Lock()
	current := epoch == cs.ingestRecoveryEpoch && episode != nil && episode.Err() == nil && !cs.stopped
	cs.mu.Unlock()
	if current {
		cs.RecaptureFrameContext(episode, event.Frame)
	}
}

// armIngestRecoveryLocked schedules the next evaluation. Caller holds cs.mu.
// Any previously armed timer is stopped first, so there is never more than one
// pending evaluation and therefore never more than one recapture in flight
// from this state machine.
func (cs *CaptureSession) armIngestRecoveryLocked(d time.Duration) {
	cs.stopIngestRecoveryLocked()
	epoch := cs.ingestRecoveryEpoch
	cs.ingestRecoveryTimer = time.AfterFunc(d, func() { cs.runIngestRecoveryForEpoch(epoch) })
}

// stopIngestRecoveryLocked cancels any pending evaluation. Caller holds cs.mu.
func (cs *CaptureSession) stopIngestRecoveryLocked() {
	if cs.ingestRecoveryTimer != nil {
		cs.ingestRecoveryTimer.Stop()
		cs.ingestRecoveryTimer = nil
	}
}
