// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package sandbox

// T-123(e) Windows twin (office-preview spec §3.8 BR-2.1…BR-2.3; founder
// Q100 A): on Windows the startup grants input is ignored — the confined
// boot policy is byte-identical with and without it, and equal to
// DefaultPolicyForModel. Compiles in the GOOS=windows vet gate; RED on
// 24f2f912c because DefaultBootPolicyForModel does not exist yet (spec §13:
// stub first).

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestT123_WindowsBootPolicyIgnoresStartupGrants(t *testing.T) {
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
		"T-123(e): Windows must ignore the startup grants byte-identically")
	require.True(t, reflect.DeepEqual(classic, withGrants),
		"T-123(e): on Windows the boot sibling equals DefaultPolicyForModel")
}
