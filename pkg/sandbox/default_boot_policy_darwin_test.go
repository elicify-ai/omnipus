// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build darwin

package sandbox

// T-123(e) macOS twin (office-preview spec §3.8 BR-2.1…BR-2.3; founder
// Q100 A): on macOS the startup grants input is ignored — the confined boot
// policy is byte-identical with and without it, and equal to
// DefaultPolicyForModel. RED on 24f2f912c: DefaultBootPolicyForModel does
// not exist yet (spec §13: stub first).

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestT123_DarwinBootPolicyIgnoresStartupGrants(t *testing.T) {
	home := t.TempDir()
	grants := []string{
		"/usr/share/omnipus/office",
		"/usr/share/omnipus/pdfium",
		"/usr/bin/omnipus",
	}
	withGrants := DefaultBootPolicyForModel(FilesystemModelConfined, home, nil, nil, nil, nil, grants)
	without := DefaultBootPolicyForModel(FilesystemModelConfined, home, nil, nil, nil, nil, nil)
	classic := DefaultPolicyForModel(FilesystemModelConfined, home, nil, nil, nil, nil)
	require.True(t, reflect.DeepEqual(without, withGrants),
		"T-123(e): macOS must ignore the startup grants byte-identically")
	require.True(t, reflect.DeepEqual(classic, withGrants),
		"T-123(e): on macOS the boot sibling equals DefaultPolicyForModel")
}
