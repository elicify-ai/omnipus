// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials_test

import (
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// ADR-096 D2 / spec "Exa" section: Exa's credential reference must join
// pkg/credentials/inject.go::nonChannelRefsFor — the shared enumeration that
// feeds BOTH InjectFromConfig (os.Setenv at boot step 4) and ResolveAll (the
// redaction bundle). A missing entry means Exa fails the way Tavily did,
// silently: the key sits in the vault, the process environment never sees
// it, and search degrades to DuckDuckGo.
//
// Oracle: spec Exa table, "Key injection" row. The assertion is on the REAL
// process environment (os.Getenv), not on a mock — per the dispatch, "assert
// on the injected value's presence, not on a mock returning what the mock
// was told".
func TestInjectFromConfig_ExaRefReachesProcessEnvironment(t *testing.T) {
	store := newUnlockedTestStore(t)
	const ref = "EXA_API_KEY"
	requireEnvUnsetExa(t, ref)

	if err := store.Set(ref, "exa-plain-value-123"); err != nil {
		t.Fatalf("store.Set: %v", err)
	}
	cfg := &config.Config{
		Tools: config.ToolsConfig{
			Web: config.WebToolsConfig{
				Exa: config.ExaConfig{Enabled: true, APIKeyRef: ref},
			},
		},
	}

	// Before injection the environment is empty — the Tavily trap in miniature.
	if got := os.Getenv(ref); got != "" {
		t.Fatalf("precondition: %s already set to %q; the test needs an empty environment", ref, got)
	}

	errs := credentials.InjectFromConfig(cfg, store)
	if len(errs) != 0 {
		t.Fatalf("InjectFromConfig errors: %v", errs)
	}
	if got := os.Getenv(ref); got != "exa-plain-value-123" {
		t.Fatalf("after injection %s = %q, want %q — Exa's ref did not reach the process environment", ref, got, "exa-plain-value-123")
	}

	// The shared enumeration also feeds ResolveAll (redaction): the same ref
	// must appear in the bundle, or scrubbing misses Exa's key.
	bundle, berrs := credentials.ResolveAll(cfg, store)
	if len(berrs) != 0 {
		t.Fatalf("ResolveAll errors: %v", berrs)
	}
	if got := bundle[ref]; got != "exa-plain-value-123" {
		t.Fatalf("ResolveAll bundle[%s] = %q, want the plaintext — the redaction bundle must cover Exa too", ref, got)
	}
}

// requireEnvUnsetExa keeps the Exa ref's environment variable absent for the
// test's lifetime (mirrors requireEnvUnset in pkg/config's web-roles tests;
// duplicated here because the packages cannot share test helpers).
func requireEnvUnsetExa(t *testing.T, ref string) {
	t.Helper()
	if err := os.Unsetenv(ref); err != nil {
		t.Fatalf("unset %s: %v", ref, err)
	}
	t.Cleanup(func() {
		if err := os.Unsetenv(ref); err != nil {
			_ = err
		}
	})
}
