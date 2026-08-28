package gateway

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// newRegistrarTestStore builds an unlocked credential store in a temp dir.
func newRegistrarTestStore(t *testing.T) *credentials.Store {
	t.Helper()
	store := credentials.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 3)
	}
	if err := store.UnlockWithKey(key); err != nil {
		t.Fatalf("UnlockWithKey: %v", err)
	}
	return store
}

func storeOAuthEntry(t *testing.T, store *credentials.Store, vendor, access, refresh string) {
	t.Helper()
	payload := map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"expires_at":    time.Now().Add(time.Hour).Format(time.RFC3339Nano),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := store.Set(credentials.OAuthEntryName(vendor), string(data)); err != nil {
		t.Fatalf("Set: %v", err)
	}
}

// TestOAuthSensitiveValueRegistrar_ScrubsRefreshedToken is the gateway half of
// ADR-068 FR-046's agent-path gap. providers hands the registrar a freshly
// minted token; what has to happen next is that the LIVE config's scrubber
// starts filtering it — and, because RegisterSensitiveValues replaces rather
// than appends, that no OTHER already-protected secret is evicted in the
// process. A registrar that registered only the new value would look correct
// and would silently unprotect everything else.
func TestOAuthSensitiveValueRegistrar_ScrubsRefreshedToken(t *testing.T) {
	store := newRegistrarTestStore(t)
	storeOAuthEntry(t, store, "openai", "stored-access-token", "stored-refresh-token")
	// A SECOND signed-in vendor, and a provider API key reached through a
	// config ref. Both are part of the canonical "complete current set" that
	// boot registers, so both must survive a refresh-triggered
	// re-registration — RegisterSensitiveValues replaces rather than appends,
	// so a registrar that passed only the new token would silently unprotect
	// every one of them.
	storeOAuthEntry(t, store, "xai", "other-vendor-access-token", "other-vendor-refresh-token")
	if err := store.Set("OPENAI_API_KEY", "provider-api-key-secret"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	cfg := &config.Config{
		Providers: []*config.ModelConfig{{Provider: "openai", APIKeyRef: "OPENAI_API_KEY"}},
	}

	registrar := oauthSensitiveValueRegistrar(func() *config.Config { return cfg }, store)
	registrar("brand-new-access-token", "brand-new-refresh-token")

	replacer := cfg.SensitiveDataReplacer()
	if replacer == nil {
		t.Fatal("SensitiveDataReplacer returned nil after registration")
	}

	for _, secret := range []string{
		"brand-new-access-token",     // handed to the registrar directly
		"brand-new-refresh-token",    // ditto
		"stored-access-token",        // recomputed from the store
		"stored-refresh-token",       // ditto
		"other-vendor-access-token",  // a different vendor's stored tokens
		"other-vendor-refresh-token", // ditto
		"provider-api-key-secret",    // the config-ref-driven bundle
	} {
		out := replacer.Replace("prefix " + secret + " suffix")
		if strings.Contains(out, secret) {
			t.Errorf("secret %q survives the scrubber: %q", secret, out)
		}
	}
}

// TestOAuthSensitiveValueRegistrar_ReadsTheLiveConfig: a config reload swaps
// the *config.Config, so the registrar must read it through the getter on
// every call. Capturing the boot-time instance would leave every refresh after
// the first reload registering onto an object nothing consults.
func TestOAuthSensitiveValueRegistrar_ReadsTheLiveConfig(t *testing.T) {
	store := newRegistrarTestStore(t)

	bootCfg := &config.Config{}
	reloadedCfg := &config.Config{}
	live := bootCfg

	registrar := oauthSensitiveValueRegistrar(func() *config.Config { return live }, store)

	live = reloadedCfg
	registrar("post-reload-token")

	if out := reloadedCfg.SensitiveDataReplacer().Replace("post-reload-token"); strings.Contains(out, "post-reload-token") {
		t.Error("the post-reload config's scrubber does not filter the token — the registrar is not reading the live config")
	}
	if out := bootCfg.SensitiveDataReplacer().Replace("post-reload-token"); !strings.Contains(out, "post-reload-token") {
		t.Error("the token was registered onto the stale boot config")
	}
}

// TestOAuthSensitiveValueRegistrar_SurvivesMissingDependencies: this runs on
// the refresh path inside a live turn. A nil config (pre-boot, or a test that
// never built one) must be a no-op, never a panic that takes the turn down.
func TestOAuthSensitiveValueRegistrar_SurvivesMissingDependencies(t *testing.T) {
	store := newRegistrarTestStore(t)

	oauthSensitiveValueRegistrar(func() *config.Config { return nil }, store)("tok")
	oauthSensitiveValueRegistrar(nil, store)("tok")
	oauthSensitiveValueRegistrar(func() *config.Config { return &config.Config{} }, nil)("tok")
}
