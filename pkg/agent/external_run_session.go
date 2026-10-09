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
//     the retained native conversation or refuse VISIBLY (errExternalResumeUnavailable)
//     — never a silent fresh conversation. `started` records "this session key
//     has run before" so the refusal survives the driver release below.
//   - When a run exits and the session has no pending continuation
//     (pendingSteeringCountForScope == 0), the DRIVER is released
//     (releaseDriverLocked): its retained RunOptions — the last prompt and the
//     environment snapshot — stop being reachable, bounding retention to the
//     live episode (N6). The tiny holder itself stays (a mutex, a few booleans,
//     nil fields), because `started` is what makes a later follow-up refuse
//     rather than silently relaunch a fresh conversation.
//   - A task-origin session (Origin.Kind == task) keeps its explicit fresh Run
//     per turn until the U6 CONTINUE / native-identity work lands; it is exempt
//     from the steered resume-only rule.
package agent

import (
	"context"
	"errors"
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
//     turns; it is released (set nil) once the run exited and no continuation
//     is pending (N6), but `started` is kept so a later follow-up refuses rather
//     than relaunching a fresh conversation (N5/N7).
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
	mu          sync.Mutex
	driver      runner.ExternalAgentRunner
	started     bool
	running     bool
	claim       executionClaim
	cancelRun   context.CancelFunc
	workDir     string
	workspaceID string
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

// externalRunIsTaskSession reports whether sessionKey's durable record names a
// task origin (OriginKindTask) — the marker that exempts the run from the
// steered "resume-only" rule (the task path keeps its explicit fresh Run per
// turn until the U6 CONTINUE work).
func (al *AgentLoop) externalRunIsTaskSession(sessionKey string) bool {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil || sessionKey == "" {
		return false
	}
	rec, err := lifecycle.Load(sessionKey)
	if err != nil || rec == nil {
		return false
	}
	return rec.Origin != nil && rec.Origin.Kind == session.OriginKindTask
}

// beginExternalRun selects the driver for one external-CLI dispatch and marks
// the session running until finishExternalRun. It returns the driver to call
// plus whether the caller must Resume (true) or Run (false).
//
//   - A Resume is selected when the caller requested a continuation
//     (externalResumeRequested) OR this is a later entry of a steered session
//     (sess.started, N7) AND a driver is retained. The resume path never creates
//     a driver: it reuses the one holding the captured native conversation id
//     and the preserved RunOptions.
//   - When a resume is required but no driver is retained (N5: the driver was
//     released, the original dispatch failed before construction, …) it refuses
//     with errExternalResumeUnavailable — NEVER a fresh fallback.
//   - A FRESH Run is permitted only for a genuine first launch of a steered
//     session, or for any entry of a task-origin session (which keeps its
//     fresh-Run-per-turn behaviour until U6).
//
// isTaskRun is the caller's own determination (it holds the run's context and
// the durable record); it is passed in rather than re-derived so this function
// holds no lifecycle lock while sess.mu is held.
func (al *AgentLoop) beginExternalRun(
	sessionKey string,
	ts *turnState,
	cli string,
	consent runner.ConsentHandler,
	cancel context.CancelFunc,
	isTaskRun bool,
) (sess *externalCLIRunSession, driver runner.ExternalAgentRunner, resume bool, err error) {
	sess = al.externalRunSession(sessionKey)
	sess.mu.Lock()
	defer sess.mu.Unlock()

	claim := al.tsExecutionClaim(ts, sessionKey)
	resumeRequested := externalResumeRequested(ts)
	needsResume := resumeRequested || (sess.started && !isTaskRun)
	if needsResume {
		if sess.driver == nil {
			// N5/N7: a continuation (or a later steered entry) with no retained
			// driver must refuse — never start a fresh conversation.
			return sess, nil, false, errExternalResumeUnavailable
		}
		sess.running = true
		sess.claim = claim
		sess.cancelRun = cancel
		return sess, sess.driver, true, nil
	}

	driver, err = newExternalDriver(cli, consent)
	if err != nil {
		return nil, nil, false, err
	}
	sess.driver = driver
	sess.started = true
	sess.running = true
	sess.claim = claim
	sess.cancelRun = cancel
	return sess, driver, false, nil
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

// finishExternalRun ends the session's run: it clears the running flag and, when
// no continuation is pending, releases the driver (N6). Holding sess.mu across
// both the steering-queue check and the release makes it atomic with a
// concurrent delivery (which takes sess.mu to check-run/enqueue/cancel): either
// the delivery enqueues first (the queue is non-empty, so the driver is KEPT for
// the drain that consumes it), or this runs first (the delivery then sees
// running=false and refuses — no stranded instruction).
func (al *AgentLoop) finishExternalRun(sess *externalCLIRunSession, sessionKey string) {
	if sess == nil {
		return
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.running = false
	sess.cancelRun = nil
	if al.pendingSteeringCountForScope(sessionKey) == 0 {
		sess.releaseDriverLocked()
	}
}

// releaseDriverLocked drops the retained driver (and with it its RunOptions
// snapshot: the last prompt and the child environment) while keeping the
// session's `started` marker so a later follow-up refuses rather than relaunching
// a fresh CLI conversation. Caller holds sess.mu.
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
