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
