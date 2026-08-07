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
			reclaimable := readCgroupReclaimableBytesAt(root + "/memory.stat")
			return cgroupAvailableBytes(limit, usage, reclaimable), true
		}
	}
	// cgroup v1 fallback.
	if limit, ok := readCgroupV1LimitBytes(root + "/memory/memory.limit_in_bytes"); ok {
		if usage, ok := readCgroupPlainUintBytes(root + "/memory/memory.usage_in_bytes"); ok {
			reclaimable := readCgroupReclaimableBytesAt(root + "/memory/memory.stat")
			return cgroupAvailableBytes(limit, usage, reclaimable), true
		}
	}
	return 0, false
}

// reclaimableMemoryStatKeys are the memory.stat fields summed to estimate
// reclaimable memory within cgroup usage (MAJOR-1, code review 2026-08-04):
//   - inactive_file — reclaimable page cache on its way out under memory
//     pressure. Present in both cgroup v1 and v2 memory.stat.
//   - slab_reclaimable — reclaimable kernel slab allocations (dentries,
//     inodes, etc). cgroup v2 only; v1's memory.stat has no slab breakdown,
//     so it simply never matches and contributes 0 — a partial reclaimable
//     figure UNDER-counts what's reclaimable, which makes "available" come
//     out smaller than reality, never larger (the safe direction to be
//     wrong in).
//
// This deliberately does NOT include active_file: memory still on the
// active LRU list is not immediately reclaimable without first aging onto
// the inactive list, so counting it would over-state available memory.
var reclaimableMemoryStatKeys = []string{"inactive_file", "slab_reclaimable"}

// readCgroupReclaimableBytesAt sums reclaimableMemoryStatKeys from the
// memory.stat file at statPath. Returns 0 (never an error) when the file is
// missing or a key is absent — see reclaimableMemoryStatKeys' doc comment on
// why a partial/zero read is the conservative direction.
func readCgroupReclaimableBytesAt(statPath string) uint64 {
	var total uint64
	for _, key := range reclaimableMemoryStatKeys {
		if v, ok := readCgroupStatFieldBytesAt(statPath, key); ok {
			total += v
		}
	}
	return total
}

// readCgroupStatFieldBytesAt reads a single "<key> <N>" line (bytes, not kB —
// unlike /proc/meminfo, cgroup memory.stat values are already in bytes) from
// a memory.stat-formatted file. ok is false when the file can't be opened or
// the key isn't present as an exact field-0 match (memory.stat also carries
// hierarchical "total_<key>" variants on cgroup v1, which must NOT be
// mistaken for the plain key via a prefix match).
func readCgroupStatFieldBytesAt(path, key string) (uint64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || fields[0] != key {
			continue
		}
		v, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr != nil {
			return 0, false
		}
		return v, true
	}
	return 0, false
}

// cgroupAvailableBytes returns limitBytes minus the NON-reclaimable portion
// of usageBytes, floored at zero. usageBytes (memory.current /
// memory.usage_in_bytes) includes reclaimable page cache and slab (MAJOR-1,
// code review 2026-08-04) — subtracting it raw systematically under-reports
// available memory once the kernel has grown page cache toward the cgroup
// limit, which is the normal steady state after sustained file I/O, not an
// anomaly. reclaimableBytes (from readCgroupReclaimableBytesAt) is subtracted
// from usage first so only genuinely pinned memory competes with the limit.
func cgroupAvailableBytes(limitBytes, usageBytes, reclaimableBytes uint64) uint64 {
	var nonReclaimableUsage uint64
	if reclaimableBytes < usageBytes {
		nonReclaimableUsage = usageBytes - reclaimableBytes
	}
	if nonReclaimableUsage >= limitBytes {
		return 0
	}
	return limitBytes - nonReclaimableUsage
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
