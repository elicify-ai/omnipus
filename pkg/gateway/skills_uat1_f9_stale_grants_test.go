package gateway

// UAT-1 F-9 (founder Q-S3 option A, spec r15 §5.10 SKILL_GRANT_STALE: "the SPA
// surfaces the stale row on the Agents screen"): the agent API exposes, per
// agent, the granted skill names whose stored grant no longer matches the
// installed skill (deleted and recreated, so a new generation).
//
// ASSUMED SHAPE (the spec does not name the field; recorded in the ledger):
// an optional, read-only array of strings `stale_skill_grants` on the Agent
// schema (contracts/components/schemas/Agent.yaml) and on GET
// /api/v1/agents/{id}. Omitted when empty, like `skills`. Each entry is a name
// from `skills` whose installed generation differs from the binding in
// `skill_grants`. A name whose skill is absent is not stale (it is unbound or
// missing, and heals when the skill appears).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func f9Skill(t *testing.T, home, slug, generation string) {
	t.Helper()
	dir := filepath.Join(home, "skills", slug)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+slug+"\ndescription: f9\n---\nBody.\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".omnipus-identity"), []byte(generation+"\n"), 0o644))
}

func TestAgentAPI_StaleSkillGrantsArePerAgentField(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	t.Setenv(config.EnvHome, api.homePath)
	api.agentLoop.SetReloadFunc(func() error {
		defer api.agentLoop.ClearReloadPending()
		return api.refreshConfigAndRewireServices(api.configPath())
	})
	f9Skill(t, api.homePath, "kept", "11111111-1111-1111-1111-111111111111")
	f9Skill(t, api.homePath, "recreated", "22222222-2222-2222-2222-222222222222")

	createW := httptest.NewRecorder()
	createR := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(
		`{"name": "Grant Holder", "type": "Main", "model": "claude-sonnet-4-6", "soul": "I hold grants.", "skills": ["kept", "recreated"]}`))
	createR.Header.Set("Content-Type", "application/json")
	api.HandleAgents(createW, createR)
	require.Equal(t, http.StatusCreated, createW.Code, "create: %s", createW.Body.String())
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(createW.Body.Bytes(), &created))
	require.NotEmpty(t, created.ID)

	get := func() map[string]any {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+created.ID, nil)
		api.HandleAgents(w, r)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		return body
	}
	before := get()
	require.Equal(t, []any{"kept", "recreated"}, before["skills"], "instrument: both grants are held")
	assert.NotContains(t, before, "stale_skill_grants", "nothing is stale yet; the field is omitted")

	// The granted skill is deleted and the name recreated: a new generation.
	require.NoError(t, os.RemoveAll(filepath.Join(api.homePath, "skills", "recreated")))
	f9Skill(t, api.homePath, "recreated", "99999999-9999-9999-9999-999999999999")

	after := get()
	assert.Equal(t, []any{"recreated"}, after["stale_skill_grants"], "only the recreated name is stale")
	assert.Equal(t, []any{"kept", "recreated"}, after["skills"], "the names are still listed")
}

func TestAgentContract_DeclaresStaleSkillGrants(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "components", "schemas", "Agent.yaml"))
	require.NoError(t, err)
	text := string(raw)
	idx := strings.Index(text, "\n  stale_skill_grants:")
	require.GreaterOrEqual(t, idx, 0, "Agent.yaml declares the stale_skill_grants property")
	block := text[idx+1:]
	if next := strings.Index(block[1:], "\n  "); next >= 0 {
		// cut at the next sibling property (two-space indent, not deeper)
		for _, line := range strings.Split(block, "\n")[1:] {
			if line != "" && !strings.HasPrefix(line, "   ") {
				block = block[:strings.Index(block, line)]
				break
			}
		}
	}
	assert.Contains(t, block, "type: array", "an array")
	assert.Contains(t, block, "type: string", "of skill names")
	assert.Contains(t, block, "readOnly: true", "read-only: the server computes it")
}
