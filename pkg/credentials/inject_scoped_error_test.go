// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Scoped-vs-store-wide classification of InjectFromConfig failures.
//
// Why these exist (incident 2026-08-14): gateway boot treated every error out
// of InjectFromConfig as fatal, so ONE providers[] entry whose api_key_ref
// named a credential that was never stored (an onboarding-created
// "openrouter_API_KEY") made the gateway exit on every start — with no way to
// reach the UI and delete the entry. The gateway now degrades that single
// entry and keeps store-wide failures fatal, and it decides which is which by
// type, not by reading the message. These tests pin the property the gateway
// depends on: a per-ref failure IS a *CredentialRefError carrying the owner
// and the ref; a locked store is NOT.
package credentials_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// TestInjectFromConfig_ProviderMissingCredential_IsScopedRefError verifies the
// failure for a single unresolvable provider ref is a *CredentialRefError that
// names the provider, the ref and the underlying NotFoundError — everything a
// caller needs to report the provider as unusable without parsing text.
func TestInjectFromConfig_ProviderMissingCredential_IsScopedRefError(t *testing.T) {
	store := newUnlockedTestStore(t)
	const missingRef = "INJECT_SCOPED_TEST_MISSING_KEY"

	cfg := providersCfg([2]string{"broken-provider", missingRef})

	errs := credentials.InjectFromConfig(cfg, store)
	if len(errs) != 1 {
		t.Fatalf("expected exactly 1 error, got %d: %v", len(errs), errs)
	}

	var refErr *credentials.CredentialRefError
	if !errors.As(errs[0], &refErr) {
		t.Fatalf(
			"a single unresolvable provider ref must be a *CredentialRefError so the gateway can degrade "+
				"just that provider instead of aborting boot; got %T: %v",
			errs[0], errs[0],
		)
	}
	if refErr.Scope != credentials.ScopeProvider {
		t.Errorf("Scope = %q, want %q", refErr.Scope, credentials.ScopeProvider)
	}
	if refErr.Owner != "broken-provider" {
		t.Errorf("Owner = %q, want the provider id %q", refErr.Owner, "broken-provider")
	}
	if refErr.Ref != missingRef {
		t.Errorf("Ref = %q, want %q", refErr.Ref, missingRef)
	}

	// The cause must remain reachable — callers classify "missing" vs
	// "present but undecryptable" through it.
	var notFound *credentials.NotFoundError
	if !errors.As(errs[0], &notFound) {
		t.Errorf("cause must still unwrap to *NotFoundError; got %v", errs[0])
	}

	// The operator-facing text is unchanged by the typed wrapper.
	msg := errs[0].Error()
	if want := `provider "broken-provider": credential "` + missingRef + `"`; !strings.Contains(msg, want) {
		t.Errorf("message = %q, want it to start with %q", msg, want)
	}
}

// TestInjectFromConfig_MailboxMissingCredential_IsScopedRefError covers the
// second scope: a mailbox password_ref carries the agent AND workspace it
// belongs to, and keeps its "mailbox %q/%q: ..." message shape.
func TestInjectFromConfig_MailboxMissingCredential_IsScopedRefError(t *testing.T) {
	store := newUnlockedTestStore(t)
	const missingRef = "INJECT_SCOPED_TEST_MBX_MISSING"

	cfg := nestedMailboxesCfg("mia", map[string]string{"ws_missing": missingRef})

	errs := credentials.InjectFromConfig(cfg, store)
	if len(errs) != 1 {
		t.Fatalf("expected exactly 1 error, got %d: %v", len(errs), errs)
	}

	var refErr *credentials.CredentialRefError
	if !errors.As(errs[0], &refErr) {
		t.Fatalf("mailbox ref failure must be a *CredentialRefError; got %T: %v", errs[0], errs[0])
	}
	if refErr.Scope != credentials.ScopeMailbox {
		t.Errorf("Scope = %q, want %q", refErr.Scope, credentials.ScopeMailbox)
	}
	if refErr.Owner != "mia" || refErr.SubOwner != "ws_missing" {
		t.Errorf("Owner/SubOwner = %q/%q, want %q/%q", refErr.Owner, refErr.SubOwner, "mia", "ws_missing")
	}
	if want := `mailbox "mia"/"ws_missing": credential "` + missingRef + `"`; !strings.Contains(errs[0].Error(), want) {
		t.Errorf("message = %q, want it to contain %q", errs[0].Error(), want)
	}
}

// TestInjectFromConfig_LockedStore_IsNotScopedRefError is the other half of the
// discriminator: a locked vault is store-WIDE (no ref will resolve, the cause
// is the master key, not the config), so it must NOT look like a scoped
// per-entry failure — that is exactly what keeps it fatal at boot and reload.
func TestInjectFromConfig_LockedStore_IsNotScopedRefError(t *testing.T) {
	// A store that was never unlocked.
	store := credentials.NewStore(t.TempDir() + "/credentials.json")

	cfg := providersCfg([2]string{"any-provider", "ANY_REF"})

	errs := credentials.InjectFromConfig(cfg, store)
	if len(errs) != 1 {
		t.Fatalf("expected exactly 1 error for a locked store, got %d: %v", len(errs), errs)
	}
	if !errors.Is(errs[0], credentials.ErrStoreLocked) {
		t.Errorf("locked store must report ErrStoreLocked; got %v", errs[0])
	}
	var refErr *credentials.CredentialRefError
	if errors.As(errs[0], &refErr) {
		t.Fatalf(
			"a locked store must NOT be reported as a scoped *CredentialRefError — callers use that " +
				"distinction to stay fatal on a store-wide failure",
		)
	}
}
