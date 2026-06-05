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

	// Channel credential refs.
	ch := cfg.Channels
	for _, ref := range []string{
		ch.Telegram.TokenRef,
		ch.Discord.TokenRef,
		ch.Slack.BotTokenRef,
		ch.Slack.AppTokenRef,
		ch.Feishu.AppSecretRef,
		ch.Feishu.EncryptKeyRef,
		ch.Feishu.VerificationTokenRef,
		ch.QQ.AppSecretRef,
		ch.DingTalk.ClientSecretRef,
		ch.Matrix.AccessTokenRef,
		ch.Matrix.CryptoPassphraseRef,
		ch.LINE.ChannelSecretRef,
		ch.LINE.ChannelAccessTokenRef,
		ch.WeCom.SecretRef,
		ch.Weixin.TokenRef,
		ch.GoogleChat.WebhookURLRef,
		ch.GoogleChat.ServiceAccountJSONRef,
		ch.IRC.PasswordRef,
		ch.IRC.NickServPasswordRef,
		ch.IRC.SASLPasswordRef,
		cfg.Voice.ElevenLabsAPIKeyRef,
		cfg.Tools.Skills.Github.TokenRef,
		cfg.Tools.Skills.Registries.ClawHub.AuthTokenRef,
		cfg.Tools.Web.Brave.APIKeyRef,
		cfg.Tools.Web.Tavily.APIKeyRef,
		cfg.Tools.Web.Perplexity.APIKeyRef,
		cfg.Tools.Web.GLMSearch.APIKeyRef,
		cfg.Tools.Web.BaiduSearch.APIKeyRef,
	} {
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
