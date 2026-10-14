package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/stretchr/testify/require"
)

// Chief round-3 F2, 2026-10-14: reject store failure at actual reload/refresh
// boundaries even when ResolveAll cannot see a provider/channel/mailbox ref.
func issue1277R3RequireRejectedUpdate(t *testing.T, mcpOnly bool, fault, boundary string) {
	t.Helper()
	f := newIssue1277R3Fixture(t, mcpOnly)
	cfg, bundle, store := f.boot(t)
	t.Cleanup(store.Close)
	runtime := f.startRuntimeState(t, cfg, bundle)
	if mcpOnly {
		issue1277R3ConnectMCP(t, runtime)
	}
	before := runtime.api.agentLoop.GetConfig()
	beforeManager := runtime.services.ChannelManager
	beforeLevel := logger.GetLevel()
	f.cfg.Gateway.LogLevel = "debug"
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	candidate, err := config.LoadConfigWithStore(f.configPath, f.store)
	require.NoError(t, err, "instrument: the proposed config is valid before the file fault")
	issue1277R3RequireRefBoundary(t, candidate, mcpOnly)
	brokenFile := issue1277R3BreakRunningStore(t, f, fault)

	switch boundary {
	case "reload":
		err = executeReload(t.Context(), runtime.api.agentLoop, candidate, &runtime.provider,
			runtime.services, runtime.bus, false)
	case "refresh":
		err = runtime.api.refreshConfigAndRewireServices(f.configPath)
	default:
		t.Fatalf("unknown round-3 update boundary %q", boundary)
	}
	issue1277R3RequireStoreFailure(t, err, fault)
	require.Same(t, before, runtime.api.agentLoop.GetConfig(), "store rejection must keep the previous live config")
	require.Equal(t, "info", runtime.api.agentLoop.GetConfig().Gateway.LogLevel)
	require.Equal(t, beforeLevel, logger.GetLevel(), "refused settings must not change the active log level")
	require.Same(t, beforeManager, runtime.services.ChannelManager, "rejection must not replace the live channel manager")
	if boundary == "reload" {
		degraded, reason := runtime.services.reloadDegradedStatus()
		require.True(t, degraded, "the rejected reload must be reported as an operation failure")
		require.Contains(t, reason, "store")
	} else {
		issue1277RequireVisibleRefreshRefusal(t, runtime.api)
		require.Same(t, before, runtime.api.agentLoop.GetConfig())
		require.Equal(t, "info", runtime.api.agentLoop.GetConfig().Gateway.LogLevel)
		require.Equal(t, beforeLevel, logger.GetLevel())
	}
	if mcpOnly {
		issue1277R3RequireConnectedMCP(t, runtime.api)
	}
	after, readErr := os.ReadFile(filepath.Join(f.home, "credentials.json"))
	require.NoError(t, readErr)
	require.Equal(t, brokenFile, after, "rejecting an update must not overwrite the broken credential file")
}

func TestIssue1277_MCPOnlyWrongKeyReloadRejectsAndKeepsLiveConfig(t *testing.T) {
	issue1277R3RequireRejectedUpdate(t, true, issue1277R3WrongKey, "reload")
}

func TestIssue1277_MCPOnlyCorruptStoreReloadRejectsAndKeepsLiveConfig(t *testing.T) {
	issue1277R3RequireRejectedUpdate(t, true, issue1277R3Corrupt, "reload")
}

func TestIssue1277_MCPOnlyWrongKeyRefreshRejectsAndKeepsLiveConfig(t *testing.T) {
	issue1277R3RequireRejectedUpdate(t, true, issue1277R3WrongKey, "refresh")
}

func TestIssue1277_MCPOnlyCorruptStoreRefreshRejectsAndKeepsLiveConfig(t *testing.T) {
	issue1277R3RequireRejectedUpdate(t, true, issue1277R3Corrupt, "refresh")
}

func TestIssue1277_NoRefsWrongKeyReloadRejectsAndKeepsLiveConfig(t *testing.T) {
	issue1277R3RequireRejectedUpdate(t, false, issue1277R3WrongKey, "reload")
}

func TestIssue1277_NoRefsCorruptStoreReloadRejectsAndKeepsLiveConfig(t *testing.T) {
	issue1277R3RequireRejectedUpdate(t, false, issue1277R3Corrupt, "reload")
}

func TestIssue1277_NoRefsWrongKeyRefreshRejectsAndKeepsLiveConfig(t *testing.T) {
	issue1277R3RequireRejectedUpdate(t, false, issue1277R3WrongKey, "refresh")
}

func TestIssue1277_NoRefsCorruptStoreRefreshRejectsAndKeepsLiveConfig(t *testing.T) {
	issue1277R3RequireRejectedUpdate(t, false, issue1277R3Corrupt, "refresh")
}

// Positive instrument: the same MCP-only config/runtime and candidate setting
// can pass the actual refresh/reload pipeline when the store really is readable.
func TestIssue1277_MCPOnlyHealthyStoreAppliesRefreshAndReload(t *testing.T) {
	f := newIssue1277R3Fixture(t, true)
	cfg, bundle, store := f.boot(t)
	t.Cleanup(store.Close)
	runtime := f.startRuntimeState(t, cfg, bundle)
	issue1277R3ConnectMCP(t, runtime)
	before := runtime.api.agentLoop.GetConfig()
	f.cfg.Gateway.LogLevel = "debug"
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	require.NoError(t, runtime.api.refreshConfigAndRewireServices(f.configPath))
	require.NotSame(t, before, runtime.api.agentLoop.GetConfig())
	require.Equal(t, "debug", runtime.api.agentLoop.GetConfig().Gateway.LogLevel)
	f.cfg.Gateway.LogLevel = "info"
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	candidate, err := config.LoadConfigWithStore(f.configPath, f.store)
	require.NoError(t, err)
	require.NoError(t, executeReload(t.Context(), runtime.api.agentLoop, candidate, &runtime.provider,
		runtime.services, runtime.bus, false))
	require.Equal(t, "info", runtime.api.agentLoop.GetConfig().Gateway.LogLevel)
	degraded, reason := runtime.services.reloadDegradedStatus()
	require.False(t, degraded)
	require.Empty(t, reason)
	require.NoError(t, runtime.api.agentLoop.ReconcileMCP(t.Context()))
	issue1277R3RequireConnectedMCP(t, runtime.api)
}
