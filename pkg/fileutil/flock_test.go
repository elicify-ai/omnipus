// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fileutil

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAdvisoryFileLock verifies WithFlock acquires and releases the lock correctly.
// Traces to: wave1-core-foundation-spec.md Scenario: Concurrent writes to config are serialized (US-10 AC3, FR-022)
func TestAdvisoryFileLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "locked.json")

	// Create the file first.
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))

	// Single lock acquisition must succeed.
	err := WithFlock(path, func() error {
		return nil
	})
	assert.NoError(t, err, "WithFlock must succeed with no-op function")
}

// TestAdvisoryFileLockWrites verifies concurrent WithFlock calls serialize writes.
// Traces to: wave1-core-foundation-spec.md Scenario: Concurrent writes to config are serialized (US-10 AC2, FR-023)
func TestAdvisoryFileLockWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"counter":0}`), 0o600))

	var (
		wg      sync.WaitGroup
		errCh   = make(chan error, 20)
		counter atomic.Int32
	)

	// 10 goroutines each increment via WithFlock.
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := WithFlock(path, func() error {
				counter.Add(1)
				// Small sleep to increase contention probability.
				time.Sleep(time.Millisecond)
				return nil
			})
			if err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("WithFlock error: %v", err)
	}

	assert.Equal(t, int32(10), counter.Load(),
		"all 10 goroutines must execute the locked function exactly once")
}

// TestWithFlockFileNotExistCreatesIt verifies WithFlock creates the file if absent.
// Traces to: wave1-core-foundation-spec.md US-10 AC1
func TestWithFlockFileNotExistCreatesIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "newfile.json")

	// File does not exist yet.
	err := WithFlock(path, func() error {
		return WriteFileAtomic(path, []byte(`{"created":true}`), 0o600)
	})
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "created")
}

// TestWithFlockFnError verifies that an error returned by fn is propagated to
// the caller (F15: no silent error swallowing).
func TestWithFlockFnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))

	sentinel := fmt.Errorf("fn error sentinel")
	err := WithFlock(path, func() error {
		return sentinel
	})
	assert.ErrorIs(t, err, sentinel, "WithFlock must propagate fn errors to the caller")
}

// TestConcurrentConfigWrites verifies multiple goroutines can write config safely.
// Traces to: wave1-core-foundation-spec.md Scenario: Concurrent writes to config are serialized (US-10 AC2)
func TestConcurrentConfigWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	var wg sync.WaitGroup
	errs := make(chan error, 50)

	// 20 goroutines each write their own JSON object atomically.
	for i := range 20 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			data, marshalErr := json.Marshal(map[string]any{"writer": n, "ok": true})
			if marshalErr != nil {
				errs <- marshalErr
				return
			}
			if err := WithFlock(path, func() error {
				return WriteFileAtomic(path, data, 0o600)
			}); err != nil {
				errs <- err
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent write error: %v", err)
	}

	// Final file must be valid JSON.
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result),
		"config.json must be valid JSON after concurrent writes")
}
