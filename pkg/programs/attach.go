// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package programs

import (
	"errors"
	"os/exec"
)

// ErrUnscrubbedCommand is returned by Attach when program variables would be
// added to a command whose environment was never set (cmd.Env == nil). Such a
// child would inherit the whole gateway environment, so Attach refuses and
// changes nothing; the caller must not start the command (FR-239, ruling D10).
var ErrUnscrubbedCommand = errors.New("programs: command has no scrubbed environment; program variables not added")

// Attach adds program variables to a child and installs one masking writer
// per output stream (invariant I-1). It is the ONLY way to add program
// variables to a child; the spawn guard (S-106) enforces the call sites.
//
// Call it after the command's Env, Stdout and Stderr are set and before the
// launch. Attach never reads the gateway environment: when there are program
// variables to add and cmd.Env is nil (no scrubbed environment), it returns
// ErrUnscrubbedCommand and changes nothing, and the caller must not start the
// command.
// Stdout and Stderr that are the same writer share one masking writer, so
// os/exec still serialises their writes. The writers flush their last ≤ 7
// held bytes when os/exec's copy reaches EOF, before Cmd.Wait returns.
func Attach(cmd *exec.Cmd, set ResolvedPrograms) error {
	if cmd == nil {
		return nil
	}
	if len(set.Env) > 0 {
		if cmd.Env == nil {
			return ErrUnscrubbedCommand
		}
		cmd.Env = append(cmd.Env, set.Env...)
	}
	if len(set.Tokens) == 0 {
		return nil
	}
	sameStream := cmd.Stdout != nil && cmd.Stderr != nil && sameWriter(cmd.Stdout, cmd.Stderr)
	if cmd.Stdout != nil {
		cmd.Stdout = NewMaskingWriter(cmd.Stdout, set.Tokens)
	}
	if sameStream {
		cmd.Stderr = cmd.Stdout
	} else if cmd.Stderr != nil {
		cmd.Stderr = NewMaskingWriter(cmd.Stderr, set.Tokens)
	}
	return nil
}

// AttachTo is Attach with this set: it lets the sandbox runner take a
// *ResolvedPrograms through its sandbox.ChildAttachment interface without
// importing this package.
func (set *ResolvedPrograms) AttachTo(cmd *exec.Cmd) error {
	return Attach(cmd, *set)
}

// sameWriter compares two writers the way os/exec does, without panicking on
// uncomparable dynamic types.
func sameWriter(a, b any) (same bool) {
	defer func() {
		if recover() != nil {
			same = false
		}
	}()
	return a == b
}
