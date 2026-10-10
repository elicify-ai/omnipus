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
	any, _ := al.externalRunSessions.LoadOrStore(sessionKey, fresh)
	sess, ok := any.(*externalCLIRunSession)
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
	any, ok := al.externalRunSessions.Load(sessionKey)
	if !ok {
		return nil
	}
	sess, ok := any.(*externalCLIRunSession)
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
func (al *AgentLoop) externalRunResumeOnly(sessionKey string) bool {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil || sessionKey == "" {
		return false
	}
	rec, err := lifecycle.Load(sessionKey)
	if err != nil || rec == nil {
		return false
	}
	if rec.Origin != nil && rec.Origin.Kind == session.OriginKindTask {
		return false
	}
	return rec.SteeredBy != nil
}

// externalRunPriorStart reports whether the durable lifecycle record says an
// external CLI run already started for this session (LifecycleRecord.
// ExternalRunStarted). It is the restart-proof half of `started` (N7). A missing
// store, an unreadable or absent record is "no mark": the caller keeps today's
// behaviour for a record-less fixture, and a real steered session always has a
// record (externalRunResumeOnly requires one before this is consulted).
func (al *AgentLoop) externalRunPriorStart(sessionKey string) bool {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil || sessionKey == "" {
		return false
	}
	rec, err := lifecycle.Load(sessionKey)
	return err == nil && rec != nil && rec.ExternalRunStarted
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

// externalConversationAvailable reports whether reviving sessionKey's steered
// external-CLI helper can continue a retained CLI conversation. A helper whose
// external run never started (a queued child stopped before it ran) has no
// conversation to lose, so its first run may still be fresh. Otherwise a driver
// must still be retained; when it is not (released at the end of the episode, or
// the gateway restarted), the revive must refuse visibly instead of dispatching
// a turn that would start a fresh CLI conversation in the old chat (N7).
func (al *AgentLoop) externalConversationAvailable(sessionKey string, rec *session.LifecycleRecord) bool {
	if rec == nil || !rec.Is3P || !rec.ExternalRunStarted || !al.externalRunResumeOnly(sessionKey) {
		return true
	}
	sess := al.externalRunSessionIfPresent(sessionKey)
	if sess == nil {
		return false
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.driver != nil
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
	priorRun := resumeOnly && al.externalRunPriorStart(sessionKey)

	sess = al.externalRunSession(sessionKey)
	sess.mu.Lock()
	defer sess.mu.Unlock()

	claim := al.tsExecutionClaim(ts, sessionKey)
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
	if sess.running || al.pendingSteeringCountForScope(sessionKey) != 0 {
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

// isRunning reports whether a run is in flight for this session.
func (s *externalCLIRunSession) isRunning() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// externalRunLive reports whether sessionKey currently has an external-CLI run
// in flight on this loop — the live-conversation precondition for delivering a
// steer by interrupt + resume. A session that merely ran once and ended is NOT
// live (its instruction would be orphaned with no drain to consume it).
func (al *AgentLoop) externalRunLive(sessionKey string) bool {
	return al.externalRunSessionIfPresent(sessionKey).isRunning()
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
