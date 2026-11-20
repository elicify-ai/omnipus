// packages_b1_freshness_red_test.go — CHECK F07 / FR-042 / Dataset D14.
//
// Apply re-checks the preview. An additive difference (a skill installed or
// removed, a provider becoming configured or losing the key that made it
// configured) still applies, and each difference is its own
// changed_since_preview warning naming the element and the item. A name-only
// difference stays a single warning whose items are "name".
package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

func previewFreshnessCases(t *testing.T) {
	t.Helper()
	t.Run("skill removed and provider configured are separate differences", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv(config.EnvBuiltinSkills, root)
		writeFixtureSkill(t, root, "writing")
		api := packageAPI(t)
		usePackageSeams(t)
		packageT5.Reload = func() (bool, error) { return true, nil }
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		prev := stageUpload(t, api, freshnessPackage(t))
		require.Contains(t, previewWarningCodes(prev), "skill_bound_local", "writing must be installed at preview")
		require.Contains(t, previewWarningCodes(prev), "provider_not_configured")
		require.NoError(t, os.RemoveAll(filepath.Join(root, "writing")))
		addConfiguredProvider(t, api, "openrouter")
		res := applyFresh(t, api, prev.StagingId, ws)
		require.Equal(t, "Writer", res.Created[0].Name, "the name did not change")
		requireChangedSince(t, res, "writing")
		requireChangedSince(t, res, "openrouter")
		require.Equal(t, 2, countChangedSince(res), "the skill and the provider are separate warnings, not one name warning")
		stored := readStoredAgent(t, api, res.Created[0].NewId)
		require.NotContains(t, storedSkillIDs(stored), "writing", "the skill removed after preview is dropped")
		model, _ := stored["model"].(map[string]any)
		require.Equal(t, "openrouter", model["provider"], "the provider name is still kept")
	})

	t.Run("skill installed after preview is kept and listed", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv(config.EnvBuiltinSkills, root)
		api := packageAPI(t)
		usePackageSeams(t)
		packageT5.Reload = func() (bool, error) { return true, nil }
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		prev := stageUpload(t, api, freshnessPackage(t))
		require.Contains(t, previewWarningCodes(prev), "skill_dropped")
		writeFixtureSkill(t, root, "writing")
		res := applyFresh(t, api, prev.StagingId, ws)
		requireChangedSince(t, res, "writing")
		require.Contains(t, storedSkillIDs(readStoredAgent(t, api, res.Created[0].NewId)), "writing")
	})

	t.Run("provider that loses its key is listed", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		packageT5.Reload = func() (bool, error) { return true, nil }
		row := addConfiguredProvider(t, api, "openrouter")
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		rec := mainRecord(specSourceID, "Writer", "Drafts")
		prev := stageUpload(t, api, buildAgentPackage(t, specSourceID, "Writer", rec, []byte("Answer in rhyme.\n"), nil, 0))
		require.NotContains(t, previewWarningCodes(prev), "provider_not_configured", "the key is present at preview")
		row.APIKeyRef = ""
		require.False(t, providerConfigured(api.agentLoop.GetConfig(), "openrouter"), "clearing the key must make the provider unconfigured")
		res := applyFresh(t, api, prev.StagingId, ws)
		require.Contains(t, resultWarningCodes(res), "provider_not_configured", "the current state still warns")
		requireChangedSince(t, res, "openrouter")
	})

	t.Run("a name-only difference stays items name", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		packageT5.Reload = func() (bool, error) { return true, nil }
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		prev := stageUpload(t, api, validMainPackage(t))
		seedClashNames(t, api, []string{"Writer"})
		res := applyFresh(t, api, prev.StagingId, ws)
		require.Equal(t, "Writer (imported)", res.Created[0].Name)
		got := changedSinceItems(t, res)
		require.Equal(t, [][]string{{"name"}}, got, "a name clash does not become a skill or provider warning")
	})
}

func freshnessPackage(t *testing.T) []byte {
	t.Helper()
	rec := mainRecord(specSourceID, "Writer", "Drafts")
	rec["skills"] = []string{"writing"}
	return buildAgentPackage(t, specSourceID, "Writer", rec, []byte("Answer in rhyme.\n"), nil, 0)
}

func writeFixtureSkill(t *testing.T, root, slug string) {
	t.Helper()
	dir := filepath.Join(root, slug)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	body := "---\nname: " + slug + "\ndescription: fixture for preview freshness\n---\n# " + slug + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600))
}

func addConfiguredProvider(t *testing.T, api *restAPI, id string) *config.ModelConfig {
	t.Helper()
	row := &config.ModelConfig{Provider: id, APIKeyRef: "provider-key"}
	cfg := api.agentLoop.GetConfig()
	cfg.Providers = append(cfg.Providers, row)
	require.True(t, providerConfigured(cfg, id), "the row with a key must count as configured")
	return row
}

func applyFresh(t *testing.T, api *restAPI, stagingID, ws string) gen.PackageImportResult {
	t.Helper()
	got := callPackageApply(t, api, stagingID, applyBody("new", ws))
	require.Equal(t, http.StatusOK, got.Code, "an additive preview difference still applies; body=%s", got.Body)
	return decodeResult(t, got.Body)
}

func countChangedSince(res gen.PackageImportResult) int {
	n := 0
	for _, w := range res.Warnings {
		if w.Code == packageCodeChangedSincePreview {
			n++
		}
	}
	return n
}

func requireChangedSince(t *testing.T, res gen.PackageImportResult, item string) {
	t.Helper()
	for _, w := range res.Warnings {
		if w.Code != packageCodeChangedSincePreview || w.Items == nil {
			continue
		}
		if w.ElementSourceId == nil || *w.ElementSourceId != specSourceID {
			continue
		}
		for _, got := range *w.Items {
			if got == item {
				return
			}
		}
	}
	t.Fatalf("FR-042: no changed_since_preview for element %s item %q in %#v", specSourceID, item, res.Warnings)
}

func changedSinceItems(t *testing.T, res gen.PackageImportResult) [][]string {
	t.Helper()
	var out [][]string
	for _, w := range res.Warnings {
		if w.Code != packageCodeChangedSincePreview {
			continue
		}
		require.NotNil(t, w.ElementSourceId)
		require.Equal(t, specSourceID, *w.ElementSourceId)
		if w.Items == nil {
			out = append(out, nil)
			continue
		}
		out = append(out, append([]string(nil), (*w.Items)...))
	}
	return out
}

func readStoredAgent(t *testing.T, api *restAPI, id string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(api.homePath, "entities", "agents", id+".json"))
	require.NoError(t, err)
	var stored map[string]any
	require.NoError(t, json.Unmarshal(raw, &stored), "record=%s", raw)
	return stored
}

func storedSkillIDs(stored map[string]any) []string {
	raw, ok := stored["skills"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
