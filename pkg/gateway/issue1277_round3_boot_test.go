package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/require"
)

// Chief round-3 F2, 2026-10-14; ADR-004 Failure Semantics: reference kinds
// cannot decide whether a genuine whole-store failure is fatal.
func issue1277R3RequireFatalBoot(t *testing.T, mcpOnly bool, fault string) {
	t.Helper()
	f := newIssue1277R3Fixture(t, mcpOnly)
	before := issue1277R3BreakBootStore(t, f, fault)
	cfg, bundle, store, err := bootCredentials(f.home, f.configPath)
	if store != nil {
		t.Cleanup(store.Close)
	}
	issue1277R3RequireStoreFailure(t, err, fault)
	require.Nil(t, cfg, "store-level failure must not return a usable gateway config")
	require.Nil(t, bundle)
	require.Nil(t, store)
	after, readErr := os.ReadFile(filepath.Join(f.home, "credentials.json"))
	require.NoError(t, readErr)
	require.Equal(t, before, after, "refusal must not overwrite the unreadable store")
}

func TestIssue1277_MCPOnlyWrongMasterKeyIsBootFatal(t *testing.T) {
	issue1277R3RequireFatalBoot(t, true, issue1277R3WrongKey)
}

func TestIssue1277_MCPOnlyCorruptStoreIsBootFatal(t *testing.T) {
	issue1277R3RequireFatalBoot(t, true, issue1277R3Corrupt)
}

func TestIssue1277_NoRefsWrongMasterKeyWithNonemptyStoreIsBootFatal(t *testing.T) {
	issue1277R3RequireFatalBoot(t, false, issue1277R3WrongKey)
}

func TestIssue1277_NoRefsPresentCorruptStoreIsBootFatal(t *testing.T) {
	issue1277R3RequireFatalBoot(t, false, issue1277R3Corrupt)
}

func TestIssue1277_MCPOnlyRightKeyBootsAndConnects(t *testing.T) {
	f := newIssue1277R3Fixture(t, true)
	cfg, bundle, store := f.boot(t)
	t.Cleanup(store.Close)
	issue1277R3RequireRefBoundary(t, cfg, true)
	value, err := store.Get(issue1277R3Ref)
	require.NoError(t, err)
	require.Equal(t, issue1277R3Value, value)
	runtime := f.startRuntimeState(t, cfg, bundle)
	issue1277R3ConnectMCP(t, runtime)
	require.Empty(t, runtime.services.ChannelManager.FailedChannels())
}

func TestIssue1277_MCPOnlyMissingEntryDegradesOnlyThatServer(t *testing.T) {
	f := newIssue1277R3Fixture(t, true)
	f.cfg.Tools.MCP.Servers["qa1277-r3-missing"] = config.MCPServerConfig{
		Enabled: true, Type: "http", URL: f.cfg.Tools.MCP.Servers[issue1277R3Server].URL,
		EnvRefs: map[string]string{"TOKEN": issue1277R3MissingRef},
	}
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	_, missingErr := f.store.Get(issue1277R3MissingRef)
	var missing *credentials.NotFoundError
	require.ErrorAs(t, missingErr, &missing)
	require.Equal(t, issue1277R3MissingRef, missing.Name)
	cfg, bundle, store := f.boot(t)
	t.Cleanup(store.Close)
	runtime := f.startRuntimeState(t, cfg, bundle)
	issue1277R3ConnectMCP(t, runtime)
	status, count, reason := runtime.api.agentLoop.MCPServerStatus("qa1277-r3-missing")
	require.Equal(t, "error", status)
	require.Zero(t, count)
	require.Contains(t, reason, issue1277R3MissingRef)
	require.NotContains(t, reason, issue1277R3Value)
	require.True(t, runtime.api.agentLoop.GetConfig().Tools.MCP.Servers["qa1277-r3-missing"].Enabled)
	require.Empty(t, runtime.services.ChannelManager.FailedChannels())
}

func TestIssue1277_NoRefsAbsentStoreBootsNormally(t *testing.T) {
	f := newIssue1277R3Fixture(t, false)
	require.NoError(t, os.Remove(filepath.Join(f.home, "credentials.json")))
	require.False(t, f.store.Exists(), "instrument: no store file exists")
	cfg, bundle, store, err := bootCredentials(f.home, f.configPath)
	require.NoError(t, err, "reference-free fresh install is not a corrupt-store failure")
	require.NotNil(t, cfg)
	require.NotNil(t, store)
	t.Cleanup(store.Close)
	require.Empty(t, bundle)
	issue1277R3RequireRefBoundary(t, cfg, false)
	require.False(t, store.Exists(), "boot must not manufacture a store failure for an absent file")
}
