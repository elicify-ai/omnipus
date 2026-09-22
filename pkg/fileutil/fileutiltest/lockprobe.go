// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package fileutiltest holds assertions for file stores that write a data file
// with fileutil.WriteFileAtomic under the cross-process lock on its sidecar
// (fileutil.SidecarLockPath). Import it from _test.go files only.
//
// The two properties it checks are the ones a store loses when it locks the
// data file itself instead of the sidecar:
//
//   - HoldFirstWrite + RequireAbsentOrComplete*: a reader never sees an empty or
//     partial data file while the file's first write is in flight.
//   - RequireWaitsForSidecarLock: a write takes the same lock as every other
//     writer of that file, so they exclude each other across processes.
package fileutiltest

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// WriteFunc is the signature of fileutil.WriteFileAtomic, and of the
// package-level writeFileAtomicFn seam a store routes its locked writes through
// so a test can pause one.
type WriteFunc = func(path string, data []byte, perm os.FileMode) error

const waitTimeout = 10 * time.Second

// lockProbeWindow is how long RequireWaitsForSidecarLock gives a write that does
// not take the sidecar lock to finish anyway. A write that does take it cannot
// finish inside the window at all, so the window only bounds how quickly a
// wrong store is caught, never whether a correct one passes.
const lockProbeWindow = 300 * time.Millisecond

// HoldFirstWrite replaces *seam so that the first write to a file whose base
// name is name parks, inside whatever lock its caller holds, until release is
// called. parked receives that write's path once it has parked. The seam is
// restored, and a still-parked write released, when the test ends.
//
// Call it before starting the goroutine that performs the write.
func HoldFirstWrite(t testing.TB, seam *WriteFunc, name string) (parked <-chan string, release func()) {
	t.Helper()
	parkedCh := make(chan string, 1)
	releaseCh := make(chan struct{})
	var holdOnce sync.Once
	orig := *seam
	t.Cleanup(func() { *seam = orig })
	*seam = func(path string, data []byte, perm os.FileMode) error {
		if filepath.Base(path) == name {
			hold := false
			holdOnce.Do(func() { hold = true })
			if hold {
				parkedCh <- path
				<-releaseCh
			}
		}
		return orig(path, data, perm)
	}
	var releaseOnce sync.Once
	release = func() { releaseOnce.Do(func() { close(releaseCh) }) }
	// Registered after the restore above, so it runs first: a write still
	// parked when the test fails is released before the seam is restored.
	t.Cleanup(release)
	return parkedCh, release
}

// WaitParked returns the path of the write HoldFirstWrite parked, failing the
// test if none parks in time.
func WaitParked(t testing.TB, parked <-chan string) string {
	t.Helper()
	select {
	case path := <-parked:
		return path
	case <-time.After(waitTimeout):
		t.Fatal("the write never reached its locked section")
		return ""
	}
}

// WaitDone waits for a store call running in the background to return and
// requires it to have succeeded.
func WaitDone(t testing.TB, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(waitTimeout):
		t.Fatal("the store call never returned")
	}
}

// RequireAbsentOrCompleteJSON fails when path exists but does not hold one
// complete JSON document — the state a reader outside the store's locks must
// never observe.
func RequireAbsentOrCompleteJSON(t testing.TB, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	require.NoError(t, err)
	require.Truef(t, json.Valid(data),
		"%s exists while its write is still in flight, but holds %d bytes that are not a complete JSON document "+
			"(%q): a reader outside the store's locks reads a corrupt record, and a crash now would leave it that way",
		filepath.Base(path), len(data), data)
}

// RequireCompleteJSON fails unless path holds one complete JSON document.
func RequireCompleteJSON(t testing.TB, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Truef(t, json.Valid(data), "%s must be a complete JSON document once its write returns, got %q",
		filepath.Base(path), data)
}

// RequireAbsentOrCompleteJSONL fails when path exists but is empty, does not end
// in a newline, or holds a line that is not a complete JSON document.
func RequireAbsentOrCompleteJSONL(t testing.TB, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	require.NoError(t, err)
	requireCompleteJSONL(t, path, data)
}

// RequireCompleteJSONL fails unless path holds at least one line and every line
// is a complete JSON document.
func RequireCompleteJSONL(t testing.TB, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	requireCompleteJSONL(t, path, data)
}

func requireCompleteJSONL(t testing.TB, path string, data []byte) {
	t.Helper()
	require.NotEmptyf(t, data,
		"%s exists while its first write is still in flight, but is EMPTY: a crash now would leave an empty file "+
			"that no write ever produced", filepath.Base(path))
	require.Truef(t, bytes.HasSuffix(data, []byte("\n")), "%s ends mid-line (%q)", filepath.Base(path), data)
	for _, line := range bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n")) {
		require.Truef(t, json.Valid(line), "%s holds a line that is not a complete JSON document: %q",
			filepath.Base(path), line)
	}
}

// RequireWaitsForSidecarLock proves op takes the cross-process lock on the
// sidecar of dataPath (fileutil.SidecarLockPath). It holds that lock itself,
// requires op not to return while it is held, then releases it and requires op
// to complete successfully.
//
// A store that locks the data file instead finishes op while the sidecar is
// held, which means it does not exclude that file's other writers.
//
// POSIX only: fileutil.WithFlock takes no lock on Windows.
func RequireWaitsForSidecarLock(t testing.TB, dataPath string, op func() error) {
	t.Helper()
	lockPath := fileutil.SidecarLockPath(dataPath)
	held := make(chan struct{})
	releaseCh := make(chan struct{})
	holderDone := make(chan error, 1)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCh) }) }
	t.Cleanup(release)

	go func() {
		holderDone <- fileutil.WithFlock(lockPath, func() error {
			close(held)
			<-releaseCh
			return nil
		})
	}()
	select {
	case <-held:
	case err := <-holderDone:
		t.Fatalf("could not take the lock on %s: %v", lockPath, err)
	case <-time.After(waitTimeout):
		t.Fatalf("timed out taking the lock on %s", lockPath)
	}

	opDone := make(chan error, 1)
	go func() { opDone <- op() }()
	select {
	case err := <-opDone:
		t.Fatalf("the write finished (err=%v) while %s was held by another caller: it does not take the lock on "+
			"the data file's sidecar, so it does not exclude the file's other writers",
			err, filepath.Base(lockPath))
	case <-time.After(lockProbeWindow):
	}

	release()
	select {
	case err := <-holderDone:
		require.NoError(t, err)
	case <-time.After(waitTimeout):
		t.Fatal("the lock holder never released")
	}
	select {
	case err := <-opDone:
		require.NoError(t, err, "the write must complete once the lock is released")
	case <-time.After(waitTimeout):
		t.Fatal("the write never completed after the lock was released")
	}
}
