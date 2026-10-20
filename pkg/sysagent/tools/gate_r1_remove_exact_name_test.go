package systools

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// TestRemoveSecret_RemovesExactlyTheListedName is Train 2 gate r1 M4
// (security T2-S6; founder: fix now): Settings can store distinct keys that
// differ only by surrounding spaces, and list_secrets shows them verbatim.
// remove_secret must delete exactly the reference_name it was approved for,
// never a trimmed sibling.
func TestRemoveSecret_RemovesExactlyTheListedName(t *testing.T) {
	for name, target := range map[string]string{
		"both sides":    " SERVICE_TOKEN ",
		"leading only":  " SERVICE_TOKEN",
		"trailing only": "SERVICE_TOKEN ",
	} {
		t.Run(name, func(t *testing.T) {
			store := credentials.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
			require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x71}, 32)))
			t.Cleanup(store.Close)
			require.NoError(t, store.Set("SERVICE_TOKEN", "unspaced-sibling-value"))
			require.NoError(t, store.Set(target, "spaced-target-value"))
			cfg := &config.Config{}
			tool := NewRemoveSecretTool(&Deps{CredStore: store, GetCfg: func() *config.Config { return cfg }})

			res := tool.Execute(context.Background(), map[string]any{"reference_name": target})
			require.False(t, res.IsError, res.ForLLM)
			names, err := store.List()
			require.NoError(t, err)
			require.Contains(t, names, "SERVICE_TOKEN", "the unspaced sibling must survive")
			require.NotContains(t, names, target, "exactly the approved name is removed")
		})
	}

	t.Run("absent spaced name does not fall through to the sibling", func(t *testing.T) {
		store := credentials.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
		require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x72}, 32)))
		t.Cleanup(store.Close)
		require.NoError(t, store.Set("SERVICE_TOKEN", "unspaced-sibling-value"))
		cfg := &config.Config{}
		tool := NewRemoveSecretTool(&Deps{CredStore: store, GetCfg: func() *config.Config { return cfg }})
		res := tool.Execute(context.Background(), map[string]any{"reference_name": " SERVICE_TOKEN "})
		require.True(t, res.IsError, "an absent name is not found: %s", res.ForLLM)
		names, err := store.List()
		require.NoError(t, err)
		require.Contains(t, names, "SERVICE_TOKEN", "the sibling is untouched")
	})
}
