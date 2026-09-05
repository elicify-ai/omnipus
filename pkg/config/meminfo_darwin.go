// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build darwin

package config

import (
	"encoding/binary"

	"golang.org/x/sys/unix"
)

// This file is the Darwin half of the ONE memory mechanism (FR-064). Before
// it existed, macOS had no memory signal at all: readMemAvailableBytes
// returned 0 from the shared non-Linux stub, and every consumer either
// collapsed to a floor or (worse, MAJOR-2) derived a number from a constant
// that had never measured anything. macOS is the primary development
// platform for this project and a supported deployment target, so "no signal
// on macOS" meant the memory mechanism was untestable by the people most
// likely to notice it misbehaving.
//
// Everything here is pure Go through golang.org/x/sys/unix sysctls — no CGo,
// no shelling out to vm_stat, no new dependency (CLAUDE.md hard constraints
// 1 and 2).

// darwinPageSizeSysctl / darwinMemTotalSysctl / the vm.page_* names are
// package-level vars rather than consts purely so the reader tests can point
// the accessor at a name that does not exist and exercise the ok=false path
// without needing a machine whose kernel is broken.
var (
	darwinMemTotalSysctl  = "hw.memsize"
	darwinPageSizeSysctl  = "hw.pagesize"
	darwinPageFreeSysctl  = "vm.page_free_count"
	darwinPagePurgeSysctl = "vm.page_purgeable_count"
	darwinPageExtSysctl   = "vm.page_pageable_external_count"
)

// readMemTotalBytes returns the machine's physical memory from the
// hw.memsize sysctl.
//
// hw.memsize is the ORACLE here, deliberately, and not
// vm.page_*_count × hw.pagesize: the page counters are a live partition of
// memory the VM subsystem is currently tracking and they do not sum to
// physical RAM (wired, compressor and reserved pages are accounted
// differently), so reconstructing the total from them would produce a number
// that drifts against the machine's actual hardware. hw.memsize is the
// hardware figure and never moves.
func readMemTotalBytes() (uint64, bool) {
	v, ok := sysctlUint(darwinMemTotalSysctl)
	if !ok || v == 0 {
		return 0, false
	}
	return v, true
}

// sysctlUint reads an unsigned-integer sysctl WITHOUT assuming its width.
//
// This is not defensive padding — it is a bug fix found by running the reader
// on a real Mac, which Linux-only CI cannot do. The vm.page_* OIDs are not
// uniformly sized: on macOS 15 / arm64, vm.page_free_count and
// vm.page_pageable_external_count return 4 bytes while
// vm.page_purgeable_count returns 8. Calling unix.SysctlUint32 on the 8-byte
// one fails with ENOMEM and unix.SysctlUint64 on the 4-byte ones fails with
// EIO, so ANY fixed-width reader gets an error on some term and reports the
// whole host undeterminable. The first version of this file did exactly that:
// it compiled on every platform, cross-compiled clean, and returned
// "availability cannot be determined" on every Mac in existence.
//
// Widths are also not guaranteed stable across macOS releases or
// architectures, and a width change would fail exactly the same silent way.
// Decoding whatever the kernel returns removes the whole class.
func sysctlUint(name string) (uint64, bool) {
	raw, err := unix.SysctlRaw(name)
	if err != nil {
		return 0, false
	}
	switch len(raw) {
	case 4:
		return uint64(binary.LittleEndian.Uint32(raw)), true
	case 8:
		return binary.LittleEndian.Uint64(raw), true
	default:
		return 0, false
	}
}

// readMemAvailableBytes approximates Linux's /proc/meminfo MemAvailable on
// Darwin. It is an APPROXIMATION OF A DIFFERENT MEASUREMENT, not the same
// measurement — macOS publishes no single "memory available to start new
// work without swapping" figure, so this assembles one from the Mach VM page
// counters:
//
//	available = (free + purgeable + pageable_external) × pagesize
//
// Term by term:
//
//   - vm.page_free_count — pages on the free list, immediately usable.
//   - vm.page_purgeable_count — volatile pages the kernel may discard
//     outright under pressure without writing them anywhere. These are
//     anonymous (internal) pages, so they do NOT overlap the external term.
//   - vm.page_pageable_external_count — file-backed pages, i.e. the macOS
//     equivalent of Linux's page cache. Reclaimable by eviction.
//
// THE SPECULATIVE TERM IS DELIBERATELY ABSENT, and this is a measured
// finding rather than an omission. vm.page_speculative_count (file read-ahead
// pages) is CONTAINED IN vm.page_pageable_external_count, so adding it would
// double-count. Determined on a real 32 GiB Darwin host (2026-09-02, three
// snapshots one second apart) by cross-checking the sysctl counters against
// vm_stat: in every run
//
//	page_pageable_internal_count + page_pageable_external_count
//	  ==  vm_stat(active) + vm_stat(inactive) + vm_stat(speculative)
//
// to within 421 pages out of 5.13 million (0.008%, entirely attributable to
// the two commands sampling at different instants). Speculative pages
// therefore already sit inside that partition; since they are file-backed
// they sit in the EXTERNAL half of it. Adding the term separately would have
// over-stated availability by ~190 MB on that host.
//
// Two caveats an operator reading a number from this reader should know,
// both accepted:
//
//   - COMPRESSION. macOS compresses inactive anonymous pages rather than
//     swapping them out. Compressed pages are not counted as available here
//     even though the compressor can free real memory under pressure, so this
//     reader UNDER-states availability on a machine with a large compressor
//     footprint. Under-stating is the safe direction: a gate built on it
//     refuses too early, never too late.
//   - ACTIVE FILE PAGES. The external term is all file-backed pages, active
//     and inactive alike, because macOS publishes no active/inactive split
//     for them. Linux's cgroup reader in this same package deliberately
//     excludes active_file for exactly this reason (see
//     reclaimableMemoryStatKeys). Darwin cannot make that distinction, so
//     this term OVER-states reclaimable memory to the extent that file pages
//     are hot. This is the one direction this reader is optimistic in, and it
//     is why the result is documented as an approximation.
func readMemAvailableBytes() (uint64, bool) {
	pageSize, ok := sysctlUint(darwinPageSizeSysctl)
	if !ok || pageSize == 0 {
		return 0, false
	}
	free, ok := sysctlUint(darwinPageFreeSysctl)
	if !ok {
		return 0, false
	}
	purgeable, ok := sysctlUint(darwinPagePurgeSysctl)
	if !ok {
		return 0, false
	}
	external, ok := sysctlUint(darwinPageExtSysctl)
	if !ok {
		return 0, false
	}
	return (free + purgeable + external) * pageSize, true
}

// readCgroupMemoryAvailableBytes: cgroups are a Linux kernel feature and are
// never present on Darwin.
func readCgroupMemoryAvailableBytes() (uint64, bool) {
	return 0, false
}

// readCgroupMemoryBudgetBytes: cgroups are a Linux kernel feature and are
// never present on Darwin.
func readCgroupMemoryBudgetBytes() (available, limit uint64, ok bool) {
	return 0, 0, false
}
