// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConfig_RejectsSubTurnKey pins ADR-091 D9's config fold at the load
// boundary: a config.json carrying the retired agents.defaults.subturn block
// must be REFUSED with a message naming the replacement key, so an operator's
// customized depth is never silently dropped. The old timeout has no
// replacement (issue #1306: delegated work has no time limit) and the message
// says so instead of pointing at a key that no longer does anything. This is the same
// treatment the retired `await` delegation mode already gets, and the opposite
// of the lenient unknown-key path (no DisallowUnknownFields on load).
func TestConfig_RejectsSubTurnKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{"version":1,"agents":{"defaults":{"subturn":{"max_depth":4,"default_timeout_minutes":9}}}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("LoadConfig(subturn) = nil, want explicit retired-key rejection")
	}
	for _, key := range []string{"agents.defaults.subturn", "performance.max_delegation_depth", "default_timeout_minutes has no replacement"} {
		if !strings.Contains(err.Error(), key) {
			t.Fatalf("LoadConfig(subturn) error = %v, want it to name %q", err, key)
		}
	}
}

// TestConfig_AcceptsPerformanceDelegationKeys is the positive control for the
// fold: a config using the replacement key loads cleanly, so the rejection
// above targets the old key and never a correct current config. It also keeps
// the retired performance.delegation_timeout_minutes key in the file: an
// older install's config still carries it and must keep loading (#1306).
func TestConfig_AcceptsPerformanceDelegationKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{"version":1,"performance":{"max_delegation_depth":4,"delegation_timeout_minutes":9}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig(performance delegation keys) = %v, want nil", err)
	}
	if cfg.Performance.MaxDelegationDepth != 4 {
		t.Fatalf("performance.max_delegation_depth not read: depth=%d, want 4", cfg.Performance.MaxDelegationDepth)
	}
}
