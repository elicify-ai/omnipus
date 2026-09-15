// Regression tests for BUG 4 (architect finding, post-review fix wave):
// buildEnabledRefMap enumerated channels/voice/web-search/marketplaces/
// mailboxes for the sensitive-value scrubber (SensitiveDataReplacer via
// cfg.RegisterSensitiveValues) but had ZERO MCP references, so a resolved
// MCP server env secret was never registered and so never scrubbed from LLM
// output, audit logs, or task evidence. mcpEnabledEnvSensitiveValues (added
// alongside buildEnabledRefMap in gateway.go) closes that gap; these tests
// exercise it directly against a real (in-memory-keyed) credential store.

package gateway

import (
	"crypto/rand"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/require"
)

// newUnlockedTestCredStore returns a credential store unlocked with a
// deterministic random key — hermetic and fast, no passphrase/Argon2id
// overhead (mirrors newTestRestAPIWithHomeAndCredStore).
func newUnlockedTestCredStore(t *testing.T) *credentials.Store {
	t.Helper()
	store := credentials.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	require.NoError(t, store.UnlockWithKey(key))
	return store
}
