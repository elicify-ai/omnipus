// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package preview

import (
	"os/exec"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// confinement is the AD-7 file and network shape of one program start. It
// is the program's whole file list: no program inherits the calling turn's
// read rights (spec §3.4 D8c, MV-6), and the policy carries no deny list —
// nothing outside the list is granted.
type confinement struct {
	// readExec are folders the program may read and execute.
	readExec []string
	// readOnly are folders the program may read but not change (LibreOffice:
	// the macOS filtered registry folder and the system font folders).
	readOnly []string
	// readWrite are folders the program may write (LibreOffice: scratch).
	readWrite []string
	// socketDir is the macOS LibreOffice socket folder (bind + connect).
	socketDir string
}

// spawnHooks are the launcher Prepare/Start hooks for this platform and the
// unconfined reason ("" when the kernel sandbox applies).
type spawnHooks struct {
	prepare    func(*exec.Cmd) error
	start      func(*exec.Cmd) (func(), error)
	unconfined string
	// policy is the kernel policy prepare or start applies; nil when
	// unconfined or on Windows (Job Object only).
	policy *sandbox.SandboxPolicy
}

// converterStart is what one confined LibreOffice start used: the policy its
// hooks applied and its command line after them (on macOS
// `sandbox-exec -p <profile> -- …`, so the profile is the one rendered for
// this start). hooks applies the same policy to another command.
type converterStart struct {
	policy sandbox.SandboxPolicy
	args   []string
	hooks  spawnHooks
}

// testHookConverterStart, when set, is called once per confined LibreOffice
// start, synchronously and before the program runs. Only tests set it (spec
// §13 T-11, T-102, T-105 (e): the policy a real start used, never one built
// by the test).
var testHookConverterStart func(converterStart)

// observedBy returns h with observe called once per start, after prepare
// (the command line then carries the sandbox wrap) or before start. h is
// unchanged when observe is nil or h applies no policy.
func (h spawnHooks) observedBy(observe func(converterStart)) spawnHooks {
	if observe == nil || h.policy == nil {
		return h
	}
	seen := converterStart{policy: *h.policy, hooks: h}
	switch {
	case h.prepare != nil:
		inner := h.prepare
		h.prepare = func(cmd *exec.Cmd) error {
			if err := inner(cmd); err != nil {
				return err
			}
			got := seen
			got.args = append([]string(nil), cmd.Args...)
			observe(got)
			return nil
		}
	case h.start != nil:
		inner := h.start
		h.start = func(cmd *exec.Cmd) (func(), error) {
			got := seen
			got.args = append([]string(nil), cmd.Args...)
			observe(got)
			return inner(cmd)
		}
	}
	return h
}
