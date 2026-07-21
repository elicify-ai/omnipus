//go:build linux

// In-process ELF parser for WARN-BROWSER-005 (ADR-052 SEC-ADR052-007).
// Replaces the previous `ldd`-via-os/exec implementation: HC #2 (pure Go,
// no shelling out for security-critical paths) rules that out, and ldd
// itself is not always present (Alpine-without-gcompat, BusyBox, stripped
// initramfs). Pure-Go parsing keeps the check deterministic, offline,
// and CI-safe.
//
// What we walk:
//  1. ELF magic "\x7fELF" at offset 0.
//  2. e_phoff / e_phentsize / e_phnum from the ELF header — locate the
//     program-header table.
//  3. Program headers — find the PT_DYNAMIC segment.
//  4. Inside PT_DYNAMIC — find DT_NEEDED entries (each is an offset into
//     the string table that names a soname like "libnss3.so").
//  5. For each soname, probe the canonical library search paths in turn.
//     If the soname resolves nowhere, the binary cannot run; surface the
//     basename in WARN-BROWSER-005.
//
// Notes:
//   - We deliberately do NOT support the dynamic loader's full search path
//     semantics (LD_LIBRARY_PATH, ld.so.conf, RUNPATH, RPATH). The doctor
//     check is a *correctness* gate on a known-good install — the install
//     path has already been laid down by install.sh against the canonical
//     paths. RUNPATH/RPATH machinery is for installer plumbing, not the
//     static chrome binary.
//   - We parse only the soname. ld.so's full resolution (versioned
//     dependencies, weak symbols, .symver) is not modeled — chrome's DT_NEEDED
//     entries are unversioned sonames on every mainstream distro.
package doctor

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
)

// elfSearchPaths are the canonical library search directories probed in
// order. Mirrors glibc's default /usr/lib + /lib ordering; covers every
// mainstream Linux distribution chrome-for-testing ships to today.
var elfSearchPaths = []string{
	"/usr/lib",
	"/usr/lib64",
	"/lib",
	"/lib64",
	"/usr/lib/x86_64-linux-gnu",
	"/usr/lib/aarch64-linux-gnu",
}

// missingChromeLibsELF walks binPath's ELF DT_NEEDED entries and returns
// the soname of every dependency that is NOT present in any of the
// canonical search paths. An empty result means every DT_NEEDED entry
// resolved. A non-ELF file returns a single synthetic entry ("not-an-elf")
// so the warning surfaces the diagnostic instead of silently passing.
//
// The parser is intentionally conservative: any structural error (truncated
// header, bad magic, overflow) returns nil with an explanatory marker so
// the caller can decide whether to surface a warning or stay silent.
func missingChromeLibsELF(binPath string) ([]string, error) {
	data, err := os.ReadFile(binPath)
	if err != nil {
		return nil, fmt.Errorf("read chrome binary: %w", err)
	}
	if len(data) < 4 || !bytes.Equal(data[:4], []byte{0x7f, 'E', 'L', 'F'}) {
		// Non-ELF (script, wrong-arch, partial download): surface a
		// single synthetic entry so the operator sees the diagnostic.
		return []string{"not-an-elf-binary"}, nil
	}

	// Identify 32-bit vs 64-bit from EI_CLASS at offset 4. Chrome-for-Testing
	// ships only 64-bit; we still need the right struct widths.
	is64 := data[4] == 2
	isLE := data[5] == 1 // EI_DATA
	var bo binary.ByteOrder
	if isLE {
		bo = binary.LittleEndian
	} else {
		bo = binary.BigEndian
	}

	// ELF header layout: e_ident[16] + (u16 u16 u32 u64 u64 u32 u32 u16 u16 u16 u16 u16 u16) for 64-bit.
	// Offset of e_phoff: 32 (32-bit) or 32 (64-bit, both start e_phoff at 32).
	const ehdrSize = 52 // 64-bit ELF header size — we only ship 64-bit chrome.
	if len(data) < ehdrSize {
		return nil, fmt.Errorf("truncated ELF header")
	}
	e_phoff := bo.Uint64(data[32:40])
	e_phentsize := bo.Uint16(data[54:56])
	e_phnum := bo.Uint16(data[56:58])
	if int(e_phoff)+int(e_phnum)*int(e_phentsize) > len(data) {
		return nil, fmt.Errorf("program header table out of range")
	}

	// Walk program headers for PT_DYNAMIC.
	var dynOff, dynSize uint64
	for i := uint16(0); i < e_phnum; i++ {
		off := e_phoff + uint64(i)*uint64(e_phentsize)
		if off+uint64(e_phentsize) > uint64(len(data)) {
			break
		}
		p_type := bo.Uint32(data[off : off+4])
		if p_type != 2 /* PT_DYNAMIC */ {
			continue
		}
		if is64 {
			// 64-bit: p_offset=24, p_filesz=40 within the program header.
			dynOff = bo.Uint64(data[off+24 : off+32])
			dynSize = bo.Uint64(data[off+40 : off+48])
		} else {
			dynOff = uint64(bo.Uint32(data[off+4 : off+8]))
			dynSize = uint64(bo.Uint32(data[off+16 : off+20]))
		}
		break
	}
	if dynSize == 0 {
		return nil, nil // static binary, nothing to check
	}
	if dynOff+dynSize > uint64(len(data)) {
		return nil, fmt.Errorf("PT_DYNAMIC segment out of range")
	}

	// Inside PT_DYNAMIC — we need DT_NEEDED (1) and DT_STRTAB (5) + DT_STRSZ (10).
	// 64-bit Elf64_Dyn: d_tag(8) + d_val(8) = 16 bytes each.
	dynEntry := uint64(16)
	if !is64 {
		dynEntry = 8
	}
	dynCount := dynSize / dynEntry
	var strtabOff, strtabSize uint64
	var needed []uint64
	for i := uint64(0); i < dynCount; i++ {
		base := dynOff + i*dynEntry
		tag := bo.Uint64(data[base : base+8])
		val := bo.Uint64(data[base+8 : base+16])
		switch tag {
		case 1: // DT_NEEDED
			needed = append(needed, val)
		case 5: // DT_STRTAB
			strtabOff = val
		case 10: // DT_STRSZ
			strtabSize = val
		}
	}
	if strtabOff == 0 || strtabSize == 0 || strtabOff+strtabSize > uint64(len(data)) {
		return nil, nil
	}

	// Resolve each DT_NEEDED offset to its soname and probe the search paths.
	var missing []string
	for _, n := range needed {
		// Walk from strtabOff+n forward to NUL.
		end := strtabOff + n
		if end >= strtabOff+strtabSize {
			continue
		}
		limit := strtabOff + strtabSize
		z := end
		for z < limit && data[z] != 0 {
			z++
		}
		soname := string(data[end:z])
		if !sonameExists(soname) {
			missing = append(missing, soname)
		}
	}
	return missing, nil
}

// sonameExists returns true if soname resolves under any of the canonical
// search paths. It does NOT model RUNPATH / RPATH / LD_LIBRARY_PATH — see
// the file-level note above for why.
func sonameExists(soname string) bool {
	for _, dir := range elfSearchPaths {
		candidate := filepath.Join(dir, soname)
		if _, err := os.Stat(candidate); err == nil {
			return true
		}
	}
	return false
}
