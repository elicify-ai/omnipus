// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package launcher

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Main runs run-limited on Windows: create a Job Object with the limits,
// assign this process, wait for the go-byte, start the target inside the job
// with this process's stdio handles, and relay its exit code. The job handle
// is never closed explicitly: it closes when this process ends, and
// kill-on-close then ends the whole tree.
func Main(args []string) int {
	limits, target, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitUsage
	}
	if err := enterJob(limits); err != nil {
		fmt.Fprintln(os.Stderr, "run-limited:", err)
		return ExitLimitFailed
	}
	if !readGoByte() {
		return ExitNoGoByte
	}
	// #nosec G204 -- target is the verified component path the gateway
	// resolved and fingerprinted (AD-4); no shell is involved.
	cmd := exec.Command(target[0], target[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "run-limited: start target:", err)
		return ExitStartFailed
	}
	err = cmd.Wait()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "run-limited: wait target:", err)
		return ExitStartFailed
	}
	return 0
}

func enterJob(l Limits) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("create job: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	flags := uint32(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION)
	if l.MemoryBytes > 0 {
		flags |= windows.JOB_OBJECT_LIMIT_JOB_MEMORY
		info.JobMemoryLimit = uintptr(l.MemoryBytes)
	}
	if l.Processes > 0 {
		flags |= windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS
		info.BasicLimitInformation.ActiveProcessLimit = l.Processes
	}
	if l.CPUSeconds > 0 {
		flags |= windows.JOB_OBJECT_LIMIT_JOB_TIME
		// 100-nanosecond units.
		info.BasicLimitInformation.PerJobUserTimeLimit = int64(l.CPUSeconds) * 10_000_000
	}
	info.BasicLimitInformation.LimitFlags = flags
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return fmt.Errorf("set job limits: %w", err)
	}
	if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		return fmt.Errorf("assign to job: %w", err)
	}
	return nil
}

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
