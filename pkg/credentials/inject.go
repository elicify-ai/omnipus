// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// Injection scopes and operations reported by CredentialRefError.
const (
	ScopeProvider = "provider"
	ScopeMailbox  = "mailbox"

	opResolve = "credential"
	opSetEnv  = "set env"
)

// CredentialRefError names the ONE credential reference that failed to inject,
// and the config entry that referenced it. It carries no more information than
// the error string already did — its whole purpose is to let a caller tell a
// SCOPED failure ("this single provider's key is missing") apart from a
// STORE-WIDE one ("the vault is locked; nothing will resolve") without parsing
// the message.
//
// Why it exists (incident 2026-08-14): gateway boot treated ANY error out of
// InjectFromConfig as fatal, so one stale providers[] entry — an
// onboarding-created "openrouter_API_KEY" ref whose credential was never
// stored — made the gateway log "provider credential injection failed" and
// EXIT. Not one broken provider: the whole install, with no way to reach the
// UI and delete the entry. Boot now degrades that single provider loudly and
// keeps store-wide failures fatal, and the classification has to be reliable
// to do that. The alternative — matching on message text — is the approach
// gateway.go's enabledRefFromBundleError was forced into and documents as a
// misdirection hazard; a second instance of it is not wanted. Unknown error
// shapes stay fatal by default precisely because they are NOT this type.
//
// IMPORTANT — this type does NOT itself mean "degradable". *CredentialRefError
// is emitted for every per-ref failure this package's per-entry loop sees,
// including ones whose root cause is store-wide: a wrong/rotated master key
// (ErrWrongKey) or a corrupted credentials.json entry make EVERY credential
// unreadable, not just the one InjectFromConfig happened to be resolving
// when it hit the failure — it only LOOKS scoped because the per-ref loop
// attributes it to whichever ref it was checking. The reliable discriminator
// callers must use is the Err field's TYPE, checked with errors.As, exactly
// mirroring rest.go's describeCredentialResolutionError:
//   - Err is *NotFoundError: the ref genuinely is not in the vault. Scoped,
//     degradable — this ONE entry is unusable, nothing else is affected.
//   - Err is anything else (ErrWrongKey, a corrupted-entry decrypt failure,
//     an unreadable credentials.json, an os.Setenv failure, …): store-wide
//     or environment-wide. MUST be treated the same as the bare
//     ErrStoreLocked short-circuit below — fatal, not degraded. Gateway.go's
//     reportInjectionErrors is the canonical caller; see its doc comment for
//     the incident (2026-08-15) where degrading on *CredentialRefError alone
//     let a wrong master key boot as if only one provider were broken.
type CredentialRefError struct {
	// Scope is ScopeProvider or ScopeMailbox.
	Scope string
	// Owner is the provider's model_name, or the mailbox's agent id.
	Owner string
	// SubOwner is the mailbox's workspace id. Empty for providers.
	SubOwner string
	// Ref is the credential name that failed (the api_key_ref / password_ref value).
	Ref string
	// Op is opResolve (store lookup failed) or opSetEnv (os.Setenv failed).
	Op string
	// Err is the underlying cause — *NotFoundError, ErrWrongKey, an os error, …
	Err error
}

// Error reproduces, byte for byte, the message shape this package emitted
// before the type existed ("provider %q: credential %q: %v",
// "mailbox %q/%q: set env %q: %v", …) so operator-facing logs and any
// message-matching caller are unchanged by the refactor.
func (e *CredentialRefError) Error() string {
	if e.Scope == ScopeMailbox {
		return fmt.Sprintf("mailbox %q/%q: %s %q: %v", e.Owner, e.SubOwner, e.Op, e.Ref, e.Err)
	}
	return fmt.Sprintf("%s %q: %s %q: %v", e.Scope, e.Owner, e.Op, e.Ref, e.Err)
}

// Unwrap exposes the cause so errors.As/errors.Is still reach *NotFoundError,
// ErrWrongKey and friends through this wrapper.
func (e *CredentialRefError) Unwrap() error { return e.Err }

// InjectFromConfig iterates over cfg.Providers entries, reads each entry's
// APIKeyRef field, resolves the referenced credential name from store, and
// injects the plaintext value into the process environment under that name.
//
// If a referenced credential is missing, the affected provider fails to
// initialize with a descriptive *CredentialRefError. Other providers continue.
// All errors are collected and returned as a slice.
//
// A LOCKED store is different in kind: nothing can resolve, so it is reported
// once as the bare ErrStoreLocked — deliberately NOT a *CredentialRefError,
// because callers use that distinction to decide fatal-vs-degrade (see the
// type's doc comment).
//
// Implements US-11 acceptance criteria (SEC-22).
func InjectFromConfig(cfg *config.Config, store *Store) []error {
	if store.IsLocked() {
		return []error{ErrStoreLocked}
	}

	var errs []error
	injected := map[string]bool{} // avoid re-injecting duplicates

	for i := range cfg.Providers {
		model := cfg.Providers[i]
		ref := strings.TrimSpace(model.APIKeyRef)
		if ref == "" {
			continue
		}
		if injected[ref] {
			continue
		}

		value, err := store.Get(ref)
		if err != nil {
			errs = append(errs, &CredentialRefError{
				Scope: ScopeProvider, Owner: model.Provider, Ref: ref, Op: opResolve, Err: err,
			})
			continue
		}

		if err := os.Setenv(ref, value); err != nil {
			errs = append(errs, &CredentialRefError{
				Scope: ScopeProvider, Owner: model.Provider, Ref: ref, Op: opSetEnv, Err: err,
			})
			continue
		}
		injected[ref] = true
		slog.Debug("credentials: injected", "ref", ref, "provider", model.Provider)
	}

	// Mailbox passwords (M11): email is a TOOL surface, and the per-(agent,
	// workspace) email tools resolve their mailbox password via
	// os.Getenv(password_ref) — the same env-injection pattern as provider
	// keys and skill marketplace credentials. cfg.Mailboxes is agent ID →
	// workspace ID → mailbox (pair-addressed 2026-07-03): the same agent may
	// own a distinct mailbox — and thus a distinct password ref — per
	// workspace.
	for agentID, byWorkspace := range cfg.Mailboxes {
		for workspaceID, mb := range byWorkspace {
			ref := strings.TrimSpace(mb.PasswordRef)
			if ref == "" || injected[ref] {
				continue
			}
			value, err := store.Get(ref)
			if err != nil {
				errs = append(errs, &CredentialRefError{
					Scope: ScopeMailbox, Owner: agentID, SubOwner: workspaceID, Ref: ref, Op: opResolve, Err: err,
				})
				continue
			}
			if err := os.Setenv(ref, value); err != nil {
				errs = append(errs, &CredentialRefError{
					Scope: ScopeMailbox, Owner: agentID, SubOwner: workspaceID, Ref: ref, Op: opSetEnv, Err: err,
				})
				continue
			}
			injected[ref] = true
			slog.Debug("credentials: injected", "ref", ref, "mailbox_agent", agentID, "mailbox_workspace", workspaceID)
		}
	}

	return errs
}

// ResolveAll returns every resolved {ref → plaintext} pair for all provider and
// channel credential references in cfg, WITHOUT calling os.Setenv. It is the
// side-effect-free counterpart to InjectFromConfig — useful for sensitive-data
// redaction callers that need the plaintexts but must not publish them into the
// process environment.
//
// Empty refs are skipped. ErrNotFound for a configured ref is collected in the
// error slice but does not prevent other refs from resolving.
func ResolveAll(cfg *config.Config, store *Store) (map[string]string, []error) {
	if store.IsLocked() {
		return nil, []error{ErrStoreLocked}
	}

	result := make(map[string]string)
	var errs []error

	addRef := func(ref string) {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return
		}
		if _, already := result[ref]; already {
			return
		}
		value, err := store.Get(ref)
		if err != nil {
			errs = append(errs, fmt.Errorf("ResolveAll: credential %q: %w", ref, err))
			return
		}
		result[ref] = value
	}

	// Provider API keys.
	for _, m := range cfg.Providers {
		addRef(m.APIKeyRef)
	}

	// Channel credential refs — walk all instances in the map and collect every
	// *_ref field. The union struct carries only the relevant refs per type; empty
	// strings are skipped by addRef.
	for _, inst := range cfg.Channels {
		addRef(inst.TokenRef)
		addRef(inst.BotTokenRef)
		addRef(inst.AppTokenRef)
		addRef(inst.AppSecretRef)
		addRef(inst.EncryptKeyRef)
		addRef(inst.VerificationTokenRef)
		addRef(inst.ClientSecretRef)
		addRef(inst.AccessTokenRef)
		addRef(inst.CryptoPassphraseRef)
		addRef(inst.ChannelSecretRef)
		addRef(inst.ChannelAccessTokenRef)
		addRef(inst.SecretRef)
		addRef(inst.WebhookURLRef)
		addRef(inst.ServiceAccountJSONRef)
		addRef(inst.PasswordRef)
		addRef(inst.NickServPasswordRef)
		addRef(inst.SASLPasswordRef)
	}

	// Mailbox passwords (M11) — sensitive, must be collected for redaction.
	// cfg.Mailboxes is agent ID → workspace ID → mailbox (pair-addressed).
	for _, byWorkspace := range cfg.Mailboxes {
		for _, mb := range byWorkspace {
			addRef(mb.PasswordRef)
		}
	}

	// Non-channel credential refs.
	nonChannelRefs := []string{
		cfg.Voice.ElevenLabsAPIKeyRef,
		cfg.Voice.GroqAPIKeyRef,
		cfg.Tools.Web.Brave.APIKeyRef,
		cfg.Tools.Web.Tavily.APIKeyRef,
		cfg.Tools.Web.Perplexity.APIKeyRef,
		cfg.Tools.Web.GLMSearch.APIKeyRef,
		cfg.Tools.Web.BaiduSearch.APIKeyRef,
	}
	// Skill marketplace credential refs (FR-10.1 unified list): each
	// marketplace entry may carry a ClawHub AuthTokenRef and/or a GitHub
	// TokenRef, both resolved via the credential store (SEC-23).
	for _, m := range cfg.Tools.Skills.Marketplaces {
		nonChannelRefs = append(nonChannelRefs, m.AuthTokenRef, m.TokenRef)
	}
	for _, ref := range nonChannelRefs {
		addRef(ref)
	}

	return result, errs
}

// ResolveRef looks up a single credential reference and returns its plaintext.
// Returns a descriptive error if the store is locked or the ref is not found.
func ResolveRef(store *Store, ref string) (string, error) {
	if store.IsLocked() {
		return "", ErrStoreLocked
	}
	return store.Get(ref)
}
