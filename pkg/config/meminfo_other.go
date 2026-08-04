// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !linux

package config

// readMemTotalBytes returns a conservative 4 GB constant on non-Linux platforms.
// The auto-detect heuristic in autoDetectMaxParallel is also floored/capped by
// clampParallel, so a bad estimate here still yields a bounded, sane default.
func readMemTotalBytes() uint64 {
	return 4 * 1024 * 1024 * 1024 // 4 GB
}

// readMemAvailableBytes returns a conservative constant on non-Linux
// platforms (no /proc/meminfo to read). Assumes half of the conservative 4 GB
// total is available, mirroring the Linux pre-MemAvailable-kernel fallback
// for consistency across platforms.
func readMemAvailableBytes() uint64 {
	return readMemTotalBytes() / 2
}

// readCgroupMemoryAvailableBytes: cgroups are a Linux kernel feature and are
// never present on other platforms.
func readCgroupMemoryAvailableBytes() (uint64, bool) {
	return 0, false
}
