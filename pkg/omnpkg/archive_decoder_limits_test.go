package omnpkg_test

// RED T-2 — TestArchive_DecoderLimits (spec TDD plan order 4).
// Spec: FR-008 (zstd decoder max window 8 MiB, max memory 256 MiB,
// concurrency 1, for upload, verify and inspect; the whole decoded stream
// counted); D1 rows 13 (decoded stream 256 MiB + 1 → 413, reading stops at
// the limit) and 26 (a small file whose zstd frame asks for a 512 MiB window
// → 422 package_too_large, details.limit: window); BDD "Oversized decoder
// window refused offline".
//
// The oversized-window fixture is a hand-crafted zstd frame whose window
// descriptor declares 2^(10+19) = 512 MiB while carrying one 2 KB raw block —
// the standard encoder declares only the window it actually used (verified
// against klauspost/compress v1.20.2 while preparing this suite).

import (
	"bytes"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

// oversizedWindowFrame builds a valid zstd frame whose header declares a
// 512 MiB window and whose payload is one raw (uncompressed) 2 KiB block.
func oversizedWindowFrame(t *testing.T, payload []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	out.Write([]byte{0x28, 0xB5, 0x2F, 0xFD})  // zstd magic
	out.WriteByte(0x00)                        // frame header descriptor: no FCS, no single-segment, no checksum, no dict
	out.WriteByte(19 << 3)                     // window descriptor: exponent 19 → 2^29 = 512 MiB, mantissa 0
	hdr := uint32(1) | uint32(len(payload))<<3 // last_block=1, type=0 (raw), size=len(payload)
	out.Write([]byte{byte(hdr), byte(hdr >> 8), byte(hdr >> 16)})
	out.Write(payload)
	return out.Bytes()
}

func TestArchive_DecoderLimits(t *testing.T) {
	// D1 26 / BDD "Oversized decoder window refused offline": a small file
	// whose frame asks for a 512 MiB window is refused as too large WITHOUT
	// allocating the window. Code 422 with details.limit "window".
	t.Run("D1-26 512 MiB window frame refused at inspect", func(t *testing.T) {
		pkg := oversizedWindowFrame(t, bytes.Repeat([]byte("A"), 2048))
		_, err := omnpkg.Inspect(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodePackageTooLarge)
	})
	t.Run("D1-26 512 MiB window frame refused at verify", func(t *testing.T) {
		pkg := oversizedWindowFrame(t, bytes.Repeat([]byte("A"), 2048))
		_, err := omnpkg.Verify(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodePackageTooLarge)
	})
	t.Run("D1-26 512 MiB window frame refused at upload reader", func(t *testing.T) {
		pkg := oversizedWindowFrame(t, bytes.Repeat([]byte("A"), 2048))
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodePackageTooLarge)
	})
	t.Run("D1-26 refusal names details.limit window", func(t *testing.T) {
		pkg := oversizedWindowFrame(t, bytes.Repeat([]byte("A"), 2048))
		_, err := omnpkg.Inspect(bytes.NewReader(pkg))
		require.Error(t, err)
		require.Equal(t, "window", limitDetail(t, err), "details.limit must name the decoder window")
	})

	// D1 13: a decoded stream of 256 MiB + 1 byte (high-ratio bomb) is refused
	// with 413; the whole decoded stream is counted, headers included. The
	// bomb is one tar member of zeros beyond the limit inside an otherwise
	// valid package.
	t.Run("D1-13 decoded stream 256 MiB + 1 refused as too large", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		// The bomb content is unlisted: the entry-count/size limit fires while
		// decoding, before listing reconciliation (FR-008 decoder limits).
		bomb := bytes.Repeat([]byte{0}, omnpkg.MaxDecodedBytes+1-tarOverhead())
		entries := make([]tarEntry, 0, 1+len(files)+1)
		entries = append(entries, manifestEntry(t, m, nil, nil, 0))
		for _, name := range sortedKeys(files) {
			entries = append(entries, tarEntry{Name: name, Body: files[name]})
		}
		entries = append(entries, tarEntry{Name: "bomb.bin", Body: bomb})
		pkg := buildArchive(t, entries)
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodePackageTooLarge)
	})

	// FR-008 writer side: an exported package must stay decodable under the
	// reader limits (writer window ≤ 8 MiB). Asserted at GREEN through Export;
	// here the property is pinned on the reader contract.
	t.Run("FR-008 a normal package decodes under the 8 MiB window limit", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		pkg := buildPackage(t, m, files)
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		require.NoError(t, err, "a standard zstd frame within the limits must decode")
	})
}

// limitDetail extracts details.limit from a coded error.
func limitDetail(t *testing.T, err error) string {
	t.Helper()
	require.Error(t, err)
	var pe *omnpkg.PackageError
	require.ErrorAs(t, err, &pe)
	return pe.Detail["limit"]
}

// tarOverhead is the per-entry tar header + padding overhead the engine must
// count too (the decoded-stream limit covers headers and padding, FR-008).
// 512 bytes header + 511 bytes padding is the worst case; we budget 1 KiB.
func tarOverhead() int { return 1024 }
