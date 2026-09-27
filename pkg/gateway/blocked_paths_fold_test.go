// blocked_paths_fold_test.go — #904 gate round 2, item 1 (security-lead N1,
// silent-failure F1): encoding/json binds object keys to struct fields with
// Unicode simple case folding (bytes.EqualFold: U+017F 'ſ' ≡ 's', U+212A
// KELVIN SIGN ≡ 'k'), so a blocked-path check that only lower-cases can be
// walked past with a folded spelling that still lands on the protected field.
//
// Oracle: what encoding/json itself binds (TestFoldBypass_Instrument proves
// the attack keys really reach the protected config fields), never the
// walker's own output.

package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

const (
	longS  = "ſ" // LATIN SMALL LETTER LONG S — folds to 's'
	kelvin = "K" // KELVIN SIGN — folds to 'k'
)

// TestFoldBypass_Instrument proves the attack keys used below are real: the
// decoder that loads config.json binds each of them onto the protected field.
func TestFoldBypass_Instrument(t *testing.T) {
	body := `{"agents":{"defaults":{"max_tool_iteration` + longS + `":1000}},` +
		`"gateway":{"dev_mode_bypa` + longS + `s":true,"u` + longS + `ers":[{"username":"evil"}]}}`
	var cfg config.Config
	require.NoError(t, json.Unmarshal([]byte(body), &cfg))
	assert.Equal(t, 1000, cfg.Agents.Defaults.MaxToolIterations, "ſ binds to max_tool_iterations")
	assert.True(t, cfg.Gateway.DevModeBypass, "ſ binds to dev_mode_bypass")
	require.Len(t, cfg.Gateway.Users, 1, "ſ binds to users")
	assert.Equal(t, "evil", cfg.Gateway.Users[0].Username)
}

func TestMatchBlockedPath_UnicodeFoldedKeys(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"#904 global nested", map[string]any{"agents": map[string]any{"defaults": map[string]any{
			"max_tool_iteration" + longS: 1000}}}, string(config.AgentsDefaultsMaxToolIterations)},
		{"#904 global dotted", map[string]any{"agents.defaults.max_tool_iteration" + longS: 1000},
			string(config.AgentsDefaultsMaxToolIterations)},
		{"#904 marker", map[string]any{"agents": map[string]any{"defaults": map[string]any{
			"max_tool_iteration" + longS + "_env_imported": false}}},
			string(config.AgentsDefaultsMaxToolIterationsEnvImported)},
		{"gateway.dev_mode_bypass", map[string]any{"gateway": map[string]any{"dev_mode_bypa" + longS + "s": true}},
			string(config.GatewayDevModeBypass)},
		{"gateway.users", map[string]any{"gateway": map[string]any{"u" + longS + "ers": []any{}}},
			string(config.GatewayUsers)},
		{"sandbox", map[string]any{longS + "andbox": map[string]any{}}, "sandbox"},
		{"security", map[string]any{longS + "ecurity": map[string]any{}}, "security"},
		{"ASCII upper case", map[string]any{"GATEWAY": map[string]any{"USERS": []any{}}}, string(config.GatewayUsers)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, blocked := matchBlockedPath(tc.body, blockedPaths)
			assert.True(t, blocked, "a key encoding/json folds onto a blocked path must match it")
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestUpdateConfig_UnicodeFoldBypass_Refused(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"#904 global", `{"agents":{"defaults":{"max_tool_iteration` + longS + `":1000}}}`},
		{"gateway.dev_mode_bypass", `{"gateway":{"dev_mode_bypa` + longS + `s":true}}`},
		{"gateway.users", `{"gateway":{"u` + longS + `ers":[{"username":"evil","role":"admin"}]}}`},
		{"providers credential check", `{"provider` + longS + `":[]}`},
		{"api_key credential check (Kelvin)", `{"api_` + kelvin + `ey":"x"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newMTIAPI(t, "250")
			before := mtiSnapshotFiles(t, api)
			w := mtiPutConfig(t, api, tc.body)
			require.Equal(t, http.StatusForbidden, w.Code, "body: %s", w.Body.String())
			assert.Equal(t, before, mtiSnapshotFiles(t, api), "a refused PUT leaves config.json byte-identical")
			assert.EqualValues(t, 250, mtiGetPerf(t, api)["max_tool_iterations"])
		})
	}
}

// TestUpdateConfig_NonASCIIKeyUnderProtectedSection: every config.Config key
// is ASCII, so a non-ASCII key in a map that holds a blocked path (the root,
// gateway, agents, agents.defaults) can only be a folding trick or garbage.
func TestUpdateConfig_NonASCIIKeyUnderProtectedSection(t *testing.T) {
	for _, body := range []map[string]any{
		{"gateway": map[string]any{"pört": 1}},
		{"agents": map[string]any{"defaults": map[string]any{"modél": "x"}}},
		{"étc": 1},
	} {
		got, refused := nonASCIIKeyNearBlockedPath(body, blockedPaths)
		assert.True(t, refused, "body %v", body)
		assert.NotEmpty(t, got)
	}
	for _, body := range []map[string]any{
		{"gateway": map[string]any{"port": 1}},
		{"channels": map[string]any{"telegram": map[string]any{"café": 1}}},
	} {
		_, refused := nonASCIIKeyNearBlockedPath(body, blockedPaths)
		assert.False(t, refused, "body %v is not under a protected section", body)
	}
}

// TestProtectedAgentDefaults_ValueCheck is the defence-in-depth layer on its
// own: even if a folded key got past matchBlockedPath, the merged config
// decoded the way config.json is loaded must keep both #904 values.
func TestProtectedAgentDefaults_ValueCheck(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{"agents": map[string]any{"defaults": map[string]any{
			"max_tool_iterations": 250.0, "max_tool_iterations_env_imported": true, "max_tokens": 4096.0}}}
	}
	before, err := protectedAgentDefaultsFingerprint(base())
	require.NoError(t, err)

	sibling := base()
	foldTestDefaults(t, sibling)["max_tokens"] = 8192.0
	assert.NoError(t, checkProtectedAgentDefaultsUnchanged(before, sibling), "a sibling change is allowed")

	folded := base()
	foldTestDefaults(t, folded)["max_tool_iteration"+longS] = 1000.0
	assert.Error(t, checkProtectedAgentDefaultsUnchanged(before, folded), "a folded key that changes the global is refused")

	marker := base()
	foldTestDefaults(t, marker)["max_tool_iteration"+longS+"_env_imported"] = false
	assert.Error(t, checkProtectedAgentDefaultsUnchanged(before, marker), "a folded key that changes the marker is refused")

	// "agentſ" sorts after "agents", so it is decoded last and wins.
	foldedAgents := base()
	foldedAgents["agent"+longS] = map[string]any{"defaults": map[string]any{"max_tool_iterations": 1.0}}
	assert.Error(t, checkProtectedAgentDefaultsUnchanged(before, foldedAgents),
		"a folded ancestor that wins the decode is refused")
}

func foldTestDefaults(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	agents, ok := m["agents"].(map[string]any)
	require.True(t, ok)
	defaults, ok := agents["defaults"].(map[string]any)
	require.True(t, ok)
	return defaults
}
