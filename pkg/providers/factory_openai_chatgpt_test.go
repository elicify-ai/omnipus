package providers

import (
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// withTestCredentialStore wires DefaultCredentialStore to a fresh, unlocked
// store in a temp dir for the duration of the test, restoring the previous
// value on cleanup — mirrors the seam pkg/gateway/gateway.go's
// bootCredentials wires once at real gateway boot.
func withTestCredentialStore(t *testing.T) *credentials.Store {
	t.Helper()
	store := credentials.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	if err := store.UnlockWithKey(key); err != nil {
		t.Fatalf("UnlockWithKey: %v", err)
	}
	prev := DefaultCredentialStore
	SetDefaultCredentialStore(store)
	t.Cleanup(func() { SetDefaultCredentialStore(prev) })
	return store
}

// openAIChatGPTTestConfig builds a ModelConfig that reaches the
// ProtocolOpenAICompatible dispatch case without depending on the real
// catalog carrying an "openai-chatgpt" row — CreateProviderFromConfig's
// `cfg.Provider == "openai-chatgpt"` special case (T068-14, ahead of
// requireKey) only cares about the resolved protocol and the provider id,
// both of which the "custom row" path (T067-08's factory collapse,
// resolveRow) produces identically to a real catalog row. Mirrors the
// Custom:true pattern factory_provider_test.go already uses for DS-3.13/14.
func openAIChatGPTTestConfig() *config.ModelConfig {
	return &config.ModelConfig{
		Provider: "openai-chatgpt",
		Custom:   true,
		Protocol: "openai-compatible",
		Model:    "gpt-5.4",
		APIBase:  "https://chatgpt.example/backend-api/codex",
	}
}

// TestCreateProviderFromConfig_OpenAIChatGPT_Dispatches — ADR-068 §8b
// (T068-14): "openai-chatgpt" dispatches to a *CodexProvider backed by a
// store-OAuth token source, distinct from codex-cli's subprocess dispatch.
func TestCreateProviderFromConfig_OpenAIChatGPT_Dispatches(t *testing.T) {
	withTestCredentialStore(t)

	provider, modelID, err := CreateProviderFromConfig(openAIChatGPTTestConfig())
	if err != nil {
		t.Fatalf("CreateProviderFromConfig(openai-chatgpt) error = %v", err)
	}
	if _, ok := provider.(*CodexProvider); !ok {
		t.Fatalf("provider = %T, want *CodexProvider", provider)
	}
	if modelID != "gpt-5.4" {
		t.Errorf("modelID = %q, want %q", modelID, "gpt-5.4")
	}
}

// TestCreateProviderFromConfig_OpenAIChatGPT_NoStoreFailsClosed asserts that
// with DefaultCredentialStore never wired (the SetDefaultCredentialStore
// seam untouched), dispatch fails with a clear error rather than a
// nil-pointer panic deep inside the token source.
func TestCreateProviderFromConfig_OpenAIChatGPT_NoStoreFailsClosed(t *testing.T) {
	prev := DefaultCredentialStore
	SetDefaultCredentialStore(nil)
	t.Cleanup(func() { SetDefaultCredentialStore(prev) })

	_, _, err := CreateProviderFromConfig(openAIChatGPTTestConfig())
	if err == nil {
		t.Fatal("expected an error when no credential store has been wired")
	}
}
