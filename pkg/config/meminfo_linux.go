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

// procMeminfoPath is the real /proc/meminfo location. A package-level var
// (not a const) purely so tests can point the reader at a fixture file
// instead of the real kernel-provided file — production code never mutates
// it.
var procMeminfoPath = "/proc/meminfo"

// readMemTotalBytes reads the total physical memory from /proc/meminfo.
//
// It is DETERMINABLE-OR-NOT, never fabricated (FR-078). This function used
// to return a hardcoded 4 GB constant (fallbackTotalRAMBytes, deleted) when
// /proc/meminfo could not be read or parsed. That constant was invented
// data: on a /proc-less Linux host (gVisor, a distroless container with no
// procfs mount, a hardened seccomp profile) it reported 4 GB of RAM that
// nothing had measured, and every consumer downstream treated it as a real
// reading. An unreadable /proc/meminfo now reports ok=false and the caller
// decides what an unmeasurable host means for it — see AvailableMemoryBytes.
func readMemTotalBytes() (uint64, bool) {
	return readMeminfoFieldBytesAt(procMeminfoPath, "MemTotal:")
}

// readMemAvailableBytes reads MemAvailable from /proc/meminfo — the kernel's
// own estimate of memory available for starting a new application without
// swapping, which (unlike MemFree) accounts for reclaimable page cache and
// slab.
//
// Two determinable answers, in order:
//
//  1. MemAvailable itself, when the kernel publishes it (3.14+, 2014).
//  2. Half of a REAL MemTotal reading, when MemAvailable is absent but
//     MemTotal parses. This pre-3.14 heuristic is PRESERVED deliberately
//     (FR-078): unlike the deleted 4 GB constant, it is derived from a
//     number this host actually reported, so it is a coarse estimate of
//     real memory rather than invented data.
//
// Anything else — the file is missing, unreadable, or neither field parses —
// is undeterminable and reports ok=false rather than a fabricated figure.
func readMemAvailableBytes() (uint64, bool) {
	if v, ok := readMeminfoFieldBytesAt(procMeminfoPath, "MemAvailable:"); ok {
		return v, true
	}
	if total, ok := readMemTotalBytes(); ok {
		return total / 2, true
	}
	return 0, false
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
	avail, _, ok := readCgroupMemoryBudgetBytesAt(cgroupRoot)
	return avail, ok
}

// readCgroupMemoryBudgetBytes returns BOTH halves of the cgroup memory
// signal — the memory still available under the limit, and the limit itself
// — so a caller can express the reading as a pressure RATIO
// (1 - available/limit, i.e. the non-reclaimable share of memory.max) as
// well as an absolute headroom figure. ok is false in exactly the cases
// readCgroupMemoryAvailableBytes reports false.
//
// The ratio matters because it is the SINGLE shared threshold every
// admission consumer reads (see MemoryPressureHigh): the browser pool and
// agent admission must ask the same question of the same numbers, and a
// bytes-only accessor cannot express "85% of the limit is in use" without
// each consumer re-deriving the denominator for itself.
func readCgroupMemoryBudgetBytes() (available, limit uint64, ok bool) {
	return readCgroupMemoryBudgetBytesAt(cgroupRoot)
}

// cgroupRoot is the real cgroup mount root. A package-level var (not a
// const) purely so tests can point the reader at a fixture directory tree
// instead of the real /sys/fs/cgroup — production code never mutates it.
var cgroupRoot = "/sys/fs/cgroup"

// readCgroupMemoryAvailableBytesAt is readCgroupMemoryAvailableBytes's body
// parameterized on the cgroup root, split out for testability.
func readCgroupMemoryAvailableBytesAt(root string) (uint64, bool) {
	avail, _, ok := readCgroupMemoryBudgetBytesAt(root)
	return avail, ok
}

// readCgroupMemoryBudgetBytesAt is readCgroupMemoryBudgetBytes's body
// parameterized on the cgroup root, split out for testability.
func readCgroupMemoryBudgetBytesAt(root string) (available, limit uint64, ok bool) {
	// cgroup v2 unified hierarchy.
	if lim, ok := readCgroupV2LimitBytes(root + "/memory.max"); ok {
		if usage, ok := readCgroupPlainUintBytes(root + "/memory.current"); ok {
			reclaimable := readCgroupReclaimableBytesAt(root + "/memory.stat")
			return cgroupAvailableBytes(lim, usage, reclaimable), lim, true
		}
	}
	// cgroup v1 fallback.
	if lim, ok := readCgroupV1LimitBytes(root + "/memory/memory.limit_in_bytes"); ok {
		if usage, ok := readCgroupPlainUintBytes(root + "/memory/memory.usage_in_bytes"); ok {
			reclaimable := readCgroupReclaimableBytesAt(root + "/memory/memory.stat")
			return cgroupAvailableBytes(lim, usage, reclaimable), lim, true
		}
	}
	return 0, 0, false
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
