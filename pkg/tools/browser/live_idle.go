// live_idle.go: Idle sweeper and stand-down of live views nobody is watching (ADR-085 FR-026a/FR-029/FR-031a/FR-041/FR-047/FR-052).

package browser

import (
	"strings"
	"sync"
	"time"
)

// SetControlIdleReleaseHooks registers h as THIS registry's FR-031a/FR-052
// sweeper observer, overriding the process-wide registration (see
// SetGlobalControlReleaseHooks). Safe to call at any time — the field is
// mutex-guarded against the sweeper's own tick.
func (r *LiveViewRegistry) SetControlIdleReleaseHooks(h ControlIdleReleaseHooks) {
	r.hooksMu.Lock()
	defer r.hooksMu.Unlock()
	r.hooks = h
}

// globalControlReleaseHooks is the PROCESS-WIDE FR-031a/FR-052 sweeper
// observer, registered once by the gateway (pkg/gateway/browser_ws.go::
// newBrowserWSHandler) and consulted by every registry that has no
// per-registry override of its own.
//
// WHY THIS EXISTS AT ALL, given SetControlIdleReleaseHooks already did. A
// LiveViewRegistry is constructed lazily, per BrowserManager, by
// newLiveViewRegistry — which pkg/agent calls, not the gateway. The gateway
// never holds a registry until a panel ATTACHES to one, and the releases this
// observer exists to audit (SF-3) happen on tab sets no panel need ever have
// attached to: an agent-initiated browser_handover stands a tab set down with
// no viewer at all, and the sweeper then releases it. Registering per-registry
// at attach time would therefore leave exactly the releases nobody watched
// unaudited. A process-wide default, set once at wiring time, covers every
// manager including ones created later by a hot reload.
var (
	globalControlReleaseHooksMu sync.RWMutex
	globalControlReleaseHooksV  ControlIdleReleaseHooks
)

// SetGlobalControlReleaseHooks registers h as the process-wide sweeper
// observer. Passing the zero value clears it. Called once by the gateway at
// wiring time; a nil field is nil-checked individually at every call site, so
// a build with no gateway (headless, tests) simply observes nothing.
func SetGlobalControlReleaseHooks(h ControlIdleReleaseHooks) {
	globalControlReleaseHooksMu.Lock()
	defer globalControlReleaseHooksMu.Unlock()
	globalControlReleaseHooksV = h
}

// effectiveIdleReleaseHooks resolves the observer this registry's sweeper must
// report to: its own per-registry registration if it has one, otherwise the
// process-wide one. A registry whose own registration sets only ONE of the two
// fields is taken at its word — it is an override, not a partial merge, so a
// test that deliberately watches only idle releases is never surprised by the
// gateway's disabled-release hook firing underneath it.
func (r *LiveViewRegistry) effectiveIdleReleaseHooks() ControlIdleReleaseHooks {
	r.hooksMu.RLock()
	own := r.hooks
	r.hooksMu.RUnlock()
	if own.OnIdleRelease != nil || own.OnDisabledRelease != nil {
		return own
	}
	globalControlReleaseHooksMu.RLock()
	defer globalControlReleaseHooksMu.RUnlock()
	return globalControlReleaseHooksV
}

// StandDownNotice is everything the FR-041 waiting surface needs, captured at
// the exact moment a tab set TRANSITIONS into a stood-down state — never on a
// repeat take/handover within one unbroken hold, which is what makes FR-044's
// "exactly one line per unbroken held period" a property of the emission
// rather than of a de-duplicating reader.
type StandDownNotice struct {
	// TabSetID is the resolved tab-set key — sessionKey(BrowsingKey,
	// TabOwner), e.g. "ws:<workspace>/session:<transcript id>".
	TabSetID string
	// WorkspaceID is the workspace whose browser this is.
	WorkspaceID string
	// OwnerSessionID is the transcript session id the tab set BELONGS to,
	// parsed back out of the owner half of TabSetID, or "" for the
	// operator's workspace-owned set (which belongs to no chat). The
	// gateway prefers a chat session it learned at attach time and falls
	// back to this — see browser_ws.go::onStandDownNotice.
	OwnerSessionID string
	// Reason is browser_handover's model-authored explanation, already
	// trimmed and truncated to 200 runes by the tool (FR-048a). Always ""
	// for StandDownByTake.
	Reason string
	// Producer says which side raised this.
	Producer StandDownNoticeProducer
	// HoldStartedAtUnixNano is the FR-044 hold-start timestamp the
	// deterministic notice id is derived from. Stable for the whole unbroken
	// hold, so the live frame and the persisted transcript entry converge on
	// one message id.
	HoldStartedAtUnixNano int64
}

// StandDownNoticeSink receives one StandDownNotice per TRANSITION into a
// stood-down state. Process-wide, registered once by the gateway
// (pkg/gateway/browser_ws.go::newBrowserWSHandler) — package-level rather than
// per-registry for the same reason SetGlobalControlReleaseHooks is: a
// browser_handover can stand a tab set down that no panel has ever attached
// to, so there is no per-registry moment at which the gateway could have
// registered in time.
//
// Invoked on its own goroutine with NO LiveView or registry lock held: the
// gateway's implementation writes a transcript entry to disk and broadcasts a
// WS frame, neither of which may run under lv.mu.
type StandDownNoticeSink func(n StandDownNotice)

var (
	standDownNoticeSinkMu sync.RWMutex
	standDownNoticeSink   StandDownNoticeSink
)

// SetStandDownNoticeSink registers fn as the process-wide FR-041 waiting-
// surface emitter. Passing nil clears it; a nil sink is a silent no-op, so a
// headless build with no gateway simply raises no surface.
func SetStandDownNoticeSink(fn StandDownNoticeSink) {
	standDownNoticeSinkMu.Lock()
	defer standDownNoticeSinkMu.Unlock()
	standDownNoticeSink = fn
}

// emitStandDownNotice fans one notice out to the registered sink, on its own
// goroutine. MUST be called with no lock held. A no-op when nothing is
// registered, and when holdStartedAtUnixNano is zero (a transition with no
// hold-start stamp cannot produce FR-044's deterministic id, so emitting
// would produce a line that duplicates itself on the next take).
func emitStandDownNotice(n StandDownNotice) {
	if n.HoldStartedAtUnixNano == 0 {
		return
	}
	standDownNoticeSinkMu.RLock()
	sink := standDownNoticeSink
	standDownNoticeSinkMu.RUnlock()
	if sink == nil {
		return
	}
	go sink(n)
}

// ownerSessionIDFromTabSetKey parses the transcript session id back out of a
// rendered tab-set key ("ws:<workspace>/session:<id>"), returning "" for the
// operator's workspace-owned set and for anything that is not a session-owned
// key. It is the inverse of sessionKey(k, TabOwnerSession(id)) and exists so
// a notice raised deep inside the LiveView — which has only its own map key —
// can name the chat the tab set belongs to.
func ownerSessionIDFromTabSetKey(key string) string {
	idx := strings.LastIndex(key, "/")
	if idx < 0 {
		return ""
	}
	owner := key[idx+1:]
	if !strings.HasPrefix(owner, tabOwnerSessionPrefix) {
		return ""
	}
	return strings.TrimPrefix(owner, tabOwnerSessionPrefix)
}

// noticeFor builds the StandDownNotice for this view. Safe to call with or
// without lv.mu held — it reads only immutable fields (the view's own key and
// its manager's browsing key).
func (lv *LiveView) noticeFor(producer StandDownNoticeProducer, reason string, holdStart int64) StandDownNotice {
	workspaceID := ""
	if lv.mgr != nil {
		workspaceID = lv.mgr.key.WorkspaceID()
	}
	return StandDownNotice{
		TabSetID:              lv.sessionID,
		WorkspaceID:           workspaceID,
		OwnerSessionID:        ownerSessionIDFromTabSetKey(lv.sessionID),
		Reason:                reason,
		Producer:              producer,
		HoldStartedAtUnixNano: holdStart,
	}
}

// controlIdleSweepTick is the FR-031a sweeper's tick interval. The window
// itself (tools.browser.control_idle_release, default 900s) is two orders
// of magnitude coarser, so tick precision is irrelevant — a cheap, coarse
// tick is the point (FR-031a: "delivery is EAGER, not lazy").
const controlIdleSweepTick = 30 * time.Second

// startControlIdleSweeper starts the FR-031a/FR-052 background sweeper.
// Idempotent guard is the caller's responsibility (newLiveViewRegistry calls
// it exactly once). Stopped by Shutdown.
func (r *LiveViewRegistry) startControlIdleSweeper() {
	// CAPTURE BOTH CHANNELS AS LOCALS. The goroutine must never read
	// r.sweepStop / r.sweepDone through the receiver, for two reasons:
	//
	//  1. DEADLOCK. Shutdown nils r.sweepStop under r.mu so a second call is
	//     a no-op. A select re-reads its channel operand on every pass, so a
	//     goroutine selecting on the FIELD starts receiving from a nil
	//     channel the instant Shutdown nils it — and a receive on a nil
	//     channel blocks forever. The sweeper then never returns, never runs
	//     its `defer close(done)`, and Shutdown's own `<-done` hangs the
	//     caller. Measured: TestLiveViewRegistryShutdownIsIdempotent sat
	//     until go test's 10m timeout, with goroutine 9 in `chan receive` at
	//     Shutdown and goroutine 10 still parked in this select.
	//  2. DATA RACE. Shutdown writes both fields under r.mu; this goroutine
	//     read them under no lock at all.
	//
	// Locals are immune to both: the goroutine owns the exact channel pair it
	// was started with, whatever the fields later say.
	stop := make(chan struct{})
	done := make(chan struct{})
	r.sweepStop = stop
	r.sweepDone = done
	go func() {
		defer close(done)
		ticker := time.NewTicker(controlIdleSweepTick)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				r.sweepTick()
			}
		}
	}()
}

// Shutdown stops the FR-031a sweeper goroutine and blocks until it has
// exited. Safe to call on a registry that was never ticked (e.g. a
// hand-built test registry that never called startControlIdleSweeper) —
// nil-guarded.
func (r *LiveViewRegistry) Shutdown() {
	// IDEMPOTENT, and it must be. The nil guard alone was not enough: the
	// second call reached close() on an already-closed channel and PANICKED
	// the whole gateway process with "close of closed channel". It fired on
	// the config-reload path, where pkg/agent/loop.go's
	// rewireBrowserManagerForKey called pool.Release(key, prior) — which
	// reaches m.Shutdown() via coordinator.Release -> dropConnection — and
	// THEN called prior.Shutdown() again.
	//
	// Found by the ADR-084/085/086 CI run: the ui-heavy e2e shard died at
	// live.go:604 mid-reload and took eleven later tests with it as cascade
	// casualties. ui-heavy was the only shard affected because it is the only
	// one that actually drives Chrome, so it is the only one whose manager is
	// coordinator-registered.
	//
	// ADR-085 FR-031a introduced the sweeper and this close(); every other
	// step of BrowserManager.Shutdown was already idempotent (map deletes,
	// nil'd cancels, started=false) and its doc comment says so. This restores
	// that property. Taking r.mu makes concurrent Shutdowns safe too, and
	// nil'ing sweepStop under the lock is what makes the second call a no-op.
	r.mu.Lock()
	stop := r.sweepStop
	done := r.sweepDone
	r.sweepStop = nil
	r.mu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	if done != nil {
		<-done
	}
}

// sweepTick is one FR-031a/FR-052 pass: for every tab set currently stood
// down, release it if either (a) tools.browser.take_control_enabled is
// false (FR-052 — regardless of the idle window) or (b) its liveness window
// has elapsed (FR-031a). Liveness for a tab set WITH an attached viewer is
// TouchControlActivity's lastControlActivity clock; for a stood-down state
// with NO attached viewer (a handover nobody came to, a latch left behind
// after the operator closed the panel) there is no liveness signal by
// construction, so the window runs unconditionally from the FR-044
// hold-start transition instead. A zero idle window (operator config 0)
// disables expiry entirely UNLESS the disabled-flag branch already forces
// the release.
func (r *LiveViewRegistry) sweepTick() {
	disabled := !r.mgr.cfg.TakeControlEnabled
	idleWindow := r.mgr.cfg.ControlIdleRelease

	r.mu.Lock()
	keys := make([]string, 0, len(r.views))
	for k := range r.views {
		keys = append(keys, k)
	}
	r.mu.Unlock()

	now := time.Now()
	for _, sessionID := range keys {
		r.mu.Lock()
		lv, ok := r.views[sessionID]
		r.mu.Unlock()
		if !ok {
			continue
		}
		lv.mu.Lock()
		snap := lv.controlActivitySnapshotLocked()
		lv.mu.Unlock()
		if !snap.stoodDown {
			continue
		}

		expired := disabled
		if !expired && idleWindow > 0 {
			if snap.hasAttachedViewer {
				expired = now.Sub(snap.lastControlActivity) >= idleWindow
			} else if snap.holdStartedAtUnixNano != 0 {
				expired = now.Sub(time.Unix(0, snap.holdStartedAtUnixNano)) >= idleWindow
			}
		}
		if !expired {
			continue
		}

		formerHolder, cleared := r.ReleaseStoodDown(sessionID)
		if !cleared {
			continue
		}
		hooks := r.effectiveIdleReleaseHooks()
		switch {
		case disabled && hooks.OnDisabledRelease != nil:
			hooks.OnDisabledRelease(sessionID, formerHolder)
		case !disabled && hooks.OnIdleRelease != nil:
			hooks.OnIdleRelease(sessionID, formerHolder)
		}
	}
}

// enterStandDownLocked marks lv as stood down (ADR-085 D3/FR-026a: a human
// take or an agent handover) and, only on a genuine transition INTO a
// stood-down state, mints the FR-044 hold-start timestamp used to derive one
// deterministic waiting-surface message id per unbroken hold. Idempotent
// within one unbroken hold — a second take by the same or a different
// viewer with no intervening release does not re-stamp the timestamp, so it
// does not produce a second visible line. Must be called with lv.mu held.
func (lv *LiveView) enterStandDownLocked() (transitioned bool, holdStartedAtUnixNano int64) {
	wasStoodDown := lv.controller != "" || lv.standDownUntilPrompt || lv.handoverPending
	lv.standDownUntilPrompt = true
	if !wasStoodDown || lv.holdStartedAtUnixNano == 0 {
		lv.holdStartedAtUnixNano = time.Now().UnixNano()
		transitioned = true
	}
	lv.lastControlActivity = time.Now()
	return transitioned, lv.holdStartedAtUnixNano
}

// clearStandDownLocked performs the ADR-085 server-initiated release: it
// clears the control lock (if held), the FR-026a stand-down latch, the
// FR-047 handover-pending state and its reason, and the FR-044 hold-start
// timestamp, all together — the "release", as distinct from a mere
// releaseControl (which only clears the interactive lock and is reachable
// from many more places, e.g. Escape, that must NOT clear the latch). Must
// be called with lv.mu held. Returns the former controller id ("" if nobody
// held the interactive lock — a handover-only or latch-only stand-down has
// no controller to report) and whether anything was actually cleared.
func (lv *LiveView) clearStandDownLocked() (formerHolder string, cleared bool) {
	formerHolder = lv.controller
	cleared = lv.controller != "" || lv.standDownUntilPrompt || lv.handoverPending
	lv.controller = ""
	lv.standDownUntilPrompt = false
	lv.handoverPending = false
	lv.handoverReason = ""
	lv.holdStartedAtUnixNano = 0
	return formerHolder, cleared
}

// isStoodDownLocked reports whether the ADR-085 control gate must defer for
// this tab set right now. Must be called with lv.mu held.
//
//   - A LIVE human hold (a still-attached controller) always defers.
//   - A hold with NO viewer bookkeeping at all (lv.viewers is empty) also
//     defers — TakeControl's own contract is "control can be requested
//     before/without an attached viewer" (its doc comment), which this
//     package's own tests rely on throughout as the normal way to simulate
//     a human hold without the heavier Attach() setup. An empty viewers map
//     cannot distinguish "nobody has ever attached" from "everybody who
//     ever attached is now gone", so it is read as the former, not the
//     ghost FR-031 exists for.
//   - A GHOST controller — present in lv.controller, but OTHER viewer(s)
//     ARE currently attached and the controller is not among them — voids
//     the hold AND the FR-026a latch together (FR-031). This is the case
//     the requirement is actually about: a crashed panel's stale lock
//     blocking a genuinely-attached replacement viewer. It never voids the
//     FR-047 handover-pending state, which is explicitly exempt from the
//     ghost rule because it has no viewer to go ghost in the first place.
//   - With no controller at all, a deliberate release (Escape, closing the
//     panel) leaves the latch or the handover-pending state to keep
//     deferring until the next prompt or the idle timer — that is the whole
//     point of FR-026a/FR-047.
func (lv *LiveView) isStoodDownLocked() bool {
	if lv.controller != "" {
		if len(lv.viewers) == 0 {
			return true
		}
		if _, attached := lv.viewers[lv.controller]; attached {
			return true
		}
		// Ghost: the hold and the latch are void together (FR-031/FR-026a),
		// but a handover-pending state survives it (FR-047).
		return lv.handoverPending
	}
	return lv.handoverPending || lv.standDownUntilPrompt
}

// IsControlledByLiveViewer reports whether sessionID's live view is
// currently held (ADR-085 FR-031) — see isStoodDownLocked's doc comment for
// the exact ghost rule, including why an EMPTY lv.viewers reads as a live
// hold rather than a ghost. Unlike IsControlled/Controller (the pre-existing
// status/presentation reads, left untouched — panel display and
// pkg/gateway/browser_ws.go's F3 gate still depend on their viewer-blind
// semantics), this treats a genuine ghost holder — one who left lv.viewers
// WHILE ANOTHER VIEWER IS attached — as uncontrolled, so the tool gate does
// not defer to a viewer who can never come back to release it.
func (r *LiveViewRegistry) IsControlledByLiveViewer(sessionID string) bool {
	sessionID = r.resolveSessionID(sessionID)
	lv, ok := r.lookup(sessionID)
	if !ok {
		return false
	}
	lv.mu.Lock()
	defer lv.mu.Unlock()
	if lv.controller == "" {
		return false
	}
	if len(lv.viewers) == 0 {
		return true
	}
	_, attached := lv.viewers[lv.controller]
	return attached
}

// IsStoodDown reports whether sessionID's tab set is in a stood-down state
// for the ADR-085 control gate — see isStoodDownLocked for the exact rule.
// "" / an unknown session is never stood down.
func (r *LiveViewRegistry) IsStoodDown(sessionID string) bool {
	sessionID = r.resolveSessionID(sessionID)
	lv, ok := r.lookup(sessionID)
	if !ok {
		return false
	}
	lv.mu.Lock()
	defer lv.mu.Unlock()
	return lv.isStoodDownLocked()
}

// StoodDownHolder returns the viewerID currently recorded as sessionID's
// controller, INCLUDING a ghost (unlike IsControlledByLiveViewer) — used
// only for audit attribution (BROWSER-FR-061/062), where "who held it last"
// remains meaningful even if that holder has since vanished. "" if nobody
// (ever) held it.
func (r *LiveViewRegistry) StoodDownHolder(sessionID string) string {
	return r.Controller(sessionID)
}

// SetHandoverPending puts sessionID's tab set into the ADR-085 FR-047
// agent-initiated handover-pending state: a stand-down distinct from
// lv.controller because the agent may be handing the browser to an operator
// with no panel currently attached. reason is the FR-048a model-authored
// explanation (already truncated by the caller). Creates the LiveView if one
// does not exist yet, mirroring TakeControl. Returns whether this is a fresh
// transition (so the caller emits the FR-041 waiting surface exactly once
// per unbroken hold) and the FR-044 hold-start timestamp to derive the
// notice's deterministic message id from.
func (r *LiveViewRegistry) SetHandoverPending(sessionID, reason string) (transitioned bool, holdStartedAtUnixNano int64) {
	sessionID = r.resolveSessionID(sessionID)
	lv := r.view(sessionID)
	lv.mu.Lock()
	wasStoodDown := lv.controller != "" || lv.standDownUntilPrompt || lv.handoverPending
	lv.handoverPending = true
	lv.handoverReason = reason
	if !wasStoodDown || lv.holdStartedAtUnixNano == 0 {
		lv.holdStartedAtUnixNano = time.Now().UnixNano()
		transitioned = true
	}
	lv.lastControlActivity = time.Now()
	holdStartedAtUnixNano = lv.holdStartedAtUnixNano
	notice := lv.noticeFor(StandDownByHandover, reason, holdStartedAtUnixNano)
	lv.mu.Unlock()

	// FR-041/FR-048: the waiting surface IS the operator's only signal that
	// the agent stopped and why — browser_handover's caller
	// (tools_handover.go) discards this method's return value, so raising it
	// here is what makes the handover visible at all. Emitted on the
	// TRANSITION only (FR-044) and with no lock held.
	if transitioned {
		emitStandDownNotice(notice)
	}
	return transitioned, holdStartedAtUnixNano
}

// SetReleaseNotifySink registers sink as the FR-031b unsolicited-release
// notifier for viewerID on sessionID's live view. Separate from Attach's
// callback quartet (rather than a new Attach parameter) so every existing
// Attach call site — including every test file outside this wave's
// write-set — is untouched; the gateway calls this once, immediately after
// a successful Attach, exactly as it already does for the other sinks. A
// no-op if sessionID has no live view yet (Attach always creates one first,
// so in practice this never observes that case in production) or viewerID
// is empty.
func (r *LiveViewRegistry) SetReleaseNotifySink(sessionID, viewerID string, sink ReleaseNotifySink) {
	sessionID = r.resolveSessionID(sessionID)
	lv, ok := r.lookup(sessionID)
	if !ok || viewerID == "" {
		return
	}
	lv.mu.Lock()
	if lv.releaseSinks == nil {
		lv.releaseSinks = make(map[string]ReleaseNotifySink)
	}
	lv.releaseSinks[viewerID] = sink
	lv.mu.Unlock()
}

// SetControlAcquiredSink reports the first dedicated-input take to its own
// viewer. ControlSink only reports changes to other viewers.
func (r *LiveViewRegistry) SetControlAcquiredSink(sessionID, viewerID string, sink func()) {
	sessionID = r.resolveSessionID(sessionID)
	lv, ok := r.lookup(sessionID)
	if !ok || viewerID == "" {
		return
	}
	lv.mu.Lock()
	defer lv.mu.Unlock()
	if _, attached := lv.viewers[viewerID]; !attached {
		return
	}
	if lv.acquiredSinks == nil {
		lv.acquiredSinks = make(map[string]func())
	}
	lv.acquiredSinks[viewerID] = sink
}

// ReleaseStoodDown performs the ADR-085 server-initiated release on ONE tab
// set: it clears the control lock (if held), the FR-026a latch and the
// FR-047 handover-pending state together, and notifies the former holder
// directly via its registered ReleaseNotifySink (FR-031b) if one is
// registered and the holder is still attached — a no-op, not an error, for
// a holder who has already left. Callers are responsible for the
// FR-020/FR-050 reachability fan-out across multiple tab sets; this acts on
// exactly the one sessionID given. Returns the former holder's viewerID
// ("" if nobody held it) and whether anything was actually cleared, so a
// caller can skip auditing/notifying a genuine no-op.
//
// EVERY OTHER ATTACHED VIEWER IS TOLD THE LOCK IS FREE (ADR-039 UAT BE-1).
// This is not an extra courtesy — it is the other half of the take fan-out,
// and omitting it is what made a second panel wedge. takeControl broadcasts
// controlledByOther=true to every viewer except the taker, so after a take
// each of them renders "Someone else is driving" and disables its own
// Take-control affordance. Only a matching controlledByOther=false broadcast
// clears that; a viewer that never receives one is locked out of a browser
// nobody is driving, for as long as it stays attached.
//
// The false half USED to ride on LiveView.releaseControl, which does its own
// broadcastControl(otherSinks, false). ADR-085 Finding 7(b) then repointed
// the gateway's release action (pkg/gateway/browser_ws.go::handleControl) at
// ReleaseStoodDown — correctly, because releaseControl clears only
// lv.controller and leaves the FR-026a latch standing — but the fan-out was
// not carried across. That left releaseControl's broadcast reachable from
// tests only, and left ReleaseStoodDown, which is now the ONLY production
// path that clears the lock other than detach, silent. Every server-initiated
// release routes through here (the FR-029 prompt release via
// ReleaseAllStoodDown, the FR-031a idle expiry and FR-052 switch-off via the
// sweeper, the operator's own release button via handleControl), so all four
// went stale-by-default, not just the button.
//
// The former holder is EXCLUDED, exactly as releaseControl excludes the
// releasing viewer: they are served by the dedicated ReleaseNotifySink above,
// which sends a real `released` lifecycle frame. A control_only frame could
// not clear their own isControlling anyway (FR-031b — see the sink's
// registration comment in handleAttach).
//
// Broadcast only when an INTERACTIVE holder was actually cleared. `cleared`
// is also true for a latch-only or handover-only stand-down, but neither of
// those ever set lv.controller, so no viewer was ever told
// controlled_by_other=true for them and there is nothing to correct.
func (r *LiveViewRegistry) ReleaseStoodDown(sessionID string) (formerHolder string, cleared bool) {
	formerHolder, cleared, _ = r.releaseStoodDown(sessionID, "")
	return formerHolder, cleared
}

// ReleaseStoodDownForViewer checks ownership and clears the entire hold in
// one critical section. An unattached second viewer cannot clear a live
// owner's hold between a separate Controller query and release.
func (r *LiveViewRegistry) ReleaseStoodDownForViewer(sessionID, viewerID string) bool {
	if viewerID == "" {
		return false
	}
	_, _, allowed := r.releaseStoodDown(sessionID, viewerID)
	return allowed
}

func (r *LiveViewRegistry) releaseStoodDown(sessionID, viewerID string) (formerHolder string, cleared, allowed bool) {
	sessionID = r.resolveSessionID(sessionID)
	lv, ok := r.lookup(sessionID)
	if !ok {
		return "", false, true
	}
	lv.mu.Lock()
	if viewerID != "" && lv.controller != "" && lv.controller != viewerID {
		lv.mu.Unlock()
		return "", false, false
	}
	formerHolder, cleared = lv.clearStandDownLocked()
	var notify ReleaseNotifySink
	var otherSinks []ControlSink
	if formerHolder != "" {
		if _, attached := lv.viewers[formerHolder]; attached {
			notify = lv.releaseSinks[formerHolder]
		}
		otherSinks = lv.snapshotControlSinksExceptLocked(formerHolder)
	}
	lv.mu.Unlock()
	if notify != nil {
		go notify()
	}
	broadcastControl(otherSinks, false)
	return formerHolder, cleared, true
}

// StoodDownRelease reports one tab set that ReleaseAllStoodDown actually
// cleared. FormerHolder is the released viewerID, "" for a handover-only or
// latch-only stand-down that had no interactive holder.
type StoodDownRelease struct {
	SessionID    string
	FormerHolder string
}

// ReleaseAllStoodDown performs the ADR-085 FR-029 prompt release across every
// tab set this registry holds, returning one entry per set that was actually
// cleared (never an entry for a set that was already free, so the caller's
// audit trail carries no phantom releases). Each release goes through
// ReleaseStoodDown, so the FR-031b holder notification fires per set exactly
// as it does for the sweeper.
//
// WHY THE WHOLE REGISTRY AND NOT ONE KEY. FR-050 requires the prompt release
// to clear "every tab set reachable from the root chat session ... exactly the
// set FR-020's second check is evaluated against". The gateway, holding only
// the root chat's session id, cannot enumerate that set: a delegated child's
// tab set is keyed by the CHILD's own transcript session id (key.go's
// TabOwnerSession doc comment), which the gateway never sees. A registry is
// scoped to ONE BrowsingKey — i.e. one workspace's browser (ADR-075 FR-001) —
// so clearing all of its stood-down sets is a superset of FR-050's set,
// bounded by the workspace the prompt arrived on. The widening is deliberate
// and is the conservative direction: the failure it replaces (Finding 7) is an
// agent locked out for the process lifetime, and every extra set it clears is
// one an operator on this same workspace stopped holding the moment they
// typed.
func (r *LiveViewRegistry) ReleaseAllStoodDown() []StoodDownRelease {
	r.mu.Lock()
	keys := make([]string, 0, len(r.views))
	for k := range r.views {
		keys = append(keys, k)
	}
	r.mu.Unlock()

	var released []StoodDownRelease
	for _, sessionID := range keys {
		formerHolder, cleared := r.ReleaseStoodDown(sessionID)
		if !cleared {
			continue
		}
		released = append(released, StoodDownRelease{SessionID: sessionID, FormerHolder: formerHolder})
	}
	return released
}

// TouchControlActivity resets the FR-031a idle-release liveness clock for
// sessionID — called on viewer input, attach, detach, and the live panel's
// WS pong (manager.ViewerHeartbeat's own call site in
// pkg/gateway/browser_ws.go also calls this). A no-op for a session with no
// live view yet.
func (r *LiveViewRegistry) TouchControlActivity(sessionID string) {
	sessionID = r.resolveSessionID(sessionID)
	lv, ok := r.lookup(sessionID)
	if !ok {
		return
	}
	lv.mu.Lock()
	lv.lastControlActivity = time.Now()
	lv.mu.Unlock()
}

// controlActivitySnapshotLocked returns the fields the FR-031a sweeper needs
// to judge this view, in one critical section. Must be called with lv.mu
// held.
type controlActivitySnapshot struct {
	stoodDown             bool
	hasAttachedViewer     bool
	lastControlActivity   time.Time
	holdStartedAtUnixNano int64
}

func (lv *LiveView) controlActivitySnapshotLocked() controlActivitySnapshot {
	return controlActivitySnapshot{
		stoodDown:             lv.isStoodDownLocked(),
		hasAttachedViewer:     len(lv.viewers) > 0,
		lastControlActivity:   lv.lastControlActivity,
		holdStartedAtUnixNano: lv.holdStartedAtUnixNano,
	}
}
