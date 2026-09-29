package config

// RED for #1056 F-2 and the founder's 2026-09-29 reversal of ADR-096 D10:
// SearXNG is removed, not hidden. The catalogue, config field, persisted block,
// and any migrated role must stop offering an unusable removed provider. These
// tests exercise the real catalogue and config load/save/migration boundaries.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFixF2_SearXNGAbsentFromCatalogueAndConfigType(t *testing.T) {
	for _, def := range SearchProviderCatalogue {
		if def.ID == "searxng" || def.Section == "searxng" {
			t.Fatalf("removed provider is still catalogued: %+v (issue #1056 F-2, amended ADR-096 D10)", def)
		}
	}
	if _, found := SearchProviderDefByID("searxng"); found {
		t.Fatal("SearchProviderDefByID still finds removed SearXNG")
	}
	if _, found := reflect.TypeOf(WebToolsConfig{}).FieldByName("SearXNG"); found {
		t.Fatal("WebToolsConfig still exposes removed SearXNG configuration (amended ADR-096 D10)")
	}
}

// The operator reference is the sole user-facing provider configuration
// guide. Historical ADR discussions can name a retired provider, but this
// current configuration table must not offer removed SearXNG (#1056 F-2).
func TestFixF2_UserToolConfigurationDoesNotOfferSearXNG(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "operations", "tools-configuration.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read user-facing tool configuration guide: %v", err)
	}
	text := strings.ToLower(string(content))
	if !strings.Contains(text, "duckduckgo") || !strings.Contains(text, "tavily") {
		t.Fatal("documentation guard cannot see the retained search services; inspect fixture path before trusting an empty SearXNG search")
	}
	if strings.Contains(text, "searxng") {
		t.Fatal("user-facing tools configuration still documents removed SearXNG as a provider (issue #1056 F-2)")
	}
}

// SearXNG never had a CredRef in the catalogue; pin the absence of a dangling
// credential-store ref without inventing a credential migration that did not
// exist. The on-load fixture below also includes a stale legacy api_key_ref.
func TestFixF2_NoSearXNGCredentialReferenceInCatalogue(t *testing.T) {
	for _, def := range SearchProviderCatalogue {
		if strings.Contains(strings.ToLower(def.CredRef), "searx") {
			t.Fatalf("removed SearXNG has a dangling credential ref in catalogue entry %+v", def)
		}
	}
}

// Founder: deletion from existing installations cannot depend on the operator
// saving another setting. Observe the file bytes immediately after a real
// LoadConfig, with an already-migrated marker to isolate the load path from
// MigrateWebSearchRoles. An unknown nested key being ignored in memory is NOT
// sufficient: the original disk block, including its stale ref, must be gone.
func TestFixF2_LegacySearXNGBlockRemovedFromDiskOnLoad(t *testing.T) {
	path := webRolesTestConfig(t, `{
		"enabled": true,
		"default_provider": "duckduckgo",
		"fallback_provider": "none",
		"roles_migrated_at": "2026-09-01T12:00:00Z",
		"duckduckgo": {"enabled": true},
		"searxng": {"enabled": true, "base_url": "https://searx.example", "api_key_ref": "OLD_SEARXNG_KEY"}
	}`)
	if _, present := readWebSection(t, path)["searxng"]; !present {
		t.Fatal("fixture has no legacy searxng block; deletion test could not fail")
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("existing config with legacy searxng block must load cleanly: %v", err)
	}
	if got := cfg.Tools.Web.DefaultProvider; got != "duckduckgo" {
		t.Fatalf("unrelated default role changed while removing retired block: got %q", got)
	}
	if block, present := readWebSection(t, path)["searxng"]; present {
		t.Fatalf("load left removed SearXNG config and credential ref ON DISK: %v", block)
	}
}

// A later ordinary save must not resurrect unknown legacy data after the load
// scrub. Pin the public load -> save -> load round-trip separately from the
// immediate on-load write: otherwise a future preservation merge could silently
// reinsert the removed block on the next unrelated settings save.
func TestFixF2_LegacySearXNGBlockStaysRemovedAfterSaveReload(t *testing.T) {
	path := webRolesTestConfig(t, `{
		"enabled": true,
		"default_provider": "duckduckgo",
		"fallback_provider": "none",
		"roles_migrated_at": "2026-09-01T12:00:00Z",
		"duckduckgo": {"enabled": true},
		"searxng": {"enabled": true, "base_url": "https://searx.example"}
	}`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig legacy file: %v", err)
	}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig after load: %v", err)
	}
	if _, err := LoadConfig(path); err != nil {
		t.Fatalf("reload after save: %v", err)
	}
	web := readWebSection(t, path)
	if block, present := web["searxng"]; present {
		t.Fatalf("save/reload resurrected removed SearXNG config: %v", block)
	}
	if got := web["default_provider"]; got != "duckduckgo" {
		t.Fatalf("unrelated search role lost during scrub: got %v, want duckduckgo", got)
	}
}

// D11 previously placed SearXNG at position 3, ahead of Tavily at 4. With
// full removal the historic ranking cannot award an ID that no longer has an
// implementation; the next usable retained provider must answer. A missing
// disk marker genuinely reaches this migration despite overlaid defaults.
func TestFixF2_UnmigratedSearXNGAndTavilyChoosesUsableTavily(t *testing.T) {
	t.Setenv("TAVILYCHAINTEST", "tvly-test-key")
	path := webRolesTestConfig(t, `{
		"enabled": true,
		"searxng": {"enabled": true, "base_url": "https://searx.example"},
		"tavily": {"enabled": true, "api_key_ref": "TAVILYCHAINTEST"}
	}`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig legacy roles: %v", err)
	}
	MigrateWebSearchRoles(cfg, path, nil)
	web := readWebSection(t, path)
	if got := web["default_provider"]; got != "tavily" {
		t.Fatalf("removed position-3 provider cannot become default; next usable retained provider is tavily, got %v", got)
	}
	if !cfg.Tools.Web.UsableSearchProvider(cfg.Tools.Web.DefaultProvider) {
		t.Fatalf("migrated default %q is not usable after SearXNG removal", cfg.Tools.Web.DefaultProvider)
	}
	if _, present := web["searxng"]; present {
		t.Fatal("roles migration reintroduced a removed SearXNG block to disk")
	}
}

func TestFixF2_UnmigratedSearXNGOnlyChoosesUsableDuckDuckGo(t *testing.T) {
	path := webRolesTestConfig(t, `{
		"enabled": true,
		"searxng": {"enabled": true, "base_url": "https://searx.example"}
	}`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig legacy roles: %v", err)
	}
	MigrateWebSearchRoles(cfg, path, nil)
	if got := readWebSection(t, path)["default_provider"]; got != "duckduckgo" {
		t.Fatalf("removed provider was the only configured one; final usable default must be duckduckgo, got %v", got)
	}
	if !cfg.Tools.Web.UsableSearchProvider(cfg.Tools.Web.DefaultProvider) {
		t.Fatalf("fallback default %q is not usable", cfg.Tools.Web.DefaultProvider)
	}
}

// A file with roles_migrated_at will never re-run MigrateWebSearchRoles. The
// normal load must instead retire stale role strings explicitly, persist that
// repair immediately, and explain it once. A removed DEFAULT is deliberately
// left undecided: choosing an available replacement would make a search call
// on the operator's behalf. A removed FALLBACK does not erase a valid default.
func TestFixF2_AlreadyMigratedSearXNGRolesAreClearedOnLoad(t *testing.T) {
	cases := []struct {
		name, defaultID, fallbackID, wantDefault, wantFallback string
		clearedRoles                                           []string
	}{
		{"both roles removed", "searxng", "searxng", "", "none", []string{"default_provider", "fallback_provider"}},
		{"removed default with retained fallback", "searxng", "duckduckgo", "", "none", []string{"default_provider", "fallback_provider"}},
		{"removed fallback keeps valid Tavily default", "tavily", "searxng", "tavily", "none", []string{"fallback_provider"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := webRolesTestConfig(t, `{
				"enabled": true,
				"default_provider": "`+tc.defaultID+`",
				"fallback_provider": "`+tc.fallbackID+`",
				"roles_migrated_at": "2026-09-01T12:00:00Z",
				"duckduckgo": {"enabled": true},
				"tavily": {"enabled": true},
				"searxng": {"enabled": true, "base_url": "https://searx.example", "api_key_ref": "OLD_SEARXNG_KEY"}
			}`)
			if before := readWebSection(t, path); before["default_provider"] != tc.defaultID || before["fallback_provider"] != tc.fallbackID {
				t.Fatalf("stale-role fixture does not carry the requested roles: %v", before)
			}
			logs := captureWarnings(t)
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("already-migrated config with removed roles must load: %v", err)
			}
			web := readWebSection(t, path) // ONE load, before any save or migration.
			if got := cfg.Tools.Web.DefaultProvider; got != tc.wantDefault {
				t.Errorf("in-memory default = %q, want %q", got, tc.wantDefault)
			}
			if got := cfg.Tools.Web.FallbackProvider; got != tc.wantFallback {
				t.Errorf("in-memory fallback = %q, want %q", got, tc.wantFallback)
			}
			if got := web["default_provider"]; got != tc.wantDefault {
				t.Errorf("on-disk default after one load = %v, want %q", got, tc.wantDefault)
			}
			if got := web["fallback_provider"]; got != tc.wantFallback {
				t.Errorf("on-disk fallback after one load = %v, want %q", got, tc.wantFallback)
			}
			if _, present := web["searxng"]; present {
				t.Error("removed SearXNG block and stale api_key_ref remain on disk after one load")
			}
			if got := web["roles_migrated_at"]; got != "2026-09-01T12:00:00Z" {
				t.Errorf("clearing stale roles changed the migration marker to %v", got)
			}
			var warnings []string
			for _, line := range strings.Split(logs.String(), "\n") {
				if strings.Contains(strings.ToLower(line), "searxng") {
					warnings = append(warnings, line)
				}
			}
			if len(warnings) != 1 {
				t.Fatalf("one clear warning must name removed SearXNG, got %d lines: %q", len(warnings), logs.String())
			}
			warning := strings.ToLower(warnings[0])
			if !strings.Contains(warning, "warn") || !strings.Contains(warning, "removed") {
				t.Errorf("warning must say the configured provider was removed: %q", warnings[0])
			}
			for _, role := range tc.clearedRoles {
				if !strings.Contains(warning, role) {
					t.Errorf("warning does not identify cleared role %q: %q", role, warnings[0])
				}
			}
		})
	}
}
