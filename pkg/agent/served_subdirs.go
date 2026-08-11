// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package agent — web_serve static-mode registration map and janitor (US-4).
//
// ServedSubdirs maintains the in-memory map of active web_serve static-mode
// registrations. Each registration binds a 32-byte random token to an agent's
// absolute workspace directory for a bounded lifetime. The janitor goroutine
// removes expired entries every 30 seconds.
//
// Per-agent cap: each agent may have at most one active registration
// at a time. Creating a new registration invalidates the previous one.

package agent

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ServedEntry holds a single web_serve static-mode registration.
type ServedEntry struct {
	// AgentID is the ID of the agent that owns this registration.
	AgentID string
	// AbsDir is the canonicalised absolute path to the served directory.
	// It is within the agent's workspace (validated by the web_serve tool).
	AbsDir string
	// Deadline is when this registration expires.
	Deadline time.Time
}

// ServedSubdirs is the process-wide registry of active web_serve static-mode
// registrations. The zero value is not usable — call NewServedSubdirs.
type ServedSubdirs struct {
	mu sync.RWMutex
	// byToken maps token → ServedEntry. The token is a 32-byte random value
	// encoded as base64-RawURL (43 ASCII characters).
	byToken map[string]*ServedEntry
	// byAgent maps agentID → token so we can enforce the per-agent cap quickly.
	byAgent map[string]string

	// stopCh signals the janitor to exit. Closed by Stop().
	stopCh chan struct{}
	// stopped is closed by the janitor's defer when it returns. Stop waits
	// on this so callers can rely on no background work after Stop returns
	// (mirrors the DevServerRegistry pattern — H6).
	stopped chan struct{}

	// onEvict is called (outside mu) whenever one or more tokens are evicted
	// from the registry (TTL expiry, manual Evict, or cap-replacement during
	// Register). Wired at boot by the gateway to purge the firstServedTokens
	// audit set (F-9 fix). May be nil.
	onEvict func(tokens []string)
}

// NewServedSubdirs creates a registry and launches the 30-second janitor.
// Call Stop when the gateway shuts down so the goroutine exits cleanly.
func NewServedSubdirs() *ServedSubdirs {
	s := &ServedSubdirs{
		byToken: make(map[string]*ServedEntry),
		byAgent: make(map[string]string),
		stopCh:  make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go s.janitor()
	return s
}

// SetOnEvict installs a callback that is invoked (outside the registry lock)
// whenever tokens are evicted. Gateway wires this to purgeFirstServedTokensBulk
// so the audit firstServedTokens set stays in sync with the registry (F-9).
// Must be called before any concurrent access.
func (s *ServedSubdirs) SetOnEvict(fn func(tokens []string)) {
	s.mu.Lock()
	s.onEvict = fn
	s.mu.Unlock()
}

// Register creates a new web_serve static-mode registration for agentID pointing
// at absDir with a lifetime of duration.
//
// Per-agent cap: an agent has at most one active registration. Re-registering
// the SAME absDir RENEWS the existing registration and returns the SAME token,
// so a preview URL already handed to a user keeps working across the ordinary
// edit-then-re-serve loop. Registering a DIFFERENT absDir still atomically
// replaces the previous registration and invalidates its token.
//
// Returns the token (for embedding in the URL) and the registration's
// expiry time.
func (s *ServedSubdirs) Register(
	agentID, absDir string,
	duration time.Duration,
) (token string, deadline time.Time, err error) {
	rawToken := make([]byte, 32)
	if _, err = rand.Read(rawToken); err != nil {
		return "", time.Time{}, fmt.Errorf("served_subdirs: generate token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(rawToken)
	deadline = time.Now().Add(duration)

	s.mu.Lock()
	// Invalidate previous registration for this agent (per-agent cap ).
	var evicted []string
	if prev, ok := s.byAgent[agentID]; ok {
		// RENEW IN PLACE when the agent re-serves the SAME directory.
		//
		// The per-agent cap is "at most one active registration", not "rotate
		// the token on every call". Minting a fresh token here unconditionally
		// meant that an agent doing the normal edit-then-re-serve loop silently
		// 404'd a URL it had already given to the user — observed in a real
		// session, where the link handed over at 15:56 was dead by 15:59.
		//
		// absDir is already canonicalised by the caller (ResolvePath /
		// RealPath, the ADR-046 chokepoint), so comparing it is robust to
		// different spellings of the same directory and still rotates when the
		// agent genuinely switches to serving something else.
		if prevEntry, found := s.byToken[prev]; found &&
			prevEntry.AbsDir == absDir && time.Now().Before(prevEntry.Deadline) {
			// Replace the struct rather than mutating it: Lookup hands the
			// *ServedEntry to callers and reads Deadline AFTER dropping the
			// read lock, so an in-place write here would be a data race.
			s.byToken[prev] = &ServedEntry{
				AgentID:  agentID,
				AbsDir:   absDir,
				Deadline: deadline,
			}
			s.mu.Unlock()
			return prev, deadline, nil
		}
		delete(s.byToken, prev)
		evicted = []string{prev}
	}

	entry := &ServedEntry{
		AgentID:  agentID,
		AbsDir:   absDir,
		Deadline: deadline,
	}
	s.byToken[token] = entry
	s.byAgent[agentID] = token
	cb := s.onEvict
	s.mu.Unlock()

	// Notify outside the lock (F-9).
	if cb != nil && len(evicted) > 0 {
		cb(evicted)
	}
	return token, deadline, nil
}

// Lookup returns the ServedEntry for the given token, or nil if the token is
// unknown or expired. An expired-but-not-yet-janitor-cleaned entry is treated
// as missing so callers always see consistent state.
func (s *ServedSubdirs) Lookup(token string) *ServedEntry {
	s.mu.RLock()
	entry, ok := s.byToken[token]
	s.mu.RUnlock()
	if !ok {
		return nil
	}
	if time.Now().After(entry.Deadline) {
		return nil
	}
	return entry
}

// ActiveForAgent returns the token and deadline of the currently active
// registration for agentID, and ok=true if one exists and has not expired.
// Returns ("", zero, false) otherwise.
func (s *ServedSubdirs) ActiveForAgent(agentID string) (token string, deadline time.Time, ok bool) {
	s.mu.RLock()
	token, ok = s.byAgent[agentID]
	if !ok {
		s.mu.RUnlock()
		return "", time.Time{}, false
	}
	entry := s.byToken[token]
	s.mu.RUnlock()
	if entry == nil || time.Now().After(entry.Deadline) {
		return "", time.Time{}, false
	}
	return token, entry.Deadline, true
}

// Evict removes all registrations for the given agentID. Called on agent
// deletion so URLs stop resolving immediately rather than waiting for the
// janitor.
func (s *ServedSubdirs) Evict(agentID string) {
	s.mu.Lock()
	var evicted []string
	if tok, ok := s.byAgent[agentID]; ok {
		delete(s.byToken, tok)
		delete(s.byAgent, agentID)
		evicted = []string{tok}
	}
	cb := s.onEvict
	s.mu.Unlock()

	// Notify outside the lock (F-9).
	if cb != nil && len(evicted) > 0 {
		cb(evicted)
	}
}

// Stop signals the janitor goroutine to exit and waits for it to return.
// Safe to call multiple times (subsequent calls are no-ops — stopCh is
// already closed and the janitor has already exited).
func (s *ServedSubdirs) Stop() {
	select {
	case <-s.stopCh:
		// already closed — wait for the janitor to finish (idempotent on <-stopped)
	default:
		close(s.stopCh)
	}
	// H6: wait for the janitor goroutine to exit so callers can rely on no
	// background work after Stop returns (mirrors DevServerRegistry.Close).
	<-s.stopped
}

// janitor runs every 30 seconds and removes expired entries.
func (s *ServedSubdirs) janitor() {
	defer close(s.stopped) // H6: signal Stop() that the goroutine has exited
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.purgeExpired()
		}
	}
}

// purgeExpired removes all entries whose deadline has passed.
func (s *ServedSubdirs) purgeExpired() {
	now := time.Now()
	s.mu.Lock()
	var evicted []string
	for tok, entry := range s.byToken {
		if now.After(entry.Deadline) {
			delete(s.byToken, tok)
			if s.byAgent[entry.AgentID] == tok {
				delete(s.byAgent, entry.AgentID)
			}
			evicted = append(evicted, tok)
			slog.Debug("served_subdirs: janitor removed expired registration",
				"agent_id", entry.AgentID,
				"deadline", entry.Deadline,
			)
		}
	}
	cb := s.onEvict
	s.mu.Unlock()

	// Notify outside the lock (F-9).
	if cb != nil && len(evicted) > 0 {
		cb(evicted)
	}
}
