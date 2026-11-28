// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package programs

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// checkWaitDelay bounds how long Wait keeps reading the check's output pipes
// after the check ended or was stopped, in case a process outside the job
// still holds them (FR-005: the check never hangs).
const checkWaitDelay = 5 * time.Second

// checkJobs holds the Job Object of every running check, keyed by its
// command. A check's whole process tree lives in its job, so stopping it
// stops every process the check started, and closing it at the end
// (JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE) stops any helper left behind with the
// token.
var checkJobs = struct {
	sync.Mutex
	byProcess map[*os.Process]windows.Handle
}{byProcess: map[*os.Process]windows.Handle{}}

// setProcessGroup starts the check in a new process group (FR-005) and bounds
// the pipe wait.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
	cmd.WaitDelay = checkWaitDelay
}

// startProcessGroup puts the started check into its own Job Object with
// KILL_ON_JOB_CLOSE. It fails closed: when the job cannot be created or the
// check cannot be assigned to it, the check is killed and an error returned,
// so a check whose process tree cannot be owned never runs on.
func startProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return fmt.Errorf("programs: account check has no process")
	}
	job, err := newKillOnCloseJob()
	if err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		_ = cmd.Process.Kill()
		return fmt.Errorf("programs: open account check process: %w", err)
	}
	defer windows.CloseHandle(proc)
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		_ = windows.CloseHandle(job)
		_ = cmd.Process.Kill()
		return fmt.Errorf("programs: assign account check to its job: %w", err)
	}
	checkJobs.Lock()
	checkJobs.byProcess[cmd.Process] = job
	checkJobs.Unlock()
	return nil
}

func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("programs: create account check job: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("programs: configure account check job: %w", err)
	}
	return job, nil
}

// stopProcessGroup terminates every process in the check's job, then the
// check itself.
func stopProcessGroup(proc *os.Process) {
	if proc == nil {
		return
	}
	checkJobs.Lock()
	job, ok := checkJobs.byProcess[proc]
	checkJobs.Unlock()
	if ok {
		_ = windows.TerminateJobObject(job, 1)
	}
	_ = proc.Kill()
}

// jobAccounting mirrors JOBOBJECT_BASIC_ACCOUNTING_INFORMATION.
type jobAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

// awaitProcessGroupGone waits (bounded) until no process of the stopped
// check's job is left, terminating it again while any is, then releases the
// job. Call it after cmd.Wait returned.
func awaitProcessGroupGone(proc *os.Process) {
	checkJobs.Lock()
	job, ok := checkJobs.byProcess[proc]
	checkJobs.Unlock()
	if !ok {
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var acct jobAccounting
		err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation,
			uintptr(unsafe.Pointer(&acct)), uint32(unsafe.Sizeof(acct)), nil)
		if err != nil || acct.ActiveProcesses == 0 {
			break
		}
		_ = windows.TerminateJobObject(job, 1)
		time.Sleep(20 * time.Millisecond)
	}
	releaseProcessGroup(proc)
}

// releaseProcessGroup closes the check's job. KILL_ON_JOB_CLOSE stops any
// process the check left behind, so no helper outlives its check with the
// token.
func releaseProcessGroup(proc *os.Process) {
	checkJobs.Lock()
	job, ok := checkJobs.byProcess[proc]
	delete(checkJobs.byProcess, proc)
	checkJobs.Unlock()
	if ok {
		_ = windows.CloseHandle(job)
	}
}
