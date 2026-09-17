// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

package fileutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// signalNthOpen installs flockOpenedHook so that the nth open of a lock file
// (counting every WithFlock call in the test) closes the returned channel.
func signalNthOpen(t *testing.T, n int32) <-chan struct{} {
	t.Helper()
	ch := make(chan struct{})
	var opens atomic.Int32
	orig := flockOpenedHook
	t.Cleanup(func() { flockOpenedHook = orig })
	flockOpenedHook = func(string) {
		if opens.Add(1) == n {
			close(ch)
		}
	}
	return ch
}

// waitFor waits for ch to close. It returns an error rather than failing the
// test so it is safe to call from the goroutines these tests start.
func waitFor(ch <-chan struct{}, what string) error {
	select {
	case <-ch:
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("timed out waiting for %s", what)
	}
}

func receiveErr(t *testing.T, ch <-chan error, what string) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return nil
	}
}

// A lock file may be removed by the caller holding it (RemoveLocked does this
// when a record is deleted). A caller that opened the lock file before the
// removal and is waiting for it must not then run its critical section on the
// removed file while a later caller holds the lock on the file that replaced
// it — that is two holders at once, the exact failure the sidecar design
// exists to prevent.
func TestWithFlock_LockFileRemovedWhileWaiting_NeverTwoHoldersAtOnce(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "record.json.lock")
	// Open #1 is the remover, open #2 the waiter.
	waiterOpened := signalNthOpen(t, 2)

	removerHolds := make(chan struct{})
	laterHolds := make(chan struct{})
	releaseLater := make(chan struct{})
	removerDone := make(chan error, 1)
	laterDone := make(chan error, 1)
	waiterDone := make(chan error, 1)
	waiterEntered := make(chan struct{})
	var laterInside, overlap atomic.Bool

	go func() {
		removerDone <- WithFlock(lockPath, func() error {
			close(removerHolds)
			if err := waitFor(waiterOpened, "the waiter to open the lock file"); err != nil {
				return err
			}
			if err := os.Remove(lockPath); err != nil {
				return fmt.Errorf("remove lock: %w", err)
			}
			go func() {
				laterDone <- WithFlock(lockPath, func() error {
					laterInside.Store(true)
					close(laterHolds)
					<-releaseLater
					laterInside.Store(false)
					return nil
				})
			}()
			return waitFor(laterHolds, "the later caller to lock the replacement file")
		})
	}()

	require.NoError(t, waitFor(removerHolds, "the remover to take the lock"))
	go func() {
		waiterDone <- WithFlock(lockPath, func() error {
			if laterInside.Load() {
				overlap.Store(true)
			}
			close(waiterEntered)
			return nil
		})
	}()

	require.NoError(t, receiveErr(t, removerDone, "the remover to finish"))
	// The remover has released the removed file. Give the waiter every chance
	// to (wrongly) enter while the later caller still holds the lock.
	select {
	case <-waiterEntered:
	case <-time.After(500 * time.Millisecond):
	}
	close(releaseLater)
	require.NoError(t, receiveErr(t, laterDone, "the later caller to finish"))
	require.NoError(t, receiveErr(t, waiterDone, "the waiter to finish after the later caller released the lock"))
	assert.False(t, overlap.Load(),
		"the waiter ran its critical section on the removed lock file while another caller held the lock "+
			"on the file now at that path — two holders at once")
}

// When the whole directory holding a lock file is removed while a caller waits
// for it (a session or task directory deleted mid-write), the waiter must fail
// rather than run its critical section on a file nobody else can reach.
func TestWithFlock_DirectoryRemovedWhileWaiting_ReturnsNotExist(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "entity")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	lockPath := filepath.Join(dir, "meta.json.lock")
	waiterOpened := signalNthOpen(t, 2)

	removerHolds := make(chan struct{})
	removerDone := make(chan error, 1)
	go func() {
		removerDone <- WithFlock(lockPath, func() error {
			close(removerHolds)
			if err := waitFor(waiterOpened, "the waiter to open the lock file"); err != nil {
				return err
			}
			return os.RemoveAll(dir)
		})
	}()
	require.NoError(t, waitFor(removerHolds, "the remover to take the lock"))

	var ran atomic.Bool
	waiterDone := make(chan error, 1)
	go func() {
		waiterDone <- WithFlock(lockPath, func() error {
			ran.Store(true)
			return nil
		})
	}()

	require.NoError(t, receiveErr(t, removerDone, "the remover to finish"))
	require.ErrorIs(t, receiveErr(t, waiterDone, "the waiter to return after its directory was removed"), fs.ErrNotExist)
	assert.False(t, ran.Load(), "the waiter must not run its critical section on a lock file in a removed directory")
}

func TestSidecarLockPath(t *testing.T) {
	assert.Equal(t, "/home/tasks/abc.json.lock", SidecarLockPath("/home/tasks/abc.json"))
	assert.Equal(t, "day.jsonl.lock", SidecarLockPath("day.jsonl"))
}

func TestRemoveLocked_RemovesDataFileAndSidecar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abc.json")
	require.NoError(t, WithFlock(SidecarLockPath(path), func() error {
		return WriteFileAtomic(path, []byte(`{"id":"abc"}`), 0o600)
	}))
	require.FileExists(t, SidecarLockPath(path), "precondition: the write left its sidecar in place")

	require.NoError(t, RemoveLocked(path))
	assert.NoFileExists(t, path)
	assert.NoFileExists(t, SidecarLockPath(path), "a removed record must not leave its lock file behind")
}

func TestRemoveLocked_MissingFile_ReportsNotExistAndCreatesNoSidecar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gone.json")
	require.ErrorIs(t, RemoveLocked(path), fs.ErrNotExist)
	assert.NoFileExists(t, SidecarLockPath(path))

	missingDir := filepath.Join(t.TempDir(), "no-such-dir", "gone.json")
	require.ErrorIs(t, RemoveLocked(missingDir), fs.ErrNotExist)
}

// flockExclusive/flockUnlock wrap unix.Flock. Wrapping the call inline
// (`return fmt.Errorf("x: %w", unix.Flock(...))`) turns a successful
// lock into a non-nil error because fmt.Errorf("%w", nil) is not nil —
// that bug shipped in an earlier wrapfix pass. These two assertions are
// the proof the rewrite must keep: success stays nil, and a real errno
// is still matchable with errors.Is through the wrap.
func TestFlockExclusive_SuccessStaysNilAndErrnoIsPreserved(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "flock")
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	require.NoError(t, flockExclusive(f),
		"a successful flock must stay a nil error; wrapping nil manufactures a failure")
	require.NoError(t, flockUnlock(f))
	require.NoError(t, f.Close())

	err = flockExclusive(f)
	require.Error(t, err)
	require.True(t, errors.Is(err, unix.EBADF),
		"wrap must preserve the unix errno via errors.Is; got %v", err)
}
