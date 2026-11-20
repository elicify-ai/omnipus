package omnpkg_test

// TestArchive_SparseAndDeclaredSize covers S1a security finding F1 (#1094):
// FR-008's limits hold for what an entry DECLARES and for what is READ, and a
// sparse entry is refused before any byte of it is read. archive/tar expands
// a sparse entry's holes into zero bytes that never pass through the decoded
// stream, so the 256 MiB decoded-stream counter cannot see them: a 512-byte
// map can claim gigabytes. FR-003 allows only regular files and directories;
// a sparse file is neither, whatever typeflag carries it.
//
// The fixtures are hand-built tar blocks (header fields patched, checksum
// recomputed) because archive/tar's writer neither writes sparse files nor
// lets a header declare more bytes than it is given.

import (
	"archive/tar"
	"bytes"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
)

func TestArchive_SparseAndDeclaredSize(t *testing.T) {
	t.Run("PAX 1.0 sparse agent file within the size cap is refused as unsafe_entry_type", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		const realSize = 10 // ten zero bytes, all hole
		m.Files[soulPath(testSourceID)] = omnpkg.FileInfo{SHA256: sha256hex(make([]byte, realSize)), Size: realSize}
		pkg := rawPackage(t, m, files, map[string][]byte{soulPath(testSourceID): paxSparse(t, soulPath(testSourceID), realSize)})
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, "unsafe_entry_type")
	})

	t.Run("PAX 1.0 sparse unlisted entry claiming 512 MiB is refused as unsafe_entry_type", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		name := "omnipus/extra.bin"
		pkg := rawPackage(t, m, files, map[string][]byte{name: paxSparse(t, name, 512*specMiB)})
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, "unsafe_entry_type")
	})

	t.Run("GNU sparse typeflag S is refused as unsafe_entry_type", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		name := "omnipus/extra.bin"
		blk := ustarBlock(t, name, make([]byte, 512))
		// Old GNU format: magic "ustar  \0", an empty sparse map and a
		// 512 MiB real size.
		copy(blk[257:265], "ustar  \x00")
		copy(blk[483:495], fmt.Sprintf("%011o\x00", 512*specMiB))
		patchHeader(blk, tar.TypeGNUSparse, -1)
		pkg := rawPackage(t, m, files, map[string][]byte{name: blk})
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, "unsafe_entry_type")
	})

	t.Run("unlisted entry declaring 300 MiB with a 10-byte body is package_too_large decoded", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		name := "omnipus/extra.bin"
		blk := ustarBlock(t, name, bytes.Repeat([]byte("z"), 10))
		patchHeader(blk, 0, 300*specMiB)
		pkg := rawPackage(t, m, files, map[string][]byte{name: blk})
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, "package_too_large")
		require.Equal(t, "decoded", limitDetail(t, err))
	})

	t.Run("agent file declaring 2 MiB with a 10-byte body is package_too_large agent_file", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		blk := ustarBlock(t, soulPath(testSourceID), bytes.Repeat([]byte("z"), 10))
		patchHeader(blk, 0, 2*specMiB)
		pkg := rawPackage(t, m, files, map[string][]byte{soulPath(testSourceID): blk})
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, "package_too_large")
		require.Equal(t, "agent_file", limitDetail(t, err))
	})

	t.Run("a body longer than its declared size is refused as not_a_package", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		body := bytes.Repeat([]byte("q"), 1500)
		const declared = 5
		m.Files[soulPath(testSourceID)] = omnpkg.FileInfo{SHA256: sha256hex(body[:declared]), Size: declared}
		blk := ustarBlock(t, soulPath(testSourceID), body)
		patchHeader(blk, 0, declared)
		pkg := rawPackage(t, m, files, map[string][]byte{soulPath(testSourceID): blk})
		_, payload, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, "not_a_package")
		require.Nil(t, payload, "a refused package yields no payload")
	})
}

// rawPackage is buildPackage with some entries replaced by raw tar blocks.
// Entries are written in sorted order after the manifest; an entry in raw
// replaces (or adds) that path's blocks verbatim.
func rawPackage(t *testing.T, m *omnpkg.Manifest, files map[string][]byte, raw map[string][]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	me := manifestEntry(t, m, nil, nil, 0)
	out.Write(ustarBlock(t, me.Name, me.Body))
	all := map[string][]byte{}
	for k, v := range files {
		all[k] = v
	}
	for k := range raw {
		all[k] = nil
	}
	for _, name := range sortedKeys(all) {
		if blk, ok := raw[name]; ok {
			out.Write(blk)
			continue
		}
		out.Write(ustarBlock(t, name, all[name]))
	}
	out.Write(make([]byte, 1024)) // end-of-archive marker
	var z bytes.Buffer
	zw, err := zstd.NewWriter(&z)
	require.NoError(t, err)
	_, err = zw.Write(out.Bytes())
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return z.Bytes()
}

// ustarBlock renders one plain USTAR regular-file entry (header block, body,
// padding) with no end-of-archive marker.
func ustarBlock(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: name, Size: int64(len(body)), Mode: 0o644, Typeflag: tar.TypeReg,
		ModTime: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC), Format: tar.FormatUSTAR,
	}))
	_, err := tw.Write(body)
	require.NoError(t, err)
	require.NoError(t, tw.Flush())
	return buf.Bytes()
}

// patchHeader rewrites the first header block's typeflag (0 keeps it) and
// size field (negative keeps it), then recomputes the header checksum.
func patchHeader(blk []byte, typeflag byte, size int64) {
	if typeflag != 0 {
		blk[156] = typeflag
	}
	if size >= 0 {
		copy(blk[124:136], fmt.Sprintf("%011o\x00", size))
	}
	copy(blk[148:156], "        ")
	sum := 0
	for _, b := range blk[:512] {
		sum += int(b)
	}
	copy(blk[148:156], fmt.Sprintf("%06o\x00 ", sum))
}

// paxSparse renders a PAX 1.0 sparse file: an 'x' extended header carrying
// the GNU.sparse records, then a regular-file header whose 512-byte body is
// the sparse map with one empty data region at the end, so all realSize
// bytes are hole.
func paxSparse(t *testing.T, name string, realSize int64) []byte {
	t.Helper()
	var recs bytes.Buffer
	for _, kv := range [][2]string{
		{"GNU.sparse.major", "1"},
		{"GNU.sparse.minor", "0"},
		{"GNU.sparse.name", name},
		{"GNU.sparse.realsize", strconv.FormatInt(realSize, 10)},
	} {
		recs.WriteString(paxRecord(kv[0], kv[1]))
	}
	x := ustarBlock(t, "PaxHeaders/sparse", recs.Bytes())
	patchHeader(x, tar.TypeXHeader, -1)
	sparseMap := make([]byte, 512)
	copy(sparseMap, fmt.Sprintf("1\n%d\n0\n", realSize))
	return append(x, ustarBlock(t, "GNUSparseFile/sparse", sparseMap)...)
}

// paxRecord is one "<len> <key>=<value>\n" record; len counts itself.
func paxRecord(k, v string) string {
	body := " " + k + "=" + v + "\n"
	n := len(body) + 1
	for len(strconv.Itoa(n))+len(body) != n {
		n++
	}
	return strconv.Itoa(n) + body
}
