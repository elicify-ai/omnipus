// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for final-gate S3/S1 finding M6: the ONE shared session store has one
// key, counted owners, and an open failure is an error - never an empty registry
// with a log line, never a panic mid-construction.

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

func s3StoreConfig(home string) *config.Config {
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              home,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: home}},
		},
	}
}

// s3UnopenableHome returns a workspace path whose parent is a regular file, so
// the shared store directory (<parent>/sessions) cannot be created.
func s3UnopenableHome(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	return filepath.Join(file, "workspace")
}

// Two loops over one home share one store; it closes (and is forgotten) only
// when the last owner releases, and a second release of the same store is a no-op.
func TestSharedSessionStore_ClosesOnlyWhenTheLastOwnerReleases(t *testing.T) {
	home := t.TempDir()
	first, err := acquireSharedSessionStore(home)
	require.NoError(t, err)
	second, err := acquireSharedSessionStore(home)
	require.NoError(t, err)
	require.Same(t, first, second)

	require.NoError(t, releaseSharedSessionStore(first))
	again, err := openSharedSessionStore(home)
	require.NoError(t, err)
	require.Same(t, first, again, "one owner remains: the store must stay registered and open")

	require.NoError(t, releaseSharedSessionStore(first))
	fresh, err := openSharedSessionStore(home)
	require.NoError(t, err)
	require.NotSame(t, first, fresh, "the last owner released: the store must have been forgotten")
	require.NoError(t, releaseSharedSessionStore(fresh))
	require.NoError(t, releaseSharedSessionStore(first), "releasing an already-released store must not close it twice")
}

// The loop must open the store under the same key the registry and instances
// use. With no configured home the old loop key was ".", so the loop and the
// registry each opened a different store for the same install.
func TestInitializeConvertedSessions_OpensTheStoreTheRegistryUses(t *testing.T) {
	omnipusHomeDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", omnipusHomeDir)
	t.Chdir(t.TempDir())
	cfg := s3StoreConfig("")
	cfg.Agents.List = nil

	nal := &newAgentLoop{cfg: cfg}
	require.NoError(t, nal.initializeConvertedSessions())
	t.Cleanup(func() { _ = releaseSharedSessionStore(nal.sharedStore) })

	viaRegistry, err := openSharedSessionStore(sessionsHomeFor(cfg))
	require.NoError(t, err)
	require.Same(t, nal.sharedStore, viaRegistry, "the loop and the registry must share one store")
	require.Equal(t, filepath.Join(omnipusHomeDir, "sessions"), nal.sharedStore.BaseDir())
}

// An unopenable store is reported by the registry and by the instance; neither
// panics and no agent is registered.
func TestNewAgentRegistry_ReportsAnUnopenableStore(t *testing.T) {
	cfg := s3StoreConfig(s3UnopenableHome(t))

	registry := NewAgentRegistry(cfg, &mockProvider{})
	require.Error(t, registry.InitErr())
	require.Empty(t, registry.ListAgentIDs())

	var inst *AgentInstance
	require.NotPanics(t, func() { inst = NewAgentInstance(&cfg.Agents.List[0], &cfg.Agents.Defaults, cfg, &mockProvider{}) })
	require.Error(t, inst.initErr, "the instance must carry the store failure")
}

// A hot reload whose registry cannot be built whole must fail and leave the
// running registry in place, not swap in an empty one.
func TestReloadProviderAndConfig_RefusesARegistryWithoutAStore(t *testing.T) {
	al := mustNewAgentLoop(t, s3StoreConfig(t.TempDir()), bus.NewMessageBus(), &mockProvider{})
	before := al.GetRegistry()
	require.NotEmpty(t, before.ListAgentIDs(), "instrument check: the running registry has an agent")

	err := al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, s3StoreConfig(s3UnopenableHome(t)))
	require.Error(t, err)
	require.Same(t, before, al.GetRegistry(), "the running registry must not be replaced")
	require.NotEmpty(t, al.GetRegistry().ListAgentIDs())
}
