package gateway

// r15 §5.6/§9.2, S-6.11 and S-9.1–4; CHECK-12.
// Plan: real install handler, real publication/history, download-only fake.
// Each row has a fresh home. Install/restore/edit prerequisites are committed;
// an outside edit is deliberately NOT activated before the request. Refusals
// preserve the exact live bytes; successes really replace and retain history.
import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func t9ManifestFiles(t *testing.T, root, slug string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(skills.SkillHistoryRoot(root), slug, "versions", "*.json"))
	require.NoError(t, err)
	return paths
}

func TestRestInstall_ExpectationAndLocalConfirmationMatrix(t *testing.T) {
	for _, state := range []string{"absent", "installed-current", "installed-stale", "installed-missing-revision", "managed-edit-refused", "managed-edit-confirmed", "outside-undetected-refused", "outside-undetected-confirmed", "restored-install-refused", "case-twin-replacement"} {
		t.Run(state, func(t *testing.T) {
			api, cleanup := newTestRestAPI(t)
			defer cleanup()
			api.skillRegistry = &fakeSkillRegistry{installRes: &skills.InstallResult{Version: "2.0.0"}}
			root := filepath.Join(api.homePath, "skills")
			rev := ""
			if state != "absent" {
				_, rev = t9InstalledFixture(t, api, "x")
			}
			initial := t9ManifestFiles(t, root, "x")
			switch state {
			case "managed-edit-refused", "managed-edit-confirmed":
				out, err := skills.ApplyChange(root, skills.ChangeRequest{Kind: skills.KindEdit, Slug: "x", Path: "notes.md", Content: []byte("OWNER CONTENT\n"), Expect: skills.Expectation{Revision: rev}, Actor: skills.ChangeActor{Kind: "agent", AgentID: "editor", Tool: "write_file"}})
				require.NoError(t, err, "real managed-edit prerequisite")
				rev = out.Revision
			case "outside-undetected-refused", "outside-undetected-confirmed":
				require.NoError(t, os.WriteFile(filepath.Join(root, "x", "SKILL.md"), []byte("---\nname: x\ndescription: outside edit\n---\nOUTSIDE CONTENT\n"), 0o644))
				var err error
				rev, err = skills.ContentRevision(filepath.Join(root, "x"))
				require.NoError(t, err)
				assert.Equal(t, initial, t9ManifestFiles(t, root, "x"), "outside write is not detected by fixture setup")
			case "restored-install-refused":
				require.Len(t, initial, 1)
				id := strings.TrimSuffix(filepath.Base(initial[0]), ".json")
				out, err := skills.ApplyChange(root, skills.ChangeRequest{Kind: skills.KindRestore, Slug: "x", Version: id, Expect: skills.Expectation{Revision: rev}, Actor: skills.ChangeActor{Kind: "person", Tool: "edit_skill"}})
				require.NoError(t, err, "restore really commits a restore-kind version")
				rev = out.Revision
			}
			request := map[string]any{"slug": "x"}
			if state != "absent" && state != "installed-missing-revision" {
				request["revision"] = rev
			}
			if state == "installed-stale" {
				request["revision"] = strings.Repeat("0", 64)
			}
			if strings.HasSuffix(state, "-confirmed") {
				request["replace_local_changes"] = true
			}
			if state == "case-twin-replacement" {
				request["slug"] = "X"
			}
			var before []byte
			if state != "absent" {
				var err error
				before, err = os.ReadFile(filepath.Join(root, "x", "SKILL.md"))
				require.NoError(t, err)
			}
			encoded, err := json.Marshal(request)
			require.NoError(t, err)
			w := t9Do(api.HandleSkills, http.MethodPost, "/api/v1/skills/install", "", string(encoded))
			want := http.StatusOK
			if state == "installed-stale" || strings.HasSuffix(state, "-refused") {
				want = http.StatusConflict
			}
			if state == "installed-missing-revision" {
				// §5.6 specifies the refusal text, not a distinct install missing-field status.
				assert.GreaterOrEqual(t, w.Code, 400)
				assert.Less(t, w.Code, 500)
				assert.Contains(t, w.Body.String(), "revision is required")
			} else {
				require.Equal(t, want, w.Code, "%s: %s", state, w.Body.String())
			}
			if want != http.StatusOK || state == "installed-missing-revision" {
				raw, err := os.ReadFile(filepath.Join(root, "x", "SKILL.md"))
				require.NoError(t, err)
				assert.Equal(t, before, raw, "refused replacement changed no live instruction bytes")
				if strings.HasSuffix(state, "-refused") {
					assert.Contains(t, w.Body.String(), "LOCAL_CHANGES_CONFIRMATION_REQUIRED")
					assert.Contains(t, w.Body.String(), "Confirm with replace_local_changes to continue.")
				} else if state == "installed-stale" {
					assert.Contains(t, w.Body.String(), "SKILL_CHANGED")
				}
				if state == "outside-undetected-refused" {
					manifests := t9ManifestFiles(t, root, "x")
					require.Len(t, manifests, len(initial)+1, "detection precedes the confirmation refusal under the same lock")
					raw, err := os.ReadFile(manifests[len(manifests)-1])
					require.NoError(t, err)
					var last map[string]any
					require.NoError(t, json.Unmarshal(raw, &last))
					assert.Equal(t, "external_change", last["kind"])
				}
				return
			}
			t9ValidateSchema(t, "Skill", w)
			assert.Equal(t, "x", t9Body(t, w)["id"], "case-twin replace keeps the normalized identity")
			raw, err := os.ReadFile(filepath.Join(root, "x", "SKILL.md"))
			require.NoError(t, err)
			assert.Equal(t, "---\nname: x\ndescription: Use this installed test skill when requested.\n---\n\nBody.\n", string(raw))
			if state == "case-twin-replacement" {
				_, err := os.Stat(filepath.Join(root, "X"))
				assert.True(t, os.IsNotExist(err), "never creates a second case-twin folder")
			}
			manifests := t9ManifestFiles(t, root, "x")
			require.NotEmpty(t, manifests, "successful install commits real history")
			raw, err = os.ReadFile(manifests[len(manifests)-1])
			require.NoError(t, err)
			var last map[string]any
			require.NoError(t, json.Unmarshal(raw, &last))
			kind := "install_replace"
			if state == "absent" {
				kind = "install"
			}
			assert.Equal(t, kind, last["kind"])
			assert.NotEmpty(t, last["files"], "manifest references the installed content")
			_, err = os.Stat(filepath.Join(root, "x", ".skill-origin.json"))
			assert.True(t, os.IsNotExist(err), "removed origin metadata writer stays removed")
			if strings.HasSuffix(state, "-confirmed") {
				assert.GreaterOrEqual(t, len(manifests), 3, "previous changed state remains versioned")
			}
		})
	}
}
