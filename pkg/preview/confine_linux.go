// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build linux

package preview

import (
	"os"
	"os/exec"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// linuxSystemReadExec are the folders a dynamically linked program needs to
// start (loader, libc, the loader cache).
var linuxSystemReadExec = []string{"/lib", "/lib64", "/usr/lib", "/usr/lib64", "/etc/ld.so.cache"}

// hooksFor starts the launcher on a Landlock-restricted thread that stays
// parked until the program exits, so the parent-death SIGKILL is not fired by
// the thread's retirement (sandbox.StartLockedWithPolicyHeld). Landlock is
// inherited across execve.
func hooksFor(c confinement) spawnHooks {
	lb := sandbox.CurrentLinuxBackend()
	if !lb.EnforcingChildren() {
		return spawnHooks{
			start:      func(cmd *exec.Cmd) (func(), error) { return func() {}, cmd.Start() },
			unconfined: "the Linux kernel sandbox is not enforcing",
		}
	}
	extra := make([]string, 0, len(linuxSystemReadExec))
	for _, d := range linuxSystemReadExec {
		// Landlock refuses a rule for a path that does not exist (Alpine
		// and arm64 have no /lib64).
		if _, err := os.Stat(d); err == nil {
			extra = append(extra, d)
		}
	}
	c.readExec = append(append([]string(nil), c.readExec...), extra...)
	policy := c.policy()
	// A port allow-list naming only port 0: with Landlock ABI v4+ a
	// non-empty list denies every real TCP bind/connect, where an empty list
	// would mean "unrestricted". UDP is not covered by Landlock (documented).
	policy.BindPortRules = []sandbox.NetPortRule{{Port: 0}}
	policy.ConnectPortRules = []sandbox.NetPortRule{{Port: 0}}
	return spawnHooks{
		start: func(cmd *exec.Cmd) (func(), error) { return sandbox.StartLockedWithPolicyHeld(cmd, &policy) },
	}
}
