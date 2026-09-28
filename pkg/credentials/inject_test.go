// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials_test

import (
	"encoding/hex"
	"errors"
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

// providersCfg builds a config.Config whose Providers slice holds
// *config.ModelConfig entries for the given (Name, APIKeyRef) pairs,
// exercising the cfg.Providers loop in InjectFromConfig (inject.go lines
// ~33-55) — the loop that resolves each configured provider's APIKeyRef
// through the credential store and injects the plaintext into the process
// environment (SEC-22).
func providersCfg(pairs ...[2]string) *config.Config {
	providers := make([]*config.ModelConfig, 0, len(pairs))
	for _, p := range pairs {
		// The error Owner is the PROVIDER id (ADR-067 X-25 deleted the
		// `model_name` alias the wrapper used to name).
		providers = append(providers, &config.ModelConfig{Provider: p[0], APIKeyRef: p[1]})
	}
	return &config.Config{Providers: providers}
}

// TestInjectFromConfig_MailboxesNestedMap_TwoPairsBothInjected verifies that
// InjectFromConfig walks the NESTED agent→workspace→mailbox map and injects
// a distinct env var for each (agent, workspace) pair's password_ref.
func TestInjectFromConfig_MailboxesNestedMap_TwoPairsBothInjected(t *testing.T) {
	store := newUnlockedTestStore(t)
	const refA, refB = "INJECT_TEST_MBX_PW_A", "INJECT_TEST_MBX_PW_B"
	// Test cleanup: env var unset is best-effort; process env is
	// scoped to this test process and any leak has no security impact.
	t.Cleanup(func() {
		if unsetErr := os.Unsetenv(refA); unsetErr != nil {
			_ = unsetErr
		}
		if unsetErr := os.Unsetenv(refB); unsetErr != nil {
			_ = unsetErr
		}
	})

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
	t.Cleanup(func() {
		if unsetErr := os.Unsetenv(sharedRef); unsetErr != nil {
			_ = unsetErr
		}
	})

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
	t.Cleanup(func() {
		if unsetErr := os.Unsetenv(okRef); unsetErr != nil {
			_ = unsetErr
		}
		if unsetErr := os.Unsetenv(missingRef); unsetErr != nil {
			_ = unsetErr
		}
	})

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

// ---------------------------------------------------------------------------
// cfg.Providers loop coverage (inject.go lines ~33-55) — this loop had ZERO
// direct test coverage before this addition; all 6 pre-existing tests above
// only exercise the cfg.Mailboxes loop / ResolveAll. Gap identified in the
// whole-codebase Backend-High test-gap review (2026-07-07).
// ---------------------------------------------------------------------------

// TestInjectFromConfig_ProviderValidAPIKeyRef_InjectsCorrectKey verifies that
// a provider with a valid APIKeyRef gets its resolved credential injected
// into the process environment under that ref's name. Two DIFFERENT
// providers with DIFFERENT refs must end up with DIFFERENT injected values —
// this is a differentiation check that would catch a hardcoded/copy-paste
// injection bug (e.g. always injecting the first provider's value).
func TestInjectFromConfig_ProviderValidAPIKeyRef_InjectsCorrectKey(t *testing.T) {
	store := newUnlockedTestStore(t)
	const refA, refB = "INJECT_TEST_PROV_KEY_A", "INJECT_TEST_PROV_KEY_B"
	t.Cleanup(func() {
		if unsetErr := os.Unsetenv(refA); unsetErr != nil {
			_ = unsetErr
		}
		if unsetErr := os.Unsetenv(refB); unsetErr != nil {
			_ = unsetErr
		}
	})

	if err := store.Set(refA, "sk-provider-a-secret"); err != nil {
		t.Fatalf("store.Set(refA): %v", err)
	}
	if err := store.Set(refB, "sk-provider-b-secret"); err != nil {
		t.Fatalf("store.Set(refB): %v", err)
	}

	cfg := providersCfg([2]string{"openai", refA}, [2]string{"anthropic", refB})

	errs := credentials.InjectFromConfig(cfg, store)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	gotA, gotB := os.Getenv(refA), os.Getenv(refB)
	if gotA != "sk-provider-a-secret" {
		t.Fatalf("env %s = %q, want %q", refA, gotA, "sk-provider-a-secret")
	}
	if gotB != "sk-provider-b-secret" {
		t.Fatalf("env %s = %q, want %q", refB, gotB, "sk-provider-b-secret")
	}
	if gotA == gotB {
		t.Fatalf("provider A and B injected the SAME value %q — suggests a hardcoded/copy-paste injection bug", gotA)
	}
}

// TestInjectFromConfig_ProviderMissingCredential_CollectedErrorDoesNotBlockOthers
// documents the ACTUAL current behavior for a provider whose APIKeyRef does
// not resolve in the store: the error is collected (naming the provider's
// Name and the missing ref) and returned, but processing continues — a
// sibling provider's valid ref is still injected. This mirrors the
// cfg.Mailboxes loop's already-tested contract; here it is verified for the
// cfg.Providers loop specifically.
func TestInjectFromConfig_ProviderMissingCredential_CollectedErrorDoesNotBlockOthers(t *testing.T) {
	store := newUnlockedTestStore(t)
	const okRef, missingRef = "INJECT_TEST_PROV_KEY_OK", "INJECT_TEST_PROV_KEY_MISSING"
	t.Cleanup(func() {
		if unsetErr := os.Unsetenv(okRef); unsetErr != nil {
			_ = unsetErr
		}
		if unsetErr := os.Unsetenv(missingRef); unsetErr != nil {
			_ = unsetErr
		}
	})

	if err := store.Set(okRef, "ok-provider-secret"); err != nil {
		t.Fatalf("store.Set: %v", err)
	}
	// missingRef is deliberately never stored — store.Get(missingRef) must fail.

	cfg := providersCfg([2]string{"working-provider", okRef}, [2]string{"broken-provider", missingRef})

	errs := credentials.InjectFromConfig(cfg, store)
	if len(errs) != 1 {
		t.Fatalf("expected exactly 1 collected error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), missingRef) {
		t.Fatalf("error must name the missing ref, got %q", errs[0].Error())
	}
	if !strings.Contains(errs[0].Error(), "broken-provider") {
		t.Fatalf("error must name the failing provider id, got %q", errs[0].Error())
	}
	if got := os.Getenv(okRef); got != "ok-provider-secret" {
		t.Fatalf(
			"sibling provider's ref must still be injected: env %s = %q, want %q",
			okRef,
			got,
			"ok-provider-secret",
		)
	}
	if got := os.Getenv(missingRef); got != "" {
		t.Fatalf("missing ref must NOT have been injected into env, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Non-channel credential refs (voice, web-search providers, skill
// marketplaces) — Lane H hotfix regression tests. Before the fix,
// InjectFromConfig walked ONLY cfg.Providers and cfg.Mailboxes, so the refs
// enumerated by the shared non-channel list (the same list ResolveAll
// resolves for redaction) never reached the process environment: a fully
// configured Tavily (enabled=true, api_key_ref set, credential in the vault)
// still ran keyless and selection fell through to DuckDuckGo.
// ---------------------------------------------------------------------------

// TestInjectFromConfig_NonChannelRefs_VoiceWebMarketplace_Injected verifies
// InjectFromConfig injects EVERY credential ref the shared non-channel
// enumeration declares: voice (ElevenLabs, Groq), web search (Brave, Tavily,
// Perplexity, GLM Search, Baidu Search) and skill marketplace (ClawHub
// AuthTokenRef, GitHub TokenRef). Each ref gets a DISTINCT stored value so a
// copy-paste/cross-wired injection (e.g. tavily receiving brave's key) is
// caught, not just a missing one.
func TestInjectFromConfig_NonChannelRefs_VoiceWebMarketplace_Injected(t *testing.T) {
	store := newUnlockedTestStore(t)

	refs := map[string]string{
		"LANEH_TEST_ELEVENLABS_KEY": "laneh-elevenlabs-secret",
		"LANEH_TEST_GROQ_KEY":       "laneh-groq-secret",
		"LANEH_TEST_BRAVE_KEY":      "laneh-brave-secret",
		"LANEH_TEST_TAVILY_KEY":     "laneh-tavily-secret",
		"LANEH_TEST_PERPLEXITY_KEY": "laneh-perplexity-secret",
		"LANEH_TEST_GLM_KEY":        "laneh-glm-secret",
		"LANEH_TEST_BAIDU_KEY":      "laneh-baidu-secret",
		"LANEH_TEST_CLAWHUB_TOKEN":  "laneh-clawhub-secret",
		"LANEH_TEST_GITHUB_TOKEN":   "laneh-github-secret",
	}
	for ref, val := range refs {
		if err := store.Set(ref, val); err != nil {
			t.Fatalf("store.Set(%s): %v", ref, err)
		}
	}
	t.Cleanup(func() {
		for ref := range refs {
			if unsetErr := os.Unsetenv(ref); unsetErr != nil {
				_ = unsetErr
			}
		}
	})

	cfg := &config.Config{
		Voice: config.VoiceConfig{
			ElevenLabsAPIKeyRef: "LANEH_TEST_ELEVENLABS_KEY",
			GroqAPIKeyRef:       "LANEH_TEST_GROQ_KEY",
		},
		Tools: config.ToolsConfig{
			Web: config.WebToolsConfig{
				Brave:       config.BraveConfig{APIKeyRef: "LANEH_TEST_BRAVE_KEY"},
				Tavily:      config.TavilyConfig{APIKeyRef: "LANEH_TEST_TAVILY_KEY"},
				Perplexity:  config.PerplexityConfig{APIKeyRef: "LANEH_TEST_PERPLEXITY_KEY"},
				GLMSearch:   config.GLMSearchConfig{APIKeyRef: "LANEH_TEST_GLM_KEY"},
				BaiduSearch: config.BaiduSearchConfig{APIKeyRef: "LANEH_TEST_BAIDU_KEY"},
			},
			Skills: config.SkillsToolsConfig{
				Marketplaces: []config.MarketplaceConfig{
					{Name: "clawhub", Type: "clawhub", AuthTokenRef: "LANEH_TEST_CLAWHUB_TOKEN", TokenRef: "LANEH_TEST_GITHUB_TOKEN"},
				},
			},
		},
	}

	errs := credentials.InjectFromConfig(cfg, store)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	for ref, want := range refs {
		if got := os.Getenv(ref); got != want {
			t.Fatalf("env %s = %q, want the value stored under that ref (non-channel refs must be injected at boot)", ref, got)
		}
	}
}

// TestInjectFromConfig_NonChannelMissingRef_IsScopedRefError verifies a
// missing non-channel ref is reported per category: the collected error is a
// *CredentialRefError whose Scope names the category (web search / voice /
// marketplace) and whose cause still unwraps to *NotFoundError — the same
// scoped-vs-store-wide contract the gateway's fatal-vs-degrade boot
// classification relies on for providers and mailboxes.
func TestInjectFromConfig_NonChannelMissingRef_IsScopedRefError(t *testing.T) {
	store := newUnlockedTestStore(t)

	cases := []struct {
		name  string
		cfg   *config.Config
		ref   string
		scope string
	}{
		{
			name:  "web search (tavily)",
			ref:   "LANEH_SCOPED_TEST_TAVILY_MISSING",
			scope: credentials.ScopeWebSearch,
			cfg: &config.Config{Tools: config.ToolsConfig{Web: config.WebToolsConfig{
				Tavily: config.TavilyConfig{APIKeyRef: "LANEH_SCOPED_TEST_TAVILY_MISSING"},
			}}},
		},
		{
			name:  "voice (groq)",
			ref:   "LANEH_SCOPED_TEST_GROQ_MISSING",
			scope: credentials.ScopeVoice,
			cfg: &config.Config{Voice: config.VoiceConfig{
				GroqAPIKeyRef: "LANEH_SCOPED_TEST_GROQ_MISSING",
			}},
		},
		{
			name:  "marketplace (clawhub)",
			ref:   "LANEH_SCOPED_TEST_CLAWHUB_MISSING",
			scope: credentials.ScopeMarketplace,
			cfg: &config.Config{Tools: config.ToolsConfig{Skills: config.SkillsToolsConfig{
				Marketplaces: []config.MarketplaceConfig{
					{Name: "clawhub", Type: "clawhub", AuthTokenRef: "LANEH_SCOPED_TEST_CLAWHUB_MISSING"},
				},
			}}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := credentials.InjectFromConfig(tc.cfg, store)
			if len(errs) != 1 {
				t.Fatalf("expected exactly 1 collected error, got %d: %v", len(errs), errs)
			}
			var refErr *credentials.CredentialRefError
			if !errors.As(errs[0], &refErr) {
				t.Fatalf("non-channel ref failure must be a *CredentialRefError (scoped, degradable); got %T: %v", errs[0], errs[0])
			}
			if refErr.Scope != tc.scope {
				t.Errorf("Scope = %q, want %q", refErr.Scope, tc.scope)
			}
			if refErr.Ref != tc.ref {
				t.Errorf("Ref = %q, want %q", refErr.Ref, tc.ref)
			}
			var notFound *credentials.NotFoundError
			if !errors.As(errs[0], &notFound) {
				t.Errorf("cause must still unwrap to *NotFoundError; got %v", errs[0])
			}
		})
	}
}

// TestResolveAll_NonChannelRefs_StillResolves guards the shared-enumeration
// refactor: ResolveAll's redaction bundle must still resolve voice, web
// search and marketplace plaintexts after the enumeration moved into the
// helper both functions now call.
func TestResolveAll_NonChannelRefs_StillResolves(t *testing.T) {
	store := newUnlockedTestStore(t)

	stored := map[string]string{
		"LANEH_RALL_TEST_ELEVENLABS_KEY": "rall-elevenlabs-secret",
		"LANEH_RALL_TEST_TAVILY_KEY":     "rall-tavily-secret",
		"LANEH_RALL_TEST_GITHUB_TOKEN":   "rall-github-secret",
	}
	for ref, val := range stored {
		if err := store.Set(ref, val); err != nil {
			t.Fatalf("store.Set(%s): %v", ref, err)
		}
	}

	cfg := &config.Config{
		Voice: config.VoiceConfig{ElevenLabsAPIKeyRef: "LANEH_RALL_TEST_ELEVENLABS_KEY"},
		Tools: config.ToolsConfig{
			Web: config.WebToolsConfig{
				Tavily: config.TavilyConfig{APIKeyRef: "LANEH_RALL_TEST_TAVILY_KEY"},
			},
			Skills: config.SkillsToolsConfig{
				Marketplaces: []config.MarketplaceConfig{
					{Name: "github", Type: "github", TokenRef: "LANEH_RALL_TEST_GITHUB_TOKEN"},
				},
			},
		},
	}

	bundle, errs := credentials.ResolveAll(cfg, store)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	for ref, want := range stored {
		if got := bundle[ref]; got != want {
			t.Fatalf("bundle[%s] = %q, want %q (ResolveAll must keep resolving non-channel refs)", ref, got, want)
		}
	}
}

// TestInjectFromConfig_ProviderNoAPIKeyRef_SkippedWithoutError verifies a
// provider with no APIKeyRef (a plain/no-credential-needed provider, e.g. a
// local model) is left alone: InjectFromConfig neither errors nor sets any
// environment variable for it, and its presence does not prevent a sibling
// provider (with a valid ref) from being injected. Also covers the
// whitespace-only-ref boundary: APIKeyRef "   " must be treated as empty,
// same as "", because inject.go does strings.TrimSpace(model.APIKeyRef)
// before the emptiness check.
func TestInjectFromConfig_ProviderNoAPIKeyRef_SkippedWithoutError(t *testing.T) {
	store := newUnlockedTestStore(t)
	const withRef = "INJECT_TEST_PROV_KEY_WITHREF"
	t.Cleanup(func() {
		if unsetErr := os.Unsetenv(withRef); unsetErr != nil {
			_ = unsetErr
		}
	})

	if err := store.Set(withRef, "with-ref-secret"); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	cfg := providersCfg(
		[2]string{"local-model", ""},       // no credential needed
		[2]string{"whitespace-ref", "   "}, // whitespace-only ref — TrimSpace makes this empty too
		[2]string{"cloud-model", withRef},
	)

	errs := credentials.InjectFromConfig(cfg, store)
	if len(errs) != 0 {
		t.Fatalf("expected no errors for providers with no/blank APIKeyRef, got %v", errs)
	}
	if got := os.Getenv(withRef); got != "with-ref-secret" {
		t.Fatalf(
			"provider with a real ref must still be injected: env %s = %q, want %q",
			withRef,
			got,
			"with-ref-secret",
		)
	}
}
