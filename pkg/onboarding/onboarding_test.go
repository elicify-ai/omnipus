// Omnipus — Onboarding Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Wave 5b spec tests #12, #13, #14, #23: first-launch detection, onboarding state,
// resume flow, and doctor diagnostics persistence.

package onboarding_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/onboarding"
)

// =====================================================================
// Test #12 — TestOnboardingStateDetection
// =====================================================================

// TestOnboardingStateDetection verifies that a missing state.json is treated as
// a fresh install with onboarding_complete=false.
//
// Traces to: wave5b-system-agent-spec.md — Scenario: First-launch detection (US-7 AC1)
// BDD: "Given no ~/.omnipus/system/state.json exists,
//
//	When app starts, Then onboarding_complete=false, show onboarding wizard"
func TestOnboardingStateDetection(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 195 (Scenario: First-launch detection)

	t.Run("missing state.json treated as fresh install", func(t *testing.T) {
		// Given: no state.json exists
		tmpDir := t.TempDir()
		// NewManager expects ~/.omnipus equivalent; system/state.json is relative to home
		m := onboarding.NewManager(tmpDir)

		// Then: onboarding_complete=false
		assert.False(t, m.IsComplete(),
			"missing state.json must mean onboarding_complete=false (fresh install)")
	})

	t.Run("empty system directory is a fresh install", func(t *testing.T) {
		// Given: system dir exists but state.json does not
		tmpDir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "system"), 0o755))

		m := onboarding.NewManager(tmpDir)
		assert.False(t, m.IsComplete(),
			"system dir present but no state.json is still a fresh install")
	})

	t.Run("state.json with onboarding_complete=false shows wizard", func(t *testing.T) {
		// Given: state.json with flat format, onboarding_complete=false
		tmpDir := t.TempDir()
		stateDir := filepath.Join(tmpDir, "system")
		require.NoError(t, os.MkdirAll(stateDir, 0o755))

		stateJSON := []byte(`{"version":1,"created_at":"2026-01-01T00:00:00Z","onboarding_complete":false}`)
		require.NoError(t, os.WriteFile(filepath.Join(stateDir, "state.json"), stateJSON, 0o600))

		m := onboarding.NewManager(tmpDir)
		assert.False(t, m.IsComplete(),
			"explicit onboarding_complete=false must show wizard")
	})

	t.Run("state.json with nested format onboarding.completed=true skips wizard", func(t *testing.T) {
		// Given: state.json in Omnipus nested format with completed=true
		tmpDir := t.TempDir()
		stateDir := filepath.Join(tmpDir, "system")
		require.NoError(t, os.MkdirAll(stateDir, 0o755))

		stateJSON := []byte(`{"version":1,"created_at":"2026-01-01T00:00:00Z","onboarding":{"completed":true}}`)
		require.NoError(t, os.WriteFile(filepath.Join(stateDir, "state.json"), stateJSON, 0o600))

		m := onboarding.NewManager(tmpDir)
		assert.True(t, m.IsComplete(),
			"nested onboarding.completed=true must skip wizard (returning user)")
	})

	t.Run("corrupted state.json treated as fresh install", func(t *testing.T) {
		// Given: state.json is corrupt JSON
		tmpDir := t.TempDir()
		stateDir := filepath.Join(tmpDir, "system")
		require.NoError(t, os.MkdirAll(stateDir, 0o755))

		require.NoError(t, os.WriteFile(filepath.Join(stateDir, "state.json"), []byte(`{invalid`), 0o600))

		// Should not panic — corrupted file treated as fresh install
		m := onboarding.NewManager(tmpDir)
		assert.False(t, m.IsComplete(),
			"corrupted state.json must degrade gracefully to fresh-install state")
	})
}

// =====================================================================
// Test #13 — TestOnboardingStateResume
// =====================================================================

// TestOnboardingStateResume verifies that when a provider is configured but
// onboarding is not yet complete, the wizard resumes at the correct step.
//
// Traces to: wave5b-system-agent-spec.md — Scenario: Resume incomplete onboarding (US-7 AC3)
// BDD: "Given provider saved but onboarding_complete=false,
//
//	When user restarts app, Then wizard reopens at provider-confirm step"
func TestOnboardingStateResume(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 210 (Scenario: Resume incomplete onboarding)

	t.Run("onboarding_complete=false is preserved across restarts", func(t *testing.T) {
		// Given: provider-like state saved but wizard not completed
		tmpDir := t.TempDir()
		stateDir := filepath.Join(tmpDir, "system")
		require.NoError(t, os.MkdirAll(stateDir, 0o755))

		// Simulate state saved mid-wizard: version/created_at set, onboarding not complete
		stateJSON := []byte(`{"version":1,"created_at":"2026-01-01T00:00:00Z","onboarding":{"completed":false}}`)
		require.NoError(t, os.WriteFile(filepath.Join(stateDir, "state.json"), stateJSON, 0o600))

		// First "launch"
		m1 := onboarding.NewManager(tmpDir)
		assert.False(t, m1.IsComplete())

		// Simulate restart by creating a new Manager (same state.json)
		m2 := onboarding.NewManager(tmpDir)
		assert.False(t, m2.IsComplete(),
			"onboarding_complete=false must persist across restarts so wizard resumes")
	})

	t.Run("CompleteOnboarding persists state durably", func(t *testing.T) {
		// Given: fresh install
		tmpDir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "system"), 0o755))

		m := onboarding.NewManager(tmpDir)
		require.False(t, m.IsComplete())

		// When: onboarding is completed
		require.NoError(t, m.CompleteOnboarding())
		assert.True(t, m.IsComplete(), "in-memory state must update immediately")

		// Then: a new Manager reading same state.json also sees completed
		m2 := onboarding.NewManager(tmpDir)
		assert.True(t, m2.IsComplete(),
			"completed state must survive restart — state.json must have been written atomically")
	})

	t.Run("state.json is written to correct path", func(t *testing.T) {
		tmpDir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "system"), 0o755))

		m := onboarding.NewManager(tmpDir)
		require.NoError(t, m.CompleteOnboarding())

		// Verify the file was created at the expected path
		statePath := filepath.Join(tmpDir, "system", "state.json")
		_, err := os.Stat(statePath)
		assert.NoError(t, err, "state.json must exist at ~/.omnipus/system/state.json after CompleteOnboarding")
	})
}

// =====================================================================
// Test #14 — TestOnboardingNeverReshow
// =====================================================================

// TestOnboardingNeverReshow verifies that once onboarding is complete, subsequent
// launches never show the wizard again.
//
// Traces to: wave5b-system-agent-spec.md — Scenario: Onboarding not reshown (US-7 AC9)
// BDD: "Given onboarding_complete=true in state.json,
//
//	When app starts 2nd time, Then wizard NOT shown"
func TestOnboardingNeverReshow(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 220 (Scenario: Onboarding not reshown)

	t.Run("completed onboarding never reshown on subsequent launches", func(t *testing.T) {
		// Given: onboarding was completed
		tmpDir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "system"), 0o755))

		m := onboarding.NewManager(tmpDir)
		require.NoError(t, m.CompleteOnboarding())

		// When: app "restarts" 3 times
		for i := 0; i < 3; i++ {
			m2 := onboarding.NewManager(tmpDir)
			assert.True(t, m2.IsComplete(),
				"launch %d: onboarding wizard must never reshow once completed (US-7 AC9)", i+1)
		}
	})

	t.Run("flat-format state.json with onboarding_complete=true skips wizard", func(t *testing.T) {
		// Given: state.json in legacy flat format with onboarding_complete=true
		tmpDir := t.TempDir()
		stateDir := filepath.Join(tmpDir, "system")
		require.NoError(t, os.MkdirAll(stateDir, 0o755))

		stateJSON := []byte(`{"version":1,"created_at":"2026-01-01T00:00:00Z","onboarding_complete":true}`)
		require.NoError(t, os.WriteFile(filepath.Join(stateDir, "state.json"), stateJSON, 0o600))

		m := onboarding.NewManager(tmpDir)
		assert.True(t, m.IsComplete(),
			"flat onboarding_complete=true must skip wizard (backward compatibility)")
	})

	t.Run("onboarding_complete is idempotent", func(t *testing.T) {
		// Calling CompleteOnboarding twice must not error
		tmpDir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "system"), 0o755))

		m := onboarding.NewManager(tmpDir)
		require.NoError(t, m.CompleteOnboarding())
		assert.NoError(t, m.CompleteOnboarding(), "CompleteOnboarding must be idempotent")
		assert.True(t, m.IsComplete())
	})
}

// =====================================================================
// Test #23 — TestDoctorRunIntegration
// =====================================================================

// TestDoctorRunIntegration verifies that running the doctor tool persists the
// last run time and risk score to state.json.
//
// Traces to: wave5b-system-agent-spec.md — Scenario: Doctor diagnostics persistence (US-9 AC5)
// BDD: "Given system.doctor.run completes with score=42,
//
//	When app restarts, Then last_doctor_run and last_doctor_score present in state"
func TestDoctorRunIntegration(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 462 (Scenario: Doctor diagnostics persistence)

	t.Run("RecordDoctorRun persists run time and score", func(t *testing.T) {
		// Given: fresh install, no doctor run yet
		tmpDir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "system"), 0o755))
		m := onboarding.NewManager(tmpDir)

		// Before any run
		assert.Nil(t, m.LastDoctorRun(), "LastDoctorRun must be nil before first doctor run")
		assert.Nil(t, m.LastDoctorScore(), "LastDoctorScore must be nil before first doctor run")

		// When: doctor runs with score=42
		before := time.Now().UTC().Add(-time.Second)
		require.NoError(t, m.RecordDoctorRun(42))
		after := time.Now().UTC().Add(time.Second)

		// Then: in-memory state updated
		require.NotNil(t, m.LastDoctorRun(), "LastDoctorRun must be set after RecordDoctorRun")
		require.NotNil(t, m.LastDoctorScore(), "LastDoctorScore must be set after RecordDoctorRun")
		assert.Equal(t, 42, *m.LastDoctorScore(), "score must be 42")
		assert.True(t, m.LastDoctorRun().After(before) && m.LastDoctorRun().Before(after),
			"LastDoctorRun timestamp must be within test window")
	})

	t.Run("doctor run state survives app restart", func(t *testing.T) {
		// Given: doctor was run with score=75
		tmpDir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "system"), 0o755))
		m := onboarding.NewManager(tmpDir)
		require.NoError(t, m.RecordDoctorRun(75))

		// When: app restarts (new Manager reads same state.json)
		m2 := onboarding.NewManager(tmpDir)

		// Then: last_doctor_run and last_doctor_score are present
		require.NotNil(t, m2.LastDoctorRun(), "last_doctor_run must persist across restart (US-9 AC5)")
		require.NotNil(t, m2.LastDoctorScore(), "last_doctor_score must persist across restart")
		assert.Equal(t, 75, *m2.LastDoctorScore())
	})

	t.Run("RecordDoctorRun overwrites previous score", func(t *testing.T) {
		// Given: doctor already ran with score=30
		tmpDir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "system"), 0o755))
		m := onboarding.NewManager(tmpDir)
		require.NoError(t, m.RecordDoctorRun(30))

		// When: doctor runs again with score=85
		require.NoError(t, m.RecordDoctorRun(85))

		// Then: score is updated to 85
		require.NotNil(t, m.LastDoctorScore())
		assert.Equal(t, 85, *m.LastDoctorScore(),
			"RecordDoctorRun must overwrite previous score with latest run")
	})

	t.Run("doctor score boundary values", func(t *testing.T) {
		// Dataset from spec: boundary scores
		tests := []struct {
			name  string
			score int
		}{
			{"score=0 (min, all clear)", 0},
			{"score=100 (max, critical)", 100},
			{"score=42 (mid-range)", 42},
			{"score=1 (near-min)", 1},
			{"score=99 (near-max)", 99},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				tmpDir := t.TempDir()
				require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "system"), 0o755))
				m := onboarding.NewManager(tmpDir)
				require.NoError(t, m.RecordDoctorRun(tc.score))

				require.NotNil(t, m.LastDoctorScore())
				assert.Equal(t, tc.score, *m.LastDoctorScore())
			})
		}
	})

	t.Run("onboarding_complete and doctor run coexist in state.json", func(t *testing.T) {
		// Given: onboarding was completed AND doctor was run
		tmpDir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "system"), 0o755))
		m := onboarding.NewManager(tmpDir)
		require.NoError(t, m.CompleteOnboarding())
		require.NoError(t, m.RecordDoctorRun(55))

		// When: app restarts
		m2 := onboarding.NewManager(tmpDir)

		// Then: both pieces of state are present
		assert.True(t, m2.IsComplete(),
			"onboarding_complete must survive after RecordDoctorRun overwrites state.json")
		require.NotNil(t, m2.LastDoctorScore())
		assert.Equal(t, 55, *m2.LastDoctorScore())
	})
}

// TestOnboardingReserveComplete verifies the two-phase commit API introduced by
// Sprint B F2: ReserveComplete, commit closure, ReleaseReservation, ErrAlreadyComplete.
func TestOnboardingReserveComplete(t *testing.T) {
	t.Run("ReserveComplete returns a working commit closure", func(t *testing.T) {
		tmpDir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "system"), 0o755))
		m := onboarding.NewManager(tmpDir)
		require.False(t, m.IsComplete())

		commit, err := m.ReserveComplete()
		require.NoError(t, err, "ReserveComplete must succeed on a fresh manager")
		require.NotNil(t, commit)

		// Calling commit persists state.json and marks complete.
		require.NoError(t, commit())
		assert.True(t, m.IsComplete(), "commit() must mark onboarding complete in-memory")

		// Verify persistence: new manager reads the written state.json.
		m2 := onboarding.NewManager(tmpDir)
		assert.True(t, m2.IsComplete(), "state.json must be written by commit()")
	})

	t.Run("concurrent ReserveComplete only one succeeds", func(t *testing.T) {
		tmpDir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "system"), 0o755))
		m := onboarding.NewManager(tmpDir)

		const goroutines = 10
		results := make(chan error, goroutines)
		commits := make(chan func() error, goroutines)

		for range goroutines {
			go func() {
				commit, err := m.ReserveComplete()
				results <- err
				if err == nil {
					commits <- commit
				}
			}()
		}

		var successCount, alreadyCount int
		for range goroutines {
			err := <-results
			if err == nil {
				successCount++
			} else if errors.Is(err, onboarding.ErrAlreadyComplete) {
				alreadyCount++
			}
		}
		close(commits)
		// Drain any commit closures to avoid goroutine leak.
		for c := range commits {
			_ = c()
		}

		assert.Equal(t, 1, successCount, "exactly one goroutine must win the reservation")
		assert.Equal(t, goroutines-1, alreadyCount, "all others must get ErrAlreadyComplete")
	})

	t.Run("ReleaseReservation allows retry", func(t *testing.T) {
		tmpDir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "system"), 0o755))
		m := onboarding.NewManager(tmpDir)

		// Reserve, then release without committing.
		_, err := m.ReserveComplete()
		require.NoError(t, err)
		m.ReleaseReservation()

		// After release, a second Reserve must succeed.
		commit2, err2 := m.ReserveComplete()
		require.NoError(t, err2, "after ReleaseReservation, ReserveComplete must succeed again")
		require.NoError(t, commit2())
		assert.True(t, m.IsComplete())
	})

	t.Run("ReserveComplete fails after CompleteOnboarding", func(t *testing.T) {
		tmpDir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "system"), 0o755))
		m := onboarding.NewManager(tmpDir)
		require.NoError(t, m.CompleteOnboarding())

		_, err := m.ReserveComplete()
		assert.ErrorIs(t, err, onboarding.ErrAlreadyComplete,
			"ReserveComplete must return ErrAlreadyComplete when already done")
	})

	t.Run("ErrAlreadyComplete is the exported sentinel", func(t *testing.T) {
		// Verify the exported error is distinct and stable.
		assert.NotNil(t, onboarding.ErrAlreadyComplete)
		assert.Equal(t, "onboarding already complete", onboarding.ErrAlreadyComplete.Error())
	})
}

// TestOnboardingManagerConcurrency verifies the Manager is safe for concurrent use.
//
// Traces to: wave5b-system-agent-spec.md — US-7 (concurrency safety implied by sync.RWMutex)
func TestOnboardingManagerConcurrency(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "system"), 0o755))
	m := onboarding.NewManager(tmpDir)

	// Run concurrent reads and writes — must not race (run with -race)
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			_ = m.IsComplete()
			if i%3 == 0 {
				_ = m.RecordDoctorRun(i * 10)
			}
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	// No panic, no data race = pass
}
