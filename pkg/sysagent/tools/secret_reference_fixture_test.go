package systools

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// IssueCardReferencesForTest replaces ONLY a legacy test's plaintext setup.
// It publishes real bound versions before Execute; the tool cannot create or
// overwrite these values. It is not a substitute for the separate real
// create -> card -> Submit -> bounded attachment acceptance journey.
func IssueCardReferencesForTest(t *testing.T, d *Deps, in secretrequest.Input, values ...string) []string {
	t.Helper()
	return issueCardReferencesForConfig(t, d.CredStore, d.GetCfg(), in, values...)
}

func issueCardReferencesForConfig(t *testing.T, store *credentials.Store, cfg *config.Config, in secretrequest.Input, values ...string) []string {
	t.Helper()
	recipients, _, _, err := secretrequest.ResolveRecipient(cfg, in)
	require.NoError(t, err, "fixture must resolve the actual recipient before selection")
	require.Len(t, recipients, len(values))
	items := make([]credentials.CardValue, 0, len(values))
	for i, recipient := range recipients {
		items = append(items, credentials.CardValue{BaseName: secretrequest.ReferenceBase(recipient), Recipient: recipient, Value: values[i]})
	}
	receipt, err := store.CommitCard(uuid.NewString(), "wu4b-fixture-session", items, time.Unix(1_800_000_000, 0))
	require.NoError(t, err)
	metadata, err := store.CardSecretMetadata()
	require.NoError(t, err)
	for i, ref := range receipt.References {
		require.Equal(t, recipients[i], metadata[ref].Recipient, "test setup has a real stored recipient binding")
		value, readErr := store.Get(ref)
		require.NoError(t, readErr)
		require.Equal(t, values[i], value)
	}
	return receipt.References
}

// IssueProviderReferenceForTest binds to the confirmed effective provider
// descriptor, including a proposed valid api_base. Only the cloned setup is
// changed; the live config, its revision and its consumer remain untouched.
// Malformed api_base/unknown-provider tests use a valid control reference and
// still assert the original syntax/catalogue refusal at the tool boundary.
func IssueProviderReferenceForTest(t *testing.T, d *Deps, name, apiBase, value string) string {
	t.Helper()
	raw, err := json.Marshal(d.GetCfg())
	require.NoError(t, err)
	var cfg config.Config
	require.NoError(t, json.Unmarshal(raw, &cfg))
	provider := name
	u, parseErr := url.Parse(apiBase)
	if parseErr == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") {
		found := false
		for _, row := range cfg.Providers {
			if row != nil && row.Provider == provider {
				row.APIBase = apiBase
				found = true
			}
		}
		if !found {
			cfg.Providers = append(cfg.Providers, &config.ModelConfig{Provider: provider, APIBase: apiBase})
		}
	}
	return issueCardReferencesForConfig(t, d.CredStore, &cfg, secretrequest.Input{Provider: provider}, value)[0]
}
