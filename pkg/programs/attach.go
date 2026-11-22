// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package programs

import (
	"os"
	"os/exec"
)

// Attach adds program variables to a child and installs one masking writer
// per output stream (invariant I-1). It is the ONLY way to add program
// variables to a child; the spawn guard (S-106) enforces the call sites.
//
// Call it after the command's Env, Stdout and Stderr are set and before the
// launch. When cmd.Env is nil the child would inherit the gateway's
// environment, so Attach starts from os.Environ() to keep that behaviour.
// Stdout and Stderr that are the same writer share one masking writer, so
// os/exec still serialises their writes. The writers flush their last ≤ 7
// held bytes when os/exec's copy reaches EOF, before Cmd.Wait returns.
func Attach(cmd *exec.Cmd, set ResolvedPrograms) {
	if cmd == nil {
		return
	}
	if len(set.Env) > 0 {
		if cmd.Env == nil {
			cmd.Env = os.Environ()
		}
		cmd.Env = append(cmd.Env, set.Env...)
	}
	if len(set.Tokens) == 0 {
		return
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
