// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !darwin

package launcher

// MemoryKillCause is the kill cause a memory watchdog records (macOS only;
// Linux and Windows enforce memory with kernel limits instead).
const MemoryKillCause = "memory"

// startWatchdog is a no-op where the kernel enforces the memory limit.
func startWatchdog(_ *Process, _ uint64) func() { return func() {} }

// ResidentBytes is unavailable off macOS.
func ResidentBytes(int) (uint64, bool) { return 0, false }
