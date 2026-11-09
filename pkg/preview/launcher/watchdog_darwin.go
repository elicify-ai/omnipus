// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build darwin

package launcher

import (
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// WatchdogInterval is the macOS resident-memory sampling period (founder
// decision D4 A, 2026-10-30: 100 ms; S-1 measured up to ~260 MiB overshoot at
// 250 ms with fast allocation). The watchdog is best effort, never a cap.
const WatchdogInterval = 100 * time.Millisecond

// proc_pid_rusage from libSystem, called without cgo through an assembly
// trampoline — the technique golang.org/x/sys/unix uses on darwin (S-1).
//
//go:cgo_import_dynamic libc_proc_pid_rusage proc_pid_rusage "/usr/lib/libSystem.B.dylib"

//nolint:revive,stylecheck // name must match the assembly trampoline symbol
var libc_proc_pid_rusage_trampoline_addr uintptr

//go:linkname syscall_syscall syscall.syscall
func syscall_syscall(fn, a1, a2, a3 uintptr) (r1, r2 uintptr, err syscall.Errno)

// Offsets of ri_resident_size and ri_phys_footprint in struct
// rusage_info_v0 (<sys/resource.h>): uuid[16], five u64 fields, then
// ri_wired_size, ri_resident_size, ri_phys_footprint.
const (
	riResidentOffset  = 64
	riFootprintOffset = 72
)

// residentBytes returns the larger of resident size and physical footprint
// (footprint also counts compressed memory) for pid.
func residentBytes(pid int) (uint64, bool) {
	var buf [512]byte
	r1, _, _ := syscall_syscall(libc_proc_pid_rusage_trampoline_addr, uintptr(pid), 0, uintptr(unsafe.Pointer(&buf[0])))
	if int32(r1) != 0 {
		return 0, false
	}
	rss := *(*uint64)(unsafe.Pointer(&buf[riResidentOffset]))
	footprint := *(*uint64)(unsafe.Pointer(&buf[riFootprintOffset]))
	return max(rss, footprint), true
}

// MemoryKillCause is the kill cause the watchdog records.
const MemoryKillCause = "memory"

// startWatchdog samples the program every WatchdogInterval and kills its
// process group once it is above budget. It returns an idempotent stop.
func startWatchdog(p *Process, budget uint64) func() {
	stop := make(chan struct{})
	go func() {
		tk := time.NewTicker(WatchdogInterval)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				if used, ok := residentBytes(p.PID); ok && used > budget {
					p.Kill(MemoryKillCause)
					return
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stop) }) }
}

// ResidentBytes exposes the sampler for diagnostics and tests.
func ResidentBytes(pid int) (uint64, bool) { return residentBytes(pid) }
