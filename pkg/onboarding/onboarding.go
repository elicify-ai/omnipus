// Omnipus — Onboarding
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package onboarding implements first-launch detection and onboarding state
// management per BRD Wave 5b user stories US-7 and US-8.
//
// State is persisted in ~/.omnipus/system/state.json. Missing state.json is
// treated as a fresh install (onboarding_complete=false). The file is never
// auto-created here — datamodel.Init() handles directory bootstrapping.
package onboarding

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// ErrAlreadyComplete is returned by ReserveComplete when onboarding has
// already been marked complete. Callers should use errors.Is to check for it.
var ErrAlreadyComplete = errors.New("onboarding already complete")

// State holds the persistent onboarding and system status fields.
type State struct {
	Version            int        `json:"version"`
	CreatedAt          time.Time  `json:"created_at"`
	OnboardingComplete bool       `json:"onboarding_complete"`
	LastDoctorRun      *time.Time `json:"last_doctor_run,omitempty"`
	LastDoctorScore    *int       `json:"last_doctor_score,omitempty"`
}

// Manager manages onboarding state with atomic saves.
// It is safe for concurrent use.
type Manager struct {
	mu        sync.RWMutex
	statePath string
	state     State
	reserved  bool // true when ReserveComplete has been called but commit not yet invoked
}

// NewManager creates a Manager backed by statePath.
// If the file does not exist, state defaults to fresh-install
// (OnboardingComplete=false) without creating the file.
func NewManager(home string) *Manager {
	statePath := filepath.Join(home, "system", "state.json")
	m := &Manager{statePath: statePath}
	if err := m.load(); err != nil {
		slog.Warn("onboarding: could not load state, treating as fresh install",
			"path", statePath, "error", err)
	}
	return m
}

// CompleteOnboarding marks onboarding as done and persists the state.
// Subsequent launches skip the onboarding flow (US-7 §AC9).
func (m *Manager) CompleteOnboarding() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.OnboardingComplete = true
	return m.save()
}

// ReserveComplete implements the first phase of the two-phase commit for
// onboarding. It:
//   - Checks IsComplete (and reserved) under the write lock; returns
//     ErrAlreadyComplete if either is true.
//   - Sets an in-memory "reserved" flag so concurrent callers see
//     "already complete" immediately (before state.json is written).
//   - Returns a commit closure that atomically persists state.json and
//     clears the reserved flag. The caller MUST invoke commit() after the
//     config write succeeds, or call ReleaseReservation() on any error path.
//
// Two-phase invariant: state.json is never written before config.json.
// If the process crashes between config.json write and state.json write,
// the next boot will not have state.json, re-enter onboarding, and detect
// the admin user already exists — idempotently updating the config entry.
func (m *Manager) ReserveComplete() (commit func() error, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.OnboardingComplete || m.reserved {
		return nil, ErrAlreadyComplete
	}
	m.reserved = true
	commit = func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.state.OnboardingComplete = true
		m.reserved = false
		return m.save()
	}
	return commit, nil
}

// ReleaseReservation clears the in-memory "reserved" flag without writing
// state.json. Call this on any error path after ReserveComplete to allow
// subsequent callers to retry onboarding.
func (m *Manager) ReleaseReservation() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reserved = false
}

// IsComplete returns true if onboarding has already been completed.
func (m *Manager) IsComplete() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.OnboardingComplete
}

// RecordDoctorRun stores the last doctor run time and risk score.
func (m *Manager) RecordDoctorRun(score int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	m.state.LastDoctorRun = &now
	m.state.LastDoctorScore = &score
	return m.save()
}

// LastDoctorRun returns the last doctor run time, or nil if never run.
func (m *Manager) LastDoctorRun() *time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.LastDoctorRun
}

// LastDoctorScore returns the last risk score, or nil if never run.
func (m *Manager) LastDoctorScore() *int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.LastDoctorScore
}

// load reads state.json from disk.
// Missing file is not an error — defaults to fresh-install state.
func (m *Manager) load() error {
	data, err := os.ReadFile(m.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			m.state = State{Version: 1, CreatedAt: time.Now().UTC()}
			return nil
		}
		return fmt.Errorf("onboarding: read %s: %w", m.statePath, err)
	}

	// Parse into a union type that handles both flat and nested formats.
	// Nested format: {"onboarding": {"completed": true}}
	// Flat format:   {"onboarding_complete": true}
	var raw struct {
		Version            int       `json:"version"`
		CreatedAt          time.Time `json:"created_at"`
		OnboardingComplete bool      `json:"onboarding_complete"` // flat
		Onboarding         *struct { // nested (nil when absent)
			Completed bool `json:"completed"`
		} `json:"onboarding"`
		LastDoctorRun   *time.Time `json:"last_doctor_run,omitempty"`
		LastDoctorScore *int       `json:"last_doctor_score,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		// Back up the corrupt file so the user can recover it manually, then
		// reset to fresh-install state rather than blocking startup.
		backupPath := m.statePath + ".corrupt." + time.Now().UTC().Format("20060102T150405Z")
		if berr := os.Rename(m.statePath, backupPath); berr != nil {
			slog.Warn("onboarding: could not back up corrupt state.json", "path", m.statePath, "error", berr)
		} else {
			slog.Warn("onboarding: state.json was corrupt — backed up and reset to fresh install",
				"backup", backupPath, "parse_error", err)
		}
		m.state = State{Version: 1, CreatedAt: time.Now().UTC()}
		return nil
	}
	complete := raw.OnboardingComplete
	if raw.Onboarding != nil {
		complete = raw.Onboarding.Completed
	}
	m.state = State{
		Version:            raw.Version,
		CreatedAt:          raw.CreatedAt,
		OnboardingComplete: complete,
		LastDoctorRun:      raw.LastDoctorRun,
		LastDoctorScore:    raw.LastDoctorScore,
	}
	return nil
}

// save atomically writes state.json.
// Must be called with m.mu held (write).
func (m *Manager) save() error {
	if m.state.Version == 0 {
		m.state.Version = 1
	}
	if m.state.CreatedAt.IsZero() {
		m.state.CreatedAt = time.Now().UTC()
	}

	// Write in the nested Omnipus format so datamodel.Init() can also read it.
	nested := map[string]any{
		"version":    m.state.Version,
		"created_at": m.state.CreatedAt.Format(time.RFC3339),
		"onboarding": map[string]any{
			"completed": m.state.OnboardingComplete,
		},
	}
	if m.state.LastDoctorRun != nil {
		nested["last_doctor_run"] = m.state.LastDoctorRun.Format(time.RFC3339)
	}
	if m.state.LastDoctorScore != nil {
		nested["last_doctor_score"] = *m.state.LastDoctorScore
	}

	data, err := json.MarshalIndent(nested, "", "  ")
	if err != nil {
		return fmt.Errorf("onboarding: marshal state: %w", err)
	}
	if err := fileutil.WriteFileAtomic(m.statePath, data, 0o600); err != nil {
		return fmt.Errorf("onboarding: write state: %w", err)
	}
	return nil
}
