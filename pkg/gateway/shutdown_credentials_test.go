// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/credentials"
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
