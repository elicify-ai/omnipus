package doctor

// command_libs_macho.go - OS-agnostic Mach-O parser for ADR-052 Phase 3 (macOS).
//
// This file contains the pure byte-level Mach-O LC_LOAD_DYLIB walker. It
// has NO build tag so it compiles on every platform - the parser is
// unit-testable on Linux with synthetic Mach-O byte fixtures (the parsing
// logic is just byte arithmetic, not OS-specific). The darwin-specific
// path resolution (os.Stat against /usr/lib, /System/Library, etc.) lives
// in command_libs_darwin.go.
//
// What we walk:
//  1. Mach-O magic at offset 0 (0xFEEDFACF = 64-bit, 0xFEEDFACE = 32-bit;
//     both little-endian and big-endian byte orders are detected).
//  2. mach_header: ncmds + sizeofcmds to locate the load-command region.
//  3. The entire load-command region (bounded by sizeofcmds - typically
//     a few KB for Chrome Mach-O, safe to read whole unlike the
//     multi-hundred-MB binary body).
//  4. Each load command - check for LC_LOAD_DYLIB (and its siblings
//     LC_LOAD_WEAK_DYLIB, LC_LAZY_LOAD_DYLIB, LC_REEXPORT_DYLIB - all
//     share the dylib_command struct layout).
//  5. For each dylib load command, extract the name string (the dylib
//     install name like "@rpath/...", "/usr/lib/libSystem.B.dylib", etc.).
//
// HC #2 (pure Go): no otool shell-out - mirrors the ELF parser's no-ldd
// rule. otool may not be present (Xcode CLT not installed), and a pure-Go
// parser keeps the check deterministic, offline, and CI-safe.
//
// PERF-002 (streaming): we read only the header (28-32 bytes) + the
// load-command region (a few KB) via io.ReaderAt.ReadAt - the binary
// body (hundreds of MB) is never touched. Mirrors the ELF parser's
// discipline of never slurping the full binary into the Go heap.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Mach-O magic numbers (see mach-o/loader.h). The magic is stored in
// the file's native byte order; the CIGAM variants are the byte-swapped
// forms that signal the opposite endianness.
const (
	mhMagic   uint32 = 0xFEEDFACE // MH_MAGIC: 32-bit, native byte order
	mhMagic64 uint32 = 0xFEEDFACF // MH_MAGIC_64: 64-bit, native byte order
	mhCigam   uint32 = 0xCEFAEDFE // MH_CIGAM: 32-bit, swapped byte order
	mhCigam64 uint32 = 0xCFFAEDFE // MH_CIGAM_64: 64-bit, swapped byte order
)

// Load command types that reference a dylib path. All share the
// dylib_command struct layout: cmd + cmdsize + (name offset, timestamp,
// current_version, compatibility_version) + NUL-terminated name string.
// See mach-o/loader.h.
const (
	lcLoadDylib       uint32 = 0x0c       // LC_LOAD_DYLIB (hard dependency)
	lcLoadWeakDylib   uint32 = 0x80000018 // LC_LOAD_WEAK_DYLIB | LC_REQ_DYLD
	lcReexportDylib   uint32 = 0x8000001f // LC_REEXPORT_DYLIB | LC_REQ_DYLD
	lcLazyLoadDylib   uint32 = 0x20       // LC_LAZY_LOAD_DYLIB
	lcLoadUpwardDylib uint32 = 0x80000023 // LC_LOAD_UPWARD_DYLIB | LC_REQ_DYLD
)

// Mach-O header sizes (mach_header vs mach_header_64). The 64-bit header
// has one extra uint32 reserved field at the end.
const (
	machOHeaderSize32 = 28
	machOHeaderSize64 = 32
)

// dylibCommandMinSize is the minimum valid size of a dylib load command:
// cmd(4) + cmdsize(4) + name_offset(4) + timestamp(4) +
// current_version(4) + compatibility_version(4) = 24 bytes. The name
// string follows immediately after these 24 bytes.
const dylibCommandMinSize = 24

// loadCommandHeaderSize is the common load-command header: cmd + cmdsize.
const loadCommandHeaderSize = 8

// maxMachOOffset bounds the offsets and sizes we accept when walking a
// Mach-O. Chrome's Mach-O is well under 512 MB; anything past 1 GiB is
// implausible and treated as a structural error. Mirrors the ELF
// parser's maxELFOffset.
const maxMachOOffset uint64 = 1 << 30 // 1 GiB

// maxMachOLoadCmdRegion caps the load-command region we will materialize.
// Chrome's load commands total ~10-20 KB; 16 MiB is a generous ceiling
// that rejects any implausible sizeofcmds without risking a huge heap
// allocation. Mirrors the ELF parser's DT_STRTAB ceiling.
const maxMachOLoadCmdRegion = 16 * 1024 * 1024

// notAMachOBinary is the synthetic soname returned when the input is not
// a Mach-O binary (wrong magic, truncated below the magic, etc.). Mirrors
// the ELF parser's "not-an-elf-binary" contract so the warning surfaces
// the diagnostic instead of silently passing.
const notAMachOBinary = "not-a-macho-binary"

// parseMachODylibs walks r as a Mach-O binary and returns the names of
// every dylib referenced by LC_LOAD_DYLIB (and sibling) load commands.
// A non-Mach-O input returns a single synthetic entry (notAMachOBinary)
// so the caller surfaces a diagnostic rather than silently passing.
//
// The parser is intentionally conservative: any structural error
// (truncated header, bad magic, implausible offset, truncated load
// command) returns a non-nil error with a descriptive message so the
// caller can decide whether to surface a warning or stay silent.
//
// This function is OS-agnostic (pure byte parsing via io.ReaderAt) so
// it can be unit-tested on Linux with synthetic Mach-O fixtures. The
// darwin wrapper missingChromeLibsMachO handles file opening and
// path resolution.
func parseMachODylibs(r io.ReaderAt) ([]string, error) {
	// --- Magic + byte-order detection ---
	// Read the first 4 bytes. io.ReaderAt.ReadAt returns io.EOF for a
	// 0-byte file and ErrUnexpectedEOF for a 1-3 byte file; both are
	// treated as "not a Mach-O" so a missing or zero-length binary
	// surfaces a clean diagnostic rather than crashing.
	magicBuf := make([]byte, 4)
	n, err := r.ReadAt(magicBuf, 0)
	if n < 4 {
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, fmt.Errorf("read mach-o magic: %w", err)
		}
		return []string{notAMachOBinary}, nil
	}

	bo, headerSize, ok := detectMachOFormat(magicBuf)
	if !ok {
		return []string{notAMachOBinary}, nil
	}

	// --- Mach-O header: read ncmds + sizeofcmds ---
	hdr, err := readAtChunk(r, 0, int(headerSize))
	if err != nil {
		return nil, fmt.Errorf("read mach-o header: %w", err)
	}

	// ncmds at offset 16, sizeofcmds at offset 20 (same layout in both
	// 32-bit mach_header and 64-bit mach_header_64).
	ncmds := bo.Uint32(hdr[16:20])
	sizeofcmds := bo.Uint32(hdr[20:24])

	if ncmds == 0 {
		return nil, nil // no load commands, nothing to check
	}
	if uint64(sizeofcmds) > maxMachOOffset || sizeofcmds > maxMachOLoadCmdRegion {
		return nil, fmt.Errorf("load-command region implausibly large: sizeofcmds=%d", sizeofcmds)
	}

	// --- Read the entire load-command region in one bounded ReadAt ---
	// sizeofcmds is typically a few KB for Chrome's Mach-O; reading it
	// whole is safe and avoids hundreds of small ReadAt syscalls.
	// The binary body (hundreds of MB) is never touched.
	lcRegion, err := readAtChunk(r, headerSize, int(sizeofcmds))
	if err != nil {
		return nil, fmt.Errorf("read load-command region (sizeofcmds=%d): %w", sizeofcmds, err)
	}

	// --- Walk load commands in-memory ---
	// Each load command starts with cmd (uint32) + cmdsize (uint32).
	// cmdsize includes the header and must be aligned (8-byte for 64-bit,
	// 4-byte for 32-bit). We advance by cmdsize through the region.
	var dylibs []string
	off := 0
	for i := uint32(0); i < ncmds; i++ {
		if off+loadCommandHeaderSize > len(lcRegion) {
			return nil, fmt.Errorf("truncated load command %d at region offset %d (region=%d)", i, off, len(lcRegion))
		}
		cmd := bo.Uint32(lcRegion[off : off+4])
		cmdsize := int(bo.Uint32(lcRegion[off+4 : off+8]))

		if cmdsize < loadCommandHeaderSize {
			return nil, fmt.Errorf("load command %d has cmdsize %d below minimum %d", i, cmdsize, loadCommandHeaderSize)
		}
		if uint64(cmdsize) > maxMachOOffset {
			return nil, fmt.Errorf("load command %d has implausible cmdsize %d", i, cmdsize)
		}
		if off+cmdsize > len(lcRegion) {
			return nil, fmt.Errorf(
				"load command %d extends past region (off=%d cmdsize=%d region=%d)",
				i,
				off,
				cmdsize,
				len(lcRegion),
			)
		}

		if isDylibLoadCommand(cmd) {
			name := extractDylibName(lcRegion[off:off+cmdsize], bo)
			if name != "" {
				dylibs = append(dylibs, name)
			}
		}

		off += cmdsize
	}

	return dylibs, nil
}

// detectMachOFormat examines the 4-byte magic and returns the byte order,
// header size, and whether the magic is a recognized Mach-O variant.
// We check via LittleEndian only because all four magic constants
// (MH_MAGIC, MH_CIGAM, MH_MAGIC_64, MH_CIGAM_64) are distinguishable
// when the 4 raw bytes are interpreted as a little-endian uint32:
//
//   - File bytes CF FA ED FE -> LE reads 0xFEEDFACF -> MH_MAGIC_64 (LE 64-bit)
//   - File bytes FE ED FA CF -> LE reads 0xCFFAEDFE -> MH_CIGAM_64 (BE 64-bit)
//   - File bytes CE FA ED FE -> LE reads 0xFEEDFACE -> MH_MAGIC    (LE 32-bit)
//   - File bytes FE ED FA CE -> LE reads 0xCEFAEDFE -> MH_CIGAM    (BE 32-bit)
func detectMachOFormat(magic []byte) (bo binary.ByteOrder, headerSize int64, ok bool) {
	switch binary.LittleEndian.Uint32(magic) {
	case mhMagic64:
		return binary.LittleEndian, machOHeaderSize64, true
	case mhCigam64:
		return binary.BigEndian, machOHeaderSize64, true
	case mhMagic:
		return binary.LittleEndian, machOHeaderSize32, true
	case mhCigam:
		return binary.BigEndian, machOHeaderSize32, true
	}
	return nil, 0, false
}

// extractDylibName reads the NUL-terminated dylib name from a dylib load
// command. The name offset is at byte 8 within the command (the first
// field of the embedded lc_dylib struct) and is relative to the start
// of the load command.
func extractDylibName(cmd []byte, bo binary.ByteOrder) string {
	if len(cmd) < dylibCommandMinSize {
		return ""
	}
	nameOff := int(bo.Uint32(cmd[8:12]))
	if nameOff >= len(cmd) {
		return ""
	}
	// Walk forward to NUL within the command bounds.
	for end := nameOff; end < len(cmd); end++ {
		if cmd[end] == 0 {
			return string(cmd[nameOff:end])
		}
	}
	// No NUL terminator found - return what we have. Some toolchains
	// may omit NUL if the name fills cmdsize exactly (non-standard but
	// we handle it gracefully rather than panicking).
	return string(cmd[nameOff:])
}

// isDylibLoadCommand returns true for Mach-O load commands that use the
// dylib_command layout (a NUL-terminated dylib path at a name offset).
// These are the commands whose presence we care about for WARN-BROWSER-005.
func isDylibLoadCommand(cmd uint32) bool {
	switch cmd {
	case lcLoadDylib, lcLoadWeakDylib, lcReexportDylib, lcLazyLoadDylib, lcLoadUpwardDylib:
		return true
	}
	return false
}

// readAtChunk reads exactly n bytes from r at offset off via ReadAt.
// Returns an error on any short read (truncated file). Mirrors the ELF
// parser's readChunked but works on io.ReaderAt instead of *os.File so
// it is testable with bytes.Reader on Linux.
func readAtChunk(r io.ReaderAt, off int64, n int) ([]byte, error) {
	if n <= 0 {
		return nil, nil
	}
	buf := make([]byte, n)
	rd, err := r.ReadAt(buf, off)
	if rd == n {
		return buf, nil
	}
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	return nil, fmt.Errorf("truncated read at offset %d: wanted %d bytes, got %d: %w", off, n, rd, err)
}
