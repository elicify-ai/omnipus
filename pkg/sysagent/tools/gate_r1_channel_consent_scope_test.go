package systools

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// cardFor stores a card value for the channel recipient computed from ch,
// exactly as request_secret records it, and returns its reference.
func cardFor(t *testing.T, store *credentials.Store, id string, ch config.ChannelInstanceConfig, requestID string) string {
	t.Helper()
	recipient, err := channelReferenceRecipient(id, "token", ch)
	require.NoError(t, err)
	receipt, err := store.CommitCard(requestID, "s1", []credentials.CardValue{{
		BaseName: "channel_" + id + "_token", Recipient: recipient, Value: "token-value-" + requestID,
	}}, time.Unix(1, 0))
	require.NoError(t, err)
	require.Len(t, receipt.References, 1)
	return receipt.References[0]
}

// TestConnectChannel_ConsentScopeIsAccountSettingsAndField is Train 2 gate r1
// M5 (security T2-S4; founder ruling 2026-10-20, option A): a channel card's
// consent covers the platform account, its settings and the credential
// field. Binding (workspace, identity) and enabled are routing, approved at
// the connect_channel step. References are validated against the FINAL saved
// entry under that rule.
func TestConnectChannel_ConsentScopeIsAccountSettingsAndField(t *testing.T) {
	newTool := func(t *testing.T) (*ChannelConnectTool, *config.Config, *credentials.Store) {
		tool, cfg := connectRevisionFixture(t, func(*config.Config) {})
		store := credentials.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
		require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x55}, 32)))
		t.Cleanup(store.Close)
		tool.deps.CredStore = store
		return tool, cfg, store
	}
	stub := config.ChannelInstanceConfig{Type: "telegram", BaseURL: "https://api.telegram.org"}

	t.Run("only binding and enabled differ: accepted", func(t *testing.T) {
		tool, cfg, store := newTool(t)
		ref := cardFor(t, store, "telegram", stub, "req-a")
		bound := stub
		bound.WorkspaceID = "sales"
		bound.Identity = &config.ChannelIdentity{Kind: "agent", ID: "mia"}
		bound.Enabled = true
		cfg.Channels = map[string]config.ChannelInstanceConfig{"telegram": bound}
		res := tool.Execute(context.Background(), map[string]any{
			"id": "telegram", "workspace_id": "sales", "agent_id": "mia", "token_ref": ref,
			"expected_revision": channelRevision(cfg, "telegram")})
		require.NotNil(t, res)
		require.NotContains(t, strings.ToLower(res.ForLLM), "nothing saved", "a card recorded before binding stays valid for the bound, enabled entry: %s", res.ForLLM)
		require.Equal(t, ref, cfg.Channels["telegram"].TokenRef, "the reference is selected on the final entry")
	})

	t.Run("account settings differ: refused", func(t *testing.T) {
		tool, cfg, store := newTool(t)
		ref := cardFor(t, store, "telegram", stub, "req-b")
		moved := stub
		moved.BaseURL = "https://other.example"
		cfg.Channels = map[string]config.ChannelInstanceConfig{"telegram": moved}
		res := tool.Execute(context.Background(), map[string]any{
			"id": "telegram", "workspace_id": "sales", "agent_id": "mia", "token_ref": ref,
			"expected_revision": channelRevision(cfg, "telegram")})
		require.NotNil(t, res)
		require.True(t, res.IsError, "a reference recorded for other account settings is refused: %s", res.ForLLM)
		require.Empty(t, cfg.Channels["telegram"].TokenRef, "nothing selected")
	})
}
