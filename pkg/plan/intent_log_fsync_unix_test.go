//go:build !windows

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// intent_log_fsync_unix_test.go proves — non-vacuously — that the
// write-ahead intent-log's write path (shared by AppendIntent, MarkCommitted,
// and MarkDone via writeLocked, sec-MINOR-3/#539 item 3) actually calls
// f.Sync() rather than merely appending to the page cache. A plain assertion
// that "the code calls fileutil.AppendJSONLSync" would be vacuous (it
// restates the production line rather than observing a real durability
// property, and would stay green even if that function silently stopped
// syncing, or if a future edit swapped in a hand-rolled non-syncing append).
//
// The technique: fsync(2) on a named pipe (FIFO) fails with EINVAL on Linux
// ("invalid argument") — pipes have no backing store to flush, whereas a
// plain Write() to a FIFO succeeds normally. So: point the intent log's
// per-plan file at a FIFO instead of a regular file, drive a real
// AppendIntent call through it, and assert the call surfaces a sync-specific
// error. If the write path stopped calling f.Sync(), this test would stop
// observing an error and go RED — see the MANDATORY mutation-test evidence
// pasted in the #539 closing report.
//
// FIFOs require a reader/writer handshake to avoid blocking opens, and the
// two opens in play (readAllLocked's read-open, then AppendJSONLSync's
// write-open) happen strictly sequentially inside the SAME writeLocked call.
// Getting this handshake wrong doesn't produce a wrong-but-green result — it
// deadlocks (a Go named-pipe open() with no counterpart blocks forever) —
// so any ordering bug here shows up loudly as a test timeout, never a false
// pass. Two verified-empirically bugs along the way, both left documented
// so the fix isn't "undone" by a future edit:
//
//  1. readAllLocked's os.Open(O_RDONLY) blocks until a writer exists. A
//     transient helper opens O_WRONLY and immediately closes without
//     writing, so the read side sees an immediate EOF (0 records) — exactly
//     what a fresh, empty intent log looks like, rather than hanging.
//
//  2. fileutil.AppendJSONLSync's later os.OpenFile(O_WRONLY) ALSO blocks
//     until a reader exists. The naive fix — a second one-shot transient
//     reader, opened concurrently with helper 1 from the start — flaked
//     roughly 1-in-5: a single blocked reader-open can be woken by ANY
//     writer presence, so it would sometimes pair with helper 1's transient
//     write (meant only for the read phase) and exit before the real write
//     ever happened, starving it of a reader forever.
//
//     The next attempt — a PERMANENT O_NONBLOCK reader opened before
//     anything else touches the path — traded one race for another: once
//     that permanent reader exists, helper 1's write-open no longer needs
//     production's read-open to unblock it (ANY reader satisfies a blocked
//     writer-open), so helper 1 can complete before production's read-open
//     has even been issued, leaving production's read-open with no writer
//     ever again. This flaked in the OTHER direction, also roughly 1-in-5.
//
//     The fix that is actually deterministic: keep the two phases
//     temporally disjoint. Do NOT open the permanent write-phase reader
//     until helper 1 reports that its open()+Close() round-trip has
//     completed. While the permanent reader does not yet exist, helper 1's
//     blocking write-open has exactly one possible counterpart — production's
//     own readAllLocked call — so the pairing cannot be stolen, regardless
//     of goroutine scheduling order (this is the same unconditional
//     rendezvous guarantee two independently-started shell processes rely on
//     with a named pipe). Only once that handshake is confirmed done do we
//     open the permanent nonblocking reader for the write phase; Go's
//     runtime integrates that nonblocking pipe fd with its poller, so a
//     manual Read() retry loop (NOT io.Copy, which would return for good on
//     the very first EOF and never notice the later real write — pipes only
//     signal EOF for as long as writer-count is momentarily zero, not
//     permanently) behaves like a normal blocking drain from the caller's
//     perspective.
//
// Build-tag note: syscall.Mkfifo is unix-only (mirrors the existing
// pkg/gateway/rest_clivalidate_fifo_test.go precedent in this repo), so this
// file is excluded on Windows.
package plan

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/testutil"
)

// TestIntentLog_AppendIntent_ActuallyFsyncs is the non-vacuous fsync proof
// for sec-MINOR-3/#539 item 3. It does NOT assert "AppendJSONLSync was
// called" (a restatement of the source); it asserts the OBSERVABLE
// consequence of a real fsync(2) call against a destination that cannot
// support one.
//
// Darwin-only exception (found via Cross-Platform CI, matrix
// (macos-latest, arm64), 2026-08-06): this test's whole technique rests on
// fsync(2) against a FIFO returning EINVAL, which the file-level comment
// above already flags as a Linux-specific guarantee ("fails with EINVAL on
// Linux"). On Darwin/XNU, fsync(2) on a named pipe returns success instead
// of EINVAL, so AppendIntent's real f.Sync() call (fileutil.AppendJSONLSync,
// unconditional, no platform branching) returns nil on macOS even though it
// genuinely executed — there is no production bug here, only a
// non-portable observability trick. Skip rather than assert a Linux-only
// kernel quirk as if it held everywhere.
func TestIntentLog_AppendIntent_ActuallyFsyncs(t *testing.T) {
	testutil.SkipOnDarwin(t)
	dir := t.TempDir()
	planID := "p-fsync-fifo"
	path := filepath.Join(dir, planID+".jsonl")

	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	// Phase 1 (read): kick off the real AppendIntent call and, concurrently,
	// a transient writer that pairs ONLY with production's own readAllLocked
	// open (nothing else is listening on this path yet — see the file-level
	// comment for why that exclusivity is what makes the rendezvous
	// deterministic). Its Close() delivers an immediate EOF, so readAllLocked
	// observes "0 records" rather than hanging.
	testKey, keyErr := resolveChainKey(nil)
	if keyErr != nil {
		t.Fatalf("resolveChainKey: %v", keyErr)
	}
	il := &IntentLog{dir: dir, lock: &StripedLock{}, chainKey: testKey}

	appendDone := make(chan error, 1)
	go func() {
		appendDone <- il.AppendIntent(IntentRecord{IntentID: "i-fsync", PlanID: planID})
	}()

	writerDone := make(chan error, 1)
	go func() {
		w, werr := os.OpenFile(path, os.O_WRONLY, 0)
		if werr != nil {
			writerDone <- werr
			return
		}
		writerDone <- w.Close()
	}()

	select {
	case werr := <-writerDone:
		if werr != nil {
			t.Fatalf("transient read-phase writer failed: %v", werr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("read-phase writer handshake did not complete within 10s — deadlocked " +
			"(this would indicate a test-harness bug, not a production one)")
	}

	// Phase 2 (write): ONLY NOW open the permanent nonblocking reader that
	// AppendJSONLSync's later blocking write-open will pair against. Because
	// the read-phase handshake above is confirmed complete, this cannot
	// steal the phase-1 pairing (see file-level comment).
	permReader, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatalf("open permanent nonblocking reader: %v", err)
	}
	stopDrain := make(chan struct{})
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		buf := make([]byte, 4096)
		for {
			select {
			case <-stopDrain:
				return
			default:
			}
			// A pipe/FIFO's EOF is a momentary "no writer right now"
			// condition, not a sticky end-of-stream marker — a NEW writer
			// (the real production write) can arrive after an earlier one
			// (irrelevant here, since phase 2 starts after phase 1's writer
			// already closed) and later reads will see fresh data again.
			// io.Copy would treat the first EOF as terminal and never look
			// again, so this loop retries by design.
			_, rerr := permReader.Read(buf)
			if rerr != nil {
				time.Sleep(time.Millisecond)
			}
		}
	}()
	t.Cleanup(func() {
		close(stopDrain)
		_ = permReader.Close()
		select {
		case <-drainDone:
		case <-time.After(2 * time.Second):
			t.Log("permReader drain goroutine did not finish teardown promptly (harmless: process-exit reclaims it)")
		}
	})

	var appendErr error
	select {
	case appendErr = <-appendDone:
	case <-time.After(10 * time.Second):
		t.Fatal("AppendIntent did not return within 10s — FIFO handshake deadlocked " +
			"(this would indicate a test-harness bug, not a production one)")
	}
	if appendErr == nil {
		t.Fatal("AppendIntent against a FIFO-backed path returned nil error — " +
			"this means no fsync(2) was attempted (a FIFO write without an " +
			"explicit Sync() call succeeds silently). The write path must call " +
			"f.Sync() at the append (sec-MINOR-3/#539 item 3).")
	}
	if !strings.Contains(appendErr.Error(), "sync") {
		t.Fatalf("AppendIntent failed for an unexpected reason (want a Sync()-stage "+
			"failure — fsync on a FIFO is EINVAL): %v", appendErr)
	}
	t.Logf("observed expected sync-stage failure against FIFO-backed path: %v", appendErr)
}
