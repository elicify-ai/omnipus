package main

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Observe the existing external cancellation boundary, never a fabricated
// verifier/guard result. The first gate is C1's approved real-publication gate;
// the second pauses only a real CLI call to the shared verifier. Both continue
// to return the actual parent cancellation state. No production hook is added.
type repairFinalGuardContext struct {
	*repairLateContext
	armed         atomic.Bool
	verifying     chan struct{}
	verifyRelease chan struct{}
	verifyOnce    sync.Once
	verifyFree    sync.Once
}

func repairFinalGuardVerifierOnStack() bool {
	var callers [48]uintptr
	n := runtime.Callers(2, callers[:])
	frames := runtime.CallersFrames(callers[:n])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, "/pkg/session.VerifyChatHistoryIndex") {
			return true
		}
		if !more {
			return false
		}
	}
}

func (c *repairFinalGuardContext) Err() error {
	if err := c.repairLateContext.Err(); err != nil {
		return err
	}
	if c.armed.Load() && repairFinalGuardVerifierOnStack() {
		c.verifyOnce.Do(func() {
			close(c.verifying)
			select {
			case <-c.verifyRelease:
			case <-c.Context.Done():
			}
		})
	}
	return c.Context.Err()
}

func (c *repairFinalGuardContext) unblockVerifier() {
	c.verifyFree.Do(func() { close(c.verifyRelease) })
}

type repairFinalGuardWork struct {
	ctx    *repairFinalGuardContext
	cancel context.CancelFunc
	done   chan repairLateResult
	joined bool
}

func repairStartFinalGuardApply(t *testing.T, batch repairLateBatch) *repairFinalGuardWork {
	t.Helper()
	base, cancel := context.WithCancel(context.Background())
	observed := &repairFinalGuardContext{
		repairLateContext: &repairLateContext{Context: base, firstDir: repairLateSessionDir(batch, batch.ids[0]),
			entered: make(chan struct{}), release: make(chan struct{})},
		verifying: make(chan struct{}), verifyRelease: make(chan struct{}),
	}
	work := &repairFinalGuardWork{ctx: observed, cancel: cancel, done: make(chan repairLateResult, 1)}
	go func() {
		var output bytes.Buffer
		code := runRepair(observed, batch.applyArgs(), &output)
		work.done <- repairLateResult{exit: code, output: output.String()}
	}()
	t.Cleanup(func() {
		cancel()
		observed.unblock()
		observed.unblockVerifier()
		if !work.joined {
			select {
			case <-work.done:
				work.joined = true
			case <-time.After(30 * time.Second):
				t.Error("F7 owned actual CLI did not join after both observation gates released")
			}
		}
	})
	select {
	case <-observed.entered:
		repairLatePublicationState(t, batch, true)
	case result := <-work.done:
		work.joined = true
		t.Fatalf("F7 prerequisite: actual CLI ended before first real publication: exit=%d output=%s", result.exit, result.output)
	case <-time.After(30 * time.Second):
		t.Fatal("F7 real first-publication observation did not rendezvous")
	}
	return work
}

func repairAwaitFinalGuardVerification(t *testing.T, work *repairFinalGuardWork) {
	t.Helper()
	work.ctx.armed.Store(true)
	work.ctx.unblock()
	select {
	case <-work.ctx.verifying:
		t.Log("F7 actual CLI passed post-publication guards and entered real shared index verification")
	case result := <-work.done:
		work.joined = true
		t.Fatalf("F7 prerequisite: actual CLI ended before shared verifier observation: exit=%d output=%s", result.exit, result.output)
	case <-time.After(30 * time.Second):
		t.Fatal("F7 real shared-verifier observation did not rendezvous; no guessed timing accepted")
	}
}

func repairJoinFinalGuardApply(t *testing.T, work *repairFinalGuardWork) repairLateResult {
	t.Helper()
	work.ctx.unblockVerifier()
	select {
	case result := <-work.done:
		work.joined = true
		t.Logf("F7 actual two-stage CLI exit=%d output=%s", result.exit, result.output)
		return result
	case <-time.After(30 * time.Second):
		t.Fatal("F7 actual CLI did not join after real verifier resumed")
		return repairLateResult{exit: -1}
	}
}
