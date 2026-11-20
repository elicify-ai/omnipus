package omnpkg_test

// RED T-2 — TestManifest_Relationships (spec TDD plan order 2).
// Spec: FR-009 (Dataset D12 relationships), FR-003 (ownership, listing);
// BDD "Damaged, unsafe or non-creatable packages are refused" (D12). Verify is
// the "every format check" reader (FR-052); the upload reader shares the core.

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

// refuseVerifyAndRead asserts BOTH format-check readers refuse a damaged
// package with the given code (Verify = every format check, FR-052; the upload
// reader shares the core, FR-020).
func refuseVerifyAndRead(t *testing.T, pkg []byte, code string) {
	t.Helper()
	_, err := omnpkg.Verify(bytes.NewReader(pkg))
	wantCode(t, err, code)
	_, _, err = omnpkg.ReadPackage(bytes.NewReader(pkg))
	wantCode(t, err, code)
}

func TestManifest_Relationships(t *testing.T) {
	t.Run("D12-1 zero elements refused as no_supported_element", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		m.Elements = nil
		m.Counts = map[string]int{}
		refuseVerifyAndRead(t, buildPackage(t, m, files), omnpkg.CodeNoSupportedElement)
	})

	t.Run("D12-2 only an unknown element refused as no_supported_element", func(t *testing.T) {
		m, _ := validManifest(testSourceID, true)
		m.Elements = []omnpkg.ManifestElement{{
			Type: "skill", Schema: 1, SourceID: "writing", Name: "writing",
			Files: []string{"skills/writing/SKILL.md"},
		}}
		m.Counts = map[string]int{"skill": 1}
		for _, p := range []string{recordPath(testSourceID), soulPath(testSourceID), instructionsPath(testSourceID)} {
			delete(m.Files, p)
		}
		m.Files["skills/writing/SKILL.md"] = omnpkg.FileInfo{SHA256: sha256hex([]byte("skill body")), Size: 10}
		refuseVerifyAndRead(t, buildPackage(t, m, map[string][]byte{"skills/writing/SKILL.md": []byte("skill body")}),
			omnpkg.CodeNoSupportedElement)
	})

	t.Run("D12-3 two agent elements refused as too_many_agents", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		second, secondFiles := validManifest(testSourceID2, false)
		m.Elements = append(m.Elements, second.Elements[0])
		m.Counts["agent"] = 2
		for p, fi := range second.Files {
			m.Files[p] = fi
			files[p] = secondFiles[p]
		}
		pkg := buildPackage(t, m, files)
		_, err := omnpkg.Verify(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeTooManyAgents)
		_, _, err = omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeTooManyAgents)
	})

	t.Run("D12-4 two elements with the same source_id refused as manifest_mismatch", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		m.Elements = append(m.Elements, omnpkg.ManifestElement{
			Type: "agent", Schema: omnpkg.AgentSchemaVersion, SourceID: testSourceID, Name: "Other",
		})
		refuseVerifyAndRead(t, buildPackage(t, m, files), omnpkg.CodeManifestMismatch)
	})

	// FR-003: a file owned by two elements is refused.
	t.Run("D12-5 file owned by two elements refused as manifest_mismatch", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		m.Elements = append(m.Elements, omnpkg.ManifestElement{
			Type: "agent", Schema: omnpkg.AgentSchemaVersion, SourceID: testSourceID2, Name: "Other",
			Files: []string{soulPath(testSourceID)},
		})
		refuseVerifyAndRead(t, buildPackage(t, m, files), omnpkg.CodeManifestMismatch)
	})

	t.Run("D12-6 elements[].files path missing from files map refused", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		m.Elements[0].Files = append(m.Elements[0].Files, instructionsPath(testSourceID)+"-ghost")
		refuseVerifyAndRead(t, buildPackage(t, m, files), omnpkg.CodeManifestMismatch)
	})

	t.Run("D12-7 counts wrong (counts.agent=2, one element) refused", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		m.Counts["agent"] = 2
		refuseVerifyAndRead(t, buildPackage(t, m, files), omnpkg.CodeManifestMismatch)
	})

	t.Run("D12-8 element name differs from record name refused", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		m.Elements[0].Name = "Editor"
		refuseVerifyAndRead(t, buildPackage(t, m, files), omnpkg.CodeManifestMismatch)
	})

	t.Run("D12-9 source_id differs from record id refused", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		var rec map[string]any
		require.NoError(t, json.Unmarshal(files[recordPath(testSourceID)], &rec))
		rec["id"] = testSourceID2 // source_id (and folder) keep testSourceID
		raw, err := json.Marshal(rec)
		require.NoError(t, err)
		files[recordPath(testSourceID)] = raw
		m.Files[recordPath(testSourceID)] = omnpkg.FileInfo{SHA256: sha256hex(raw), Size: int64(len(raw))}
		refuseVerifyAndRead(t, buildPackage(t, m, files), omnpkg.CodeManifestMismatch)
	})

	t.Run("D12-10 agent element without agent.json refused as manifest_mismatch", func(t *testing.T) {
		// Listed in the manifest but absent from the payload: the element has
		// no record entry.
		m, files := validManifest(testSourceID, true)
		delete(files, recordPath(testSourceID))
		delete(m.Files, recordPath(testSourceID))
		refuseVerifyAndRead(t, buildPackage(t, m, files), omnpkg.CodeManifestMismatch)
	})

	// FR-003 at upload: an unlisted entry is refused (D1 5).
	t.Run("D1-5 unlisted entry refused as manifest_mismatch at upload", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		files["omnipus/agents/"+testSourceID+"/stray.txt"] = []byte("stray")
		pkg := buildPackage(t, m, files)
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeManifestMismatch)
	})
}
