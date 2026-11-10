// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// FR-070 / BDD-6.8 (WU-4): agent configuration tools accept references only.
// configure_provider with a plaintext api_key is refused with "request a secret"
// and the plaintext is not written to the credential store. Settings forms
// keep plaintext entry; that half is the raw credential route, not this tool.
package systools_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// plaintextSecretSentinel is the value BDD-6.8 refuses. It is not a real key.
const plaintextSecretSentinel = "sk-sentinel-wu4-plaintext"

// TestConfigureProvider_RefusesPlaintext is spec test 11 / BDD-6.8.
// The tool under test is the real configure_provider, not a stub.
func TestConfigureProvider_RefusesPlaintext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)

	cfg := config.DefaultConfig()
	var mu sync.Mutex
	getCfg := func() *config.Config { return cfg }

	store := credentials.NewStore(home + "/credentials.json")
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	if err := store.UnlockWithKey(key); err != nil {
		t.Fatalf("UnlockWithKey: %v", err)
	}

	deps := &systools.Deps{
		Home:             home,
		GetCfg:           getCfg,
		MutateConfig:     testMutateConfig(&mu, getCfg),
		SaveConfigLocked: func(*config.Config) error { return nil },
		CredStore:        store,
	}
	tool := systools.NewProviderConfigureTool(deps)

	// BDD-6.1 / BDD-6.8: a cloud provider key passed as plaintext api_key.
	result := tool.Execute(context.Background(), map[string]any{
		"name":    "openai",
		"api_key": plaintextSecretSentinel,
	})
	if result == nil {
		t.Fatal("configure_provider returned nil")
	}
	if !result.IsError {
		t.Fatalf("plaintext api_key must be refused (FR-070, BDD-6.8); ForLLM=%q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "request a secret") {
		t.Fatalf("refusal = %q, want it to contain %q (BDD-6.8)", result.ForLLM, "request a secret")
	}
	if strings.Contains(result.ForLLM, plaintextSecretSentinel) {
		t.Fatalf("refusal echoed the plaintext key: %q", result.ForLLM)
	}

	names, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, name := range names {
		got, getErr := store.Get(name)
		if getErr != nil {
			t.Fatalf("Get %s: %v", name, getErr)
		}
		if got == plaintextSecretSentinel || strings.Contains(got, plaintextSecretSentinel) {
			t.Fatalf("plaintext key was stored under %s — FR-070 refuses the write", name)
		}
	}
}
