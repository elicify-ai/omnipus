package gateway

// Security report F1 / FR-006: an exclusively skipped-owned member cannot be
// consumed merely because its path looks like the supported agent's prompt or
// instructions. The report's invalid_element ownership control is SOUL.md;
// agent-owned AGENT.md remains supported by the current Field mapping.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackagesS1b_SkippedElementsCannotSupplyAgentFiles(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		agentOwns  bool
	}{
		{"skipped_SOUL.md", "SOUL.md", false},
		{"skipped_AGENT.md", "AGENT.md", false},
		{"agent_owned_SOUL.md_refused", "SOUL.md", true},
		{"agent_owned_AGENT.md_kept", "AGENT.md", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := packageAPI(t)
			usePackageSeams(t)
			installRosterReload(t, api, nil)
			ws := createWorkspaceViaAPI(t, api, "Research", "notes")
			raw, err := json.Marshal(s1bWorkerRecord(false))
			require.NoError(t, err)
			manifest, parts := s1bFix3RecordParts(raw)
			path := omnpkg.AgentsPrefix + specSourceID + "/" + tc.file
			body := []byte("F1_SKIPPED_SENTINEL_" + tc.file + "\n")
			manifest.Files[path] = omnpkg.FileInfo{SHA256: sha256hex(body), Size: int64(len(body))}
			parts = append(parts, tarPart{name: path, body: body})
			if tc.agentOwns {
				manifest.Elements[0].Files = append(manifest.Elements[0].Files, path)
			} else {
				manifest.Elements = append(manifest.Elements, omnpkg.ManifestElement{
					Type: "future-widget", Schema: 1, SourceID: "opaque", Name: "Opaque", Files: []string{path},
				})
				manifest.Counts["future-widget"] = 1
			}
			// Instrument control: exactly one owner, no double-ownership or
			// duplicate-member refusal that would hide the consumption bug.
			owners := make([]string, 0, 1)
			for _, element := range manifest.Elements {
				for _, member := range element.Files {
					if member == path {
						owners = append(owners, element.Type)
					}
				}
			}
			wantOwner := "future-widget"
			if tc.agentOwns {
				wantOwner = "agent"
			}
			require.Equal(t, []string{wantOwner}, owners)
			pkg := s1bArchive(t, manifest, parts)
			_, inputFiles := decodePackageBytes(t, pkg)
			require.Equal(t, body, inputFiles[path], "the checksummed sentinel is really in the uploaded member")
			before := snapshotHome(t, api.homePath)
			got := callPackageImport(t, api, pkg)
			if tc.agentOwns && tc.file == "SOUL.md" {
				requirePackageStatus(t, got, http.StatusUnprocessableEntity, "invalid_element")
				require.Empty(t, packageStagingIDs(t, api.homePath))
				require.Empty(t, agentEntityNames(t, api.homePath))
				requireHomeUnchangedExcept(t, before, snapshotHome(t, api.homePath))
				return
			}
			if !tc.agentOwns && got.Code == http.StatusUnprocessableEntity {
				er := requirePackageStatus(t, got, http.StatusUnprocessableEntity, "")
				require.Contains(t, []string{"invalid_element", "element_not_creatable", "manifest_mismatch", "unsafe_path"}, packageErrCode(er), "F1 permits an explicit ownership/layout refusal, not a generic failure")
				require.Empty(t, packageStagingIDs(t, api.homePath))
				require.Empty(t, agentEntityNames(t, api.homePath))
				require.Empty(t, listedAgentIDs(t, api))
				requireHomeUnchangedExcept(t, before, snapshotHome(t, api.homePath))
				return
			}
			require.Equal(t, http.StatusCreated, got.Code, "body=%s", got.Body)
			prev := decodePreview(t, got.Body)
			if tc.agentOwns {
				require.Empty(t, prev.SkippedElements)
			} else {
				require.Len(t, prev.SkippedElements, 1)
				assert.Equal(t, "future-widget", prev.SkippedElements[0].Type)
				assert.Equal(t, "opaque", prev.SkippedElements[0].SourceId)
			}
			requireHomeUnchangedExcept(t, before, snapshotHome(t, api.homePath))
			applied := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
			require.Equal(t, http.StatusOK, applied.Code, "body=%s", applied.Body)
			result := decodeResult(t, applied.Body)
			require.Len(t, result.Created, 1)
			id := result.Created[0].NewId
			stored, err := agentstore.New(api.homePath).ReadState(id)
			require.NoError(t, err)
			require.True(t, stored.Agent.IsExternalCLIWorker())
			assert.Equal(t, "", stored.Soul, "F1: only agent-declared files may supply the stored soul; skipped-owned SOUL.md must not leak")
			instructions, readErr := os.ReadFile(filepath.Join(api.homePath, "agents", id, "AGENT.md"))
			if tc.agentOwns {
				require.NoError(t, readErr)
				assert.Equal(t, body, instructions, "the fix must not discard legitimate agent-owned instructions")
			} else {
				assert.ErrorIs(t, readErr, os.ErrNotExist, "F1: skipped-owned AGENT.md must never become the agent's instructions")
				assert.NotContains(t, string(instructions), string(body), "F1: skipped instructions escaped into the agent's home")
			}
			assert.Contains(t, listedAgentIDs(t, api), id, "check the published agent, not merely a preview plan")
		})
	}
}
