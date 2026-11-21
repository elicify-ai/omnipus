package omnpkg_test

// S1b T-10: FR-003/005/006/007/009 and the two unknown-element BDD scenarios.
// Unknown payloads are opaque, not agent JSON, but are never exempt from the
// universal checks. Safety subcases already satisfied by S1a are guardrails,
// not claimed as newly observed RED defects.

import (
	"archive/tar"
	"bytes"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

const s1bUnknownPath = "skills/writing/SKILL.md"

func s1bMixedManifest() (*omnpkg.Manifest, map[string][]byte) {
	manifest, payload := validManifest(testSourceID, false)
	body := []byte("# writing\nNot agent JSON: opaque future payload.\n")
	manifest.Elements = append(manifest.Elements, omnpkg.ManifestElement{
		Type: "skill", Schema: 1, SourceID: "writing", Name: "Writing",
		Files: []string{s1bUnknownPath},
	})
	manifest.Counts["skill"] = 1
	manifest.Files[s1bUnknownPath] = omnpkg.FileInfo{SHA256: sha256hex(body), Size: int64(len(body))}
	payload[s1bUnknownPath] = body
	return manifest, payload
}

func TestS1bUnknownElements_CheckIntegrityThenSkipOpaqueTypes(t *testing.T) {
	for _, kind := range []string{"skill", "future-widget"} {
		for _, schema := range []int{1, 2} { // FR-005: schema 2 is too new only for a known agent.
			name := kind + "/current schema"
			if schema == 2 {
				name = kind + "/future schema"
			}
			t.Run(name, func(t *testing.T) {
				manifest, payload := s1bMixedManifest()
				manifest.Elements[1].Type = kind
				manifest.Elements[1].Schema = schema
				manifest.Counts = map[string]int{"agent": 1, kind: 1}
				pkg := buildPackage(t, manifest, payload)
				report, err := omnpkg.Verify(bytes.NewReader(pkg))
				require.NoError(t, err, "verify checks unknown files without interpreting their schema")
				require.ElementsMatch(t, []string{recordPath(testSourceID), soulPath(testSourceID), s1bUnknownPath}, report.FilesVerified)
				m, files, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
				require.NoError(t, err, "unknown-owned SKILL.md is not subject to the agent layout")
				require.Equal(t, payload, files, "every file, including the opaque one, was checksum-verified")
				require.NoError(t, omnpkg.ValidateElements(m, files), "FR-006 S1b skips unknown elements instead of element_type_not_supported")
			})
		}
	}

	t.Run("one-byte tamper in unknown file is refused and names it", func(t *testing.T) {
		manifest, payload := s1bMixedManifest()
		payload[s1bUnknownPath][0] = '!'
		pkg := buildPackage(t, manifest, payload) // Deliberately retain the original hash and size.
		for _, read := range []func() error{
			func() error { _, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg)); return err },
			func() error { _, err := omnpkg.Verify(bytes.NewReader(pkg)); return err },
		} {
			err := read()
			wantCode(t, err, "checksum_mismatch")
			require.Contains(t, err.Error(), s1bUnknownPath, "FR-006: do not silently ignore a damaged skipped file")
		}
	})

	for _, tc := range []struct {
		name string
		edit func(*omnpkg.Manifest, map[string][]byte)
	}{
		{"unknown counts wrong", func(m *omnpkg.Manifest, _ map[string][]byte) { m.Counts["skill"] = 2 }},
		{"unknown file missing", func(_ *omnpkg.Manifest, p map[string][]byte) { delete(p, s1bUnknownPath) }},
		{"double ownership by unknown", func(m *omnpkg.Manifest, _ map[string][]byte) {
			m.Elements[1].Files = append(m.Elements[1].Files, soulPath(testSourceID))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest, payload := s1bMixedManifest()
			tc.edit(manifest, payload)
			refuseVerifyAndRead(t, buildPackage(t, manifest, payload), "manifest_mismatch")
		})
	}

	for _, tc := range []struct {
		name, path, code string
		typeflag         byte
		duplicate        bool
	}{
		{"unknown traversal", "skills/../../outside", "unsafe_path", tar.TypeReg, false},
		{"unknown symlink", s1bUnknownPath, "unsafe_entry_type", tar.TypeSymlink, false},
		{"unknown duplicate", s1bUnknownPath, "unsafe_entry_type", tar.TypeReg, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest, payload := s1bMixedManifest()
			manifest.Elements[1].Files = []string{tc.path}
			manifest.Files[tc.path] = manifest.Files[s1bUnknownPath]
			if tc.path != s1bUnknownPath {
				delete(manifest.Files, s1bUnknownPath)
			}
			entries := []tarEntry{manifestEntry(t, manifest, nil, nil, 0)}
			for _, path := range manifest.Elements[0].Files {
				entries = append(entries, tarEntry{Name: path, Body: payload[path]})
			}
			unknown := tarEntry{Name: tc.path, Typeflag: tc.typeflag, Body: payload[s1bUnknownPath]}
			if tc.typeflag == tar.TypeSymlink {
				unknown.Body, unknown.Linkname = nil, "outside"
			}
			entries = append(entries, unknown)
			if tc.duplicate {
				entries = append(entries, unknown)
			}
			refuseVerifyAndRead(t, buildArchive(t, entries), tc.code)
		})
	}
}
