// OS-agnostic unit tests for the Mach-O LC_LOAD_DYLIB walker
// (command_libs_macho.go). These compile and run on ALL platforms
// (including Linux) because the parser is pure byte arithmetic — no
// darwin-specific system calls. The darwin-specific path-resolution
// tests (os.Stat against /usr/lib etc.) live in command_libs_darwin_test.go.

package doctor

import (
	"encoding/binary"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Synthetic Mach-O fixture builders ---

// machOHeaderSize64 is the 64-bit Mach-O header size (mach_header_64).
const machOHeaderSize64Test = 32

// machOHeaderSize32 is the 32-bit Mach-O header size (mach_header).
const machOHeaderSize32Test = 28

// buildMinimalMachO64 constructs the smallest valid 64-bit little-endian
// Mach-O binary that the parser can walk, with a single LC_LOAD_DYLIB
// pointing at dylibName. Mirrors buildMinimalELF64's approach for the
// ELF parser tests.
//
// Layout (offsets):
//
//	[0..32)  mach_header_64
//	[32..)   LC_LOAD_DYLIB load command(s)
func buildMinimalMachO64(t *testing.T, dylibName string) []byte {
	t.Helper()
	return buildMachO64WithDylibs(t, []string{dylibName})
}

// buildMachO64WithDylibs constructs a 64-bit LE Mach-O with one
// LC_LOAD_DYLIB load command per dylib name. Each load command is
// 8-byte aligned (Mach-O 64-bit alignment requirement).
func buildMachO64WithDylibs(t *testing.T, dylibs []string) []byte {
	t.Helper()
	const headerSize = machOHeaderSize64Test

	// Build the load-command region first so we know sizeofcmds.
	var lcRegion []byte
	for _, name := range dylibs {
		lc := buildLoadDylibCommand(name, 8) // 8-byte alignment for 64-bit
		lcRegion = append(lcRegion, lc...)
	}

	totalSize := headerSize + len(lcRegion)
	buf := make([]byte, totalSize)

	// mach_header_64 fields.
	binary.LittleEndian.PutUint32(buf[0:4], 0xFEEDFACF)              // MH_MAGIC_64
	binary.LittleEndian.PutUint32(buf[4:8], 0x0100000C)              // CPU_TYPE_ARM64
	binary.LittleEndian.PutUint32(buf[8:12], 0)                      // CPU_SUBTYPE_ALL
	binary.LittleEndian.PutUint32(buf[12:16], 2)                     // MH_EXECUTE
	binary.LittleEndian.PutUint32(buf[16:20], uint32(len(dylibs)))   // ncmds
	binary.LittleEndian.PutUint32(buf[20:24], uint32(len(lcRegion))) // sizeofcmds
	binary.LittleEndian.PutUint32(buf[24:28], 0)                     // flags
	binary.LittleEndian.PutUint32(buf[28:32], 0)                     // reserved

	// Copy load commands after the header.
	copy(buf[headerSize:], lcRegion)

	return buf
}

// buildMinimalMachO32 constructs a 32-bit LE Mach-O with one
// LC_LOAD_DYLIB. Used to verify the parser handles 32-bit Mach-O.
func buildMinimalMachO32(t *testing.T, dylibName string) []byte {
	t.Helper()
	const headerSize = machOHeaderSize32Test

	lc := buildLoadDylibCommand(dylibName, 4) // 4-byte alignment for 32-bit

	totalSize := headerSize + len(lc)
	buf := make([]byte, totalSize)

	// mach_header fields (32-bit: no reserved field).
	binary.LittleEndian.PutUint32(buf[0:4], 0xFEEDFACE)        // MH_MAGIC
	binary.LittleEndian.PutUint32(buf[4:8], 0x0000000C)        // CPU_TYPE_ARM
	binary.LittleEndian.PutUint32(buf[8:12], 0)                // CPU_SUBTYPE_ALL
	binary.LittleEndian.PutUint32(buf[12:16], 2)               // MH_EXECUTE
	binary.LittleEndian.PutUint32(buf[16:20], 1)               // ncmds
	binary.LittleEndian.PutUint32(buf[20:24], uint32(len(lc))) // sizeofcmds
	binary.LittleEndian.PutUint32(buf[24:28], 0)               // flags

	copy(buf[headerSize:], lc)

	return buf
}

// buildMinimalMachO64BigEndian constructs a 64-bit big-endian Mach-O
// with one LC_LOAD_DYLIB. Used to verify the parser handles BE byte order.
func buildMinimalMachO64BigEndian(t *testing.T, dylibName string) []byte {
	t.Helper()
	const headerSize = machOHeaderSize64Test

	lcRaw := buildLoadDylibCommand(dylibName, 8)
	// Re-encode load command in big-endian.
	lcBE := make([]byte, len(lcRaw))
	// cmd (offset 0) + cmdsize (offset 4) + name_offset (offset 8)
	binary.BigEndian.PutUint32(lcBE[0:4], 0x0c) // LC_LOAD_DYLIB
	binary.BigEndian.PutUint32(lcBE[4:8], uint32(len(lcRaw)))
	binary.BigEndian.PutUint32(lcBE[8:12], 24) // name offset within command
	// Copy name string bytes (endianness-independent for ASCII).
	copy(lcBE[24:], []byte(dylibName))

	totalSize := headerSize + len(lcBE)
	buf := make([]byte, totalSize)

	// mach_header_64 in big-endian.
	binary.BigEndian.PutUint32(buf[0:4], 0xFEEDFACF)          // MH_MAGIC_64
	binary.BigEndian.PutUint32(buf[4:8], 0x0100000C)          // CPU_TYPE_ARM64
	binary.BigEndian.PutUint32(buf[8:12], 0)                  // cpusubtype
	binary.BigEndian.PutUint32(buf[12:16], 2)                 // MH_EXECUTE
	binary.BigEndian.PutUint32(buf[16:20], 1)                 // ncmds
	binary.BigEndian.PutUint32(buf[20:24], uint32(len(lcBE))) // sizeofcmds
	binary.BigEndian.PutUint32(buf[24:28], 0)                 // flags
	binary.BigEndian.PutUint32(buf[28:32], 0)                 // reserved

	copy(buf[headerSize:], lcBE)

	return buf
}

// buildLoadDylibCommand builds a single LC_LOAD_DYLIB load command in
// little-endian. The name string starts at offset 24 within the command
// (after cmd + cmdsize + name_offset + timestamp + current_version +
// compatibility_version). cmdsize is aligned to alignBytes.
func buildLoadDylibCommand(name string, alignBytes int) []byte {
	const nameOffset = 24 // within the load command
	nameBytes := []byte(name)
	rawSize := nameOffset + len(nameBytes) + 1 // +1 for NUL
	// Align cmdsize.
	cmdsize := rawSize
	if r := cmdsize % alignBytes; r != 0 {
		cmdsize += alignBytes - r
	}

	buf := make([]byte, cmdsize)
	binary.LittleEndian.PutUint32(buf[0:4], 0x0c)            // LC_LOAD_DYLIB
	binary.LittleEndian.PutUint32(buf[4:8], uint32(cmdsize)) // cmdsize
	binary.LittleEndian.PutUint32(buf[8:12], nameOffset)     // name offset
	binary.LittleEndian.PutUint32(buf[12:16], 0)             // timestamp
	binary.LittleEndian.PutUint32(buf[16:20], 0)             // current_version
	binary.LittleEndian.PutUint32(buf[20:24], 0)             // compatibility_version
	copy(buf[nameOffset:], nameBytes)
	// NUL + padding already zeroed by make.

	return buf
}

// --- Parser tests (OS-agnostic, run on Linux) ---

// TestParseMachODylibs_SingleDylib verifies the parser extracts a single
// LC_LOAD_DYLIB name from a valid 64-bit Mach-O.
func TestParseMachODylibs_SingleDylib(t *testing.T) {
	macho := buildMinimalMachO64(t, "/usr/lib/libSystem.B.dylib")
	dylibs, err := parseMachODylibs(bytesReaderAt(macho))
	require.NoError(t, err)
	assert.Equal(t, []string{"/usr/lib/libSystem.B.dylib"}, dylibs)
}

// TestParseMachODylibs_MultipleDylibs verifies the parser extracts all
// LC_LOAD_DYLIB names when multiple are present.
func TestParseMachODylibs_MultipleDylibs(t *testing.T) {
	macho := buildMachO64WithDylibs(t, []string{
		"/usr/lib/libSystem.B.dylib",
		"@rpath/Chromium Framework.framework/Libraries/libEGL.dylib",
		"/System/Library/Frameworks/Foundation.framework/Versions/C/Foundation",
	})
	dylibs, err := parseMachODylibs(bytesReaderAt(macho))
	require.NoError(t, err)
	require.Len(t, dylibs, 3)
	assert.Equal(t, "/usr/lib/libSystem.B.dylib", dylibs[0])
	assert.Equal(t, "@rpath/Chromium Framework.framework/Libraries/libEGL.dylib", dylibs[1])
	assert.Equal(t, "/System/Library/Frameworks/Foundation.framework/Versions/C/Foundation", dylibs[2])
}

// TestParseMachODylibs_RpathPreserved verifies that @rpath/ prefixed
// dylib names are preserved verbatim (the resolution is the caller's job).
func TestParseMachODylibs_RpathPreserved(t *testing.T) {
	rpathName := "@rpath/libalsabindynamic.0.dylib"
	macho := buildMinimalMachO64(t, rpathName)
	dylibs, err := parseMachODylibs(bytesReaderAt(macho))
	require.NoError(t, err)
	require.Len(t, dylibs, 1)
	assert.Equal(t, rpathName, dylibs[0])
}

// TestParseMachODylibs_NonMachO verifies that a non-Mach-O input returns
// the synthetic "not-a-macho-binary" entry (mirrors the ELF parser's
// "not-an-elf-binary" contract).
func TestParseMachODylibs_NonMachO(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "ELF magic (wrong format)",
			data: []byte{0x7f, 'E', 'L', 'F', 2, 1, 0, 0},
		},
		{
			name: "random bytes",
			data: []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01, 0x02, 0x03},
		},
		{
			name: "PE magic (wrong format)",
			data: []byte{'M', 'Z', 0x00, 0x00},
		},
		{
			name: "empty file",
			data: []byte{},
		},
		{
			name: "one byte",
			data: []byte{0x00},
		},
		{
			name: "three bytes (truncated magic)",
			data: []byte{0xCF, 0xFA, 0xED},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMachODylibs(bytesReaderAt(tc.data))
			require.NoError(t, err, "non-Mach-O should not return an error")
			assert.Equal(t, []string{notAMachOBinary}, got)
		})
	}
}

// TestParseMachODylibs_TruncatedHeader verifies that a valid Mach-O magic
// followed by a truncated header (less than 28/32 bytes) returns a clean
// error rather than panicking.
func TestParseMachODylibs_TruncatedHeader(t *testing.T) {
	// Magic bytes for MH_MAGIC_64 (LE) + partial header.
	truncated := []byte{0xCF, 0xFA, 0xED, 0xFE, 0x0C, 0x00, 0x00, 0x01}
	_, err := parseMachODylibs(bytesReaderAt(truncated))
	assert.Error(t, err, "truncated header should return an error")
}

// TestParseMachODylibs_32Bit verifies the parser handles 32-bit Mach-O
// (MH_MAGIC = 0xFEEDFACE). Chrome-for-Testing on macOS is 64-bit only,
// but the parser should handle 32-bit cleanly rather than panicking.
func TestParseMachODylibs_32Bit(t *testing.T) {
	macho := buildMinimalMachO32(t, "/usr/lib/libSystem.B.dylib")
	dylibs, err := parseMachODylibs(bytesReaderAt(macho))
	require.NoError(t, err)
	assert.Equal(t, []string{"/usr/lib/libSystem.B.dylib"}, dylibs)
}

// TestParseMachODylibs_BigEndian verifies the parser handles big-endian
// Mach-O (MH_CIGAM_64 magic when read as LE). While macOS on x86_64/arm64
// is always little-endian, the parser should handle BE for robustness.
func TestParseMachODylibs_BigEndian(t *testing.T) {
	macho := buildMinimalMachO64BigEndian(t, "/usr/lib/libSystem.B.dylib")
	dylibs, err := parseMachODylibs(bytesReaderAt(macho))
	require.NoError(t, err)
	assert.Equal(t, []string{"/usr/lib/libSystem.B.dylib"}, dylibs)
}

// TestParseMachODylibs_NoLoadCommands verifies that a Mach-O with ncmds=0
// returns nil (no dylibs, nothing to check) without error.
func TestParseMachODylibs_NoLoadCommands(t *testing.T) {
	const headerSize = machOHeaderSize64Test
	buf := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(buf[0:4], 0xFEEDFACF) // MH_MAGIC_64
	binary.LittleEndian.PutUint32(buf[4:8], 0x0100000C) // CPU_TYPE_ARM64
	binary.LittleEndian.PutUint32(buf[8:12], 0)
	binary.LittleEndian.PutUint32(buf[12:16], 2) // MH_EXECUTE
	binary.LittleEndian.PutUint32(buf[16:20], 0) // ncmds = 0
	binary.LittleEndian.PutUint32(buf[20:24], 0) // sizeofcmds = 0
	binary.LittleEndian.PutUint32(buf[24:28], 0)
	binary.LittleEndian.PutUint32(buf[28:32], 0)

	dylibs, err := parseMachODylibs(bytesReaderAt(buf))
	require.NoError(t, err)
	assert.Nil(t, dylibs)
}

// TestParseMachODylibs_TruncatedLoadCommand verifies that a Mach-O with
// a valid header but truncated load-command region returns a clean error.
func TestParseMachODylibs_TruncatedLoadCommand(t *testing.T) {
	const headerSize = machOHeaderSize64Test
	// Header claims ncmds=1, sizeofcmds=100, but only 8 bytes follow.
	buf := make([]byte, headerSize+8)
	binary.LittleEndian.PutUint32(buf[0:4], 0xFEEDFACF)
	binary.LittleEndian.PutUint32(buf[4:8], 0x0100000C)
	binary.LittleEndian.PutUint32(buf[8:12], 0)
	binary.LittleEndian.PutUint32(buf[12:16], 2)   // MH_EXECUTE
	binary.LittleEndian.PutUint32(buf[16:20], 1)   // ncmds = 1
	binary.LittleEndian.PutUint32(buf[20:24], 100) // sizeofcmds = 100 (but file is shorter)
	binary.LittleEndian.PutUint32(buf[24:28], 0)
	binary.LittleEndian.PutUint32(buf[28:32], 0)
	// 8 bytes of garbage load command (not enough for sizeofcmds=100).
	buf[headerSize] = 0x0c // LC_LOAD_DYLIB

	_, err := parseMachODylibs(bytesReaderAt(buf))
	assert.Error(t, err, "truncated load-command region should return an error")
}

// TestParseMachODylibs_BadCmdsize verifies that a load command with an
// implausible cmdsize (below minimum or past maxMachOOffset) returns a
// clean error rather than panicking.
func TestParseMachODylibs_BadCmdsize(t *testing.T) {
	const headerSize = machOHeaderSize64Test
	// Build a Mach-O with a load command whose cmdsize is too small (4).
	buf := make([]byte, headerSize+24)
	binary.LittleEndian.PutUint32(buf[0:4], 0xFEEDFACF)
	binary.LittleEndian.PutUint32(buf[4:8], 0x0100000C)
	binary.LittleEndian.PutUint32(buf[8:12], 0)
	binary.LittleEndian.PutUint32(buf[12:16], 2)  // MH_EXECUTE
	binary.LittleEndian.PutUint32(buf[16:20], 1)  // ncmds = 1
	binary.LittleEndian.PutUint32(buf[20:24], 24) // sizeofcmds
	binary.LittleEndian.PutUint32(buf[24:28], 0)
	binary.LittleEndian.PutUint32(buf[28:32], 0)
	// Load command with cmd=LC_LOAD_DYLIB but cmdsize=4 (too small).
	binary.LittleEndian.PutUint32(buf[headerSize:headerSize+4], 0x0c) // cmd
	binary.LittleEndian.PutUint32(buf[headerSize+4:headerSize+8], 4)  // cmdsize = 4 (< 8 minimum)

	_, err := parseMachODylibs(bytesReaderAt(buf))
	assert.Error(t, err)
}

// TestParseMachODylibs_PanicFree verifies that various malformed inputs
// do not cause panics. This is a regression guard — the parser must
// never crash, only return errors.
func TestParseMachODylibs_PanicFree(t *testing.T) {
	good := buildMinimalMachO64(t, "/usr/lib/libSystem.B.dylib")

	tests := []struct {
		name    string
		payload []byte
	}{
		{"empty", []byte{}},
		{"one byte", []byte{0xCF}},
		{"three bytes", []byte{0xCF, 0xFA, 0xED}},
		{"valid magic only", []byte{0xCF, 0xFA, 0xED, 0xFE}},
		{"ELF magic", []byte{0x7f, 'E', 'L', 'F'}},
		{"truncated header", good[:10]},
		{"full good binary", good},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("parseMachODylibs panicked on %q: %v", tc.name, r)
					}
				}()
				_, _ = parseMachODylibs(bytesReaderAt(tc.payload))
			}()
		})
	}
}

// TestParseMachODylibs_WeakDylib verifies that LC_LOAD_WEAK_DYLIB
// (0x80000018) is also recognized as a dylib load command.
func TestParseMachODylibs_WeakDylib(t *testing.T) {
	const headerSize = machOHeaderSize64Test
	const nameOffset = 24
	dylibName := "/usr/lib/liboptional.dylib"
	nameBytes := []byte(dylibName)
	rawSize := nameOffset + len(nameBytes) + 1
	cmdsize := rawSize
	if r := cmdsize % 8; r != 0 {
		cmdsize += 8 - r
	}

	buf := make([]byte, headerSize+cmdsize)
	binary.LittleEndian.PutUint32(buf[0:4], 0xFEEDFACF)
	binary.LittleEndian.PutUint32(buf[4:8], 0x0100000C)
	binary.LittleEndian.PutUint32(buf[8:12], 0)
	binary.LittleEndian.PutUint32(buf[12:16], 2)               // MH_EXECUTE
	binary.LittleEndian.PutUint32(buf[16:20], 1)               // ncmds
	binary.LittleEndian.PutUint32(buf[20:24], uint32(cmdsize)) // sizeofcmds
	binary.LittleEndian.PutUint32(buf[24:28], 0)
	binary.LittleEndian.PutUint32(buf[28:32], 0)

	// LC_LOAD_WEAK_DYLIB = 0x80000018
	binary.LittleEndian.PutUint32(buf[headerSize:headerSize+4], 0x80000018)
	binary.LittleEndian.PutUint32(buf[headerSize+4:headerSize+8], uint32(cmdsize))
	binary.LittleEndian.PutUint32(buf[headerSize+8:headerSize+12], nameOffset)
	copy(buf[headerSize+nameOffset:], nameBytes)

	dylibs, err := parseMachODylibs(bytesReaderAt(buf))
	require.NoError(t, err)
	require.Len(t, dylibs, 1)
	assert.Equal(t, dylibName, dylibs[0])
}

// TestParseMachODylibs_OnlyNonDylibCommands verifies that load commands
// that are NOT dylib commands (e.g., LC_SEGMENT_64) are skipped without
// error and yield no dylib names.
func TestParseMachODylibs_OnlyNonDylibCommands(t *testing.T) {
	const headerSize = machOHeaderSize64Test
	// LC_SEGMENT_64 = 0x19, cmdsize=72 (standard segment_command_64 size).
	const cmdsize = 72
	buf := make([]byte, headerSize+cmdsize)
	binary.LittleEndian.PutUint32(buf[0:4], 0xFEEDFACF)
	binary.LittleEndian.PutUint32(buf[4:8], 0x0100000C)
	binary.LittleEndian.PutUint32(buf[8:12], 0)
	binary.LittleEndian.PutUint32(buf[12:16], 2)               // MH_EXECUTE
	binary.LittleEndian.PutUint32(buf[16:20], 1)               // ncmds
	binary.LittleEndian.PutUint32(buf[20:24], uint32(cmdsize)) // sizeofcmds
	binary.LittleEndian.PutUint32(buf[24:28], 0)
	binary.LittleEndian.PutUint32(buf[28:32], 0)
	// LC_SEGMENT_64 command.
	binary.LittleEndian.PutUint32(buf[headerSize:headerSize+4], 0x19) // LC_SEGMENT_64
	binary.LittleEndian.PutUint32(buf[headerSize+4:headerSize+8], cmdsize)

	dylibs, err := parseMachODylibs(bytesReaderAt(buf))
	require.NoError(t, err)
	assert.Nil(t, dylibs, "non-dylib load commands should yield no dylibs")
}

// --- Helper: wrap a []byte in an io.ReaderAt ---

// bytesReaderAt wraps a []byte to implement io.ReaderAt for testing.
// bytes.Reader already implements io.ReaderAt, so this is a thin helper.
type bytesReaderAt []byte

func (b bytesReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(b)) {
		return 0, io.EOF
	}
	end := off + int64(len(p))
	if end > int64(len(b)) {
		n := copy(p, b[off:])
		return n, io.EOF
	}
	return copy(p, b[off:]), nil
}
