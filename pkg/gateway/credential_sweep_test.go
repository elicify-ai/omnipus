// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// credential_sweep_test.go — T068-10 (ADR-068 FR-010 last clause, TDD row
// 10b): the startup sweep of orphaned `<id>_API_KEY` credentials.
//
// BDD "Startup sweep removes an orphaned credential": given
// `credentials.json` holds `groq_API_KEY` and `config.json` has no `groq`
// provider row, boot deletes `groq_API_KEY` with one INFO line and an audit
// entry `provider.credential_swept`, and a name that does not match the
// `<id>_API_KEY` pattern is left untouched. The tests drive
// sweepOrphanedProviderCredentials (the exact function run says boot in
// gateway.go's RunContext, right after config load + store unlock once the
// audit logger exists) directly, mirroring how rest_providers_delete_test.go
// exercises runProviderDelete without booting a gateway.
package gateway

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// newSweepFixture builds an UNLOCKED credential store plus an audit logger,
// both on temp dirs, deterministic via the fixed test master key.
func newSweepFixture(t *testing.T) (*credentials.Store, *audit.Logger, string) {
	t.Helper()
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKeyHex)
	store := credentials.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	require.NoError(t, credentials.Unlock(store))

	auditDir := t.TempDir()
	auditor, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = auditor.Close() })
	return store, auditor, auditDir
}

// sweepCredentialExists reports whether the store still holds name.
func sweepCredentialExists(t *testing.T, store *credentials.Store, name string) bool {
	t.Helper()
	_, err := store.Get(name)
	if err == nil {
		return true
	}
	var nf *credentials.NotFoundError
	require.ErrorAs(t, err, &nf, "unexpected store error probing %q", name)
	return false
}

// TestCredentialSweep_RemovesOrphans is TDD row 10b: an orphaned
// `<id>_API_KEY` is removed at boot with an audit entry; every name that is
// configured, referenced, or does not match the pattern survives.
func TestCredentialSweep_RemovesOrphans(t *testing.T) {
	store, auditor, auditDir := newSweepFixture(t)

	// The BDD orphan: no `groq` provider row exists below.
	require.NoError(t, store.Set("groq_API_KEY", "gsk-orphan-secret"))
	// Configured: its provider row is present — must survive.
	require.NoError(t, store.Set("openrouter_API_KEY", "sk-live-secret"))
	// Non-matching names (BDD last clause) — ALL must survive:
	// an integration ref (UPPERCASE prefix — the Brave search key is not a
	// provider credential even though it ends in _API_KEY),
	require.NoError(t, store.Set("BRAVE_API_KEY", "brave-integration-secret"))
	// the voice-groq integration ref, distinct from the swept groq_API_KEY,
	require.NoError(t, store.Set("GROQ_API_KEY", "voice-groq-secret"))
	// a channel secret (lowercase _api_key suffix, not _API_KEY),
	require.NoError(t, store.Set("channel_telegram_api_key", "tg-secret"))
	// and a bare suffix with an empty id.
	require.NoError(t, store.Set("_API_KEY", "empty-id-secret"))

	cfg := &config.Config{Providers: []*config.ModelConfig{{
		Name: "openrouter", Provider: "openrouter",
		Model: "z-ai/glm-5", APIKeyRef: "openrouter_API_KEY",
	}}}

	sweepOrphanedProviderCredentials(cfg, store, auditor)

	assert.False(t, sweepCredentialExists(t, store, "groq_API_KEY"),
		"orphaned groq_API_KEY must be deleted by the sweep")
	for _, name := range []string{
		"openrouter_API_KEY", "BRAVE_API_KEY", "GROQ_API_KEY",
		"channel_telegram_api_key", "_API_KEY",
	} {
		assert.True(t, sweepCredentialExists(t, store, name),
			"%q must survive the sweep", name)
	}

	require.Equal(t, 1, countAuditEvents(t, auditDir, EventProviderCredentialSwept),
		"exactly one provider.credential_swept entry for the one orphan")
	rec := findAuditEvent(t, auditDir, EventProviderCredentialSwept)
	require.NotNil(t, rec)
	details, ok := rec["details"].(map[string]any)
	require.True(t, ok, "audit entry carries details")
	assert.Equal(t, "groq", details["provider"])
	assert.Equal(t, "groq_API_KEY", details["credential_ref"],
		"audit carries the ref NAME, never the value")
	for k, v := range details {
		s, isString := v.(string)
		assert.False(t, isString && s == "gsk-orphan-secret",
			"audit details %q must never carry the secret value", k)
	}
}

// TestCredentialSweep_RowReferencedRefSurvives covers the belt-and-braces
// keep rule: a name some provider row's api_key_ref points at survives even
// when no row's provider id matches its `<id>` prefix.
func TestCredentialSweep_RowReferencedRefSurvives(t *testing.T) {
	store, auditor, auditDir := newSweepFixture(t)
	require.NoError(t, store.Set("legacy_API_KEY", "sk-referenced"))

	cfg := &config.Config{Providers: []*config.ModelConfig{{
		Name: "custom", Provider: "my-custom",
		Model: "some/model", APIKeyRef: "legacy_API_KEY",
	}}}

	sweepOrphanedProviderCredentials(cfg, store, auditor)

	assert.True(t, sweepCredentialExists(t, store, "legacy_API_KEY"),
		"a ref named by a provider row's api_key_ref must survive")
	assert.Equal(t, 0, countAuditEvents(t, auditDir, EventProviderCredentialSwept))
}

// TestCredentialSweep_CleanStoreNoEffect is the DoD's "no effect on a clean
// store": nothing deleted, nothing audited.
func TestCredentialSweep_CleanStoreNoEffect(t *testing.T) {
	store, auditor, auditDir := newSweepFixture(t)
	require.NoError(t, store.Set("openrouter_API_KEY", "sk-live"))

	cfg := &config.Config{Providers: []*config.ModelConfig{{
		Name: "openrouter", Provider: "openrouter",
		Model: "z-ai/glm-5", APIKeyRef: "openrouter_API_KEY",
	}}}

	sweepOrphanedProviderCredentials(cfg, store, auditor)

	assert.True(t, sweepCredentialExists(t, store, "openrouter_API_KEY"))
	assert.Equal(t, 0, countAuditEvents(t, auditDir, EventProviderCredentialSwept),
		"a clean store produces no audit entries")
}

// TestCredentialSweep_LockedStoreNoOp: a locked store is left alone — the
// sweep never forces an unlock and never errors boot.
func TestCredentialSweep_LockedStoreNoOp(t *testing.T) {
	_, auditor, auditDir := newSweepFixture(t)
	locked := credentials.NewStore(filepath.Join(t.TempDir(), "credentials.json"))

	cfg := &config.Config{}

	// Must not panic and must not audit.
	sweepOrphanedProviderCredentials(cfg, locked, auditor)
	assert.Equal(t, 0, countAuditEvents(t, auditDir, EventProviderCredentialSwept))
}

// TestCredentialSweep_NilInputsNoOp pins the guard clauses: nil config, nil
// store, and nil auditor (audit disabled) never panic; with a nil auditor the
// orphan is still swept.
func TestCredentialSweep_NilInputsNoOp(t *testing.T) {
	store, auditor, _ := newSweepFixture(t)
	require.NoError(t, store.Set("groq_API_KEY", "gsk-orphan"))

	sweepOrphanedProviderCredentials(nil, store, auditor)
	assert.True(t, sweepCredentialExists(t, store, "groq_API_KEY"),
		"nil config must sweep nothing")

	sweepOrphanedProviderCredentials(&config.Config{}, nil, auditor)

	// Nil auditor (sandbox.audit_log disabled): the orphan is still removed.
	sweepOrphanedProviderCredentials(&config.Config{}, store, nil)
	assert.False(t, sweepCredentialExists(t, store, "groq_API_KEY"),
		"nil auditor must not stop the sweep itself")
}
