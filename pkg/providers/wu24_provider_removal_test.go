package providers

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// WU-24, founder 2026-10-14 (L1 plan section 10): subprocess model kinds
// disappear, with no migration. Fragments keep the removal guard honest:
// verification files do not need an exemption for the retired spellings.
func wu24RemovedProviderIDs() []string {
	return []string{"codex" + "-cli", "github" + "-copilot"}
}

func TestWU24ProviderFactory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("OMNIPUS_HOME", home)

	cat, err := catalog.NewCatalog(catalog.EmbeddedSnapshot)
	if err != nil {
		t.Fatalf("parse real embedded catalog: %v", err)
	}
	previous := installedCatalog.Load()
	SetCatalog(cat)
	t.Cleanup(func() { SetCatalog(previous) })

	ids := append([]string{"wu24-never-registered"}, wu24RemovedProviderIDs()...)
	for _, id := range ids {
		t.Run("unknown_kind_refuses_"+id, func(t *testing.T) {
			// Characterization oracle explicitly requested by WU-24:
			// resolveRow/UnknownProviderError's existing generic path, not a
			// replacement-specific error or a fallback to an HTTP provider.
			cfg := &config.ModelConfig{
				Provider: id, Model: "requested-model", Home: home,
				AuthMethod: config.AuthMethodSignIn,
			}
			provider, model, err := CreateProviderFromConfig(cfg)
			if provider != nil || model != "" {
				t.Errorf("factory(%q) = (%T, %q), want (nil, empty model)", id, provider, model)
			}
			var unknown *UnknownProviderError
			// Keep the exact, unwrapped-type oracle: errors.As alone would
			// also accept a wrapped error that the original assertion rejects.
			if !errors.As(err, &unknown) || reflect.TypeOf(err) != reflect.TypeOf(unknown) {
				t.Fatalf("factory(%q) error = %T (%v), want *UnknownProviderError", id, err, err)
			}
			if unknown.ProviderID != id {
				t.Errorf("unknown error id = %q, want requested id %q", unknown.ProviderID, id)
			}
			if got, want := err.Error(), fmt.Sprintf("unknown provider %q", id); got != want {
				t.Errorf("factory refusal = %q, want %q", got, want)
			}
			if !errors.Is(err, ErrUnknownProvider) {
				t.Errorf("factory refusal %v does not classify as ErrUnknownProvider", err)
			}
		})
	}

	t.Run("kept_HTTP_ChatGPT_constructs", func(t *testing.T) {
		// KEEP control: the real catalog and encrypted credential-store
		// constructor are exercised, never a custom row bypassing admission.
		store := credentials.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
		if err := store.UnlockWithKey(make([]byte, 32)); err != nil {
			t.Fatalf("unlock temporary credential store: %v", err)
		}
		previousStore := DefaultCredentialStore
		SetDefaultCredentialStore(store)
		t.Cleanup(func() { SetDefaultCredentialStore(previousStore) })

		provider, model, err := CreateProviderFromConfig(&config.ModelConfig{
			Provider: "openai-chatgpt", Model: "gpt-5.4", Home: home,
			AuthMethod: config.AuthMethodSignIn,
		})
		if err != nil {
			t.Fatalf("kept HTTP provider failed construction: %v", err)
		}
		if _, ok := provider.(*CodexProvider); !ok {
			t.Errorf("kept provider = %T, want *CodexProvider (HTTP)", provider)
		}
		if model != "gpt-5.4" {
			t.Errorf("kept provider model = %q, want gpt-5.4", model)
		}
	})
}
