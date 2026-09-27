// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// ADR-092 review finding F: docs/security.md promises that an invalid
// sandbox.command_rules entry in a hand-edited config.json leaves the
// previous config in force AND marks GET /health degraded with the same
// message, until the file is fixed. The file-watcher poller used to log the
// load error and carry on, so /health stayed green.
func TestConfigWatcherPoll_RejectedReloadMarksDegraded(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.json")
	valid := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(configPath, valid, 0o600))

	// The production sink: /health's degraded check reads exactly these
	// two services fields (configureHealthAndWatcher).
	svc := &services{}
	configChan, stop := setupConfigWatcherPolling(configPath, home, false,
		credentials.NewStore(filepath.Join(home, "credentials.json")), nil, svc.markReloadDegraded)
	t.Cleanup(stop)

	// Make sure the rewrite is seen as a change even on a coarse-mtime
	// filesystem: the poller compares size too, and this content is longer.
	time.Sleep(50 * time.Millisecond)
	invalid := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],` +
		`"sandbox":{"command_rules":[{"action":"Deny","binary":"rm"}]}}`)
	require.NoError(t, os.WriteFile(configPath, invalid, 0o600))

	degraded := func() (bool, error) {
		svc.reloadMu.Lock()
		defer svc.reloadMu.Unlock()
		return svc.reloadDegraded, svc.reloadError
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if d, _ := degraded(); d || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	isDegraded, reloadErr := degraded()
	require.True(t, isDegraded, "a rejected config reload must mark the gateway degraded for GET /health")
	require.Error(t, reloadErr)
	t.Logf("GET /health reason: config reload failed: %v", reloadErr)
	assert.Contains(t, reloadErr.Error(), "command_rules[0]", "the /health reason must name the bad rule")
	assert.Contains(t, reloadErr.Error(), `action "Deny" must be one of allow, ask, deny`)

	select {
	case cfg := <-configChan:
		t.Fatalf("a rejected config must not be handed to the reload loop, got %+v", cfg)
	default:
	}

	// Fixing the file hands a valid config to the reload loop again; the
	// loop's executeReload then clears the degraded mark (clearDegraded).
	fixed := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"sandbox":{"command_rules":[]}}`)
	require.NoError(t, os.WriteFile(configPath, fixed, 0o600))
	select {
	case cfg := <-configChan:
		require.NotNil(t, cfg)
		assert.Empty(t, cfg.Sandbox.CommandRules)
	case <-time.After(15 * time.Second):
		t.Fatal("the fixed config was never handed to the reload loop")
	}
	assert.True(t, strings.Contains(reloadErr.Error(), "config reload rejected"), "reason: %v", reloadErr)
}
