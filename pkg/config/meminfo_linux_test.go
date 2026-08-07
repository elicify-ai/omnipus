//go:build linux

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// withFakeMeminfo points procMeminfoPath at a temp file with the given
// content for the duration of the test, restoring the real path on cleanup.
func withFakeMeminfo(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "meminfo")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture meminfo: %v", err)
	}
	old := procMeminfoPath
	procMeminfoPath = path
	t.Cleanup(func() { procMeminfoPath = old })
}

// TestReadMemAvailableBytes_ParsesMemAvailable verifies the modern
// (kernel 3.14+) MemAvailable field is read and converted from kB to bytes.
func TestReadMemAvailableBytes_ParsesMemAvailable(t *testing.T) {
	withFakeMeminfo(t, "MemTotal:        4048208 kB\nMemFree:          123456 kB\nMemAvailable:     378880 kB\n")
	got := readMemAvailableBytes()
	want := uint64(378880) * 1024
	if got != want {
		t.Fatalf("readMemAvailableBytes() = %d, want %d", got, want)
	}
}

// TestReadMemAvailableBytes_FallsBackToHalfTotal verifies the pre-3.14
// kernel fallback: when MemAvailable is absent, half of MemTotal is used.
func TestReadMemAvailableBytes_FallsBackToHalfTotal(t *testing.T) {
	withFakeMeminfo(t, "MemTotal:        4048208 kB\nMemFree:          123456 kB\n")
	got := readMemAvailableBytes()
	want := (uint64(4048208) * 1024) / 2
	if got != want {
		t.Fatalf("readMemAvailableBytes() fallback = %d, want %d (half of MemTotal)", got, want)
	}
}

// TestReadMemAvailableBytes_MissingFile verifies a completely unreadable
// meminfo file degrades to the fallbackTotalRAMBytes/2 constant rather than
// panicking or returning zero.
func TestReadMemAvailableBytes_MissingFile(t *testing.T) {
	old := procMeminfoPath
	procMeminfoPath = filepath.Join(t.TempDir(), "does-not-exist")
	t.Cleanup(func() { procMeminfoPath = old })

	got := readMemAvailableBytes()
	want := uint64(fallbackTotalRAMBytes) / 2
	if got != want {
		t.Fatalf("readMemAvailableBytes() with missing file = %d, want %d", got, want)
	}
}

// TestReadMemAvailableBytes_MalformedLine verifies a malformed MemAvailable
// line (non-numeric value) degrades gracefully rather than panicking.
func TestReadMemAvailableBytes_MalformedLine(t *testing.T) {
	withFakeMeminfo(t, "MemTotal:        4048208 kB\nMemAvailable:     not-a-number kB\n")
	got := readMemAvailableBytes()
	want := (uint64(4048208) * 1024) / 2
	if got != want {
		t.Fatalf("readMemAvailableBytes() with malformed line = %d, want %d (fallback to half MemTotal)", got, want)
	}
}

// TestReadCgroupMemoryAvailableBytesAt_V2 verifies cgroup v2 memory.max -
// memory.current is computed correctly.
func TestReadCgroupMemoryAvailableBytesAt_V2(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "memory.max"), "1073741824")    // 1 GiB
	writeFile(t, filepath.Join(dir, "memory.current"), "268435456") // 256 MiB used

	got, ok := readCgroupMemoryAvailableBytesAt(dir)
	if !ok {
		t.Fatal("readCgroupMemoryAvailableBytesAt() ok = false, want true (v2 files present)")
	}
	want := uint64(1073741824 - 268435456)
	if got != want {
		t.Fatalf("readCgroupMemoryAvailableBytesAt() = %d, want %d", got, want)
	}
}

// TestReadCgroupMemoryAvailableBytesAt_V2Unlimited verifies memory.max="max"
// (cgroup v2's literal "unlimited" spelling) is treated as no-limit and the
// function reports not-ok so the caller falls back to meminfo.
func TestReadCgroupMemoryAvailableBytesAt_V2Unlimited(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "memory.max"), "max")
	writeFile(t, filepath.Join(dir, "memory.current"), "268435456")

	_, ok := readCgroupMemoryAvailableBytesAt(dir)
	if ok {
		t.Fatal("readCgroupMemoryAvailableBytesAt() ok = true for memory.max=max, want false (unlimited)")
	}
}

// TestReadCgroupMemoryAvailableBytesAt_V1 verifies the cgroup v1 fallback
// path (memory/memory.limit_in_bytes, memory/memory.usage_in_bytes) when no
// v2 files are present.
func TestReadCgroupMemoryAvailableBytesAt_V1(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "memory"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(dir, "memory", "memory.limit_in_bytes"), "536870912") // 512 MiB
	writeFile(t, filepath.Join(dir, "memory", "memory.usage_in_bytes"), "104857600") // 100 MiB used

	got, ok := readCgroupMemoryAvailableBytesAt(dir)
	if !ok {
		t.Fatal("readCgroupMemoryAvailableBytesAt() ok = false, want true (v1 files present)")
	}
	want := uint64(536870912 - 104857600)
	if got != want {
		t.Fatalf("readCgroupMemoryAvailableBytesAt() = %d, want %d", got, want)
	}
}

// TestReadCgroupMemoryAvailableBytesAt_V1UnlimitedSentinel verifies a v1
// "unlimited" huge sentinel value is treated as no-limit.
func TestReadCgroupMemoryAvailableBytesAt_V1UnlimitedSentinel(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "memory"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(dir, "memory", "memory.limit_in_bytes"), strconv.FormatUint(cgroupUnlimitedThresholdBytes, 10))
	writeFile(t, filepath.Join(dir, "memory", "memory.usage_in_bytes"), "104857600")

	_, ok := readCgroupMemoryAvailableBytesAt(dir)
	if ok {
		t.Fatal("readCgroupMemoryAvailableBytesAt() ok = true for a v1 unlimited sentinel, want false")
	}
}

// TestReadCgroupMemoryAvailableBytesAt_Absent verifies a directory with
// neither v2 nor v1 memory-controller files reports not-ok (no cgroup
// memory limit signal available at all — e.g. non-containerized host).
func TestReadCgroupMemoryAvailableBytesAt_Absent(t *testing.T) {
	dir := t.TempDir() // empty — no cgroup files at all
	_, ok := readCgroupMemoryAvailableBytesAt(dir)
	if ok {
		t.Fatal("readCgroupMemoryAvailableBytesAt() ok = true for an empty directory, want false")
	}
}

// TestReadCgroupMemoryAvailableBytesAt_UsageAtLimit verifies usage >= limit
// floors available at zero rather than underflowing (uint64 wraparound would
// otherwise silently produce a huge bogus "available" number).
func TestReadCgroupMemoryAvailableBytesAt_UsageAtLimit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "memory.max"), "1000")
	writeFile(t, filepath.Join(dir, "memory.current"), "1500") // usage exceeds limit

	got, ok := readCgroupMemoryAvailableBytesAt(dir)
	if !ok {
		t.Fatal("readCgroupMemoryAvailableBytesAt() ok = false, want true")
	}
	if got != 0 {
		t.Fatalf("readCgroupMemoryAvailableBytesAt() = %d, want 0 (floored, not underflowed)", got)
	}
}

// TestAvailableRAMBytes_PrefersTighterCgroupLimit verifies availableRAMBytes
// takes the MINIMUM of the meminfo-reported host-wide value and the
// cgroup-derived value — a container with a tight memory.max must never be
// told it has more memory available than its cgroup actually permits, even
// if the host-wide /proc/meminfo reading is much larger.
func TestAvailableRAMBytes_PrefersTighterCgroupLimit(t *testing.T) {
	withFakeMeminfo(t, "MemTotal:        8000000 kB\nMemAvailable:    6000000 kB\n") // ~6 GB host-wide available

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "memory.max"), "104857600")    // 100 MiB cgroup limit
	writeFile(t, filepath.Join(dir, "memory.current"), "10485760") // 10 MiB used -> 90 MiB available

	oldRoot := cgroupRoot
	cgroupRoot = dir
	t.Cleanup(func() { cgroupRoot = oldRoot })

	got := availableRAMBytes()
	want := uint64(104857600 - 10485760)
	if got != want {
		t.Fatalf("availableRAMBytes() = %d, want %d (tighter cgroup limit must win over host-wide meminfo)", got, want)
	}
}

// TestReadCgroupMemoryAvailableBytesAt_V2ExcludesReclaimablePageCache
// reproduces MAJOR-1 from the 2026-08-04 code review: memory.current (v2)
// includes reclaimable page cache, so a naive limit-usage subtraction
// under-reports available memory whenever the kernel has grown page cache
// toward the cgroup limit (the normal steady state after sustained file I/O,
// not an anomaly). Fixed by subtracting memory.stat's inactive_file +
// slab_reclaimable from usage before subtracting from the limit.
//
// Concrete numbers mirror the review's "--memory=2g container, 300 MB RSS"
// scenario: memory.current sits 1 MiB below the 2 GiB limit (kernel reclaims
// at the limit), and reclaimable cache (inactive_file + slab_reclaimable)
// accounts for all but ~300 MB of that usage.
func TestReadCgroupMemoryAvailableBytesAt_V2ExcludesReclaimablePageCache(t *testing.T) {
	const (
		limit           = uint64(2) * 1024 * 1024 * 1024 // 2 GiB --memory=2g
		usage           = limit - 1*1024*1024            // memory.current: 1 MiB below limit (warm page cache steady state)
		rss             = uint64(314572800)              // ~300 MB real app RSS
		inactiveFile    = uint64(1_800_000_000)
		slabReclaimable = usage - rss - inactiveFile // remainder of reclaimable cache
	)

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "memory.max"), strconv.FormatUint(limit, 10))
	writeFile(t, filepath.Join(dir, "memory.current"), strconv.FormatUint(usage, 10))
	writeFile(t, filepath.Join(dir, "memory.stat"), "anon 100000000\n"+
		"inactive_file "+strconv.FormatUint(inactiveFile, 10)+"\n"+
		"slab_reclaimable "+strconv.FormatUint(slabReclaimable, 10)+"\n"+
		"slab_unreclaimable 5000000\n")

	got, ok := readCgroupMemoryAvailableBytesAt(dir)
	if !ok {
		t.Fatal("readCgroupMemoryAvailableBytesAt() ok = false, want true")
	}

	// Correct: limit - (usage - reclaimable) == limit - rss (approximately).
	wantAvailable := limit - rss
	if got != wantAvailable {
		t.Fatalf("readCgroupMemoryAvailableBytesAt() = %d, want %d (limit minus true non-reclaimable usage, not limit minus raw memory.current)", got, wantAvailable)
	}

	// The naive (buggy) limit-usage subtraction would floor this to ~1 MiB,
	// which after autoDetectMaxParallel's /3.5MB division and clampParallel's
	// floor collapses concurrency to 2. Assert the fix yields something far
	// above that floor-collapse regime so a regression back to the naive
	// subtraction is caught even if the exact byte math above ever drifts.
	naiveAvailable := limit - usage // what the old buggy code would have returned
	if got <= naiveAvailable*10 {
		t.Fatalf("readCgroupMemoryAvailableBytesAt() = %d is not meaningfully larger than the naive (buggy) limit-usage=%d; reclaimable cache is not being excluded from usage", got, naiveAvailable)
	}
}

// TestReadCgroupMemoryAvailableBytesAt_V1ExcludesReclaimablePageCache is the
// cgroup v1 equivalent: memory.usage_in_bytes also includes page cache, and
// memory.stat carries the same inactive_file key (v1 has no slab_reclaimable
// breakdown — that field is simply absent and must contribute 0, not error).
func TestReadCgroupMemoryAvailableBytesAt_V1ExcludesReclaimablePageCache(t *testing.T) {
	const (
		limit        = uint64(2) * 1024 * 1024 * 1024
		usage        = limit - 1*1024*1024
		rss          = uint64(314572800)
		inactiveFile = usage - rss
	)

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "memory"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(dir, "memory", "memory.limit_in_bytes"), strconv.FormatUint(limit, 10))
	writeFile(t, filepath.Join(dir, "memory", "memory.usage_in_bytes"), strconv.FormatUint(usage, 10))
	writeFile(t, filepath.Join(dir, "memory", "memory.stat"), "rss "+strconv.FormatUint(rss, 10)+"\n"+
		"cache "+strconv.FormatUint(inactiveFile, 10)+"\n"+
		"inactive_file "+strconv.FormatUint(inactiveFile, 10)+"\n"+
		"active_file 0\n")

	got, ok := readCgroupMemoryAvailableBytesAt(dir)
	if !ok {
		t.Fatal("readCgroupMemoryAvailableBytesAt() ok = false, want true")
	}
	wantAvailable := limit - rss
	if got != wantAvailable {
		t.Fatalf("readCgroupMemoryAvailableBytesAt() (v1) = %d, want %d", got, wantAvailable)
	}
}

// TestAutoDetectMaxParallel_WarmPageCacheContainerDoesNotCollapseToFloor is
// the end-to-end reproduction of MAJOR-1's concrete failure: a
// --memory=2g container with warm page cache must not silently collapse
// agent concurrency to the auto-detect floor of 2 hours into uptime with no
// restart and no log. The host-wide meminfo reading is deliberately generous
// (plenty of free RAM on the host) so the tight cgroup limit is the binding
// constraint, matching a real containerized deployment.
func TestAutoDetectMaxParallel_WarmPageCacheContainerDoesNotCollapseToFloor(t *testing.T) {
	withFakeMeminfo(t, "MemTotal:       32000000 kB\nMemAvailable:   24000000 kB\n") // generous host

	const (
		limit        = uint64(2) * 1024 * 1024 * 1024
		usage        = limit - 1*1024*1024
		rss          = uint64(314572800)
		inactiveFile = usage - rss
	)

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "memory.max"), strconv.FormatUint(limit, 10))
	writeFile(t, filepath.Join(dir, "memory.current"), strconv.FormatUint(usage, 10))
	writeFile(t, filepath.Join(dir, "memory.stat"), "inactive_file "+strconv.FormatUint(inactiveFile, 10)+"\nslab_reclaimable 0\n")

	oldRoot := cgroupRoot
	cgroupRoot = dir
	t.Cleanup(func() { cgroupRoot = oldRoot })

	got := autoDetectMaxParallel()
	if got <= 2 {
		t.Fatalf("autoDetectMaxParallel() = %d, want > 2 (must not collapse to the floor when the container has real headroom above its warm page cache)", got)
	}
}

// TestAvailableRAMBytes_FallsBackToMeminfoWhenNoCgroup verifies that with no
// cgroup memory-controller files present, availableRAMBytes() uses the
// meminfo-derived value directly.
func TestAvailableRAMBytes_FallsBackToMeminfoWhenNoCgroup(t *testing.T) {
	withFakeMeminfo(t, "MemTotal:        8000000 kB\nMemAvailable:    6000000 kB\n")

	oldRoot := cgroupRoot
	cgroupRoot = t.TempDir() // empty — no cgroup files
	t.Cleanup(func() { cgroupRoot = oldRoot })

	got := availableRAMBytes()
	want := uint64(6000000) * 1024
	if got != want {
		t.Fatalf("availableRAMBytes() = %d, want %d (meminfo value, no cgroup signal)", got, want)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture file %s: %v", path, err)
	}
}
