//go:build !unix

package cdppipe

import "os/exec"

// startInOwnProcessGroup is a no-op where POSIX process groups do not exist
// (Windows). Helper lifetime there is bounded by the Job Object the sandbox
// layer places Chrome in, and teardown's Wait is still bounded by
// stderrDrainDelay.
func startInOwnProcessGroup(*exec.Cmd) {}

// killProcessGroup is a no-op where POSIX process groups do not exist.
func killProcessGroup(int) error { return nil }
