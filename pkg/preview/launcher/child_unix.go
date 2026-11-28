// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build linux || darwin

package launcher

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Main runs the run-limited mode with argv after ModeArg and returns an exit
// code; on success it never returns (the process becomes the target).
func Main(args []string) int {
	limits, target, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitUsage
	}
	if limitErr := applyLimits(limits); limitErr != nil {
		fmt.Fprintln(os.Stderr, "run-limited:", limitErr)
		return ExitLimitFailed
	}
	if deathErr := armParentDeath(); deathErr != nil {
		fmt.Fprintln(os.Stderr, "run-limited:", deathErr)
		return ExitParentIsGone
	}
	if !readGoByte() {
		return ExitNoGoByte
	}
	err = unix.Exec(target[0], target, os.Environ())
	fmt.Fprintln(os.Stderr, "run-limited: start target:", err)
	return ExitStartFailed
}

func setRlimit(resource int, v uint64, name string) error {
	if v == 0 {
		return nil
	}
	if err := unix.Setrlimit(resource, &unix.Rlimit{Cur: v, Max: v}); err != nil {
		return fmt.Errorf("set %s limit to %d: %w", name, v, err)
	}
	return nil
}

func applyCommonLimits(l Limits) error {
	if err := setRlimit(unix.RLIMIT_CPU, l.CPUSeconds, "CPU"); err != nil {
		return err
	}
	if err := setRlimit(unix.RLIMIT_FSIZE, l.FileSizeBytes, "file-size"); err != nil {
		return err
	}
	return setRlimit(unix.RLIMIT_NOFILE, l.OpenFiles, "open-file")
}

// readGoByte reads exactly one byte from stdin without buffering more, so
// the request that follows stays on stdin for the target.
func readGoByte() bool {
	var b [1]byte
	for {
		n, err := os.Stdin.Read(b[:])
		if n == 1 {
			return true
		}
		if err != nil {
			return false
		}
	}
}
