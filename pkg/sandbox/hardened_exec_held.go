// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"fmt"
	"os/exec"
	"runtime"
	"sync"
)

// StartLockedWithPolicyHeld is StartLockedWithPolicy for a child whose
// lifetime must stay tied to the gateway through a parent-death signal: the
// office-preview run-limited launcher (office-preview spec AD-2, Linux).
//
// StartLockedWithPolicy retires its restricted launching thread as soon as
// the child has started and therefore has to clear Pdeathsig: Linux sends
// the parent-death signal when the parent THREAD exits, so the child would
// be killed at once. Here the restricted thread is kept parked until the
// caller calls release (after cmd.Wait), so cmd.SysProcAttr.Pdeathsig is
// left as the caller set it and fires only if the gateway itself dies.
// release then lets the goroutine exit still locked, so the restricted
// thread is destroyed rather than recycled, exactly as in StartLocked.
//
// release is idempotent and must be called exactly when the caller no longer
// needs the parent-death link — typically right after cmd.Wait returns.
// On a start error release is a no-op and the thread has already exited.
func StartLockedWithPolicyHeld(cmd *exec.Cmd, kernelPolicy *SandboxPolicy) (release func(), err error) {
	MarkStartLockedCalled()
	started := make(chan error, 1)
	hold := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		// Intentionally NO UnlockOSThread (see StartLocked).
		if restrictErr := restrictCurrentThreadIfNeeded(kernelPolicy); restrictErr != nil {
			emitRestrictFailure("hardened_exec.StartLockedHeld", restrictErr)
			started <- fmt.Errorf("hardened_exec: per-thread restrict: %w", restrictErr)
			return
		}
		startErr := cmd.Start()
		started <- startErr
		if startErr != nil {
			return
		}
		<-hold
	}()
	if err := <-started; err != nil {
		return func() {}, err
	}
	var once sync.Once
	return func() { once.Do(func() { close(hold) }) }, nil
}
