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
// later continuation (ts.opts.ExternalCLIResume) Resumes it.
//
// The registry is per-AgentLoop (al.externalRunSessions), never a package
// global, so concurrent AgentLoops in one process (tests) never share a driver.
// It is read/written only by the external-CLI dispatch path; the "running"
// flag exists so a steer can tell a genuinely live conversation from a session
// that merely ran once and has since ended (used by
// external_run_delivery.go's 3P delivery).
//
// Growth is bounded the same way workspaceRunLocks (external_dispatch.go) is:
// one entry per distinct child session that has ever run an external CLI on
// this loop — an operator-controlled set (the configured external-CLI
// delegate targets), not the run count. Entries are never evicted: a follow-up
// can arrive long after a turn ends, and evicting on session terminal would
// break that resume, so the map is an accepted, documented bound rather than a
// leak.
package agent

import (
	"sync"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
)

// externalCLIRunSession holds one child session's live external-CLI driver and
// its last-started run identity, guarded by mu. driver is the instance that ran
// (or is running) the session's external-CLI turns; started is true once a run
// has begun on it; running is true only while a run is in flight, so a steer
// delivery can refuse visibly when there is genuinely no live conversation
// (BDD-05.6) rather than queueing a message nobody will ever drain.
type externalCLIRunSession struct {
	mu      sync.Mutex
	driver  runner.ExternalAgentRunner
	started bool
	running bool
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

// externalResumeRequested reports whether this run is a CONTINUATION that must
// deliver its instruction to an existing native CLI conversation (FR-043)
// rather than a first run. It is set only by the post-turn steering drain
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

// beginExternalRun selects the driver for one external-CLI dispatch and marks
// the session running until endRun. It returns the driver to call plus whether
// the caller must Resume (true) or Run (false).
//
// A Resume is selected only when the caller requested a continuation AND this
// session already has a driver that ran (sess.started). Any other case — a
// first run, a task run, or a continuation with no prior driver on this loop —
// creates a fresh driver and Runs, so an ordinary first dispatch is byte-for-
// byte unchanged. The resume path never creates a driver: it reuses the one
// holding the captured native conversation id and the preserved RunOptions.
func (al *AgentLoop) beginExternalRun(
	sessionKey string,
	ts *turnState,
	cli string,
	consent runner.ConsentHandler,
) (sess *externalCLIRunSession, driver runner.ExternalAgentRunner, resume bool, err error) {
	sess = al.externalRunSession(sessionKey)
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if externalResumeRequested(ts) && sess.started && sess.driver != nil {
		sess.running = true
		return sess, sess.driver, true, nil
	}
	driver, err = newExternalDriver(cli, consent)
	if err != nil {
		return nil, nil, false, err
	}
	sess.driver = driver
	sess.started = true
	sess.running = true
	return sess, driver, false, nil
}

// endRun clears the session's running flag once its run (including the event
// drain) has finished. Safe to call more than once.
func (s *externalCLIRunSession) endRun() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
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
	any, ok := al.externalRunSessions.Load(sessionKey)
	if !ok {
		return false
	}
	sess, ok := any.(*externalCLIRunSession)
	if !ok {
		return false
	}
	return sess.isRunning()
}
