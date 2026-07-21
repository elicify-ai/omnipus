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
//
// Streaming (PERF-002): the previous implementation read the full binary
// into the Go heap via os.ReadFile. Chrome's binary is ~400 MB and a
// 256–512 MB cgroup kills the process during materialization. This
// version uses os.Open + chunked Seek/Read for the program-header table
// and PT_DYNAMIC region only — the only bytes we actually need.
package doctor

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ehdrSize64 is the 64-bit ELF header size: e_ident[16] + 12 fields (u16
// u16 u32 u64 u64 u64 u32 u16 u16 u16 u16 u16 u16) = 64 bytes total.
// CORR-002 fix: the prior constant was 52, which let any 53–57 byte file
// pass the bounds check while data[54:56] / data[56:58] panicked.
const ehdrSize64 = 64

// phdrSize64 is the 64-bit program-header entry size.
const phdrSize64 = 56

// dynEntrySize64 is the 64-bit DT_DYNAMIC entry size.
const dynEntrySize64 = 16

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

// maxELFOffset bounds the offsets we'll accept when walking an ELF — a
// 64-bit binary on a clean 400 MB chrome install tops out well below
// 4 GiB. Anything past 1 GiB is implausible and we treat it as a
// structural error rather than risk the seek+read.
const maxELFOffset uint64 = 1 << 30 // 1 GiB

// missingChromeLibsELF walks binPath's ELF DT_NEEDED entries and returns
// the soname of every dependency that is NOT present in any of the
// canonical search paths. An empty result means every DT_NEEDED entry
// resolved. A non-ELF file returns a single synthetic entry ("not-an-elf")
// so the warning surfaces the diagnostic instead of silently passing.
//
// The parser is intentionally conservative: any structural error
// (truncated header, bad magic, overflow) returns a non-nil error with
// a descriptive message so the caller can decide whether to surface a
// warning or stay silent.
func missingChromeLibsELF(binPath string) ([]string, error) {
	f, err := os.Open(binPath)
	if err != nil {
		return nil, fmt.Errorf("read chrome binary: %w", err)
	}
	defer f.Close()

	// Read just the first 4 bytes for the magic. io.ReadFull returns
	// ErrUnexpectedEOF for a 1-3 byte file and EOF for a 0 byte file;
	// we treat both as "not an ELF" so a missing or zero-length binary
	// surfaces a clean diagnostic rather than crashing.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek to ELF magic: %w", err)
	}
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return []string{"not-an-elf-binary"}, nil
		}
		return nil, fmt.Errorf("read chrome binary magic: %w", err)
	}
	if !bytes.Equal(magic[:], []byte{0x7f, 'E', 'L', 'F'}) {
		// Non-ELF (script, wrong-arch, partial download): surface a
		// single synthetic entry so the operator sees the diagnostic.
		return []string{"not-an-elf-binary"}, nil
	}

	// Identify the 64-bit ELF class from EI_CLASS at offset 4 and the
	// byte order from EI_DATA at offset 5.
	var bo binary.ByteOrder
	ehdrSz := ehdrSize64
	phdrSz := uint64(phdrSize64)
	dynEntry := uint64(dynEntrySize64)
	var class [1]byte
	if _, err := f.Seek(4, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek to EI_CLASS: %w", err)
	}
	if _, err := io.ReadFull(f, class[:]); err != nil {
		return nil, fmt.Errorf("read EI_CLASS: %w", err)
	}
	if class[0] != 2 {
		// Chrome-for-Testing is 64-bit on every supported platform.
		return []string{"not-an-elf-binary"}, nil
	}
	var dataByte [1]byte
	if _, err := f.Seek(5, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek to EI_DATA: %w", err)
	}
	if _, err := io.ReadFull(f, dataByte[:]); err != nil {
		return nil, fmt.Errorf("read EI_DATA: %w", err)
	}
	if dataByte[0] == 1 {
		bo = binary.LittleEndian
	} else {
		bo = binary.BigEndian
	}

	// A deliberately truncated 64-bit binary MUST be rejected cleanly.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek to ELF header: %w", err)
	}
	ehdr := make([]byte, ehdrSz)
	if _, err := io.ReadFull(f, ehdr); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("truncated ELF header: read %d bytes", len(ehdr))
		}
		return nil, fmt.Errorf("read ELF header: %w", err)
	}

	// 64-bit ELF header field offsets: e_phoff @ 32, e_phentsz @ 54,
	// and e_phnum @ 56.
	var ePhOff, ePhEntSize, ePhNum uint64
	ePhOff = bo.Uint64(ehdr[32:40])
	ePhEntSize = uint64(bo.Uint16(ehdr[54:56]))
	ePhNum = uint64(bo.Uint16(ehdr[56:58]))
	if ePhEntSize != 0 && ePhEntSize != phdrSz {
		// Some toolchains emit e_phentsize == 0 in stripped
		// binaries; treat that as "use the canonical size" but
		// reject anything else (mismatched sizes are a sign of a
		// toolchain we don't understand).
		return nil, fmt.Errorf("unexpected program-header entry size: %d", ePhEntSize)
	}
	if ePhNum == 0 {
		return nil, nil // static binary, nothing to check
	}
	if ePhOff > maxELFOffset || ePhNum*phdrSz > maxELFOffset || ePhOff+ePhNum*phdrSz > maxELFOffset {
		return nil, fmt.Errorf("program header table out of range (e_phoff=%d e_phnum=%d)", ePhOff, ePhNum)
	}

	// Walk program headers for PT_DYNAMIC.
	var dynOff, dynSize uint64
	for i := uint64(0); i < ePhNum; i++ {
		off := ePhOff + i*phdrSz
		phdr, err := readChunked(f, off, phdrSz)
		if err != nil {
			// EOF mid-table: a structural error — surface it.
			return nil, fmt.Errorf("read program header %d: %w", i, err)
		}
		pType := bo.Uint32(phdr[0:4])
		if pType != 2 /* PT_DYNAMIC */ {
			continue
		}
		// 64-bit: p_offset=24, p_filesz=40 within the program header.
		dynOff = bo.Uint64(phdr[24:32])
		dynSize = bo.Uint64(phdr[40:48])
		break
	}
	if dynSize == 0 {
		return nil, nil // static binary, nothing to check
	}
	if dynOff > maxELFOffset || dynSize > maxELFOffset || dynOff+dynSize > maxELFOffset {
		return nil, fmt.Errorf("PT_DYNAMIC segment out of range (off=%d size=%d)", dynOff, dynSize)
	}

	// Inside PT_DYNAMIC — we need DT_NEEDED (1) and DT_STRTAB (5) +
	// DT_STRSZ (10). Walk the entries via chunked reads (no full
	// slurp).
	dynCount := dynSize / dynEntry
	var strtabOff, strtabSize uint64
	var needed []uint64
	for i := uint64(0); i < dynCount; i++ {
		base := dynOff + i*dynEntry
		entry, err := readChunked(f, base, dynEntry)
		if err != nil {
			return nil, fmt.Errorf("read DT_DYNAMIC entry %d: %w", i, err)
		}
		var tag, val uint64
		tag = bo.Uint64(entry[0:8])
		val = bo.Uint64(entry[8:16])
		switch tag {
		case 1: // DT_NEEDED
			needed = append(needed, val)
		case 5: // DT_STRTAB
			strtabOff = val
		case 10: // DT_STRSZ
			strtabSize = val
		}
	}
	if strtabOff == 0 || strtabSize == 0 ||
		strtabOff > maxELFOffset || strtabSize > maxELFOffset ||
		strtabOff+strtabSize > maxELFOffset {
		return nil, nil
	}

	// Resolve each DT_NEEDED offset to its soname and probe the search
	// paths. The string-table is typically a few KB; reading it whole
	// (capped at strtabSize) is safe — much smaller than the binary.
	if strtabSize > 16*1024*1024 {
		// 16 MiB string table is implausibly large for any sane
		// binary — refuse rather than slurp.
		return nil, fmt.Errorf("DT_STRTAB unreasonably large: %d bytes", strtabSize)
	}
	strtab, err := readChunked(f, strtabOff, strtabSize)
	if err != nil {
		return nil, fmt.Errorf("read DT_STRTAB: %w", err)
	}

	var missing []string
	for _, n := range needed {
		end := n
		if end >= strtabSize {
			continue
		}
		// Walk forward to NUL.
		z := end
		for z < strtabSize && strtab[z] != 0 {
			z++
		}
		soname := string(strtab[end:z])
		if !sonameExists(soname) {
			missing = append(missing, soname)
		}
	}
	return missing, nil
}

// readChunked reads exactly n bytes from f starting at offset, via a
// bounded Seek + ReadFull. The bounded size means the OS page cache is
// the only allocation the binary's contents hit — never the Go heap —
// which is the whole point of the streaming refactor (PERF-002): the
// pre-refactor os.ReadFile materialized the entire binary into the Go
// heap, OOM-killing the gateway in 256–512 MB cgroups.
func readChunked(f *os.File, offset, n uint64) ([]byte, error) {
	if n == 0 {
		return nil, nil
	}
	if _, err := f.Seek(int64(offset), io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek to %d: %w", offset, err)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(f, buf); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, fmt.Errorf("truncated read at offset %d: wanted %d bytes", offset, n)
		}
		return nil, err
	}
	return buf, nil
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
