// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Tests for the ADR-096 web-search provider roles migration and the
// usability test it shares with every other consumer (spec:
// docs/internal/specs/web-search-provider-model-spec.md — sections
// "Config shape", "Shipped defaults (new install)", "Resolution",
// "Migration"; ADR-096 D4, D11, D12).
//
// Every expected value below is derived from the SPEC, not from running the
// implementation (oracle independence):
//
//   - The timing trap (T1) comes from the Migration section's "Where it
//     runs" correction: APIKey() reads the process environment, the
//     environment is populated at boot step 4 (InjectFromConfig), while
//     config-load migrations run at step 2. A migration at step 2 sees an
//     empty environment and would permanently record duckduckgo for every
//     install. The test pins BOTH halves: load alone writes nothing, and the
//     post-injection migration records the resolving provider.
//   - The Resolution table is the source for every usable/not-usable pair.
//   - The Migration step list and the (a)-(f) install table are the source
//     for winner, defer, depth, and flag-correction expectations.

// webRolesTestConfig writes a load-valid config.json whose tools.web section
// is the given JSON object verbatim. The surrounding shape is copied from
// minimalConfigJSON (audit_log_default_test.go), itself the CI-seed shape, so
// these tests fail for web-roles reasons only, never because the config
// could not load at all.
func webRolesTestConfig(t *testing.T, webSection string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{
  "version": ` + currentVersionJSON() + `,
  "agents": { "defaults": { "default_model": { "provider": "openrouter", "model": "z-ai/glm-5.2" } } },
  "providers": [
    {
      "provider": "openrouter",
      "model": "z-ai/glm-5.2",
      "api_base": "https://openrouter.ai/api/v1"
    }
  ],
  "tools": { "web": ` + webSection + ` }
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	return path
}

// readWebSection parses the config file at path and returns the tools.web
// object as a raw map, so assertions can distinguish "key absent" from
// "key present with empty value" — a distinction the spec makes load-bearing
// (absent means not migrated; an explicit none means no fallback).
func readWebSection(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	tools, _ := m["tools"].(map[string]any)
	web, _ := tools["web"].(map[string]any)
	if web == nil {
		t.Fatalf("tools.web section missing after migration at %s", path)
	}
	return web
}

// requireEnvUnset guarantees the ref's environment variable is absent, and
// keeps it absent for the test's lifetime. The migration's timing property
// is about an EMPTY environment at load time — without this guard a
// developer's shell could silently turn the timing test green.
func requireEnvUnset(t *testing.T, ref string) {
	t.Helper()
	if err := os.Unsetenv(ref); err != nil {
		t.Fatalf("unset %s: %v", ref, err)
	}
	t.Cleanup(func() {
		if err := os.Unsetenv(ref); err != nil {
			_ = err // best-effort: env is scoped to the test process
		}
	})
	if got := os.Getenv(ref); got != "" {
		t.Fatalf("precondition failed: %s is set in the test environment; the timing tests need it empty", ref)
	}
}

// assertRFC3339 parses s as RFC 3339 and fails when it is not.
func assertRFC3339(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("roles_migrated_at %q is not RFC 3339: %v", s, err)
	}
	return ts
}

// webRolesMarkerOrFail reads roles_migrated_at from disk and fails with a
// named message rather than panicking when the key is absent — a panic would
// mask which assertion caught the failure.
func webRolesMarkerOrFail(t *testing.T, path string) string {
	t.Helper()
	raw, ok := readWebSection(t, path)["roles_migrated_at"]
	if !ok {
		t.Fatalf("roles_migrated_at absent from disk at %s — the migration did not write the marker", path)
	}
	marker, ok := raw.(string)
	if !ok || marker == "" {
		t.Fatalf("roles_migrated_at = %v at %s, want a non-empty RFC 3339 string", raw, path)
	}
	return marker
}

// --- T1: the environment-timing trap ---------------------------------------
//
// The highest-value test in the lane. Half of it (load writes nothing) is
// the assertion that FAILS if the migration is ever moved into
// loadConfigInternal: a load-time migration on this fixture would see an
// empty environment (tavily.APIKey() == ""), fall to the final DuckDuckGo
// branch, and write default_provider=duckduckgo plus the marker — which
// makes both the disk assertion and the later tavily assertion fail.

func TestWebSearchRolesMigration_TimingTrap_TavilyRecordedOnlyWhenEnvPopulated(t *testing.T) {
	const ref = "TAVILY_API_KEY"
	requireEnvUnset(t, ref)

	path := webRolesTestConfig(t, `{
		"enabled": true,
		"tavily": { "enabled": true, "api_key_ref": "TAVILY_API_KEY" }
	}`)

	// Boot step 2: the load. It must NOT migrate.
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	web := readWebSection(t, path)
	for _, key := range []string{"default_provider", "fallback_provider", "roles_migrated_at"} {
		if _, present := web[key]; present {
			t.Fatalf("config load wrote %q to disk; the roles migration must run after credential injection (boot step 4), not at config load", key)
		}
	}
	if got := cfg.Tools.Web.Tavily.APIKey(); got != "" {
		t.Fatalf("precondition: tavily.APIKey() = %q at load time; the timing test needs an empty environment", got)
	}

	// Boot step 4: injection populates the process environment. Simulated by
	// setting the very variable InjectFromConfig's os.Setenv sets — the env
	// IS the mechanism the spec names, not a mock of it.
	t.Setenv(ref, "tvly-test-key-1")

	// The migration, where bootCredentials calls it: after injection.
	MigrateWebSearchRoles(cfg, path, nil)

	web = readWebSection(t, path)
	if got, want := web["default_provider"], "tavily"; got != want {
		t.Fatalf("default_provider = %v, want %q — the key resolves post-injection, so the chain winner is tavily", got, want)
	}
	if got, want := web["fallback_provider"], "none"; got != want {
		t.Fatalf("fallback_provider = %v, want %q", got, want)
	}
	marker, _ := web["roles_migrated_at"].(string)
	assertRFC3339(t, marker)

	if got := cfg.Tools.Web.DefaultProvider; got != "tavily" {
		t.Fatalf("in-memory DefaultProvider = %q, want %q", got, "tavily")
	}
	if got := cfg.Tools.Web.FallbackProvider; got != "none" {
		t.Fatalf("in-memory FallbackProvider = %q, want %q", got, "none")
	}
	assertRFC3339(t, cfg.Tools.Web.RolesMigratedAt)
}

// --- T2: the ambiguous install defers (Migration step 2, install row f) ----

func TestWebSearchRolesMigration_DefersWhenEnabledKeyedProviderKeyDoesNotResolve(t *testing.T) {
	const ref = "TAVILY_API_KEY"
	requireEnvUnset(t, ref)

	path := webRolesTestConfig(t, `{
		"enabled": true,
		"tavily": { "enabled": true, "api_key_ref": "TAVILYCHAINTEST" }
	}`)

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// Snapshot the in-memory roles state BEFORE the call: a deferred
	// migration must change neither disk nor memory. The seeded marker
	// timestamp makes the memory snapshot load-bearing — a wrongly-run
	// migration would re-stamp roles_migrated_at and fail this comparison.
	memBefore := cfg.Tools.Web

	MigrateWebSearchRoles(cfg, path, nil)

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("deferred migration must write NOTHING (spec step 2: retry on next boot), but config.json changed:\nbefore: %s\nafter:  %s", before, after)
	}
	if cfg.Tools.Web.DefaultProvider != memBefore.DefaultProvider {
		t.Fatalf("deferred migration changed in-memory DefaultProvider %q -> %q; step 2 writes nothing anywhere",
			memBefore.DefaultProvider, cfg.Tools.Web.DefaultProvider)
	}
	if cfg.Tools.Web.RolesMigratedAt != memBefore.RolesMigratedAt {
		t.Fatalf("deferred migration re-stamped in-memory roles_migrated_at %q -> %q; step 2 writes nothing anywhere (retry next boot)",
			memBefore.RolesMigratedAt, cfg.Tools.Web.RolesMigratedAt)
	}
}

// --- T3: install row (e) — "Set active" shape, Migration step 9 ------------

func TestWebSearchRolesMigration_KeyedProviderOffWithResolvingRefIsOperatorChoice(t *testing.T) {
	const ref = "TAVILY_API_KEY"
	t.Setenv(ref, "tvly-test-key-3")

	path := webRolesTestConfig(t, `{
		"enabled": true,
		"duckduckgo": { "enabled": true },
		"tavily": { "enabled": false, "api_key_ref": "TAVILY_API_KEY" }
	}`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	MigrateWebSearchRoles(cfg, path, nil)

	web := readWebSection(t, path)
	if got, want := web["default_provider"], "tavily"; got != want {
		t.Fatalf("default_provider = %v, want %q — step 9: a keyed provider switched off with a resolving ref is the operator's choice (install row e)", got, want)
	}
	tavily, _ := web["tavily"].(map[string]any)
	if tavily == nil {
		t.Fatal("tools.web.tavily object missing from disk")
	}
	if got, ok := tavily["enabled"].(bool); !ok || !got {
		t.Fatalf("tools.web.tavily.enabled = %v, want true — step 9 must flip the flag ON DISK, not only in memory", tavily["enabled"])
	}
	if got, want := web["fallback_provider"], "none"; got != want {
		t.Fatalf("fallback_provider = %v, want %q", got, want)
	}
	if got := cfg.Tools.Web.Tavily.Enabled; !got {
		t.Fatal("in-memory tavily.Enabled still false — the in-memory config must be corrected too")
	}
}

// --- T4: depth defaults (Migration step 7 + the cost note) -----------------

func TestWebSearchRolesMigration_DepthDefaultsWrittenOnlyWhenObjectExistsOnDisk(t *testing.T) {
	const ref = "TAVILY_API_KEY"
	t.Setenv(ref, "tvly-test-key-4")

	t.Run("objects exist on disk", func(t *testing.T) {
		path := webRolesTestConfig(t, `{
			"enabled": true,
			"tavily": { "enabled": true, "api_key_ref": "TAVILY_API_KEY" },
			"glm_search": { "enabled": false }
		}`)
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}

		MigrateWebSearchRoles(cfg, path, nil)

		web := readWebSection(t, path)
		tavily, _ := web["tavily"].(map[string]any)
		if got, ok := tavily["search_depth"].(string); !ok || got != "advanced" {
			t.Fatalf("tavily.search_depth = %v, want \"advanced\" — the cost note: TavilySearchProvider hardcodes advanced today, so writing basic would silently cheapen a search nobody asked to cheapen", tavily["search_depth"])
		}
		glm, _ := web["glm_search"].(map[string]any)
		if got, ok := glm["content_size"].(string); !ok || got != "medium" {
			t.Fatalf("glm_search.content_size = %v, want %q", glm["content_size"], "medium")
		}
		perplexity, ok := web["perplexity"].(map[string]any)
		if ok {
			if _, present := perplexity["search_context_size"]; present {
				t.Fatal("perplexity.search_context_size was written; step 7 forbids writing it (the field is not sent today and writing one would change the bill)")
			}
		}
		if got := cfg.Tools.Web.Tavily.SearchDepth; got != "advanced" {
			t.Fatalf("in-memory Tavily.SearchDepth = %q, want %q", got, "advanced")
		}
	})

	t.Run("objects absent on disk stay absent", func(t *testing.T) {
		path := webRolesTestConfig(t, `{
			"enabled": true
		}`)
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}

		MigrateWebSearchRoles(cfg, path, nil)

		web := readWebSection(t, path)
		if _, present := web["tavily"]; present {
			t.Fatal("tools.web.tavily object created by the migration; step 7 says leave it absent when the file has no tavily object")
		}
		if _, present := web["glm_search"]; present {
			t.Fatal("tools.web.glm_search object created by the migration; step 7 says leave it absent when the file has no glm_search object")
		}
	})
}

// --- T5: idempotency keys off the marker, not default_provider's presence --

func TestWebSearchRolesMigration_IdempotencyKeysOffMarker(t *testing.T) {
	const ref = "TAVILY_API_KEY"
	t.Setenv(ref, "tvly-test-key-5")

	t.Run("second run is a byte-identical no-op", func(t *testing.T) {
		path := webRolesTestConfig(t, `{
			"enabled": true,
			"tavily": { "enabled": true, "api_key_ref": "TAVILY_API_KEY" }
		}`)
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		MigrateWebSearchRoles(cfg, path, nil)
		afterFirst, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read after first run: %v", err)
		}
		firstMarker := webRolesMarkerOrFail(t, path)

		MigrateWebSearchRoles(cfg, path, nil)

		afterSecond, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read after second run: %v", err)
		}
		if string(afterFirst) != string(afterSecond) {
			t.Fatalf("second migration run rewrote config.json; the marker must gate it to a no-op\nfirst:  %s\nsecond: %s", afterFirst, afterSecond)
		}
		if got := webRolesMarkerOrFail(t, path); got != firstMarker {
			t.Fatalf("roles_migrated_at changed from %q to %q; a rerun must not re-stamp the marker", firstMarker, got)
		}
	})

	t.Run("marker absent but default_provider present still migrates", func(t *testing.T) {
		path := webRolesTestConfig(t, `{
			"enabled": true,
			"default_provider": "tavily",
			"tavily": { "enabled": true, "api_key_ref": "TAVILY_API_KEY" }
		}`)
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		MigrateWebSearchRoles(cfg, path, nil)

		web := readWebSection(t, path)
		if marker, _ := web["roles_migrated_at"].(string); marker == "" {
			t.Fatal("migration did not run despite an absent marker; the gate is the marker's presence, not default_provider's")
		}
	})
}

// --- T6: everything unusable — final DuckDuckGo branch (Migration step 8) --

func TestWebSearchRolesMigration_AllUnusableTurnsDuckDuckGoBackOn(t *testing.T) {
	const ref = "TAVILY_API_KEY"
	requireEnvUnset(t, ref)

	path := webRolesTestConfig(t, `{
		"enabled": true,
		"duckduckgo": { "enabled": false }
	}`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	MigrateWebSearchRoles(cfg, path, nil)

	web := readWebSection(t, path)
	if got, want := web["default_provider"], "duckduckgo"; got != want {
		t.Fatalf("default_provider = %v, want %q — the final DuckDuckGo branch wins even when the flag is false", got, want)
	}
	ddg, _ := web["duckduckgo"].(map[string]any)
	if got, ok := ddg["enabled"].(bool); !ok || !got {
		t.Fatalf("duckduckgo.enabled = %v, want true — step 8 turns a switched-off DuckDuckGo back on when it was the live provider", ddg["enabled"])
	}
	if got := cfg.Tools.Web.DuckDuckGo.Enabled; !got {
		t.Fatal("in-memory DuckDuckGo.Enabled still false")
	}
}

// --- T7: the winner is today's chain order; catalogue ids glm/baidu --------

func TestWebSearchRolesMigration_WinnerFollowsTodayChainOrder(t *testing.T) {
	cases := []struct {
		name       string
		webSection string
		envRefs    map[string]string
		want       string
	}{
		{
			name: "keyed pair resolves to the earlier chain entry",
			webSection: `{
				"enabled": true,
				"brave": { "enabled": true, "api_key_ref": "BRAVE_API_KEY" },
				"tavily": { "enabled": true, "api_key_ref": "TAVILYCHAINTEST" }
			}`,
			envRefs: map[string]string{"BRAVE_API_KEY": "b", "TAVILYCHAINTEST": "t"},
			want:    "brave",
		},
		{
			name: "searxng sits above tavily in the chain",
			webSection: `{
				"enabled": true,
				"searxng": { "enabled": true, "base_url": "http://searx.internal" },
				"tavily": { "enabled": true, "api_key_ref": "TAVILYCHAINTEST" }
			}`,
			envRefs: map[string]string{"TAVILYCHAINTEST": "t"},
			want:    "searxng",
		},
		{
			name: "glm catalogue id",
			webSection: `{
				"enabled": true,
				"duckduckgo": { "enabled": false },
				"glm_search": { "enabled": true, "api_key_ref": "GLMCHAINTEST" }
			}`,
			envRefs: map[string]string{"GLMCHAINTEST": "g"},
			want:    "glm",
		},
		{
			name: "baidu catalogue id",
			webSection: `{
				"enabled": true,
				"duckduckgo": { "enabled": false },
				"baidu_search": { "enabled": true, "api_key_ref": "BAIDUCHAINTEST" }
			}`,
			envRefs: map[string]string{"BAIDUCHAINTEST": "b"},
			want:    "baidu",
		},
		{
			name: "baidu above glm in the chain",
			webSection: `{
				"enabled": true,
				"duckduckgo": { "enabled": false },
				"glm_search": { "enabled": true, "api_key_ref": "GLMCHAINTEST" },
				"baidu_search": { "enabled": true, "api_key_ref": "BAIDUCHAINTEST" }
			}`,
			envRefs: map[string]string{"GLMCHAINTEST": "g", "BAIDUCHAINTEST": "b"},
			want:    "baidu",
		},
		{
			name: "perplexity at the top of the chain",
			webSection: `{
				"enabled": true,
				"perplexity": { "enabled": true, "api_key_ref": "PERPLEXITYCHAINTEST" },
				"brave": { "enabled": true, "api_key_ref": "BRAVECHAINTEST" }
			}`,
			envRefs: map[string]string{"PERPLEXITYCHAINTEST": "p", "BRAVECHAINTEST": "b"},
			want:    "perplexity",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for ref, val := range tc.envRefs {
				t.Setenv(ref, val)
			}
			path := webRolesTestConfig(t, tc.webSection)
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			MigrateWebSearchRoles(cfg, path, nil)
			if got := readWebSection(t, path)["default_provider"]; got != tc.want {
				t.Fatalf("default_provider = %v, want %q (the chain NewWebSearchTool builds today)", got, tc.want)
			}
		})
	}
}

// --- T8: the Resolution table, as boundaries on the shared usability test --

func TestUsableSearchProvider_Boundaries(t *testing.T) {
	cases := []struct {
		name string
		id   string
		web  func(w *WebToolsConfig)
		env  map[string]string // refs to set (empty-string value = set to "")
		want bool
	}{
		{
			name: "keyed provider switched off though ref set",
			id:   "tavily",
			web:  func(w *WebToolsConfig) { w.Tavily = TavilyConfig{Enabled: false, APIKeyRef: "WS_TAVILY_KEY"} },
			want: false,
		},
		{
			name: "keyed provider on but no ref configured",
			id:   "tavily",
			web:  func(w *WebToolsConfig) { w.Tavily = TavilyConfig{Enabled: true, APIKeyRef: ""} },
			want: false,
		},
		{
			name: "keyed provider on, ref set, environment not populated",
			id:   "tavily",
			web:  func(w *WebToolsConfig) { w.Tavily = TavilyConfig{Enabled: true, APIKeyRef: "WS_TAVILY_KEY"} },
			want: false,
		},
		{
			name: "keyed provider on, ref resolves to an empty value",
			id:   "tavily",
			web:  func(w *WebToolsConfig) { w.Tavily = TavilyConfig{Enabled: true, APIKeyRef: "WS_TAVILY_KEY"} },
			env:  map[string]string{"WS_TAVILY_KEY": ""},
			want: false,
		},
		{
			name: "keyed provider fully configured",
			id:   "tavily",
			web:  func(w *WebToolsConfig) { w.Tavily = TavilyConfig{Enabled: true, APIKeyRef: "WS_TAVILY_KEY"} },
			env:  map[string]string{"WS_TAVILY_KEY": "k"},
			want: true,
		},
		{
			name: "exa switched off though ref set",
			id:   "exa",
			web:  func(w *WebToolsConfig) { w.Exa = ExaConfig{Enabled: false, APIKeyRef: "WS_EXA_KEY"} },
			want: false,
		},
		{
			name: "exa on but keyless",
			id:   "exa",
			web:  func(w *WebToolsConfig) { w.Exa = ExaConfig{Enabled: true, APIKeyRef: "WS_EXA_KEY"} },
			want: false,
		},
		{
			name: "exa on with a resolving key",
			id:   "exa",
			web:  func(w *WebToolsConfig) { w.Exa = ExaConfig{Enabled: true, APIKeyRef: "WS_EXA_KEY"} },
			env:  map[string]string{"WS_EXA_KEY": "k"},
			want: true,
		},
		{
			name: "searxng on with a whitespace-only base URL",
			id:   "searxng",
			web:  func(w *WebToolsConfig) { w.SearXNG = SearXNGConfig{Enabled: true, BaseURL: "   "} },
			want: false,
		},
		{
			name: "searxng on with a padded base URL",
			id:   "searxng",
			web: func(w *WebToolsConfig) {
				w.SearXNG = SearXNGConfig{Enabled: true, BaseURL: "  http://searx.internal  "}
			},
			want: true,
		},
		{
			name: "searxng off though base URL set",
			id:   "searxng",
			web: func(w *WebToolsConfig) {
				w.SearXNG = SearXNGConfig{Enabled: false, BaseURL: "http://searx.internal"}
			},
			want: false,
		},
		{
			name: "duckduckgo switched on",
			id:   "duckduckgo",
			web:  func(w *WebToolsConfig) { w.DuckDuckGo = DuckDuckGoConfig{Enabled: true} },
			want: true,
		},
		{
			name: "duckduckgo switched off",
			id:   "duckduckgo",
			web:  func(w *WebToolsConfig) { w.DuckDuckGo = DuckDuckGoConfig{Enabled: false} },
			want: false,
		},
		{
			name: "unknown id is not usable (R9)",
			id:   "no-such-provider",
			web:  func(w *WebToolsConfig) {},
			want: false,
		},
		{
			name: "empty id is not usable",
			id:   "",
			web:  func(w *WebToolsConfig) {},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for ref, val := range tc.env {
				t.Setenv(ref, val)
			}
			var w WebToolsConfig
			tc.web(&w)
			if got := w.UsableSearchProvider(tc.id); got != tc.want {
				t.Fatalf("UsableSearchProvider(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}

// --- T9: shipped defaults for a fresh install ------------------------------

func TestDefaultToolsConfig_WebSearchRolesShipMigrated(t *testing.T) {
	cfg := DefaultConfig()
	w := cfg.Tools.Web

	if got, want := w.DefaultProvider, "duckduckgo"; got != want {
		t.Fatalf("fresh install default_provider = %q, want %q (Shipped defaults table)", got, want)
	}
	if got, want := w.FallbackProvider, "none"; got != want {
		t.Fatalf("fresh install fallback_provider = %q, want %q (Shipped defaults table)", got, want)
	}
	assertRFC3339(t, w.RolesMigratedAt) // "the install timestamp"
	if got, want := w.Tavily.SearchDepth, "basic"; got != want {
		t.Fatalf("fresh install tavily.search_depth = %q, want %q (Shipped defaults table)", got, want)
	}
	if got, want := w.GLMSearch.ContentSize, "medium"; got != want {
		t.Fatalf("fresh install glm_search.content_size = %q, want %q (Depth table, GLM shipped default)", got, want)
	}
	if w.Exa.Enabled {
		t.Fatal("fresh install exa.enabled = true, want false (Exa table shipped default)")
	}
	if !w.DuckDuckGo.Enabled {
		t.Fatal("fresh install duckduckgo.enabled = false, want true (Shipped defaults table)")
	}
	for _, off := range []struct {
		name string
		on   bool
	}{
		{"brave", w.Brave.Enabled},
		{"tavily", w.Tavily.Enabled},
		{"perplexity", w.Perplexity.Enabled},
		{"searxng", w.SearXNG.Enabled},
		{"glm", w.GLMSearch.Enabled},
		{"baidu", w.BaiduSearch.Enabled},
	} {
		if off.on {
			t.Fatalf("fresh install %s.enabled = true, want false (Shipped defaults: every other search provider disabled)", off.name)
		}
	}

	// The roles keys must not use omitempty — an explicit `none` has to
	// survive a save, and a missing key means "not migrated". Pin the
	// round-trip.
	blob, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal WebToolsConfig: %v", err)
	}
	if !strings.Contains(string(blob), `"fallback_provider":"none"`) {
		t.Fatalf("fallback_provider did not survive marshalling (omitempty on the roles keys would drop an explicit none): %s", blob)
	}
	var back WebToolsConfig
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatalf("unmarshal WebToolsConfig: %v", err)
	}
	if back.FallbackProvider != "none" || back.DefaultProvider != "duckduckgo" || back.RolesMigratedAt == "" {
		t.Fatalf("roles keys lost in a marshal/unmarshal round-trip: default=%q fallback=%q marker=%q",
			back.DefaultProvider, back.FallbackProvider, back.RolesMigratedAt)
	}
}
