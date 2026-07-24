// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package audit_test

import (
	"os"
	"testing"
)

// TestMain enables the audit test-mode dev-key opt-in (OMNIPUS_AUDIT_DEV_KEY=1)
// for the entire audit test binary. Fix B.1 made NewLogger fail closed when no
// chain key is configured and the env var is unset, so any test that
// constructs NewLogger without an explicit HMACKey needs the dev fallback to
// still fire. Tests that exercise the production fail-closed path use
// t.Setenv("OMNIPUS_AUDIT_DEV_KEY", "") to clear it locally.
//
// Defining TestMain here means no other test file in this package may
// declare one — keep this file the single source of test-binary bootstrap.
func TestMain(m *testing.M) {
	if os.Getenv("OMNIPUS_AUDIT_DEV_KEY") == "" {
		os.Setenv("OMNIPUS_AUDIT_DEV_KEY", "1")
	}
	os.Exit(m.Run())
}
