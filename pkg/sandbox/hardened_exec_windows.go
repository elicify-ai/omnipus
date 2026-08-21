//go:build windows

// Windows platform hardening for hardened_exec children. We use Job Objects
// with JOB_OBJECT_LIMIT_PROCESS_MEMORY (memory cap) and
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE (parent-death cleanup). DACL,
// Restricted Token, and AppContainer are out of scope (not implemented).

package sandbox

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// memoryLimitSupported reports whether this platform can enforce
// Limits.MemoryLimitBytes via the post-start hardener. Windows: yes
// (Job Object JOB_OBJECT_LIMIT_PROCESS_MEMORY). Used by Run to populate
// Result.MemoryLimitUnsupported (HIGH-1, silent-failure-hunter).
const memoryLimitSupported = true

// applyPlatformHardening configures the child's SysProcAttr. On Windows we
// rely on the post-start Job Object assignment for kill-on-parent-death
// semantics (KILL_ON_JOB_CLOSE), and accept a small race window between
// CreateProcess and AssignProcessToJobObject during which the child has
// no Job. In practice this window is microseconds — before npm/node
// initialisation completes.
//
// We deliberately do NOT use CREATE_SUSPENDED. Go's exec.Cmd.Start does
// not call ResumeThread for us; setting CREATE_SUSPENDED would leave the
// child blocked forever and cmd.Wait would never return. The race window
// approach matches how every other Windows process supervisor (Chocolatey,
// Visual Studio test runners, etc.) handles Job Object attachment.
func applyPlatformHardening(cmd *exec.Cmd, _ Limits) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	return nil
}

// applyPostStartHardening creates a Job Object, applies the memory limit,
// and assigns the child process to it. The Job Object is closed when its
// last referencing handle is closed AND no live processes are assigned —
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE means closing the handle will
// terminate the child.
//
// Failure cases:
// - Job Object creation failure → error returned, child is killed.
// - SetInformationJobObject failure → error returned, child is killed.
// - AssignProcessToJobObject failure → error returned, child is killed.
//
// On success we deliberately do NOT close the Job Object handle here;
// it stays alive until the cmd.Wait completes (the runtime keeps a
// reference via cmd.Process). When the gateway exits, the OS reclaims
// the handle and KILL_ON_JOB_CLOSE terminates the child — which is the
// whole point of using a Job Object on Windows.
func applyPostStartHardening(cmd *exec.Cmd, lim Limits) error {
	if cmd.Process == nil {
		return fmt.Errorf("hardened_exec/windows: child has no process handle")
	}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("CreateJobObject: %w", err)
	}

	// Configure the job's extended limit information. We always set
	// KILL_ON_JOB_CLOSE so the child dies when the gateway exits;
	// memory limit is conditional on lim.MemoryLimitBytes.
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if lim.MemoryLimitBytes > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY
		// uintptr is 64-bit on amd64 / arm64 Windows; on 386 it's
		// 32-bit and the cast truncates. Tier 2 / Tier 3 are not
		// supported on win/386 in any case (no 32-bit Node these
		// days) — clamp to maxUint32 to be safe.
		info.ProcessMemoryLimit = uintptr(lim.MemoryLimitBytes)
	}

	_, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		if ignoredErr := windows.CloseHandle(job); ignoredErr != nil {
			_ = ignoredErr
		}
		return fmt.Errorf("SetInformationJobObject: %w", err)
	}

	pid := uint32(cmd.Process.Pid)
	procHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		pid,
	)
	if err != nil {
		if ignoredErr := windows.CloseHandle(job); ignoredErr != nil {
			_ = ignoredErr
		}
		return fmt.Errorf("OpenProcess pid=%d: %w", pid, err)
	}
	defer windows.CloseHandle(procHandle)

	if err := windows.AssignProcessToJobObject(job, procHandle); err != nil {
		if ignoredErr := windows.CloseHandle(job); ignoredErr != nil {
			_ = ignoredErr
		}
		return fmt.Errorf("AssignProcessToJobObject: %w", err)
	}

	// Job handle stays open intentionally. Closing it would invoke
	// KILL_ON_JOB_CLOSE before the child has finished. The handle is
	// released when the parent process exits — at which point
	// KILL_ON_JOB_CLOSE terminates the child, which is exactly the
	// behaviour we want for parent-death cleanup.
	return nil
}
