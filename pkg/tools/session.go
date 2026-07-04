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
	Status          string
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

func (s *ProcessSession) IsDone() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Status == "done" || s.Status == "exited"
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

func (s *ProcessSession) GetStatus() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Status
}

func (s *ProcessSession) SetStatus(status string) {
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

func (s *ProcessSession) killProcess() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Status != "running" {
		return ErrSessionDone
	}

	pid := s.PID
	if pid <= 0 {
		return ErrSessionNotFound
	}

	if err := killProcessGroup(pid); err != nil {
		return err
	}

	s.Status = "done"
	s.ExitCode = -1
	return nil
}

func (s *ProcessSession) Kill() error {
	return s.killProcess()
}

func (s *ProcessSession) Write(data string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Status != "running" {
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
		Status:    s.Status,
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
// Scenario 2). A session whose Kill() call itself fails is logged at Warn and
// does not abort the rest of the cascade (matches the "on failure" contract
// for this hook: killing an individual background session that fails must not
// abort RequestCancel's own turn-cancellation flow).
func (sm *SessionManager) KillAllForSession(sessionID string) int {
	if sessionID == "" {
		return 0
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
	killed := 0
	for _, id := range candidates {
		sm.mu.RLock()
		s, ok := sm.sessions[id]
		sm.mu.RUnlock()
		if !ok {
			continue // removed between phase 1 and phase 2
		}

		if s.GetStatus() != "running" {
			continue // already done/killed — no-op for this candidate
		}

		// PID and StartTime are set once at process-creation time (before the
		// session is registered with Add) and never mutated afterward — safe
		// to read without session.mu, matching the OwnerSessionID doc comment.
		pid := s.PID
		startTime := s.StartTime

		if err := s.Kill(); err != nil {
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

	return killed
}

// KilledBackgroundSessionsCount returns the running total of background
// sessions killed by KillAllForSession on this manager. Exposed for tests and
// operator observability (FR-B14) — no pre-existing metrics/counter
// convention was found in pkg/tools, so this is a minimal addition rather than
// an integration with an existing metrics system.
func (sm *SessionManager) KilledBackgroundSessionsCount() int64 {
	return atomic.LoadInt64(&sm.killedBackgroundSessionsTotal)
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
