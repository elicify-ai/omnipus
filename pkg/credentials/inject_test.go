// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials_test

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// newUnlockedTestStore mirrors the fixture pattern in bundle_test.go: a fixed
// all-zero 32-byte master key unlocking a fresh temp-dir credentials.json.
func newUnlockedTestStore(t *testing.T) *credentials.Store {
	t.Helper()
	testKeyHex := hex.EncodeToString(make([]byte, 32))
	t.Setenv(credentials.EnvMasterKey, testKeyHex)
	storePath := t.TempDir() + "/credentials.json"
	store := credentials.NewStore(storePath)
	if err := credentials.Unlock(store); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	return store
}

// nestedMailboxesCfg builds a config.Config whose Mailboxes map holds the
// given agent ID → workspace ID → password ref pairs, exercising the same
// nested (agent, workspace)-pair shape InjectFromConfig/ResolveAll walk.
func nestedMailboxesCfg(agentID string, pairs map[string]string) *config.Config {
	byWorkspace := make(map[string]config.MailboxConfig, len(pairs))
	for wsID, ref := range pairs {
		byWorkspace[wsID] = config.MailboxConfig{
			Enabled: true, WorkspaceID: wsID, PasswordRef: ref,
			Username: "u@x.com", IMAPHost: "i", SMTPHost: "s",
		}
	}
	return &config.Config{Mailboxes: config.MailboxesConfig{agentID: byWorkspace}}
}

// TestInjectFromConfig_MailboxesNestedMap_TwoPairsBothInjected verifies that
// InjectFromConfig walks the NESTED agent→workspace→mailbox map and injects
// a distinct env var for each (agent, workspace) pair's password_ref.
func TestInjectFromConfig_MailboxesNestedMap_TwoPairsBothInjected(t *testing.T) {
	store := newUnlockedTestStore(t)
	const refA, refB = "INJECT_TEST_MBX_PW_A", "INJECT_TEST_MBX_PW_B"
	t.Cleanup(func() { os.Unsetenv(refA); os.Unsetenv(refB) })

	if err := store.Set(refA, "secret-a"); err != nil {
		t.Fatalf("store.Set(refA): %v", err)
	}
	if err := store.Set(refB, "secret-b"); err != nil {
		t.Fatalf("store.Set(refB): %v", err)
	}

	cfg := nestedMailboxesCfg("mia", map[string]string{"ws_a": refA, "ws_b": refB})

	errs := credentials.InjectFromConfig(cfg, store)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	if got := os.Getenv(refA); got != "secret-a" {
		t.Fatalf("env %s = %q, want %q", refA, got, "secret-a")
	}
	if got := os.Getenv(refB); got != "secret-b" {
		t.Fatalf("env %s = %q, want %q", refB, got, "secret-b")
	}
}

// TestInjectFromConfig_MailboxesSharedRef_DedupedNoError verifies the
// injected[ref] dedupe: two (agent, workspace) pairs sharing ONE
// password_ref resolve without error and both observe the single injected
// value — the dedupe guard must not treat the second pair as a failure.
func TestInjectFromConfig_MailboxesSharedRef_DedupedNoError(t *testing.T) {
	store := newUnlockedTestStore(t)
	const sharedRef = "INJECT_TEST_MBX_PW_SHARED"
	t.Cleanup(func() { os.Unsetenv(sharedRef) })

	if err := store.Set(sharedRef, "shared-secret"); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	cfg := nestedMailboxesCfg("mia", map[string]string{
		"ws_a": sharedRef,
		"ws_b": sharedRef,
	})

	errs := credentials.InjectFromConfig(cfg, store)
	if len(errs) != 0 {
		t.Fatalf("expected no errors for a ref shared by two pairs, got %v", errs)
	}
	if got := os.Getenv(sharedRef); got != "shared-secret" {
		t.Fatalf("env %s = %q, want %q", sharedRef, got, "shared-secret")
	}
}

// TestInjectFromConfig_MailboxesMissingCredential_CollectedErrorDoesNotBlockOthers
// verifies that a mailbox pair whose password_ref does not resolve in the
// store is collected as an error WITHOUT preventing a sibling pair's
// resolvable ref from being injected.
func TestInjectFromConfig_MailboxesMissingCredential_CollectedErrorDoesNotBlockOthers(t *testing.T) {
	store := newUnlockedTestStore(t)
	const okRef, missingRef = "INJECT_TEST_MBX_PW_OK", "INJECT_TEST_MBX_PW_MISSING"
	t.Cleanup(func() { os.Unsetenv(okRef); os.Unsetenv(missingRef) })

	if err := store.Set(okRef, "ok-secret"); err != nil {
		t.Fatalf("store.Set: %v", err)
	}
	// missingRef is deliberately never stored.

	cfg := nestedMailboxesCfg("mia", map[string]string{
		"ws_ok":      okRef,
		"ws_missing": missingRef,
	})

	errs := credentials.InjectFromConfig(cfg, store)
	if len(errs) != 1 {
		t.Fatalf("expected exactly 1 collected error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), missingRef) {
		t.Fatalf("error must name the missing ref, got %q", errs[0].Error())
	}
	if got := os.Getenv(okRef); got != "ok-secret" {
		t.Fatalf("sibling pair's ref must still be injected: env %s = %q, want %q", okRef, got, "ok-secret")
	}
}

// TestResolveAll_MailboxesNestedMap_TwoPairsBothResolved is the
// side-effect-free counterpart: ResolveAll must return both pairs'
// plaintexts in its result map without touching the process environment.
func TestResolveAll_MailboxesNestedMap_TwoPairsBothResolved(t *testing.T) {
	store := newUnlockedTestStore(t)
	const refA, refB = "RESOLVEALL_TEST_MBX_PW_A", "RESOLVEALL_TEST_MBX_PW_B"

	if err := store.Set(refA, "resolve-a"); err != nil {
		t.Fatalf("store.Set(refA): %v", err)
	}
	if err := store.Set(refB, "resolve-b"); err != nil {
		t.Fatalf("store.Set(refB): %v", err)
	}

	cfg := nestedMailboxesCfg("mia", map[string]string{"ws_a": refA, "ws_b": refB})

	bundle, errs := credentials.ResolveAll(cfg, store)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	if bundle[refA] != "resolve-a" || bundle[refB] != "resolve-b" {
		t.Fatalf("bundle = %+v, want refA=resolve-a refB=resolve-b", bundle)
	}
}

// TestResolveAll_MailboxesSharedRef_NoDuplicateError mirrors the
// InjectFromConfig dedupe test for ResolveAll's own addRef dedupe guard.
func TestResolveAll_MailboxesSharedRef_NoDuplicateError(t *testing.T) {
	store := newUnlockedTestStore(t)
	const sharedRef = "RESOLVEALL_TEST_MBX_PW_SHARED"

	if err := store.Set(sharedRef, "shared-resolve-secret"); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	cfg := nestedMailboxesCfg("mia", map[string]string{
		"ws_a": sharedRef,
		"ws_b": sharedRef,
	})

	bundle, errs := credentials.ResolveAll(cfg, store)
	if len(errs) != 0 {
		t.Fatalf("expected no errors for a ref shared by two pairs, got %v", errs)
	}
	if bundle[sharedRef] != "shared-resolve-secret" {
		t.Fatalf("bundle[%s] = %q, want %q", sharedRef, bundle[sharedRef], "shared-resolve-secret")
	}
}

// TestResolveAll_MailboxesMissingCredential_CollectedErrorDoesNotBlockOthers
// verifies ResolveAll's error-collection contract over the mailboxes map: a
// missing credential is reported but does not prevent a sibling pair's
// plaintext from being resolved into the bundle.
func TestResolveAll_MailboxesMissingCredential_CollectedErrorDoesNotBlockOthers(t *testing.T) {
	store := newUnlockedTestStore(t)
	const okRef, missingRef = "RESOLVEALL_TEST_MBX_PW_OK", "RESOLVEALL_TEST_MBX_PW_MISSING"

	if err := store.Set(okRef, "resolve-ok-secret"); err != nil {
		t.Fatalf("store.Set: %v", err)
	}
	// missingRef is deliberately never stored.

	cfg := nestedMailboxesCfg("mia", map[string]string{
		"ws_ok":      okRef,
		"ws_missing": missingRef,
	})

	bundle, errs := credentials.ResolveAll(cfg, store)
	if len(errs) != 1 {
		t.Fatalf("expected exactly 1 collected error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), missingRef) {
		t.Fatalf("error must name the missing ref, got %q", errs[0].Error())
	}
	if bundle[okRef] != "resolve-ok-secret" {
		t.Fatalf(
			"sibling pair's ref must still resolve: bundle[%s] = %q, want %q",
			okRef,
			bundle[okRef],
			"resolve-ok-secret",
		)
	}
}
