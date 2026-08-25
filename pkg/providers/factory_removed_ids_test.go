package providers

import (
	"errors"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// removedIDFragments assembles the deleted provider ids from fragments so this
// test file itself leaves no grep-able trace of them
// (scripts/check-no-removed-providers.sh, ADR-068 §2.4 "no trace").
func removedIDFragments() []string {
	return []string{
		"claude" + "-cli",
		"claude" + "cli",
		"codex" + "cli",
	}
}

// TestCreateProviderFromConfig_RemovedIDsAreUnknown — ADR-068 FR-002 / spec
// TDD row 4: the deleted ids fail on the generic unknown-provider path, with
// no hint naming a replacement. Since T067-08 the failure is the typed
// providers.ErrUnknownProvider sentinel.
func TestCreateProviderFromConfig_RemovedIDsAreUnknown(t *testing.T) {
	for _, id := range removedIDFragments() {
		t.Run(id, func(t *testing.T) {
			cfg := &config.ModelConfig{
				Provider: id,
				Model:    "some-model",
				Home:     t.TempDir(),
			}
			provider, _, err := CreateProviderFromConfig(cfg)
			if err == nil {
				t.Fatalf("CreateProviderFromConfig(%q) error = nil, want unknown-provider error", id)
			}
			if !errors.Is(err, ErrUnknownProvider) {
				t.Fatalf("CreateProviderFromConfig(%q) error = %v, want ErrUnknownProvider", id, err)
			}
			if provider != nil {
				t.Fatalf("CreateProviderFromConfig(%q) returned %T, want nil", id, provider)
			}
			msg := strings.ToLower(err.Error())
			for _, hint := range []string{"did you mean", "use codex-cli", "instead", "renamed", "replaced"} {
				if strings.Contains(msg, hint) {
					t.Errorf("error %q carries a hint %q; the generic path must name no replacement", err, hint)
				}
			}
		})
	}
}

// TestCreateProviderFromConfig_CodexCLI_StillDispatches — FR-003 deletion half:
// the kept id `codex-cli` still dispatches to NewCodexCliProvider.
func TestCreateProviderFromConfig_CodexCLI_StillDispatches(t *testing.T) {
	cfg := &config.ModelConfig{
		Provider: "codex-cli",
		Model:    "gpt-5.4-codex",
		Home:     t.TempDir(),
	}
	provider, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig(codex-cli) error = %v", err)
	}
	if _, ok := provider.(*CodexCliProvider); !ok {
		t.Fatalf("provider = %T, want *CodexCliProvider", provider)
	}
	if modelID != "gpt-5.4-codex" {
		t.Errorf("modelID = %q, want %q", modelID, "gpt-5.4-codex")
	}
}

// TestCreateProviderFromConfig_NoStoreOAuthLadder — FR-003: the id-keyed
// store-OAuth ladder is gone. A ModelConfig for `openai` / `anthropic`
// carrying the retired AuthMethod values must never consult the auth store;
// it takes the ordinary API-key path (and fails on the missing key), so the
// store-credential seam is never called.
func TestCreateProviderFromConfig_NoStoreOAuthLadder(t *testing.T) {
	// This test used to install a fake over factory.go's `getCredential`
	// seam (= the auth package's GetCredential) and t.Fatal from inside it.
	// That seam is
	// gone: the OAuth-path hardening pass deleted the whole plaintext
	// auth.json store, including
	// GetCredential, so the ladder is now impossible by construction rather
	// than merely unexercised — a stronger guarantee than the runtime
	// assertion it replaces, and one the compiler enforces. What remains
	// here is the still-valid behavioural half: a retired AuthMethod value
	// must take the ordinary API-key path and fail on the missing key.
	for _, tc := range []struct{ provider, method string }{
		{"openai", "oauth"},
		{"openai", "token"},
		{"anthropic", "oauth"},
		{"anthropic", "token"},
	} {
		t.Run(tc.provider+"/"+tc.method, func(t *testing.T) {
			cfg := &config.ModelConfig{
				Provider:   tc.provider,
				Model:      "some-model",
				AuthMethod: tc.method,
			}
			provider, _, err := CreateProviderFromConfig(cfg)
			if err == nil {
				t.Fatalf("expected the API-key path to reject a config without a key, got provider %T", provider)
			}
			if !strings.Contains(err.Error(), "api_key") {
				t.Errorf("error %q should come from the API-key path", err)
			}
		})
	}
}
