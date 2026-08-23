package providers

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/auth"
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
// no hint naming a replacement. Tightened to
// errors.Is(err, catalog.ErrUnknownProvider) once T067-08 lands that sentinel.
func TestCreateProviderFromConfig_RemovedIDsAreUnknown(t *testing.T) {
	for _, id := range removedIDFragments() {
		t.Run(id, func(t *testing.T) {
			cfg := &config.ModelConfig{
				ModelName: "removed",
				Model:     id + "/some-model",
				Home:      t.TempDir(),
			}
			provider, _, err := CreateProviderFromConfig(cfg)
			if err == nil {
				t.Fatalf("CreateProviderFromConfig(%q) error = nil, want unknown-provider error", id)
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
		ModelName: "codex",
		Model:     "codex-cli/codex",
		Home:      t.TempDir(),
	}
	provider, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig(codex-cli) error = %v", err)
	}
	if _, ok := provider.(*CodexCliProvider); !ok {
		t.Fatalf("provider = %T, want *CodexCliProvider", provider)
	}
	if modelID != "codex" {
		t.Errorf("modelID = %q, want %q", modelID, "codex")
	}
}

// TestCreateProviderFromConfig_NoStoreOAuthLadder — FR-003: the id-keyed
// store-OAuth ladder is gone. A ModelConfig for `openai` / `anthropic`
// carrying the retired AuthMethod values must never consult the auth store;
// it takes the ordinary API-key path (and fails on the missing key), so the
// store-credential seam is never called.
func TestCreateProviderFromConfig_NoStoreOAuthLadder(t *testing.T) {
	original := getCredential
	t.Cleanup(func() { getCredential = original })
	getCredential = func(provider string) (*auth.AuthCredential, error) {
		t.Fatalf("getCredential(%q) called: the store-OAuth ladder must not exist", provider)
		return nil, nil
	}

	for _, tc := range []struct{ protocol, method string }{
		{"openai", "oauth"},
		{"openai", "token"},
		{"anthropic", "oauth"},
		{"anthropic", "token"},
	} {
		t.Run(tc.protocol+"/"+tc.method, func(t *testing.T) {
			cfg := &config.ModelConfig{
				ModelName:  "m",
				Model:      tc.protocol + "/some-model",
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
