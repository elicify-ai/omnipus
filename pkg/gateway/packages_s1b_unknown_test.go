package gateway

// S1b T-10: FR-003/005/006/007/009/020/021/023, BDD unknown elements skipped
// and damaged unknown file refused. The acceptance and guardrail subcases
// intentionally share one focused top-level test, not a recursive suite run.

import (
	"archive/tar"
	"bytes"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackagesS1b_UnknownElementsSkippedOnlyAfterUniversalChecks(t *testing.T) {
	for _, tc := range []struct {
		name, kind   string
		schema       int
		unknownFirst bool
	}{
		{"skill after agent", "skill", 1, false},
		{"skill before agent", "skill", 1, true},
		{"opaque future type and schema before agent", "future-widget", 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := packageAPI(t)
			usePackageSeams(t)
			installRosterReload(t, api, nil)
			ws := createWorkspaceViaAPI(t, api, "Research", "notes")
			before := snapshotHome(t, api.homePath)
			cfgBefore := configBytes(t, api.homePath)
			manifest, parts := s1bMixedParts(t, tc.kind, tc.schema, tc.unknownFirst)
			pkg := s1bArchive(t, manifest, parts)
			// Positive fixture/instrument control: the skipped payload really
			// is in the upload, with an independent, recognizable body.
			_, inputFiles := decodePackageBytes(t, pkg)
			require.Equal(t, []byte(s1bOpaqueBody), inputFiles[s1bOpaquePath])
			prev := stageUpload(t, api, pkg)
			requireHomeUnchangedExcept(t, before, snapshotHome(t, api.homePath))
			require.Len(t, prev.WillAdd, 1, "only the supported agent can be proposed")
			assert.Equal(t, "agent", prev.WillAdd[0].Type)
			assert.Equal(t, specSourceID, prev.WillAdd[0].SourceId, "select agent by type, not first manifest position")
			assert.Equal(t, "Writer", prev.WillAdd[0].ProposedName)
			require.Len(t, prev.SkippedElements, 1, "FR-021 exact skipped-element inventory")
			assert.Equal(t, tc.kind, prev.SkippedElements[0].Type)
			assert.Equal(t, "writing", prev.SkippedElements[0].SourceId)
			assert.Empty(t, prev.NeedsSecrets)
			got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
			require.Equal(t, http.StatusOK, got.Code, "FR-006 unknown files must never be applied; body=%s", got.Body)
			res := decodeResult(t, got.Body)
			require.Len(t, res.Created, 1)
			assert.Equal(t, "agent", res.Created[0].Type)
			assert.Equal(t, specSourceID, res.Created[0].SourceId)
			assert.Equal(t, "Writer", res.Created[0].Name)
			assert.NotEqual(t, specSourceID, res.Created[0].NewId)
			agents, skipped, err := agentstore.New(api.homePath).List()
			require.NoError(t, err)
			assert.Empty(t, skipped, "not a corrupted entity disguised as a skipped element")
			require.Len(t, agents, 1, "only the supported agent is created")
			assert.Equal(t, res.Created[0].NewId, agents[0].ID)
			_, err = os.Stat(filepath.Join(api.homePath, "backups", ".staging", prev.StagingId))
			assert.ErrorIs(t, err, os.ErrNotExist, "success removes staging including unknown files")
			_, err = os.Stat(filepath.Join(api.homePath, filepath.FromSlash(s1bOpaquePath)))
			assert.ErrorIs(t, err, os.ErrNotExist, "unknown skill must not be installed")
			s1bRequireNoOpaquePayload(t, api.homePath)
			assert.Equal(t, cfgBefore, configBytes(t, api.homePath), "unknown elements cannot affect config.json")
		})
	}

	// These universal checks already exist in S1a. They must remain valid when
	// removing the unknown-type refusal; they are not fabricated RED claims.
	for _, tc := range []struct {
		name, code string
		file       string
		edit       func(*omnpkg.Manifest, []tarPart) []tarPart
	}{
		{"one-byte changed unknown SKILL.md", "checksum_mismatch", s1bOpaquePath, func(_ *omnpkg.Manifest, p []tarPart) []tarPart {
			p[3].body[0] = '!'
			return p
		}},
		{"unknown symlink", "unsafe_entry_type", s1bOpaquePath, func(_ *omnpkg.Manifest, p []tarPart) []tarPart {
			p[3].typeflag, p[3].body, p[3].link = tar.TypeSymlink, nil, "outside"
			return p
		}},
		{"unknown duplicate", "unsafe_entry_type", s1bOpaquePath, func(_ *omnpkg.Manifest, p []tarPart) []tarPart {
			return append(p, p[3])
		}},
		{"unknown traversal", "unsafe_path", "skills/../../outside", func(m *omnpkg.Manifest, p []tarPart) []tarPart {
			const unsafe = "skills/../../outside"
			m.Files[unsafe] = m.Files[s1bOpaquePath]
			delete(m.Files, s1bOpaquePath)
			m.Elements[1].Files = []string{unsafe}
			p[3].name = unsafe
			return p
		}},
		{"unknown missing file", "manifest_mismatch", s1bOpaquePath, func(_ *omnpkg.Manifest, p []tarPart) []tarPart {
			return p[:3]
		}},
		{"unknown counts mismatch", "manifest_mismatch", "", func(m *omnpkg.Manifest, p []tarPart) []tarPart {
			m.Counts["skill"] = 2
			return p
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := packageAPI(t)
			before := snapshotHome(t, api.homePath)
			manifest, parts := s1bMixedParts(t, "skill", 1, false)
			parts = tc.edit(manifest, parts)
			got := callPackageImport(t, api, s1bArchive(t, manifest, parts))
			er := requirePackageStatus(t, got, http.StatusUnprocessableEntity, tc.code)
			if tc.file != "" {
				require.NotNil(t, er.Details, "refusal identifies the unknown-owned file")
				assert.Equal(t, tc.file, (*er.Details)["file"])
			}
			assert.Empty(t, packageStagingIDs(t, api.homePath), "damaged skipped element releases upload slot")
			requireHomeUnchangedExcept(t, before, snapshotHome(t, api.homePath))
		})
	}
}

func s1bRequireNoOpaquePayload(t *testing.T, home string) {
	t.Helper()
	require.NoError(t, filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		assert.False(t, bytes.Contains(body, []byte(s1bOpaqueBody)), "FR-006 unknown payload escaped staging into %s", path)
		return nil
	}))
}
