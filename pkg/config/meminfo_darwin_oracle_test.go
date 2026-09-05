//go:build darwin

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// The helpers in this file deliberately reach the kernel through a DIFFERENT
// interface from the one meminfo_darwin.go uses: the sysctl(8) and vm_stat(1)
// binaries rather than golang.org/x/sys/unix. An oracle that shares an
// implementation with the code under test cannot catch a mistake both sides
// make — which, for a reader assembled from six sysctl names, is the most
// likely mistake there is.
//
// Shelling out is confined to TESTS. Production code stays pure Go with no
// subprocesses (CLAUDE.md hard constraint 2).

// sysctlViaBinary reads a single integer sysctl through the sysctl(8)
// command.
func sysctlViaBinary(t *testing.T, name string) uint64 {
	t.Helper()
	out, err := exec.Command("sysctl", "-n", name).Output()
	if err != nil {
		t.Fatalf("sysctl -n %s: %v", name, err)
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		t.Fatalf("sysctl -n %s returned %q, which does not parse as an integer: %v", name, strings.TrimSpace(string(out)), err)
	}
	return v
}

// vmStatPages parses vm_stat(1) into a map of page counts keyed by the short
// names this package's tests use: free, active, inactive, speculative,
// purgeable, wired.
//
// vm_stat's output lines look like "Pages free:   51881." — a label, a colon,
// a count, and a trailing period. It sources its numbers from the Mach
// host_statistics64 call, not from sysctl, which is exactly why it is usable
// as an independent oracle here.
func vmStatPages(t *testing.T) map[string]uint64 {
	t.Helper()
	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		t.Fatalf("vm_stat: %v", err)
	}

	labels := map[string]string{
		"Pages free":        "free",
		"Pages active":      "active",
		"Pages inactive":    "inactive",
		"Pages speculative": "speculative",
		"Pages purgeable":   "purgeable",
		"Pages wired down":  "wired",
	}

	pages := make(map[string]uint64, len(labels))
	for _, line := range strings.Split(string(out), "\n") {
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		key, ok := labels[strings.TrimSpace(line[:colon])]
		if !ok {
			continue
		}
		raw := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line[colon+1:]), "."))
		v, parseErr := strconv.ParseUint(raw, 10, 64)
		if parseErr != nil {
			t.Fatalf("vm_stat line %q: value %q does not parse: %v", line, raw, parseErr)
		}
		pages[key] = v
	}

	for _, want := range labels {
		if _, ok := pages[want]; !ok {
			t.Fatalf("vm_stat output did not contain a %q line — the oracle cannot be trusted:\n%s", want, out)
		}
	}
	return pages
}

// TestVmStatOracle_ConfirmsSpeculativeIsInsideExternal is the measurement
// that DECIDED the reader's formula, executed as a test rather than recorded
// as a comment (FR-064: "the overlap must be DETERMINED").
//
// It asserts the identity that establishes speculative pages sit inside the
// pageable partition:
//
//	page_pageable_internal_count + page_pageable_external_count
//	  ==  vm_stat(active) + vm_stat(inactive) + vm_stat(speculative)
//
// If this identity holds, then adding vm.page_speculative_count on top of
// vm.page_pageable_external_count double-counts, and the reader is right to
// omit it. If a future macOS changes that accounting, THIS test fails — which
// is the point. The reader's formula would otherwise silently become wrong
// with nothing to catch it, since the resulting figure would still land
// inside (0, total) and still look entirely plausible.
//
// The tolerance is 1%: the two commands sample at different instants and page
// counts move continuously. Observed spread across three snapshots one second
// apart on a 32 GiB host was 421 pages out of 5.13 million (0.008%).
func TestVmStatOracle_ConfirmsSpeculativeIsInsideExternal(t *testing.T) {
	internal := sysctlViaBinary(t, "vm.page_pageable_internal_count")
	external := sysctlViaBinary(t, "vm.page_pageable_external_count")
	stats := vmStatPages(t)

	pageable := internal + external
	partition := stats["active"] + stats["inactive"] + stats["speculative"]

	if pageable == 0 || partition == 0 {
		t.Fatalf("degenerate reading: pageable=%d partition=%d", pageable, partition)
	}

	diff := float64(pageable) - float64(partition)
	if diff < 0 {
		diff = -diff
	}
	spread := diff / float64(partition)

	t.Logf("pageable_internal+external = %d pages; vm_stat active+inactive+speculative = %d pages; spread = %.4f%%",
		pageable, partition, spread*100)

	const tolerance = 0.01
	if spread > tolerance {
		t.Fatalf("pageable_internal+external (%d) and vm_stat active+inactive+speculative (%d) differ by %.3f%%, above the %.0f%% tolerance.\n"+
			"This identity is what establishes that vm.page_speculative_count is CONTAINED IN vm.page_pageable_external_count, which is why "+
			"meminfo_darwin.go omits the speculative term. If macOS has changed this accounting, readMemAvailableBytes's formula must be re-derived — "+
			"it is currently either double-counting or missing a term, and the resulting figure will still look plausible.",
			pageable, partition, spread*100, tolerance*100)
	}
}
