// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// ErrStoreBusy is returned when a store transaction cannot be admitted within
// its bound (5 s for ordinary writes), leaving the file unchanged (connected-
// programs spec FR-049, S-146).
var ErrStoreBusy = errors.New("the credential store is busy")

// TransactHook is the test seam of scenarios S-135, S-136 and S-146: every
// store transaction calls it with a stage name at each step, so a test can
// observe a writer waiting for admission ("admission") and pause a writer
// INSIDE its transaction after its in-lock read ("after-read"). Production
// keeps it nil; only tests set it.
var TransactHook func(stage string)

const (
	// ordinaryAdmissionBound is how long an ordinary write waits to enter
	// the store transaction before it fails with ErrStoreBusy (FR-049).
	ordinaryAdmissionBound = 5 * time.Second
	// admissionRetry is the interval of the non-blocking sidecar lock retry.
	admissionRetry = 10 * time.Millisecond
)

func transactStage(stage string) {
	if hook := TransactHook; hook != nil {
		hook(stage)
	}
}

// pathGate is the process-wide half of one store file's transaction lock. It
// is shared by every Store object over the same path, so two objects in one
// process exclude each other exactly as two processes do through the sidecar.
type pathGate struct{ slot chan struct{} }

var (
	pathGatesMu sync.Mutex
	pathGates   = map[string]*pathGate{}
)

func gateFor(path string) *pathGate {
	key := filepath.Clean(path)
	if abs, err := filepath.Abs(key); err == nil {
		key = abs
	}
	pathGatesMu.Lock()
	defer pathGatesMu.Unlock()
	g, ok := pathGates[key]
	if !ok {
		g = &pathGate{slot: make(chan struct{}, 1)}
		pathGates[key] = g
	}
	return g
}

// transact runs one store transaction (invariant I-3, FR-049): admission
// through the process-wide per-path gate and the cross-process sidecar lock
// (non-blocking, retried every 10 ms, bounded at 5 s → ErrStoreBusy), a read
// of the file INSIDE the lock, then fn, which mutates the read state and
// writes it with saveFileNoLock (or returns without writing). Every exported
// mutator writes the store file only through here. Caller holds s.mu.
func (s *Store) transact(fn func(sf *storeFile) error) error {
	return s.transactUntil(time.Now().Add(ordinaryAdmissionBound), fn)
}

func (s *Store) transactUntil(deadline time.Time, fn func(sf *storeFile) error) (err error) {
	transactStage("admission")
	release, err := acquireStoreLock(s.path, deadline)
	if err != nil {
		return err
	}
	defer func() {
		// A failed transaction reports the release failure with its own
		// error. After a committed write the release failure cannot undo the
		// write, and closing the descriptor drops the lock in every case, so
		// it is reported as an error log rather than as a failed write.
		if rerr := release(); rerr != nil {
			if err != nil {
				err = errors.Join(err, rerr)
				return
			}
			slog.Error("credentials: store lock release failed after a committed write", "error", rerr)
		}
	}()
	transactStage("locked")
	sf, err := s.loadFileInternal()
	if err != nil {
		return err
	}
	transactStage("after-read")
	err = fn(sf)
	transactStage("done")
	return err
}

// acquireStoreLock takes the per-path gate and then the sidecar lock, both
// before deadline. The returned release drops both.
func acquireStoreLock(path string, deadline time.Time) (func() error, error) {
	gate := gateFor(path)
	wait := time.Until(deadline)
	if wait <= 0 {
		return nil, ErrStoreBusy
	}
	timer := time.NewTimer(wait)
	select {
	case gate.slot <- struct{}{}:
		timer.Stop()
	case <-timer.C:
		return nil, ErrStoreBusy
	}
	f, err := lockSidecarUntil(fileutil.SidecarLockPath(path), deadline)
	if err != nil {
		<-gate.slot
		return nil, err
	}
	return func() error {
		defer func() { <-gate.slot }()
		return closeSidecar(f, true)
	}, nil
}

// lockSidecarUntil opens the sidecar lock file (creating it 0600, with its
// directory) and takes its exclusive lock without blocking, retrying every
// 10 ms until deadline. The lock is only kept on the file that is at the path
// when it is taken (see fileutil.WithFlock).
func lockSidecarUntil(lockPath string, deadline time.Time) (*os.File, error) {
	// #nosec G703 -- the path is the Store's own credentials.json location (set by NewStore from the data directory at boot) or its fixed ".lock" sidecar, never request or agent input; the store exists to read and write exactly this file.
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, fmt.Errorf("credentials: create store dir: %w", err)
	}
	for {
		f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600) // #nosec G703 G304 -- the path is the Store's own credentials.json location (set by NewStore from the data directory at boot) or its fixed ".lock" sidecar, never request or agent input; the store exists to read and write exactly this file.
		if err != nil {
			return nil, fmt.Errorf("credentials: open store lock: %w", err)
		}
		locked, err := tryLockSidecar(f)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("credentials: lock store: %w", err), closeSidecar(f, false))
		}
		if locked {
			current, statErr := sameFileAtPath(f, lockPath)
			if statErr == nil && current {
				return f, nil
			}
			if cerr := closeSidecar(f, true); cerr != nil || statErr != nil {
				return nil, errors.Join(statErr, cerr)
			}
			continue // replaced while we waited: lock the current file
		}
		if cerr := closeSidecar(f, false); cerr != nil {
			return nil, cerr
		}
		if time.Now().Add(admissionRetry).After(deadline) {
			if d := time.Until(deadline); d > 0 {
				time.Sleep(d)
			}
			return nil, ErrStoreBusy
		}
		time.Sleep(admissionRetry)
	}
}

// closeSidecar unlocks (when locked) and closes the sidecar lock file,
// reporting both failures.
func closeSidecar(f *os.File, locked bool) error {
	var uerr error
	if locked {
		if err := unlockSidecar(f); err != nil {
			uerr = fmt.Errorf("credentials: unlock store: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		return errors.Join(uerr, fmt.Errorf("credentials: close store lock: %w", err))
	}
	return uerr
}

func sameFileAtPath(f *os.File, path string) (bool, error) {
	held, err := f.Stat()
	if err != nil {
		return false, fmt.Errorf("credentials: stat store lock: %w", err)
	}
	onDisk, err := os.Stat(path) // #nosec G703 -- the path is the Store's own credentials.json location (set by NewStore from the data directory at boot) or its fixed ".lock" sidecar, never request or agent input; the store exists to read and write exactly this file.
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("credentials: stat store lock: %w", err)
	}
	return os.SameFile(held, onDisk), nil
}
