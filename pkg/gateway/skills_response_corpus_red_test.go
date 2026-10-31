package gateway

// r15 S-11.19: real response -> declared schema -> real SPA adapter.
// Run this Go test, THEN src/lib/api/skills-real-responses.test.ts, serially,
// with SKILLS_REST_CORPUS naming the same absolute temporary JSON file. This
// is a test receipt, not a checked-in fabricated response. No concurrent Go
// and Vitest processes, no mock of ApplyChange or of a REST handler.
import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRestS1119_ResponseCorpus(t *testing.T) {
	rows := []map[string]any{}
	output := os.Getenv("SKILLS_REST_CORPUS")
	if output == "" {
		output = filepath.Join(t.TempDir(), "skills-real-responses.json")
	}
	// Always overwrite, including a failed producer run. A stale GREEN corpus
	// must not be reusable after a newly broken handler.
	require.NoError(t, os.WriteFile(output, []byte("[]"), 0o600))
	defer func() {
		raw, err := json.Marshal(rows)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(output, raw, 0o600))
		t.Logf("real response corpus: %s (%d rows)", output, len(rows))
	}()
	for _, leg := range []string{"install-cleanup", "install-partial", "copy-partial", "delete-rollback", "delete-rollback-failed"} {
		t.Run(leg, func(t *testing.T) {
			api, cleanup := newTestRestAPI(t)
			defer cleanup()
			root := filepath.Join(api.homePath, "skills")
			slug := "x"
			rev := ""
			if leg == "copy-partial" {
				t9SkillFixture(t, api, "src")
				slug = "dst"
			} else {
				_, rev = t9InstalledFixture(t, api, "x")
			}
			savedManifest, savedBlob, savedSwap, savedPutback := skills.FaultManifestWrite, skills.FaultBlobWrite, skills.FaultSwapRemove, skills.FaultPutBackRename
			defer func() {
				skills.FaultManifestWrite = savedManifest
				skills.FaultBlobWrite = savedBlob
				skills.FaultSwapRemove = savedSwap
				skills.FaultPutBackRename = savedPutback
			}()
			fired := 0
			putbackFired := 0
			fault := errors.New("corpus injected post-publication failure")
			method, path, body, query := http.MethodDelete, "/api/v1/skills/x", "", "revision="+rev
			want := http.StatusInternalServerError
			schema := "ConfigurationMutationFailureState"
			switch leg {
			case "install-cleanup", "install-partial":
				api.skillRegistry = &fakeSkillRegistry{installRes: &skills.InstallResult{Version: "2.0.0"}}
				method, path, query = http.MethodPost, "/api/v1/skills/install", ""
				raw, err := json.Marshal(map[string]any{"slug": "x", "revision": rev, "replace_local_changes": true})
				require.NoError(t, err)
				body = string(raw)
				if leg == "install-cleanup" {
					skills.FaultSwapRemove = func(string) error { fired++; return fault }
					want = http.StatusOK
					schema = "Skill"
				} else {
					skills.FaultManifestWrite = func(string) error {
						raw, err := os.ReadFile(filepath.Join(root, "x", "SKILL.md"))
						if err == nil && string(raw) == "---\nname: x\ndescription: Use this installed test skill when requested.\n---\n\nBody.\n" {
							fired++
							return fault
						}
						return nil
					}
				}
			case "copy-partial":
				method, path, query, body = http.MethodPost, "/api/v1/skills/src/copy", "", `{"new_id":"dst"}`
				skills.FaultManifestWrite = func(p string) error {
					if filepath.Dir(p) == filepath.Join(api.homePath, "system", "skill-history", "dst", "versions") {
						fired++
						return fault
					}
					return nil
				}
			case "delete-rollback", "delete-rollback-failed":
				skills.FaultManifestWrite = func(string) error {
					if _, err := os.Stat(filepath.Join(root, ".swap", "x")); err == nil {
						fired++
						return fault
					}
					return nil
				}
				if leg == "delete-rollback-failed" {
					skills.FaultPutBackRename = func(string, string) error { putbackFired++; return fault }
				}
			}
			w := t9Do(api.HandleSkills, method, path, query, body)
			// Save actual bytes even for a wrong status/schema; the SPA consumer must
			// reject that exact bad response, not silently skip it.
			rows = append(rows, map[string]any{"leg": leg, "method": method, "path": path, "query": query, "request_body": body, "body": w.Body.String(), "status": w.Code, "expected_status": want, "schema": schema, "slug": slug, "revision": rev})
			require.Greater(t, fired, 0, "intended publication/finishing fault must actually fire")
			if leg == "delete-rollback-failed" {
				require.Equal(t, 1, putbackFired)
			}
			require.Equal(t, want, w.Code, "%s: %s", leg, w.Body.String())
			t9ValidateSchema(t, schema, w)
			m := t9Body(t, w)
			if leg == "install-cleanup" {
				assert.Contains(t, m["message"], "cleanup")
				assert.NotContains(t, m, "warning")
			} else {
				assert.NotEmpty(t, m["error_stage"])
				if leg == "delete-rollback" {
					assert.Equal(t, "none", m["persistence_status"])
				} else {
					assert.Equal(t, "partial", m["persistence_status"])
				}
			}
		})
	}
}
