// Issue #638 gate handoff — pins the two REST-callers of
// ResolveServerHeaderRefs plus the delete/rollback/list surfaces the gate
// flagged (pr-test-analyzer F3 + F5). All expectations derive from the
// documented contracts (issues #638/#437, the handler doc comments), never
// from observed output:
//
//   - POST /api/v1/mcp-servers/{id}/test must resolve HeaderRefs exactly as
//     production reconciliation would (testMCPServer's nil-store branch is
//     fail-closed; a satisfiable ref reaches the connect attempt)
//   - GET /api/v1/mcp-servers must report the UNION of literal-header names
//     (pre-#638 installs) and ref-backed names (everything since) — the edit
//     dialog's pre-fill; values never cross the wire
//   - DELETE must clean the stored header (and env) secrets after the config
//     write confirms
//   - a 409 name-collision (the in-closure race guard) must roll back the
//     just-stored header secrets so a rejected create cannot hijack the
//     winning server's deterministic credential keys
//
// The list/delete/409 tests drive the real HandleMCPServers dispatcher over
// the real on-disk config.json and a real unlocked credential store, so a
// revert of the production wiring change fails them.

package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestMCPServerTest_HeaderRefs_NilStore_ReportsFailClosed pins testMCPServer's
// nil-credential-store branch: a server with HeaderRefs and no usable resolver
// answers success=false with a message naming the header-credential failure —
// never a successful-looking connect without the configured secret.
//
// BDD:
//
//	Given an http MCP server carrying HeaderRefs and a restAPI with NO
//	  credential store wired
//	When POST /api/v1/mcp-servers/{id}/test is called
//	Then the response is success=false with a "header credential reference"
//	  failure message (the connect is never attempted)
func TestMCPServerTest_HeaderRefs_NilStore_ReportsFailClosed(t *testing.T) {
	api, _ := newTestRestAPI(t)
	api.agentLoop.GetConfig().Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"hdr-srv": {
			Enabled: true,
			Type:    "http",
			URL:     "https://127.0.0.1:1/mcp",
			HeaderRefs: map[string]string{
				"Authorization": "mcp_hdr-srv_header_Authorization",
			},
		},
	}

	w := httptest.NewRecorder()
	api.HandleMCPServers(w, httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers/hdr-srv/test", nil))

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var resp gen.McpServerTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.False(t, resp.Success, "body=%s", w.Body.String())
	assert.Contains(t, resp.Message, "header credential reference",
		"issue #638: the Test click must report the unresolvable header ref, not attempt a connection")
}

// TestMCPServerTest_HeaderRefs_StoredSecret_ResolvesToConnectAttempt pins the
// inverse branch: a satisfiable ref (secret in the store) must not fail at
// the credential step — the handler proceeds to the connect attempt, which
// fails against the unreachable URL and is reported as a connection failure.
func TestMCPServerTest_HeaderRefs_StoredSecret_ResolvesToConnectAttempt(t *testing.T) {
	api, store := newTestRestAPIWithHomeAndCredStore(t)
	const refKey = "mcp_hdr-ok_header_Authorization"
	require.NoError(t, store.Set(refKey, "Bearer gwsec-stored-secret"))

	api.agentLoop.GetConfig().Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"hdr-ok": {
			Enabled:    true,
			Type:       "http",
			URL:        "https://127.0.0.1:1/mcp",
			HeaderRefs: map[string]string{"Authorization": refKey},
		},
	}

	w := httptest.NewRecorder()
	api.HandleMCPServers(w, httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers/hdr-ok/test", nil))

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var resp gen.McpServerTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.False(t, resp.Success, "body=%s", w.Body.String())
	assert.Contains(t, resp.Message, "connection failed",
		"the satisfiable ref must resolve and reach the connect attempt")
	assert.NotContains(t, resp.Message, "header credential reference",
		"a resolved ref must not be reported as a credential failure")
}

// TestListMCPServers_HeaderNames_UnionOfLiteralsAndRefs pins the GET /api/v1/
// mcp-servers header-name union (issue #638 / edit pre-fill #437): names from
// BOTH literal Headers and ref-backed HeaderRefs are reported, sorted; the
// values never cross the wire.
func TestListMCPServers_HeaderNames_UnionOfLiteralsAndRefs(t *testing.T) {
	api, _ := newTestRestAPI(t)
	const literalValue = "union-literal-value-sentinel"
	api.agentLoop.GetConfig().Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"union-srv": {
			Enabled:    true,
			Type:       "http",
			URL:        "https://127.0.0.1:1/mcp",
			Headers:    map[string]string{"X-Literal": literalValue},
			HeaderRefs: map[string]string{"Authorization": "mcp_union-srv_header_Authorization"},
		},
	}

	w := httptest.NewRecorder()
	api.HandleMCPServers(w, httptest.NewRequest(http.MethodGet, "/api/v1/mcp-servers", nil))

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var got []gen.McpServer
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got, 1)
	require.NotNil(t, got[0].HeaderNames, "issue #638: a ref-backed server must surface its header names")
	assert.Equal(t, []string{"Authorization", "X-Literal"}, *got[0].HeaderNames,
		"header_names must be the sorted union of literal and ref-backed names")
	assert.NotContains(t, w.Body.String(), literalValue,
		"header VALUES must never cross the wire in the list response")
}

// TestDeleteMCPServer_HeaderRefSecrets_RemovedFromStore pins deleteMCPServer's
// post-config-write credential cleanup (issue #638, gate SF3): the server's
// ref-backed header secrets (and its env secrets, the pre-existing mirror)
// must not sit orphaned in the credential store after a successful delete.
func TestDeleteMCPServer_HeaderRefSecrets_RemovedFromStore(t *testing.T) {
	api, store := newTestRestAPIWithHomeAndCredStore(t)
	env := map[string]string{"API_TOKEN": "delete-env-secret-sentinel"}
	headers := mcpSecretHeaders()
	body := gen.McpServerCreate{
		Name:      "delete-dst",
		Transport: gen.McpServerCreateTransportSse,
		Url:       gwsecStrPtr("https://127.0.0.1:1/sse"),
		Env:       &env,
		Headers:   &headers,
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "seed POST must create the server; body=%s", w.Body.String())

	for _, secret := range append([]string{"delete-env-secret-sentinel"}, mcpSecretValues()...) {
		require.True(t, credentialStoreHolds(t, store, secret),
			"seed: secret %q must be in the store before the delete", secret)
	}

	wd := httptest.NewRecorder()
	api.HandleMCPServers(wd, httptest.NewRequest(http.MethodDelete, "/api/v1/mcp-servers/delete-dst", nil))
	require.Equal(t, http.StatusOK, wd.Code, "body=%s", wd.Body.String())

	for _, secret := range append([]string{"delete-env-secret-sentinel"}, mcpSecretValues()...) {
		assert.False(t, credentialStoreHolds(t, store, secret),
			"issue #638: secret %q must be removed from the store by the delete", secret)
	}
	assert.Nil(t, readPersistedMCPServer(t, api.homePath, "delete-dst"),
		"the deleted server must be gone from config.json")
}

// gwsecStrPtr is a small local *string helper (the package has no shared one
// with a compatible name).
func gwsecStrPtr(s string) *string { return &s }

// mcpSecretValues returns the raw secret values behind mcpSecretHeaders().
func mcpSecretValues() []string {
	return []string{mcpAuthSecret, mcpProxySecret, mcpCookieSecret}
}

// TestAddMCPServer_NameCollisionRace_RollsBackHeaderSecrets pins the 409
// race-guard rollback (issue #638, gate SF1): when the authoritative in-
// closure collision check fires (the on-disk config holds the name but the
// pre-check's live snapshot did not), the just-stored header and env secrets
// must be rolled back — a rejected create must not leave its secrets stored
// under the winning server's deterministic credential keys.
func TestAddMCPServer_NameCollisionRace_RollsBackHeaderSecrets(t *testing.T) {
	api, store := newTestRestAPIWithHomeAndCredStore(t)

	// The live agent-loop config does NOT know the server, but the on-disk
	// config.json DOES — the pre-check (live snapshot) passes and the
	// authoritative in-closure check fires, which is the rollback path.
	cfgPath := filepath.Join(api.homePath, "config.json")
	rawOnDisk, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var disk map[string]any
	require.NoError(t, json.Unmarshal(rawOnDisk, &disk))
	tools := map[string]any{"mcp": map[string]any{
		"servers": map[string]any{
			"race-srv": map[string]any{"enabled": true, "type": "sse", "url": "https://127.0.0.1:1/sse"},
		},
	}}
	disk["tools"] = tools
	out, mErr := json.Marshal(disk)
	require.NoError(t, mErr)
	require.NoError(t, os.WriteFile(cfgPath, out, 0o600))

	const raceSecret = "gwsec-638-race-rollback-sentinel"
	headers := map[string]string{"Authorization": "Bearer " + raceSecret}
	env := map[string]string{"API_TOKEN": raceSecret}
	body := gen.McpServerCreate{
		Name:      "race-srv",
		Transport: gen.McpServerCreateTransportSse,
		Url:       gwsecStrPtr("https://127.0.0.1:1/sse"),
		Env:       &env,
		Headers:   &headers,
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(w, r)

	require.Equal(t, http.StatusConflict, w.Code,
		"the in-closure collision must answer 409; body=%s", w.Body.String())
	assert.False(t, credentialStoreHolds(t, store, raceSecret),
		"issue #638: the rejected create must roll back its stored header/env secrets")
	// The winning entry on disk is untouched by the failed create.
	assert.NotNil(t, readPersistedMCPServer(t, api.homePath, "race-srv"))
}
