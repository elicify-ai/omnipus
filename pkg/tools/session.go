package tools

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const maxOutputBufferSize = 1 * 1024 * 1024 // 1MB

const outputTruncateMarker = "\n... [output truncated, exceeded 1MB]\n"

// SessionStatus is the typed lifecycle status of a ProcessSession (a
// background bash/exec session). Previously a bare string: a hand-maintained
// switch over it (shell.go's backgroundCompletionResult) missed the
// "canceled" case entirely when the RequestCancel kill cascade introduced it
// — a canceled background job fell through to a generic default and was
// misreported as a plain failure. Naming/pattern mirrors pkg/task.Status
// (pkg/task/task.go). The serialized/wire string VALUES below are
// unchanged — ProcessSession.Status is not a gateway wire-contract type
// (nothing json-marshals *ProcessSession directly; ExecResponse.Status and
// SessionInfo.Status remain plain `string` JSON fields, populated via an
// explicit string(...) conversion at the tool-response boundary) — only the
// Go type wrapping the values changed.
type SessionStatus string

const (
	// StatusRunning is the initial/live state: the process is still executing.
	StatusRunning SessionStatus = "running"
	// StatusDone is the generic terminal state: a natural exit, or a kill/
	// relabel path with no more specific label to apply (KillAll's shutdown
	// reaper — see its own doc comment for why it intentionally does not
	// relabel further).
	StatusDone SessionStatus = "done"
	// StatusKilled is set by an explicit bash action=kill call (shell.go's
	// executeKill), distinguishing it from a natural exit or a timeout.
	StatusKilled SessionStatus = "killed"
	// StatusTimeout is set when the bash tool's timeout_seconds guard fires
	// (shell.go's runBackground), distinguishing it from an explicit kill.
	StatusTimeout SessionStatus = "timeout"
	// StatusCanceled is set by SessionManager.KillAllForSession — the
	// RequestCancel kill-cascade relabel (FR-B10/FR-B11/FR-B14) — so a poll
	// after a session-level cancel is distinguishable from a natural exit, an
	// explicit action=kill, or a timeout.
	StatusCanceled SessionStatus = "canceled"
	// StatusExited is a legacy terminal value predating ADR-036's
	// kill/timeout/canceled relabels; IsDone() still recognizes it for
	// back-compat, but no current code path sets it.
	StatusExited SessionStatus = "exited"
)

// PtyKeyMode represents arrow key encoding mode for PTY sessions.
// Programs send smkx/rmkx sequences to switch between CSI and SS3 modes.
type PtyKeyMode uint8

const (
	PtyKeyModeCSI PtyKeyMode = iota // triggered by rmkx (\x1b[?1l)
	PtyKeyModeSS3                   // triggered by smkx (\x1b[?1h)
)

const PtyKeyModeNotFound PtyKeyMode = 255

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionDone     = errors.New("session already completed")
	ErrPTYNotSupported = errors.New("PTY is not supported on this platform")
	ErrNoStdin         = errors.New("no stdin available")
)

type ProcessSession struct {
	mu              sync.Mutex
	ID              string
	PID             int
	Command         string
	PTY             bool
	Background      bool
	StartTime       int64
	ExitCode        int
	Status          SessionStatus
	stdinWriter     io.Writer
	outputBuffer    *bytes.Buffer
	outputTruncated bool
	ptyMaster       *os.File

	// ptyKeyMode tracks arrow key encoding mode (CSI vs SS3)
	ptyKeyMode PtyKeyMode

	// OwnerSessionID is the chat/transcript session ID that owns this
	// background process (NOT this ProcessSession's own ID field, which is
	// the exec-tool session-poll/read/kill identifier used by callers). It is
	// set once at process-creation time and never mutated afterward, so reads
	// elsewhere do not need to hold mu (mirrors StartTime's treatment in
	// cleanupOldSessions). Populated by the tool that spawned the background
	// process (bash/exec); left empty ("") for callers that don't track an
	// owning session, in which case KillAllForSession never matches it.
	OwnerSessionID string
}

// IsDone reports whether the session has reached any terminal state. "killed"
// and "timeout" are ADR-036 additions (bash-tool-spec.md FR-B1/MIN-002): the
// bash tool relabels a successful Kill() outcome from the generic "done" to
// one of these two more specific terminal values so callers (action=poll/kill)
// can distinguish an explicit kill / timeout from a natural exit. "canceled"
// is the analogous relabel KillAllForSession applies for the RequestCancel
// kill cascade (FR-B10/FR-B11), distinguishing a cancel-triggered kill from
// both a natural exit and an explicit action=kill call. All are terminal
// exactly like "done"/"exited" for every existing caller of IsDone.
func (s *ProcessSession) IsDone() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.Status {
	case StatusDone, StatusExited, StatusKilled, StatusTimeout, StatusCanceled:
		return true
	default:
		return false
	}
}

func (s *ProcessSession) GetPtyKeyMode() PtyKeyMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ptyKeyMode
}

func (s *ProcessSession) SetPtyKeyMode(mode PtyKeyMode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ptyKeyMode = mode
}

// GetStatus returns the session's current status as a plain string — the
// external-facing type callers (ExecResponse/SessionInfo JSON fields, test
// assertions) have always used. The internal Status field is SessionStatus;
// this converts at the read boundary so existing callers/tests keep working
// unchanged while internal code gets the typed-enum safety.
func (s *ProcessSession) GetStatus() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.Status)
}

func (s *ProcessSession) SetStatus(status SessionStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = status
}

func (s *ProcessSession) GetExitCode() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ExitCode
}

func (s *ProcessSession) SetExitCode(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ExitCode = code
}

// statusPriority ranks terminal SessionStatus values so KillAndRelabel can
// tell a caller with a SPECIFIC terminal reason (canceled/killed/timeout)
// apart from one with only a GENERIC fallback reason (done/exited — "no more
// specific label to apply", see StatusDone's doc comment). Two independent
// callers can legitimately race to be the one that transitions the SAME
// ProcessSession out of StatusRunning — e.g. SessionManager.KillAll's
// unconditional process-wide shutdown reaper racing SessionManager.
// KillAllForSession's owner-scoped RequestCancel cascade for the same
// session (this is not merely theoretical: in the pkg/agent test suite,
// tools.GetSharedSessionManager() is ONE process-wide singleton shared by
// every *AgentLoop any test constructs — see AgentLoop.Close's own
// "LOAD-BEARING PRECONDITION" doc comment in pkg/agent/loop.go — so one
// test's shutdown-driven KillAll() can race a DIFFERENT, concurrently
// running test's own explicit-cancel KillAllForSession() for a session
// neither test intended the other to touch). Whichever caller wins the
// mutex first has, until now, permanently decided the label — if the
// generic caller won, the specific caller's own KillAndRelabel call found
// Status already non-running and silently gave up (ErrSessionDone, the
// existing documented "benign lost race" no-op), leaving a canceled/killed/
// timed-out session mislabeled with the generic fallback forever. See
// KillAndRelabel's doc comment for how this ranking is used to fix that
// without weakening the pre-existing "a genuinely already-terminal session
// is left untouched" no-op guarantee every KillAllForSession/KillAll/
// executeKill caller pre-checks for BEFORE ever reaching KillAndRelabel
// (TestSessionManager_KillAllForSession's "skips already-done sessions"
// case, TestSessionManager_KillAll's "already-terminal session must be left
// untouched" case, and TestBash_KillRacingNaturalExit/MIN-002 all stop at
// that earlier pre-check and never exercise this ranking at all).
func statusPriority(s SessionStatus) int {
	switch s {
	case StatusKilled, StatusTimeout, StatusCanceled:
		return 2 // a caller that knows WHY the session ended
	case StatusDone, StatusExited:
		return 1 // the generic fallback — no specific reason known
	default: // StatusRunning, or any future/unrecognized value
		return 0
	}
}

// killProcessGroupFn indirects the OS-level process-group kill syscall
// (killProcessGroup, session_process_unix.go/session_process_windows.go) so
// tests can force a kill FAILURE deterministically. A real "fake" PID chosen
// well outside any process range this sandbox will ever allocate always
// SUCCEEDS via ESRCH-as-success (see killProcessGroup's own doc comment) —
// there is no way to reach the failure branch in KillAndRelabel/
// KillAllForSession/KillAll with a real syscall in a test environment, so
// this seam exists solely to make that branch (and its failed-count/audit
// wiring) testable. Production code never reassigns this var.
var killProcessGroupFn = killProcessGroup

// KillAndRelabel kills the session's process group and, ON SUCCESS ONLY,
// atomically relabels its status to the given terminal status under the
// SAME lock acquisition that performed the kill. This closes a race where a
// caller previously called Kill() (which unconditionally set
// Status=StatusDone) followed by a SEPARATE SetStatus(...) call: a
// concurrent reader (action=poll/read, or another goroutine's
// KillAllForSession/KillAll scan) could observe the generic "done" value in
// the gap between the two calls instead of the more specific terminal status
// the caller actually intended (canceled/killed/timeout).
func (s *ProcessSession) KillAndRelabel(status SessionStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Status != StatusRunning {
		// Not running anymore. Every caller of KillAndRelabel (KillAllFor
		// Session, KillAll's Kill(), the timeout guard, executeKill) already
		// pre-checked GetStatus()/IsDone() before calling in, so reaching
		// this branch means the session's terminal status was written by a
		// DIFFERENT caller in the narrow window between that pre-check and
		// this lock acquisition (every existing "leave an already-terminal
		// session untouched" test — see statusPriority's doc comment —
		// never reaches this branch at all; they stop at the pre-check).
		// Default to the existing benign no-op UNLESS the racing writer only
		// left the generic StatusDone/StatusExited fallback and THIS call
		// carries a more specific terminal reason: then correct the label in
		// place rather than silently discarding it. The process itself is
		// already terminated (or a kill for it is already in flight) by
		// construction of how the generic fallback gets set, so no further
		// kill syscall is issued here — only the label is corrected.
		// ExitCode is deliberately left untouched: whichever writer got
		// there first may have recorded a real exit code (a natural exit)
		// that remains the most accurate value available. A generic status
		// can never downgrade an already-specific one, and two
		// equally-specific statuses never flip-flop — the first specific
		// writer still wins ties, exactly as before.
		if statusPriority(status) > statusPriority(s.Status) {
			s.Status = status
			return nil
		}
		return ErrSessionDone
	}

	pid := s.PID
	if pid <= 0 {
		return ErrSessionNotFound
	}

	if err := killProcessGroupFn(pid); err != nil {
		return err
	}

	s.Status = status
	s.ExitCode = -1
	return nil
}

// Kill terminates the session and relabels it to the generic "done" status
// — for a caller with no more specific label to apply (KillAll's
// process-wide shutdown reaper). Callers that DO have a more specific
// terminal status (an explicit action=kill, a timeout_seconds guard, or the
// RequestCancel kill cascade) call KillAndRelabel directly instead so the
// relabel is atomic with the kill (see that method's doc comment).
func (s *ProcessSession) Kill() error {
	return s.KillAndRelabel(StatusDone)
}

func (s *ProcessSession) Write(data string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Status != StatusRunning {
		return ErrSessionDone
	}

	var writer io.Writer
	if s.PTY && s.ptyMaster != nil {
		writer = s.ptyMaster
	} else if s.stdinWriter != nil {
		writer = s.stdinWriter
	} else {
		return ErrNoStdin
	}

	_, err := writer.Write([]byte(data))
	return err
}

func (s *ProcessSession) Read() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.outputBuffer.Len() == 0 {
		return ""
	}

	data := s.outputBuffer.String()
	s.outputBuffer.Reset()
	return data
}

func (s *ProcessSession) ToSessionInfo() SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	return SessionInfo{
		ID:        s.ID,
		Command:   s.Command,
		Status:    string(s.Status),
		PID:       s.PID,
		StartedAt: s.StartTime,
	}
}

type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*ProcessSession

	// killedBackgroundSessionsTotal counts background sessions killed via
	// KillAllForSession (the RequestCancel cascade, FR-B14). No pre-existing
	// metrics/counter convention was found in pkg/tools (no Inc()-style
	// helpers, no expvar usage beyond ad hoc atomics in web.go/subagent.go),
	// so this is a minimal, package-local atomic counter exposed via
	// KilledBackgroundSessionsCount. Accessed only via sync/atomic — never
	// read/written directly.
	killedBackgroundSessionsTotal int64
}

func NewSessionManager() *SessionManager {
	sm := &SessionManager{
		sessions: make(map[string]*ProcessSession),
	}

	// Start cleaner goroutine - runs every 5 minutes, cleans up sessions done for >30 minutes
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			sm.cleanupOldSessions()
		}
	}()

	return sm
}

// cleanupOldSessions removes sessions that are done and older than 30 minutes.
// It avoids holding sm.mu while calling session.IsDone() (which acquires session.mu)
// to prevent lock-order inversions: collect candidate IDs under sm.mu, release sm.mu,
// check each session independently, then re-acquire sm.mu to delete.
func (sm *SessionManager) cleanupOldSessions() {
	cutoff := time.Now().Add(-30 * time.Minute)

	// Phase 1: collect candidate session IDs under the read lock.
	sm.mu.RLock()
	var candidates []string
	for id, session := range sm.sessions {
		if session.StartTime < cutoff.Unix() {
			candidates = append(candidates, id)
		}
	}
	sm.mu.RUnlock()

	// Phase 2: check each candidate (acquires session.mu) without holding sm.mu.
	var toDelete []string
	for _, id := range candidates {
		sm.mu.RLock()
		session, ok := sm.sessions[id]
		sm.mu.RUnlock()
		if ok && session.IsDone() {
			toDelete = append(toDelete, id)
		}
	}

	// Phase 3: delete under the write lock.
	if len(toDelete) > 0 {
		sm.mu.Lock()
		for _, id := range toDelete {
			delete(sm.sessions, id)
		}
		sm.mu.Unlock()
	}
}

// KillAllForSession kills every currently-running ProcessSession whose
// OwnerSessionID matches sessionID and returns the count of sessions killed.
// It backs pkg/agent/cancel.go's CancelHooks.KillBackgroundSessions hook
// (FR-B10/FR-B11): when a chat/transcript session is explicitly canceled via
// RequestCancel, this cascades the cancel to any detached background bash/exec
// work that session started.
//
// Locking mirrors cleanupOldSessions' two-phase pattern: candidate session IDs
// are collected under sm.mu.RLock (keyed on OwnerSessionID, which is set once
// at creation and never mutated, so it's safe to read without session.mu —
// same treatment as StartTime above), sm.mu is released, and only then is
// each candidate's status checked and killed (session.GetStatus()/session.Kill()
// each independently acquire session.mu). This avoids a lock-order inversion
// between SessionManager.mu and ProcessSession.mu.
//
// A session that is not "running" by the time it's inspected (already done,
// already killed by a race) is silently skipped — this is not an error, it's
// the expected outcome of a no-op cascade (User Story 5, Acceptance
// Scenario 2). A session whose kill call itself loses the race against
// another terminal-status writer (ErrSessionDone — e.g. the process exited
// naturally, or another caller's explicit action=kill/timeout/cancel won
// first) is likewise NOT counted as a failure and is not logged — it is the
// same benign no-op case as the pre-check above, just observed a moment
// later. Only a REAL kill failure (the underlying syscall itself failing,
// not a lost race) is logged at Warn and counted in the returned failed
// total; it does not abort the rest of the cascade (matches the "on
// failure" contract for this hook: killing an individual background session
// that fails must not abort RequestCancel's own turn-cancellation flow).
//
// One refinement to "loses the race against another terminal-status writer"
// above: if that other writer only left the generic StatusDone fallback (see
// KillAll's shutdown reaper, which relabels unconditionally and without a
// specific reason) and this call's KillAndRelabel(StatusCanceled) reaches
// the session microseconds later, statusPriority lets the more specific
// "canceled" label win and correct it in place — this call still counts it
// as killed/logs it normally in that case (see KillAndRelabel's doc
// comment). Only a same-or-more-specific existing label (canceled/killed/
// timeout, or a session that was ALREADY non-running well before this scan
// even started — the ordinary case this comment block otherwise describes)
// is still treated as a benign no-op.
//
// Returns (killed, failed): killed is the count successfully terminated and
// relabeled StatusCanceled (as before); failed is the count of REAL kill
// failures (excluding the benign ErrSessionDone race above) — added so a
// kill failure is visible to the caller (RequestCancel threads it into the
// EventTurnCancelBackgroundKilled audit event as background_sessions_failed)
// instead of being invisible outside a slog.Warn line.
func (sm *SessionManager) KillAllForSession(sessionID string) (killed, failed int) {
	if sessionID == "" {
		return 0, 0
	}

	// Phase 1: collect candidate session IDs under the read lock.
	sm.mu.RLock()
	var candidates []string
	for id, s := range sm.sessions {
		if s.OwnerSessionID == sessionID {
			candidates = append(candidates, id)
		}
	}
	sm.mu.RUnlock()

	// Phase 2: re-fetch and act on each candidate without holding sm.mu.
	for _, id := range candidates {
		sm.mu.RLock()
		s, ok := sm.sessions[id]
		sm.mu.RUnlock()
		if !ok {
			continue // removed between phase 1 and phase 2
		}

		if s.GetStatus() != string(StatusRunning) {
			continue // already done/killed — no-op for this candidate
		}

		// PID and StartTime are set once at process-creation time (before the
		// session is registered with Add) and never mutated afterward — safe
		// to read without session.mu, matching the OwnerSessionID doc comment.
		pid := s.PID
		startTime := s.StartTime

		// KillAndRelabel kills AND relabels to StatusCanceled atomically under
		// one lock acquisition (mirrors executeKill's "killed" relabel in
		// shell.go for the analogous action=kill path) — no separate
		// SetStatus call, no gap for a concurrent reader to observe "done".
		if err := s.KillAndRelabel(StatusCanceled); err != nil {
			if errors.Is(err, ErrSessionDone) {
				// Benign: lost the race against another terminal-status
				// writer between our GetStatus() check above and this call.
				// Not a real failure — do not count or log it as one.
				continue
			}
			failed++
			slog.Warn("tools: SessionManager.KillAllForSession: failed to kill background session",
				"owner_session_id", sessionID,
				"session_id", id,
				"pid", pid,
				"error", err,
			)
			continue
		}

		elapsedSeconds := time.Now().Unix() - startTime
		slog.Info("tools: SessionManager.KillAllForSession: killed background session on cancel cascade",
			"owner_session_id", sessionID,
			"session_id", id,
			"pid", pid,
			"elapsed_seconds", elapsedSeconds,
		)
		atomic.AddInt64(&sm.killedBackgroundSessionsTotal, 1)
		killed++
	}

	return killed, failed
}

// KilledBackgroundSessionsCount returns the running total of background
// sessions killed by KillAllForSession on this manager. Exposed for tests and
// operator observability (FR-B14) — no pre-existing metrics/counter
// convention was found in pkg/tools, so this is a minimal addition rather than
// an integration with an existing metrics system.
func (sm *SessionManager) KilledBackgroundSessionsCount() int64 {
	return atomic.LoadInt64(&sm.killedBackgroundSessionsTotal)
}

// KillAll kills every currently-running ProcessSession in the manager,
// regardless of owner, and returns the count killed. It backs
// AgentLoop.Close()'s shutdown reaper: a background bash/exec child started
// via run_in_background has its Pdeathsig deliberately CLEARED at spawn time
// (pkg/sandbox/spawn_bg_pdeath_linux.go's clearPdeathsigForBackground — the
// child must outlive a crashed gateway, not be torn down by one) so nothing
// else in the kernel reaps it on process exit. Without this reaper, a full
// gateway restart orphans every still-running background child to PID 1
// forever. This is distinct from KillAllForSession's cancel-cascade scope
// (owner-filtered, user-triggered) — KillAll is unconditional and
// process-wide, appropriate only for whole-process teardown.
//
// Locking mirrors KillAllForSession's two-phase pattern: candidate session
// IDs are collected under sm.mu.RLock, sm.mu is released, and only then is
// each candidate's status checked and killed — avoiding a lock-order
// inversion between SessionManager.mu and ProcessSession.mu.
//
// Intentionally asymmetric with KillAllForSession: a successful kill here
// stays relabeled StatusDone (via the plain Kill() call below) rather than
// being relabeled to a more specific terminal status. This is deliberate,
// not an oversight — KillAll backs whole-process SHUTDOWN teardown
// (AgentLoop.Close()), not a user-observable cancel cascade. By the time
// this returns, the process hosting the very SessionManager these sessions
// live in is itself exiting; nothing will ever poll/read one of these
// sessions again to benefit from a more specific label, so there is no
// observability upside to relabeling, and doing so would also silently
// change this method's long-tested external contract (see
// TestSessionManager_KillAll, which asserts pre-existing "done"/"killed"
// sessions are left untouched — it does not pin the label a NEWLY killed
// session receives, but "done" via the plain Kill() path is what every
// caller has observed since before this comment existed).
//
// Returns (killed, failed) — see KillAllForSession's doc comment for the
// same failed-count/ErrSessionDone-race distinction, mirrored here.
func (sm *SessionManager) KillAll() (killed, failed int) {
	// Phase 1: collect every session ID under the read lock.
	sm.mu.RLock()
	candidates := make([]string, 0, len(sm.sessions))
	for id := range sm.sessions {
		candidates = append(candidates, id)
	}
	sm.mu.RUnlock()

	// Phase 2: re-fetch and act on each candidate without holding sm.mu.
	for _, id := range candidates {
		sm.mu.RLock()
		s, ok := sm.sessions[id]
		sm.mu.RUnlock()
		if !ok {
			continue // removed between phase 1 and phase 2
		}

		if s.GetStatus() != string(StatusRunning) {
			continue // already terminal — no-op for this candidate
		}

		pid := s.PID
		if err := s.Kill(); err != nil {
			if errors.Is(err, ErrSessionDone) {
				// Benign: lost the race against another terminal-status
				// writer (natural exit, an explicit action=kill/timeout, or
				// a concurrent RequestCancel cascade) between our
				// GetStatus() check above and this call. Not a real
				// failure — do not count or log it as one.
				continue
			}
			failed++
			slog.Warn("tools: SessionManager.KillAll: failed to kill background session on shutdown",
				"session_id", id,
				"pid", pid,
				"error", err,
			)
			continue
		}

		slog.Info("tools: SessionManager.KillAll: killed background session on shutdown",
			"session_id", id,
			"pid", pid,
		)
		killed++
	}

	return killed, failed
}

func (sm *SessionManager) Add(session *ProcessSession) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessions[session.ID] = session
}

func (sm *SessionManager) Get(sessionID string) (*ProcessSession, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[sessionID]
	if !ok {
		return nil, ErrSessionNotFound
	}

	return session, nil
}

func (sm *SessionManager) Remove(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, sessionID)
}

func (sm *SessionManager) List() []SessionInfo {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	result := make([]SessionInfo, 0, len(sm.sessions))
	for _, session := range sm.sessions {
		result = append(result, session.ToSessionInfo())
	}

	return result
}

func generateSessionID() string {
	return uuid.New().String()[:8]
}

type SessionInfo struct {
	ID        string `json:"id"`
	Command   string `json:"command"`
	Status    string `json:"status"`
	PID       int    `json:"pid"`
	StartedAt int64  `json:"startedAt"`
}
