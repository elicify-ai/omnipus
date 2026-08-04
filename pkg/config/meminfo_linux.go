// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// fallbackTotalRAMBytes is the conservative assumption used when
// /proc/meminfo cannot be read or parsed at all.
const fallbackTotalRAMBytes = 4 * 1024 * 1024 * 1024 // 4 GB

// procMeminfoPath is the real /proc/meminfo location. A package-level var
// (not a const) purely so tests can point the reader at a fixture file
// instead of the real kernel-provided file — production code never mutates
// it.
var procMeminfoPath = "/proc/meminfo"

// readMemTotalBytes reads the total physical memory from /proc/meminfo.
// Returns a conservative 4 GB fallback when the file cannot be read or parsed.
func readMemTotalBytes() uint64 {
	if v, ok := readMeminfoFieldBytesAt(procMeminfoPath, "MemTotal:"); ok {
		return v
	}
	return fallbackTotalRAMBytes
}

// readMemAvailableBytes reads MemAvailable from /proc/meminfo — the kernel's
// own estimate of memory available for starting a new application without
// swapping, which (unlike MemFree) accounts for reclaimable page cache and
// slab. Falls back to half of MemTotal when the kernel predates the
// MemAvailable field (pre-3.14, released 2014 — effectively unseen today,
// handled only so a parse failure degrades gracefully rather than panicking
// or returning zero).
func readMemAvailableBytes() uint64 {
	if v, ok := readMeminfoFieldBytesAt(procMeminfoPath, "MemAvailable:"); ok {
		return v
	}
	return readMemTotalBytes() / 2
}

// readMeminfoFieldBytesAt reads a single "<key>  <N> kB" line from the
// meminfo-formatted file at path and returns its value in bytes. ok is false
// when the file can't be opened, the key isn't present, or the line doesn't
// parse. Split out from readMemTotalBytes/readMemAvailableBytes (which
// always pass procMeminfoPath) so tests can exercise the parser against a
// fixture file without touching the real /proc/meminfo.
func readMeminfoFieldBytesAt(path, key string) (uint64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, key) {
			continue
		}
		// Format: "MemAvailable:   16384000 kB"
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, false
		}
		kb, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr != nil {
			return 0, false
		}
		return kb * 1024, true
	}
	return 0, false
}

// cgroupUnlimitedThresholdBytes is the point above which a cgroup v1 numeric
// memory limit is treated as "no limit configured". cgroup v1 represents
// "unlimited" as a huge sentinel (close to the max value representable in a
// page-count-times-page-size product) rather than a keyword; cgroup v2 uses
// the literal string "max" instead (handled separately, see
// readCgroupV2LimitBytes). No real machine's physical RAM approaches this
// threshold.
const cgroupUnlimitedThresholdBytes = 1 << 62

// readCgroupMemoryAvailableBytes returns the memory still available under
// the calling process's cgroup memory limit (limit − current usage), when a
// finite cgroup memory limit is configured. Returns (0, false) when no
// cgroup memory controller is present, or its limit is unset/unlimited — the
// caller should then fall back to /proc/meminfo's host-wide MemAvailable.
//
// Best-effort: assumes the calling process's cgroup is mounted at the
// standard /sys/fs/cgroup root, true for the common containerized case
// (Docker, Podman, Fly Machines) where the container gets its own cgroup
// namespace rooted there. This does not walk /proc/self/cgroup to resolve a
// non-root nested cgroup path on a shared bare-metal multi-tenant host; on
// such a host this simply returns (0, false), and the meminfo-based
// host-wide reading is used instead — always a safe, if less precise,
// fallback (see availableRAMBytes).
func readCgroupMemoryAvailableBytes() (uint64, bool) {
	return readCgroupMemoryAvailableBytesAt(cgroupRoot)
}

// cgroupRoot is the real cgroup mount root. A package-level var (not a
// const) purely so tests can point the reader at a fixture directory tree
// instead of the real /sys/fs/cgroup — production code never mutates it.
var cgroupRoot = "/sys/fs/cgroup"

// readCgroupMemoryAvailableBytesAt is readCgroupMemoryAvailableBytes's body
// parameterized on the cgroup root, split out for testability.
func readCgroupMemoryAvailableBytesAt(root string) (uint64, bool) {
	// cgroup v2 unified hierarchy.
	if limit, ok := readCgroupV2LimitBytes(root + "/memory.max"); ok {
		if usage, ok := readCgroupPlainUintBytes(root + "/memory.current"); ok {
			return cgroupAvailableBytes(limit, usage), true
		}
	}
	// cgroup v1 fallback.
	if limit, ok := readCgroupV1LimitBytes(root + "/memory/memory.limit_in_bytes"); ok {
		if usage, ok := readCgroupPlainUintBytes(root + "/memory/memory.usage_in_bytes"); ok {
			return cgroupAvailableBytes(limit, usage), true
		}
	}
	return 0, false
}

// cgroupAvailableBytes returns limitBytes-usageBytes floored at zero (usage
// can transiently read at or slightly above a just-lowered limit).
func cgroupAvailableBytes(limitBytes, usageBytes uint64) uint64 {
	if usageBytes >= limitBytes {
		return 0
	}
	return limitBytes - usageBytes
}

// readCgroupPlainUintBytes reads a file containing a single unsigned integer
// (used for both cgroup v2's memory.current and cgroup v1's
// memory.usage_in_bytes).
func readCgroupPlainUintBytes(path string) (uint64, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	v, parseErr := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if parseErr != nil {
		return 0, false
	}
	return v, true
}

// readCgroupV2LimitBytes reads a cgroup v2 memory.max file. cgroup v2 spells
// "unlimited" as the literal string "max" rather than a numeric sentinel.
func readCgroupV2LimitBytes(path string) (uint64, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	s := strings.TrimSpace(string(raw))
	if s == "max" || s == "" {
		return 0, false
	}
	v, parseErr := strconv.ParseUint(s, 10, 64)
	if parseErr != nil {
		return 0, false
	}
	return v, true
}

// readCgroupV1LimitBytes reads a cgroup v1 memory.limit_in_bytes file,
// treating a value at/above cgroupUnlimitedThresholdBytes as "no limit".
func readCgroupV1LimitBytes(path string) (uint64, bool) {
	v, ok := readCgroupPlainUintBytes(path)
	if !ok {
		return 0, false
	}
	if v >= cgroupUnlimitedThresholdBytes {
		return 0, false
	}
	return v, true
}
