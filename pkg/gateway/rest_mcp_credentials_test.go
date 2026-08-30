// Tests for the MCP-servers REST credential-store migration (post-review
// fix wave): addMCPServer/patchMCPServer now route env secrets through the
// encrypted credential store the same way add_mcp_server (the sysagent
// tool) already does, listMCPServers reports env_keys for a ref-backed
// server, and deleteMCPServer cleans up the credential-store entries it
// left behind.

package gateway

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// newTestRestAPIWithHomeAndCredStore builds on newTestRestAPIWithHome and
// additionally wires an unlocked, in-memory-backed credential store (a
// deterministic random key rather than a real passphrase, so the test is
// hermetic and fast — mirrors newTestDepsWithCredStore in
// pkg/sysagent/tools/channel_impl_test.go).
func newTestRestAPIWithHomeAndCredStore(t *testing.T) (*restAPI, *credentials.Store) {
	t.Helper()
	api := newTestRestAPIWithHome(t)
	store := credentials.NewStore(filepath.Join(api.homePath, "credentials.json"))
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	require.NoError(t, store.UnlockWithKey(key))
	api.credStore = store
	return api, store
}

// readPersistedMCPServer reads config.json and returns the raw map[string]any
// entry for tools.mcp.servers[name] (nil if absent).
func readPersistedMCPServer(t *testing.T, homePath, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(homePath, "config.json"))
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(data, &cfg))
	tools, _ := cfg["tools"].(map[string]any)
	if tools == nil {
		return nil
	}
	mcp, _ := tools["mcp"].(map[string]any)
	if mcp == nil {
		return nil
	}
	servers, _ := mcp["servers"].(map[string]any)
	if servers == nil {
		return nil
	}
	entry, _ := servers[name].(map[string]any)
	return entry
}

// TestAddMCPServer_EnvRoutedThroughCredentialStore is regression test (b):
// POSTing a server with a plaintext env value must never persist that value
// to config.json — it must land as a credential-store reference (env_refs),
// exactly like add_mcp_server (the sysagent tool), and the ref must resolve
// back to the real value in the credential store.
func TestAddMCPServer_EnvRoutedThroughCredentialStore(t *testing.T) {
	api, store := newTestRestAPIWithHomeAndCredStore(t)

	cmd := "/nonexistent-mcp-test-bin"
	body := gen.McpServerCreate{
		Name:      "github",
		Transport: gen.McpServerCreateTransportStdio,
		Command:   &cmd,
		Env:       &map[string]string{"GITHUB_TOKEN": "super-secret-value"},
	}
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(bodyBytes))
	r.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "POST must return 201: %s", w.Body.String())

	entry := readPersistedMCPServer(t, api.homePath, "github")
	require.NotNil(t, entry, "github entry must be persisted")

	// The plaintext secret must NEVER appear anywhere in the persisted config.
	raw, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "super-secret-value",
		"plaintext env secret must never be written to config.json")

	// "env" must be absent/empty; "env_refs" must carry a credential-store ref.
	if envField, ok := entry["env"]; ok {
		envMap, _ := envField.(map[string]any)
		assert.Empty(t, envMap, "env must not contain the literal secret")
	}
	envRefs, _ := entry["env_refs"].(map[string]any)
	require.NotEmpty(t, envRefs, "env_refs must be populated")
	ref, _ := envRefs["GITHUB_TOKEN"].(string)
	require.NotEmpty(t, ref, "want a credential-store ref for GITHUB_TOKEN")

	got, err := store.Get(ref)
	require.NoError(t, err)
	assert.Equal(t, "super-secret-value", got, "credential store must hold the real value")
}

// TestPatchMCPServer_EnvRoutedThroughCredentialStore is the PATCH half of
// regression test (b): PATCHing a plaintext env value onto an existing
// server must also route it through the credential store rather than
// writing plaintext, including when the server already has a ref-backed
// EnvRefs entry for a DIFFERENT key (must be left untouched) and when the
// new value collides with an existing ref-backed key (new literal value
// wins, per add_mcp_server's own documented collision behavior).
func TestPatchMCPServer_EnvRoutedThroughCredentialStore(t *testing.T) {
	api, store := newTestRestAPIWithHomeAndCredStore(t)

	// Seed via POST with one ref-backed key (OTHER_KEY).
	cmd := "/nonexistent-mcp-test-bin"
	addBody := gen.McpServerCreate{
		Name:      "patch-env-srv",
		Transport: gen.McpServerCreateTransportStdio,
		Command:   &cmd,
		Env:       &map[string]string{"OTHER_KEY": "other-value"},
	}
	addBytes, err := json.Marshal(addBody)
	require.NoError(t, err)
	wAdd := httptest.NewRecorder()
	rAdd := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(addBytes))
	rAdd.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(wAdd, rAdd)
	require.Equal(t, http.StatusCreated, wAdd.Code, "seed POST must return 201: %s", wAdd.Body.String())

	entry := readPersistedMCPServer(t, api.homePath, "patch-env-srv")
	envRefs, _ := entry["env_refs"].(map[string]any)
	otherRef, _ := envRefs["OTHER_KEY"].(string)
	require.NotEmpty(t, otherRef, "seed OTHER_KEY must be ref-backed")

	// PATCH a new literal env value for a NEW key.
	patchBody := gen.McpServerUpdate{
		Env: &map[string]string{"NEW_KEY": "new-plaintext-value"},
	}
	patchBytes, err := json.Marshal(patchBody)
	require.NoError(t, err)
	wPatch := httptest.NewRecorder()
	rPatch := httptest.NewRequest(http.MethodPatch, "/api/v1/mcp-servers/patch-env-srv", bytes.NewReader(patchBytes))
	rPatch.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(wPatch, rPatch)
	require.Equal(t, http.StatusOK, wPatch.Code, "PATCH must return 200: %s", wPatch.Body.String())

	// The plaintext value must never appear in config.json.
	raw, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "new-plaintext-value",
		"PATCHed plaintext env value must never be written to config.json")

	entry = readPersistedMCPServer(t, api.homePath, "patch-env-srv")
	envRefs, _ = entry["env_refs"].(map[string]any)
	require.NotEmpty(t, envRefs)

	// The pre-existing OTHER_KEY ref must be untouched.
	assert.Equal(t, otherRef, envRefs["OTHER_KEY"], "unrelated pre-existing ref must be left alone")
	otherVal, err := store.Get(otherRef)
	require.NoError(t, err)
	assert.Equal(t, "other-value", otherVal, "unrelated credential must still hold its original value")

	// The new key must be ref-backed, resolving to the real value.
	newRef, _ := envRefs["NEW_KEY"].(string)
	require.NotEmpty(t, newRef, "NEW_KEY must be ref-backed after PATCH")
	newVal, err := store.Get(newRef)
	require.NoError(t, err)
	assert.Equal(t, "new-plaintext-value", newVal)

	// --- Collision case: PATCH again with a new value for the SAME key
	// (OTHER_KEY) that was already ref-backed. The new literal value must
	// win — add_mcp_server's own description promises this behavior.
	collideBody := gen.McpServerUpdate{
		Env: &map[string]string{"OTHER_KEY": "rotated-value"},
	}
	collideBytes, err := json.Marshal(collideBody)
	require.NoError(t, err)
	wCollide := httptest.NewRecorder()
	rCollide := httptest.NewRequest(http.MethodPatch, "/api/v1/mcp-servers/patch-env-srv", bytes.NewReader(collideBytes))
	rCollide.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(wCollide, rCollide)
	require.Equal(t, http.StatusOK, wCollide.Code, "collision PATCH must return 200: %s", wCollide.Body.String())

	entry = readPersistedMCPServer(t, api.homePath, "patch-env-srv")
	envRefs, _ = entry["env_refs"].(map[string]any)
	rotatedRef, _ := envRefs["OTHER_KEY"].(string)
	require.NotEmpty(t, rotatedRef)
	rotatedVal, err := store.Get(rotatedRef)
	require.NoError(t, err)
	assert.Equal(t, "rotated-value", rotatedVal, "the new literal value must win over the stale ref")
}

// TestListMCPServers_ReportsEnvKeysForRefBackedServer is regression test
// (c): a server whose secrets only ever populated EnvRefs (the
// add_mcp_server / addMCPServer path) must still be reported as having
// environment configuration by GET /api/v1/mcp-servers — listMCPServers must
// union env_keys from both srv.Env (legacy plaintext) and srv.EnvRefs.
func TestListMCPServers_ReportsEnvKeysForRefBackedServer(t *testing.T) {
	api, _ := newTestRestAPIWithHomeAndCredStore(t)

	cmd := "/nonexistent-mcp-test-bin"
	body := gen.McpServerCreate{
		Name:      "ref-only-srv",
		Transport: gen.McpServerCreateTransportStdio,
		Command:   &cmd,
		Env:       &map[string]string{"API_KEY": "abc123"},
	}
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)
	wAdd := httptest.NewRecorder()
	rAdd := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(bodyBytes))
	rAdd.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(wAdd, rAdd)
	require.Equal(t, http.StatusCreated, wAdd.Code, "POST must return 201: %s", wAdd.Body.String())

	wList := httptest.NewRecorder()
	rList := httptest.NewRequest(http.MethodGet, "/api/v1/mcp-servers", nil)
	api.HandleMCPServers(wList, rList)
	require.Equal(t, http.StatusOK, wList.Code)

	var servers []gen.McpServer
	require.NoError(t, json.Unmarshal(wList.Body.Bytes(), &servers))
	require.Len(t, servers, 1)
	require.NotNil(t, servers[0].EnvKeys, "want EnvKeys populated for a ref-backed server (BUG 2 regression)")
	assert.Equal(t, []string{"API_KEY"}, *servers[0].EnvKeys)

	// The response must never leak the actual secret value.
	assert.NotContains(t, wList.Body.String(), "abc123")
}

// TestDeleteMCPServer_CleansUpCredentialStoreEntries is regression test (d):
// DELETE must clean up the credential-store entries an EnvRefs-backed
// server owns — mirroring remove_mcp_server's (the sysagent tool) own
// cleanup — rather than orphaning them permanently.
func TestDeleteMCPServer_CleansUpCredentialStoreEntries(t *testing.T) {
	api, store := newTestRestAPIWithHomeAndCredStore(t)

	cmd := "/nonexistent-mcp-test-bin"
	body := gen.McpServerCreate{
		Name:      "to-delete",
		Transport: gen.McpServerCreateTransportStdio,
		Command:   &cmd,
		Env:       &map[string]string{"SECRET": "to-be-deleted"},
	}
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)
	wAdd := httptest.NewRecorder()
	rAdd := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(bodyBytes))
	rAdd.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(wAdd, rAdd)
	require.Equal(t, http.StatusCreated, wAdd.Code, "POST must return 201: %s", wAdd.Body.String())

	entry := readPersistedMCPServer(t, api.homePath, "to-delete")
	envRefs, _ := entry["env_refs"].(map[string]any)
	ref, _ := envRefs["SECRET"].(string)
	require.NotEmpty(t, ref, "want a credential ref for SECRET")

	// Sanity: the credential is readable before delete.
	_, err = store.Get(ref)
	require.NoError(t, err, "credential must exist before delete")

	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest(http.MethodDelete, "/api/v1/mcp-servers/to-delete", nil)
	api.HandleMCPServers(wDel, rDel)
	require.Equal(t, http.StatusOK, wDel.Code, "DELETE must return 200: %s", wDel.Body.String())

	require.Nil(t, readPersistedMCPServer(t, api.homePath, "to-delete"), "server must be gone from config")

	_, err = store.Get(ref)
	assert.Error(t, err, "credential-store entry must be deleted after DELETE (was orphaned pre-fix)")
}

// TestAddMCPServer_NameCollisionDoesNotHijackExistingCredentials mirrors the
// sysagent-tool regression test for BUG 1, but against the REST path: POSTing
// a duplicate name with a new env value must never overwrite the existing
// server's credential-store entry, even though the name collision is only
// confirmed inside the config write.
func TestAddMCPServer_NameCollisionDoesNotHijackExistingCredentials(t *testing.T) {
	api, store := newTestRestAPIWithHomeAndCredStore(t)

	cmd := "/nonexistent-mcp-test-bin"
	seedBody := gen.McpServerCreate{
		Name:      "github",
		Transport: gen.McpServerCreateTransportStdio,
		Command:   &cmd,
		Env:       &map[string]string{"GITHUB_TOKEN": "real-token"},
	}
	seedBytes, err := json.Marshal(seedBody)
	require.NoError(t, err)
	wSeed := httptest.NewRecorder()
	rSeed := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(seedBytes))
	rSeed.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(wSeed, rSeed)
	require.Equal(t, http.StatusCreated, wSeed.Code)

	entry := readPersistedMCPServer(t, api.homePath, "github")
	envRefs, _ := entry["env_refs"].(map[string]any)
	realRef, _ := envRefs["GITHUB_TOKEN"].(string)
	require.NotEmpty(t, realRef)

	// Colliding POST with a wrong token.
	collideBody := gen.McpServerCreate{
		Name:      "github",
		Transport: gen.McpServerCreateTransportStdio,
		Command:   &cmd,
		Env:       &map[string]string{"GITHUB_TOKEN": "wrong"},
	}
	collideBytes, err := json.Marshal(collideBody)
	require.NoError(t, err)
	wCollide := httptest.NewRecorder()
	rCollide := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(collideBytes))
	rCollide.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(wCollide, rCollide)
	require.Equal(t, http.StatusConflict, wCollide.Code,
		"colliding POST must return 409: %s", wCollide.Body.String())

	gotValue, err := store.Get(realRef)
	require.NoError(t, err)
	assert.Equal(t, "real-token", gotValue,
		"SECURITY REGRESSION: existing credential must be unchanged by a rejected colliding POST")
}
