package omnpkg_test

// RED T-2 — TestArchive_ManifestFirstAndUniversalChecks (spec TDD plan order 3).
// Spec: FR-001 (zstd tar, manifest first), FR-007 (universal checks on all
// entries: no absolute path, no "..", no backslash, no drive letter, regular
// files and folders only, no duplicate path; agent layout checks on
// agent-owned entries); Datasets D1 rows 17–19, D2 rows 1–10.

import (
	"archive/tar"
	"bytes"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

func TestArchive_ManifestFirstAndUniversalChecks(t *testing.T) {
	t.Run("FR-001 manifest.json is the first entry", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		pkg := buildPackage(t, m, files)
		dec := decodePackagePlain(t, pkg)
		require.NotEmpty(t, dec.Order)
		require.Equal(t, omnpkg.ManifestEntry, dec.Order[0], "FR-001: the first entry must be manifest.json")
	})

	// D1 17/18: wrong container or junk is not a package.
	t.Run("D1-17 gzip instead of zstd refused as not_a_package", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		pkg := gzipWrap(t, tarBytes(t, m, files))
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeNotAPackage)
	})
	t.Run("D1-18 random bytes refused as not_a_package", func(t *testing.T) {
		_, _, err := omnpkg.ReadPackage(bytes.NewReader([]byte("definitely not a package body, just text")))
		wantCode(t, err, omnpkg.CodeNotAPackage)
	})
	t.Run("D1-18 empty body refused as not_a_package", func(t *testing.T) {
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(nil))
		wantCode(t, err, omnpkg.CodeNotAPackage)
	})

	// D1 19: manifest not first → not_a_package.
	t.Run("D1-19 manifest second refused as not_a_package", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		entries := make([]tarEntry, 0, 4)
		entries = append(entries, tarEntry{Name: soulPath(testSourceID), Body: files[soulPath(testSourceID)]})
		entries = append(entries, manifestEntry(t, m, nil, nil, 0),
			tarEntry{Name: recordPath(testSourceID), Body: files[recordPath(testSourceID)]},
			tarEntry{Name: instructionsPath(testSourceID), Body: files[instructionsPath(testSourceID)]})
		pkg := buildArchive(t, entries)
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeNotAPackage)
	})

	// D2 1–4: unsafe paths are refused wherever they appear.
	t.Run("D2-1 absolute path refused as unsafe_path", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		m.Elements[0].Files = append(m.Elements[0].Files, "/etc/passwd")
		m.Files["/etc/passwd"] = omnpkg.FileInfo{SHA256: sha256hex([]byte("x")), Size: 1}
		pkg := pkgWithExtraFile(t, m, files, "/etc/passwd", []byte("x"))
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeUnsafePath)
	})

	t.Run("D2-2 traversal with .. refused as unsafe_path", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		evil := "omnipus/agents/" + testSourceID + "/../../../evil"
		m.Elements[0].Files = append(m.Elements[0].Files, evil)
		m.Files[evil] = omnpkg.FileInfo{SHA256: sha256hex([]byte("x")), Size: 1}
		pkg := pkgWithExtraFile(t, m, files, evil, []byte("x"))
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeUnsafePath)
	})

	t.Run("D2-3 backslash in path refused as unsafe_path", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		bad := `omnipus\agents\` + testSourceID + `\SOUL.md`
		m.Elements[0].Files = append(m.Elements[0].Files, bad)
		m.Files[bad] = omnpkg.FileInfo{SHA256: sha256hex([]byte("x")), Size: 1}
		pkg := pkgWithExtraFile(t, m, files, bad, []byte("x"))
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeUnsafePath)
	})

	t.Run("D2-4 drive letter path refused as unsafe_path", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		bad := "C:/x"
		m.Elements[0].Files = append(m.Elements[0].Files, bad)
		m.Files[bad] = omnpkg.FileInfo{SHA256: sha256hex([]byte("x")), Size: 1}
		pkg := pkgWithExtraFile(t, m, files, bad, []byte("x"))
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeUnsafePath)
	})

	// D2 5: an agent-owned path outside the agent layout is refused.
	t.Run("D2-5 agent-owned file outside the layout refused as unsafe_path", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		extra := "omnipus/agents/" + testSourceID + "/extra.txt"
		m.Elements[0].Files = append(m.Elements[0].Files, extra)
		m.Files[extra] = omnpkg.FileInfo{SHA256: sha256hex([]byte("x")), Size: 1}
		pkg := pkgWithExtraFile(t, m, files, extra, []byte("x"))
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeUnsafePath)
	})

	// D2 6–8: non-regular entry types are refused. These are raw tar entries
	// with unsafe typeflags, listed in the manifest.
	// Defect fixed: header-only types carry no body — archive/tar zeros their size and Write returns "write too long" before the engine runs (D2-6, D2-7, D2-8).
	t.Run("D2-6 symlink entry refused as unsafe_entry_type", func(t *testing.T) {
		pkg := pkgWithTypedEntry(t, tar.TypeSymlink, "creds.json")
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeUnsafeEntryType)
	})
	t.Run("D2-7 hard link entry refused as unsafe_entry_type", func(t *testing.T) {
		pkg := pkgWithTypedEntry(t, tar.TypeLink, soulPath(testSourceID))
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeUnsafeEntryType)
	})
	t.Run("D2-8 fifo and device entries refused as unsafe_entry_type", func(t *testing.T) {
		for _, tf := range []byte{tar.TypeFifo, tar.TypeChar, tar.TypeBlock} {
			pkg := pkgWithTypedEntry(t, tf, "")
			_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
			wantCode(t, err, omnpkg.CodeUnsafeEntryType)
		}
	})

	// D2 9: the same path twice is refused.
	t.Run("D2-9 duplicate path refused as unsafe_entry_type", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		entries := make([]tarEntry, 0, 5)
		entries = append(entries, manifestEntry(t, m, nil, nil, 0))
		for _, name := range []string{recordPath(testSourceID), soulPath(testSourceID), instructionsPath(testSourceID)} {
			entries = append(entries, tarEntry{Name: name, Body: files[name]})
		}
		entries = append(entries, tarEntry{Name: soulPath(testSourceID), Body: files[soulPath(testSourceID)]})
		pkg := buildArchive(t, entries)
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeUnsafeEntryType)
	})

	// D2 10: a non-ASCII source id is not a valid agent id.
	t.Run("D2-10 non-ASCII source id refused as invalid_element", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		id := "作家-1"
		m.Elements[0].SourceID = id
		np, ns := recordPath(id), soulPath(id)
		m.Elements[0].Files = []string{np, ns, instructionsPath(id)}
		delete(m.Files, recordPath(testSourceID))
		delete(m.Files, soulPath(testSourceID))
		delete(m.Files, instructionsPath(testSourceID))
		m.Files[np] = omnpkg.FileInfo{SHA256: sha256hex(files[recordPath(testSourceID)]), Size: int64(len(files[recordPath(testSourceID)]))}
		m.Files[ns] = omnpkg.FileInfo{SHA256: sha256hex(files[soulPath(testSourceID)]), Size: int64(len(files[soulPath(testSourceID)]))}
		m.Files[instructionsPath(id)] = omnpkg.FileInfo{SHA256: sha256hex(files[instructionsPath(testSourceID)]), Size: int64(len(files[instructionsPath(testSourceID)]))}
		payload := map[string][]byte{np: files[recordPath(testSourceID)], ns: files[soulPath(testSourceID)], instructionsPath(id): files[instructionsPath(testSourceID)]}
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(buildPackage(t, m, payload)))
		wantCode(t, err, omnpkg.CodeInvalidElement)
	})
}

// pkgWithExtraFile adds one more payload file (already listed in m).
func pkgWithExtraFile(t *testing.T, m *omnpkg.Manifest, payload map[string][]byte, name string, body []byte) []byte {
	t.Helper()
	all := map[string][]byte{}
	for k, v := range payload {
		all[k] = v
	}
	all[name] = body
	return buildPackage(t, m, all)
}

// pkgWithTypedEntry builds a valid one-agent package plus one raw tar entry
// with the given typeflag. When linkTarget is empty the entry is a plain
// special file; otherwise it carries that link target.
func pkgWithTypedEntry(t *testing.T, typeflag byte, linkTarget string) []byte {
	t.Helper()
	m, files := validManifest(testSourceID, true)
	bad := "omnipus/agents/" + testSourceID + "/linked"
	m.Elements[0].Files = append(m.Elements[0].Files, bad)
	// Listed so the reader reaches the type check. No bytes: types '1'-'6' are header-only, so a body dies in the fixture writer.
	m.Files[bad] = omnpkg.FileInfo{SHA256: sha256hex(nil), Size: 0}
	entries := make([]tarEntry, 0, 5)
	entries = append(entries, manifestEntry(t, m, nil, nil, 0))
	for _, name := range []string{recordPath(testSourceID), soulPath(testSourceID), instructionsPath(testSourceID)} {
		entries = append(entries, tarEntry{Name: name, Body: files[name]})
	}
	entries = append(entries, tarEntry{Name: bad, Typeflag: typeflag, Linkname: linkTarget})
	return buildArchive(t, entries)
}

// tarBytes renders only the tar (uncompressed) for container-confusion cases.
func tarBytes(t *testing.T, m *omnpkg.Manifest, files map[string][]byte) []byte {
	t.Helper()
	entries := make([]tarEntry, 0, 1+len(files))
	entries = append(entries, manifestEntry(t, m, nil, nil, 0))
	for _, name := range sortedKeys(files) {
		entries = append(entries, tarEntry{Name: name, Body: files[name]})
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{
			Name: e.Name, Size: int64(len(e.Body)), Mode: e.Mode,
			ModTime: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC),
		}
		require.NoError(t, tw.WriteHeader(hdr))
		if len(e.Body) > 0 {
			_, err := tw.Write(e.Body)
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	return buf.Bytes()
}
