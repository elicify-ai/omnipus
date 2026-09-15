// Tests for the MCP-servers REST credential-store migration (post-review
// fix wave): addMCPServer/patchMCPServer now route env secrets through the
// encrypted credential store the same way add_mcp_server (the sysagent
// tool) already does, listMCPServers reports env_keys for a ref-backed
// server, and deleteMCPServer cleans up the credential-store entries it
// left behind.

package gateway

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/require"
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
