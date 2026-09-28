// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/media"
)

// omnipusGracefulShutdown is the only teardown that runs when the gateway
// process ends, so it owns wiping the credential store's key. The sibling
// stopAndCleanupServices is deliberately NOT the place for it: it also serves
// config reload (isReload), where the process keeps running on the same key.
//
// Without the call, the master key stays readable in the heap until the process
// dies — reachable by a core dump or a debugger attached after shutdown.
func TestGracefulShutdown_WipesTheCredentialStoreKey(t *testing.T) {
	cfg := seededBootConfig(t)
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})

	store := credentials.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x5a}, 32)))
	require.False(t, store.IsLocked(), "precondition: the gateway's store is unlocked while it runs")

	omnipusGracefulShutdown(&services{credStore: store}, al, nil, cfg)

	require.True(t, store.IsLocked(),
		"shutdown must overwrite the credential store's key material")
}

// Shutdown runs with whatever services boot produced. A nil store is the shape
// a partially-configured or test-only boot leaves behind, and must not panic.
func TestGracefulShutdown_WithoutACredentialStoreIsSafe(t *testing.T) {
	cfg := seededBootConfig(t)
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})

	require.NotPanics(t, func() {
		omnipusGracefulShutdown(&services{}, al, nil, cfg)
	})
}

func TestGracefulShutdown_FlushesPendingMediaRegistry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := seededBootConfig(t)
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	store := newTestFileMediaStore(t)

	source := filepath.Join(home, "upload.txt")
	require.NoError(t, os.WriteFile(source, []byte("upload"), 0o600))
	ref, err := store.Store(source, media.MediaMeta{
		Filename:      "upload.txt",
		CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, "shutdown-test")
	require.NoError(t, err)

	omnipusGracefulShutdown(&services{MediaStore: store}, al, nil, cfg)

	reloaded := media.NewFileMediaStore()
	require.NoError(t, reloaded.LoadRegistry())
	resolved, err := reloaded.Resolve(ref)
	require.NoError(t, err, "a ref stored immediately before shutdown must survive restart")
	require.Equal(t, source, resolved)
}
