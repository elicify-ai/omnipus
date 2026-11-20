package omnpkg_test

// TestArchive_DecoderLimits oracles are FR-008: zstd window 8 MiB accepted and
// 9 MiB refused; decoded stream 256 MiB accepted and 256 MiB + 1 refused,
// including bytes after the tar ends; the writer (Export) declares a window
// of at most 8 MiB. D1 rows 13 and 26.

import (
	"archive/tar"
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

// zstdWindowFrame builds a valid zstd frame whose header declares the window
// 2^(10+exp) * (1 + mantissa/8) and whose payload is one raw block.
// exp 19 mantissa 0 is 512 MiB. exp 13 mantissa 0 is 8 MiB. exp 13 mantissa 1
// is 9 MiB. The layout is the one D1-26 already uses.
func zstdWindowFrame(t *testing.T, exp, mantissa int, payload []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	out.Write([]byte{0x28, 0xB5, 0x2F, 0xFD})
	out.WriteByte(0x00)
	out.WriteByte(byte((exp << 3) | mantissa))
	hdr := uint32(1) | uint32(len(payload))<<3
	out.Write([]byte{byte(hdr), byte(hdr >> 8), byte(hdr >> 16)})
	out.Write(payload)
	return out.Bytes()
}

func TestArchive_DecoderLimits(t *testing.T) {
	t.Run("D1-26 512 MiB window frame refused at inspect", func(t *testing.T) {
		pkg := zstdWindowFrame(t, 19, 0, bytes.Repeat([]byte("A"), 2048))
		_, err := omnpkg.Inspect(bytes.NewReader(pkg))
		wantCode(t, err, "package_too_large")
	})
	t.Run("D1-26 512 MiB window frame refused at verify", func(t *testing.T) {
		pkg := zstdWindowFrame(t, 19, 0, bytes.Repeat([]byte("A"), 2048))
		_, err := omnpkg.Verify(bytes.NewReader(pkg))
		wantCode(t, err, "package_too_large")
	})
	t.Run("D1-26 512 MiB window frame refused at upload reader", func(t *testing.T) {
		pkg := zstdWindowFrame(t, 19, 0, bytes.Repeat([]byte("A"), 2048))
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, "package_too_large")
	})
	t.Run("D1-26 refusal names details.limit window", func(t *testing.T) {
		pkg := zstdWindowFrame(t, 19, 0, bytes.Repeat([]byte("A"), 2048))
		_, err := omnpkg.Inspect(bytes.NewReader(pkg))
		require.Equal(t, "window", limitDetail(t, err))
	})

	// An 8 MiB window is the spec maximum, so the frame decodes and then
	// fails as a package (the payload is not a tar). 9 MiB is over the maximum.
	t.Run("8 MiB window frame is within the decoder limit", func(t *testing.T) {
		pkg := zstdWindowFrame(t, 13, 0, bytes.Repeat([]byte("A"), 2048))
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, "not_a_package")
	})
	t.Run("9 MiB window frame refused as too large", func(t *testing.T) {
		pkg := zstdWindowFrame(t, 13, 1, bytes.Repeat([]byte("A"), 2048))
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, "package_too_large")
		require.Equal(t, "window", limitDetail(t, err))
	})

	t.Run("decoded stream of exactly 256 MiB is not too large", func(t *testing.T) {
		pkg := packageOfDecodedLen(t, specDecodedBytes)
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, "manifest_mismatch")
	})
	t.Run("D1-13 decoded stream over 256 MiB refused as too large", func(t *testing.T) {
		// A tar block is 512 bytes, so the smallest in-tar step past 256 MiB
		// is one block. The byte-exact +1 case is the trailing-byte subtest.
		pkg := packageOfDecodedLen(t, specDecodedBytes+512)
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, "package_too_large")
		require.Equal(t, "decoded", limitDetail(t, err))
	})
	t.Run("FR-008 one decoded byte after the tar still counts", func(t *testing.T) {
		raw := decodedTar(t, specDecodedBytes)
		raw = append(raw, 0x01)
		require.Equal(t, specDecodedBytes+1, len(raw), "decoded length is 256 MiB + 1")
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(zstdWrap(t, raw)))
		wantCode(t, err, "package_too_large")
		require.Equal(t, "decoded", limitDetail(t, err))
	})

	t.Run("FR-008 Export declares a window of at most 8 MiB", func(t *testing.T) {
		src := newFakeSource()
		src.withAgent(t, testSourceID, baseRecord(testSourceID), []byte(testSoulBody), nil)
		pkg := exportOne(t, src, testSourceID)
		require.LessOrEqual(t, declaredWindow(t, pkg), specWindowBytes,
			"the writer's zstd window must be at most 8 MiB (FR-008)")
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		require.NoError(t, err, "a package Export wrote must decode under the FR-008 window")
	})
}

func limitDetail(t *testing.T, err error) string {
	t.Helper()
	require.Error(t, err)
	var pe *omnpkg.PackageError
	require.ErrorAs(t, err, &pe)
	return pe.Detail["limit"]
}

func packageOfDecodedLen(t *testing.T, n int) []byte {
	t.Helper()
	return zstdWrap(t, decodedTar(t, n))
}

// decodedTar builds a tar whose on-the-wire length is exactly n. n must be a
// multiple of 512. The bomb member is unlisted, so a reader that accepts the
// size still refuses the listing.
func decodedTar(t *testing.T, n int) []byte {
	t.Helper()
	require.Zero(t, n%512, "a tar length is a multiple of 512")
	m, files := validManifest(testSourceID, false)
	entries := make([]tarEntry, 0, 1+len(files))
	entries = append(entries, manifestEntry(t, m, nil, nil, 0))
	for _, name := range sortedKeys(files) {
		entries = append(entries, tarEntry{Name: name, Body: files[name]})
	}
	base := rawTar(t, entries)
	// One more header (512) sits before the trailer. The bomb body fills the rest.
	need := n - (len(base) + 512)
	require.Greater(t, need, 0)
	require.Zero(t, need%512)
	entries = append(entries, tarEntry{Name: "bomb.bin", Body: bytes.Repeat([]byte{0}, need)})
	raw := rawTar(t, entries)
	require.Equal(t, n, len(raw), "decoded tar must be the spec length")
	return raw
}

func rawTar(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.Name,
			Size:     int64(len(e.Body)),
			Typeflag: e.Typeflag,
			Mode:     e.Mode,
			ModTime:  time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC),
		}
		require.NoError(t, tw.WriteHeader(hdr))
		if len(e.Body) > 0 {
			_, err := tw.Write(e.Body)
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	return raw.Bytes()
}

func zstdWrap(t *testing.T, raw []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	zw, err := zstd.NewWriter(&out)
	require.NoError(t, err)
	_, err = io.Copy(zw, bytes.NewReader(raw))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return out.Bytes()
}

// declaredWindow reads the zstd frame the writer actually emitted.
// A single-segment frame's window is its content size. Otherwise the window
// descriptor is the byte after the frame header descriptor (RFC 8878):
// 2^(10+exp) * (1 + mantissa/8).
func declaredWindow(t *testing.T, pkg []byte) int {
	t.Helper()
	require.GreaterOrEqual(t, len(pkg), 6)
	require.Equal(t, []byte{0x28, 0xB5, 0x2F, 0xFD}, pkg[:4], "export must be a zstd frame")
	fhd := pkg[4]
	if fhd&0x20 != 0 {
		return frameContentSize(t, pkg)
	}
	wd := pkg[5]
	exp := int(wd >> 3)
	man := int(wd & 7)
	base := 1 << (10 + exp)
	return base + (base/8)*man
}

func frameContentSize(t *testing.T, pkg []byte) int {
	t.Helper()
	fhd := pkg[4]
	fcs := int(fhd >> 6)
	// Dictionary id, if any, sits before the content size. Export sets none.
	require.Zero(t, fhd&0x03, "export frame must not carry a dictionary id")
	off := 5
	if fhd&0x20 == 0 {
		off = 6 // window descriptor present
	}
	switch fcs {
	case 0:
		require.NotZero(t, fhd&0x20, "a missing content size is not a single-segment window")
		return int(pkg[off])
	case 1:
		v := int(pkg[off]) | int(pkg[off+1])<<8
		return v + 256
	case 2:
		return int(pkg[off]) | int(pkg[off+1])<<8 | int(pkg[off+2])<<16 | int(pkg[off+3])<<24
	default:
		t.Fatalf("export frame content size field is 8 bytes, which is over 8 MiB")
		return 0
	}
}

// oversizedWindowFrame is the D1-26 512 MiB window fixture other tests call.
func oversizedWindowFrame(t *testing.T, payload []byte) []byte {
	t.Helper()
	return zstdWindowFrame(t, 19, 0, payload)
}
