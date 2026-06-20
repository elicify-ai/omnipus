// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// InjectFromConfig iterates over cfg.Providers entries, reads each entry's
// APIKeyRef field, resolves the referenced credential name from store, and
// injects the plaintext value into the process environment under that name.
//
// If store is locked or a referenced credential is missing, the affected
// provider fails to initialize with a descriptive error. Other providers
// continue. All errors are collected and returned as a slice.
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
			errs = append(errs, fmt.Errorf("provider %q: credential %q: %w", model.ModelName, ref, err))
			continue
		}

		if err := os.Setenv(ref, value); err != nil {
			errs = append(errs, fmt.Errorf("provider %q: set env %q: %w", model.ModelName, ref, err))
			continue
		}
		injected[ref] = true
		slog.Debug("credentials: injected", "ref", ref, "provider", model.ModelName)
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

	// Non-channel credential refs.
	nonChannelRefs := []string{
		cfg.Voice.ElevenLabsAPIKeyRef,
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
