// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// external_run_session.go — the per-child-session holder of the live external
// CLI driver, so a follow-up instruction can be delivered to the SAME native
// CLI conversation by interrupt + resume instead of a fresh Run (FR-043).
//
// Why a per-session holder and not a fresh driver per call: the CLI's own
// native conversation id (claude session_id / codex thread_id / opencode
// session_id) is captured on the DRIVER that ran the first turn (its unexported
// nativeID), and the resume must reuse that same driver's captured id AND its
// prior RunOptions so runtime/workspace/model/caps are preserved (FR-043). A
// brand-new driver has neither, so it could only start a FRESH conversation —
// exactly the silent fresh-run FR-043 forbids. So the driver that ran the first
// turn is retained here, keyed by the child's own transcript session id, and a
// later continuation Resumes it.
//
// The registry is per-AgentLoop (al.externalRunSessions), never a package
// global, so concurrent AgentLoops in one process (tests) never share a driver.
// It is read/written only by the external-CLI dispatch path.
//
// Lifecycle (N5/N6/N7, FR-043):
//
//   - A holder is created on the first dispatch of a session key. The FIRST
//     launch of a steered session is the ONLY entry permitted to start a FRESH
//     CLI conversation (Run); every later entry (the post-turn drain
//     continuation, a revive of a stopped/finished session, a wake) must Resume
//     the retained native conversation or refuse VISIBLY
//     (errExternalResumeUnavailable) — never a silent fresh conversation.
//     `started` records "this session key has run before" so the refusal
//     survives the driver release below.
//   - When the whole steered episode ends (after the run has exited AND its
//     post-turn drain has emptied the steering scope), the DRIVER is released
//     (releaseExternalRunIfIdle): its retained RunOptions — the last prompt and
//     the environment snapshot — stop being reachable, bounding retention to
//     the live episode (N6). The tiny holder itself stays (a mutex, a few
//     booleans, nil fields), because `started` is what makes a later follow-up
//     refuse rather than silently relaunch a fresh conversation. Stop and
//     session deletion release it too (ForgetExternalRunSession).
//   - A task-origin session (Origin.Kind == task) keeps its explicit fresh Run
//     per turn until the U6 CONTINUE / native-identity work lands; it is exempt
//     from the steered resume-only rule.
package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// errExternalResumeUnavailable is FR-043's "missing driver visibly refuses; no
// fresh fallback" (N5) and N7's "a later entry may not silently start a fresh
// conversation". It is returned by beginExternalRun when a continuation — or
// any non-first entry of a steered session — has no retained driver to resume.
var errExternalResumeUnavailable = errors.New(
	"external-cli: the external CLI conversation is no longer available; start a new delegation")

// errExternalWorkspaceChanged is FR-043's "workspace preserved, or refuse"
// (N1): a continuation resolved a different workspace from the one the native
// CLI conversation started in, so the agent is no longer eligible for it. The
// run refuses rather than authorizing one workspace while executing in another.
var errExternalWorkspaceChanged = errors.New(
	"external-cli: this worker no longer belongs to the workspace its CLI conversation runs in")

// externalCLIRunSession holds one child session's live external-CLI driver and
// the identity of the run it is currently serving, guarded by mu.
//
//   - driver is the instance that ran (or is running) the session's external-CLI
//     turns; it is released (set nil) once the episode ends (N6), but `started`
//     is kept so a later follow-up refuses rather than relaunching a fresh
//     conversation (N5/N7).
//   - started is true once a run has begun on this session key.
//   - running is true only while a run is in flight, so a steer delivery can
//     refuse visibly when there is genuinely no live conversation (BDD-05.6).
//   - claim is the selected execution identity of the in-flight run (N2): a
//     delivery bound to a superseded claim is refused.
//   - cancelRun is the in-flight run's own context.CancelFunc (N2): the delivery
//     cancels THIS run, never "whatever turn is current for the session id".
//   - workDir/workspaceID are the workspace the native CLI conversation started
//     in (N1): a continuation must re-resolve and re-lock THAT workspace, or
//     refuse.
type externalCLIRunSession struct {
	mu        sync.Mutex
	driver    runner.ExternalAgentRunner
	started   bool
	running   bool
	claim     executionClaim
	cancelRun context.CancelFunc
	// steerInterrupt is set (under mu) by a steer delivery to a TASK-origin run:
	// the CLI run was interrupted so the task loop can resume it with the queued
	// instruction. It is consumed once by takeExternalSteerInterrupt.
	steerInterrupt bool
	workDir        string
	workspaceID    string
	// reservations holds (under mu) one OWNED entry per revival that has
	// verified this holder still retains the driver its CLI conversation needs
	// (NEW-4/NEW-5). While any entry is live, releaseExternalRunIfIdle keeps the
	// driver, so a completion that is still unwinding cannot release it between
	// a revival's availability check and the revived turn's own
	// beginExternalRun. Each entry is removed only by its owner (cancel), by the
	// turn of the generation it was bound to (beginExternalRun or the end of that
	// turn, NEW-6), or by Stop — never by another revival.
	reservations map[*externalConversationReservation]struct{}
}

// externalRunSessionKey resolves the registry key for a run — the child's own
// store-backed transcript session id (steer_reconstruct.go sets
// TranscriptSessionID = rec.SessionID), stable across a session's first run and
// every later continuation. turnID is a defensive fallback for a fixture that
// left the transcript id empty; it is never the same across two calls, so such
// a session simply never resumes (a fresh Run), which is the safe direction.
func externalRunSessionKey(ts *turnState) string {
	if ts == nil {
		return ""
	}
	if ts.transcriptSessionID != "" {
		return ts.transcriptSessionID
	}
	return ts.turnID
}

// externalResumeRequested reports whether this run is an explicit CONTINUATION
// that must deliver its instruction to an existing native CLI conversation
// (FR-043). It is set only by the post-turn steering drain
// (steer_turn_drain.go::continueSteeredTurn), i.e. exactly when a queued
// follow-up instruction is being delivered to a session whose CLI turn already
// ran — never by an ordinary first dispatch, a task run, or a wake.
func externalResumeRequested(ts *turnState) bool {
	return ts != nil && ts.opts.ExternalCLIResume
}

// externalRunSession returns sessionKey's holder, creating it on first use.
func (al *AgentLoop) externalRunSession(sessionKey string) *externalCLIRunSession {
	fresh := &externalCLIRunSession{}
	holder, _ := al.externalRunSessions.LoadOrStore(sessionKey, fresh)
	sess, ok := holder.(*externalCLIRunSession)
	if !ok || sess == nil {
		// externalRunSessions is populated exclusively here with
		// *externalCLIRunSession values; a different type under the key is a
		// programming error. Fail loudly rather than hand back a nil holder a
		// caller would dereference.
		panic("externalRunSessions: value is not *externalCLIRunSession")
	}
	return sess
}

// externalRunSessionIfPresent returns sessionKey's holder WITHOUT creating one
// (nil when this loop has never dispatched the session), so a delivery can tell
// "there is genuinely no live conversation" from "there is one".
func (al *AgentLoop) externalRunSessionIfPresent(sessionKey string) *externalCLIRunSession {
	holder, ok := al.externalRunSessions.Load(sessionKey)
	if !ok {
		return nil
	}
	sess, ok := holder.(*externalCLIRunSession)
	if !ok || sess == nil {
		return nil
	}
	return sess
}

// externalRunResumeOnly reports whether sessionKey names a STEERED (delegated,
// non-task) session — the case N7 governs: only its generation-1 launch
// dispatch may start a fresh CLI conversation, every later entry (revive, wake,
// drain) resumes or refuses. A task-origin session is excluded (it keeps its
// explicit fresh Run per turn until the U6 CONTINUE work); a session with no
// durable record is not a delegated session and keeps today's fresh-Run
// behaviour (this is what a bare fixture turnState resolves to).
//
// NEW-3: a lifecycle READ FAILURE is not "no record". It returns an error, and
// the caller refuses: treating an unreadable real session as a non-steered one
// would skip the durable start mark, and after a restart a revival would start a
// fresh CLI conversation inside the old chat. Only a missing store, an empty key
// or a genuinely absent record (ErrLifecycleNotFound) is "not a steered session".
func (al *AgentLoop) externalRunResumeOnly(sessionKey string) (bool, error) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil || sessionKey == "" {
		return false, nil
	}
	rec, err := lifecycle.Load(sessionKey)
	if errors.Is(err, session.ErrLifecycleNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("external-cli: read the session record for %q: %w", sessionKey, err)
	}
	if rec == nil {
		return false, nil
	}
	if rec.Origin != nil && rec.Origin.Kind == session.OriginKindTask {
		return false, nil
	}
	return rec.SteeredBy != nil, nil
}

// externalRunPriorStart reports whether the durable lifecycle record says an
// external CLI run already started for this session (LifecycleRecord.
// ExternalRunStarted). It is the restart-proof half of `started` (N7) and is
// consulted only for a steered session, whose record existed when
// externalRunResumeOnly read it. A missing store or key is "no mark" (a
// record-less fixture) and so is a record that is genuinely absent
// (ErrLifecycleNotFound); an unreadable record is an ERROR (NEW-3), never "no
// mark" — uncertainty about a started conversation must refuse.
func (al *AgentLoop) externalRunPriorStart(sessionKey string) (bool, error) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil || sessionKey == "" {
		return false, nil
	}
	rec, err := lifecycle.Load(sessionKey)
	if errors.Is(err, session.ErrLifecycleNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("external-cli: read the CLI conversation start mark for %q: %w", sessionKey, err)
	}
	return rec != nil && rec.ExternalRunStarted, nil
}

// markExternalRunStarted durably records, on the session's lifecycle record,
// that its first external CLI run is about to start, so every later entry —
// including one after a gateway restart — resumes or refuses instead of
// starting a fresh conversation (N7). A failure to record it refuses the run:
// starting a CLI conversation that a later entry could not recognise is exactly
// the silent fresh fallback FR-043 forbids.
func (al *AgentLoop) markExternalRunStarted(sessionKey string) error {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return errors.New("external-cli: lifecycle store is not wired; cannot record the CLI conversation start")
	}
	if err := lifecycle.Mutate(sessionKey, func(rec *session.LifecycleRecord) error {
		if rec == nil {
			return session.ErrLifecycleNotFound
		}
		rec.ExternalRunStarted = true
		return nil
	}); err != nil {
		return fmt.Errorf("external-cli: record the CLI conversation start for %q: %w", sessionKey, err)
	}
	return nil
}

// externalConversationReservation is ONE revival's owned hold on the retained
// driver. It is taken together with the availability decision, under the holder
// lock (NEW-4), and is a distinct identity per revival (NEW-5): two overlapping
// revivals each hold their own entry, so one giving up never ends the other's
// hold. After dispatch it is bound to the exact execution it was dispatched as
// (generation + run id): only that execution's own settlement — the turn
// reaching the holder (beginExternalRun), the turn ending, a failed promotion, or
// an accepted Stop selecting that admission (NEW-6/NEW-7) — or its owner's
// cancel can end it. Stop never clears a hold it did not select, and a stopped
// Resume that keeps its generation but gets a new run id keeps its own hold.
type externalConversationReservation struct {
	al   *AgentLoop
	key  string
	sess *externalCLIRunSession
	// gen/runID name the execution that will consume the hold; zero until bind
	// (a wake replay binds the generation only, runID ""). Guarded by sess.mu.
	gen   int
	runID string
}

// bind records the execution this revival dispatches, so that execution (and
// only it) can consume or retire the hold. A no-op for a nil or already-ended
// reservation.
func (r *externalConversationReservation) bind(gen int, runID string) {
	if r == nil || r.sess == nil {
		return
	}
	r.sess.mu.Lock()
	defer r.sess.mu.Unlock()
	if _, held := r.sess.reservations[r]; held {
		r.gen, r.runID = gen, runID
	}
}

// cancel ends THIS reservation only, then re-evaluates the ordinary idle release
// (NEW-9): the old episode's disposal may already have run its one release while
// this hold was live, and nothing else would run it again. It is a no-op for a
// nil reservation (a revival that needed none), idempotent, and never touches
// another revival's hold (NEW-5) — the idle release itself keeps the driver
// while any hold, run or pending steer remains.
func (r *externalConversationReservation) cancel() {
	if r == nil || r.sess == nil {
		return
	}
	r.cancelQuietly()
	if r.al != nil {
		r.al.releaseExternalRunIfIdle(r.key)
	}
}

// cancelQuietly ends this reservation without any release: the read-only
// availability probe must not release a driver as a side effect.
func (r *externalConversationReservation) cancelQuietly() {
	if r == nil || r.sess == nil {
		return
	}
	r.sess.mu.Lock()
	delete(r.sess.reservations, r)
	r.sess.mu.Unlock()
}

// matchesLocked reports whether the hold belongs to execution claim: same
// generation and, when the hold names a run, the same run id. Caller holds the
// holder mutex.
func (r *externalConversationReservation) matchesLocked(claim executionClaim) bool {
	return r.gen != 0 && r.gen == claim.Generation && (r.runID == "" || r.runID == claim.RunID)
}

// dropReservationsForExecutionLocked ends every hold that belongs to execution
// claim. Holds of another generation or another run, and holds not yet bound,
// are untouched. Caller holds s.mu.
func (s *externalCLIRunSession) dropReservationsForExecutionLocked(claim executionClaim) {
	for r := range s.reservations {
		if r.matchesLocked(claim) {
			delete(s.reservations, r)
		}
	}
}

// retireExternalReservations ends the holds that belong to execution claim once
// that execution can no longer consume them — its turn ended (whatever the
// outcome), its promotion failed, or an accepted Stop removed its queued
// admission — and then re-runs the ordinary idle release: the old episode's one
// release may already have skipped for this hold (NEW-6, NEW-9).
func (al *AgentLoop) retireExternalReservations(sessionKey string, claim executionClaim) {
	sess := al.externalRunSessionIfPresent(sessionKey)
	if sess == nil {
		return
	}
	sess.mu.Lock()
	sess.dropReservationsForExecutionLocked(claim)
	sess.mu.Unlock()
	al.releaseExternalRunIfIdle(sessionKey)
}

// reserveExternalConversation decides whether reviving sessionKey's steered
// external-CLI helper can continue a retained CLI conversation and, when it can,
// reserves that driver for the revival in the same holder-lock hold. A helper
// whose external run never started (a queued child stopped before it ran) has no
// conversation to lose, so its first run may still be fresh (nil reservation,
// nil error). Otherwise a driver must still be retained; when it is not
// (released at the end of the episode, or the gateway restarted) the revive must
// refuse visibly instead of dispatching a turn that would start a fresh CLI
// conversation in the old chat (N7). A lifecycle read failure refuses too
// (NEW-3). The returned reservation keeps releaseExternalRunIfIdle from dropping
// the driver until the revived turn begins (NEW-4).
func (al *AgentLoop) reserveExternalConversation(sessionKey string, rec *session.LifecycleRecord) (*externalConversationReservation, error) {
	if rec == nil || !rec.Is3P || !rec.ExternalRunStarted {
		return nil, nil //nolint:nilnil // No external conversation to protect means no reservation, not an error.
	}
	resumeOnly, err := al.externalRunResumeOnly(sessionKey)
	if err != nil {
		return nil, err
	}
	if !resumeOnly {
		return nil, nil //nolint:nilnil // A first run may still be fresh: no reservation, not an error.
	}
	sess := al.externalRunSessionIfPresent(sessionKey)
	if sess == nil {
		return nil, errExternalResumeUnavailable
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.driver == nil {
		return nil, errExternalResumeUnavailable
	}
	res := &externalConversationReservation{al: al, key: sessionKey, sess: sess}
	if sess.reservations == nil {
		sess.reservations = make(map[*externalConversationReservation]struct{})
	}
	sess.reservations[res] = struct{}{}
	return res, nil
}

// externalConversationAvailable is the point-in-time form of
// reserveExternalConversation: it reports whether a revival could continue the
// conversation right now and holds nothing afterwards. Production revival uses
// the reservation; this form serves read-only questions and the gate tests.
func (al *AgentLoop) externalConversationAvailable(sessionKey string, rec *session.LifecycleRecord) bool {
	res, err := al.reserveExternalConversation(sessionKey, rec)
	res.cancelQuietly()
	return err == nil
}

// beginExternalRun reserves one external-CLI dispatch on the session holder and
// decides whether the caller must Resume (a retained conversation) or Run
// (a fresh one). It does NOT instantiate the driver: that happens after the
// caller's workspace-lock + cancel checks (driverForRun), so a run that is
// canceled before it can start never pays for a driver instantiation.
//
//   - A Resume is selected when the caller requested a continuation
//     (externalResumeRequested) OR this is a later entry of a steered session
//     (resumeOnly && (sess.started || the durable ExternalRunStarted mark), N7,
//     so the rule survives a gateway restart) AND a driver is retained. When a resume
//     is required but no driver is retained (N5: released, or the original
//     dispatch failed before construction) it refuses with
//     errExternalResumeUnavailable — NEVER a fresh fallback.
//   - A FRESH Run is selected otherwise (a genuine first launch; a task-origin
//     or non-steered session, which keeps its fresh-Run behaviour).
//
// resumeOnly is the caller's own determination (it holds the durable record):
// true for a steered (delegated, non-task) session, whose every entry after the
// first launch must resume-or-refuse.
func (al *AgentLoop) beginExternalRun(
	sessionKey string,
	ts *turnState,
	cancel context.CancelFunc,
	resumeOnly bool,
) (sess *externalCLIRunSession, resume bool, err error) {
	// priorRun is the DURABLE "this steered session already started an external
	// CLI run" fact (N7). The in-memory sess.started alone is lost on a gateway
	// restart, after which a revive of a stopped/finished helper would look
	// like a first launch and start a fresh CLI conversation under the old
	// chat. It is read before sess.mu is taken so the lifecycle lock is never
	// acquired under the holder lock.
	priorRun := false
	if resumeOnly {
		var priorErr error
		if priorRun, priorErr = al.externalRunPriorStart(sessionKey); priorErr != nil {
			return nil, false, priorErr
		}
	}

	sess = al.externalRunSession(sessionKey)
	sess.mu.Lock()
	defer sess.mu.Unlock()
	claim := al.tsExecutionClaim(ts, sessionKey)
	// The revived turn has reached the holder: the reservations bound to ITS
	// execution have done their job (the entry below either takes the driver or
	// refuses visibly). A reservation of another revival stays.
	sess.dropReservationsForExecutionLocked(claim)
	resumeRequested := externalResumeRequested(ts)
	needsResume := resumeRequested || (resumeOnly && (sess.started || priorRun))
	if needsResume {
		if sess.driver == nil {
			// N5/N7: a continuation (or a later steered entry) with no retained
			// driver must refuse — never start a fresh conversation.
			return sess, false, errExternalResumeUnavailable
		}
		sess.running = true
		sess.claim = claim
		sess.cancelRun = cancel
		return sess, true, nil
	}
	sess.running = true
	sess.claim = claim
	sess.cancelRun = cancel
	return sess, false, nil
}

// driverForRun returns the driver to call once the caller's pre-start checks
// have passed: the retained driver for a Resume, or a freshly created one for a
// Run (recorded on the holder so a later continuation can Resume it).
func (s *externalCLIRunSession) driverForRun(
	cli string,
	consent runner.ConsentHandler,
	resume bool,
) (runner.ExternalAgentRunner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if resume {
		if s.driver == nil {
			return nil, errExternalResumeUnavailable
		}
		return s.driver, nil
	}
	d, err := newExternalDriver(cli, consent)
	if err != nil {
		return nil, err
	}
	s.driver = d
	s.started = true
	return d, nil
}

// recordWorkspace remembers the workspace a steered session's CLI conversation
// started in (N1), so a later continuation can re-resolve and re-lock THAT
// workspace, or refuse when the agent is no longer eligible for it. Called once,
// on the first run.
func (s *externalCLIRunSession) recordWorkspace(workDir, workspaceID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.workDir = workDir
	s.workspaceID = workspaceID
	s.mu.Unlock()
}

// workspaceSnapshot returns the workspace (work dir + id) the session's native
// CLI conversation started in, guarded by sess.mu. Empty strings mean the first
// run has not recorded one yet.
func (s *externalCLIRunSession) workspaceSnapshot() (workDir, workspaceID string) {
	if s == nil {
		return "", ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workDir, s.workspaceID
}

// finishExternalRun ends the session's run: it clears the running flag and the
// retained cancel func for THIS run. It does not release the driver — a queued
// continuation may still be pending for the session's post-turn drain, and the
// release point is the end of the whole episode (releaseExternalRunIfIdle).
// Holding sess.mu here makes the running-flag transition atomic with a
// concurrent delivery (which takes sess.mu to check-run/enqueue/cancel): either
// the delivery enqueues first (the queue is non-empty, so the drain will consume
// it), or this runs first (the delivery then sees running=false and refuses — no
// stranded instruction).
func (al *AgentLoop) finishExternalRun(sess *externalCLIRunSession, sessionKey string) {
	_ = sessionKey
	if sess == nil {
		return
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.running = false
	sess.cancelRun = nil
}

// releaseExternalRunIfIdle releases sessionKey's retained driver (and with it
// its RunOptions snapshot: the last prompt and the child environment) once the
// session's live episode is over — the run has exited AND its post-turn drain
// has emptied the steering scope (N6). Callers are the episode ends: the steered
// turn's exit (disposeSteeredTurnResult), Stop, and session deletion. The tiny
// holder stays with `started` set, so a later follow-up refuses visibly
// (errExternalResumeUnavailable) rather than silently relaunching a fresh CLI
// conversation (N5/N7).
func (al *AgentLoop) releaseExternalRunIfIdle(sessionKey string) {
	if sessionKey == "" {
		return
	}
	sess := al.externalRunSessionIfPresent(sessionKey)
	if sess == nil {
		return
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.running || len(sess.reservations) != 0 || al.pendingSteeringCountForScope(sessionKey) != 0 {
		return
	}
	sess.releaseDriverLocked()
}

// ForgetExternalRunSession drops sessionID's retained external-CLI driver and
// its option/env snapshot, and removes the holder entirely — the session is
// being deleted, so no continuation can ever follow. Called by the session
// deletion paths (N6). Idempotent.
func (al *AgentLoop) ForgetExternalRunSession(sessionID string) {
	if sessionID == "" {
		return
	}
	if sess := al.externalRunSessionIfPresent(sessionID); sess != nil {
		sess.mu.Lock()
		sess.releaseDriverLocked()
		sess.mu.Unlock()
	}
	al.externalRunSessions.Delete(sessionID)
}

// releaseDriverLocked drops the retained driver (and with it its RunOptions
// snapshot) while keeping the session's `started` marker so a later follow-up
// refuses rather than relaunching a fresh CLI conversation. Caller holds sess.mu.
func (s *externalCLIRunSession) releaseDriverLocked() {
	s.driver = nil
	s.claim = executionClaim{}
	s.cancelRun = nil
}

// takeExternalSteerInterrupt reports whether the session's last run was
// interrupted by a steer delivery (rather than ending on its own or by a Stop),
// and clears the mark so one delivery resumes exactly once.
func (al *AgentLoop) takeExternalSteerInterrupt(sessionKey string) bool {
	sess := al.externalRunSessionIfPresent(sessionKey)
	if sess == nil {
		return false
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	was := sess.steerInterrupt
	sess.steerInterrupt = false
	return was
}
