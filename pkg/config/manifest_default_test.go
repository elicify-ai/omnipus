// Omnipus — config defaults test for the tool-manifest optimization
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import "testing"

// TestDefaultConfig_ManifestCompressedIsTrue asserts that DefaultConfig() ships
// with the tool-manifest optimization enabled out of the box. A regression here
// means fresh installs would send all tool schemas on every turn (token overhead)
// instead of the compressed subset.
func TestDefaultConfig_ManifestCompressedIsTrue(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Tools.Manifest.Compressed {
		t.Errorf("DefaultConfig().Tools.Manifest.Compressed = false; want true — fresh installs must ship with compressed manifest enabled")
	}
}
