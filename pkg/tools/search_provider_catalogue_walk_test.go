package tools

// search_provider_catalogue_walk_test.go — spec tests 29 + 48 (AC-13,
// AC-20, FR-035): the warning list and the resolver derive from the single
// provider catalogue, asserted by WALKING the catalogue rather than naming
// ids. The synthetic provider "testprov" rides the REAL Exa config fields so
// the real usability and warning code paths run.

import (
	"slices"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// testProviderDef is the spec-test-48 synthetic provider: a keyed catalogue
// entry wired to the REAL Exa config fields, so tests toggle enabled/key
// state through real config and the real code paths run. Only the id,
// section and ref labels are synthetic.
func testProviderDef() config.SearchProviderDef {
	return config.SearchProviderDef{
		ID:          "testprov",
		Section:     "testprov",
		DisplayName: "Test Provider",
		Keyed:       true,
		CredRef:     "TESTPROV_API_KEY",
		Enabled:     func(w *config.WebToolsConfig) bool { return w.Exa.Enabled },
		SetEnabled:  func(w *config.WebToolsConfig, on bool) { w.Exa.Enabled = on },
		APIKeyRef:   func(*config.WebToolsConfig) string { return "TESTPROV_API_KEY" },
		APIKey:      func(w *config.WebToolsConfig) string { return w.Exa.APIKey() },
	}
}

// appendTestProviderCatalogue appends testProviderDef and returns a restore
// func the caller defers.
func appendTestProviderCatalogue() func() {
	saved := config.SearchProviderCatalogue
	grown := make([]config.SearchProviderDef, 0, len(saved)+1)
	grown = append(grown, saved...)
	grown = append(grown, testProviderDef())
	config.SearchProviderCatalogue = grown
	return func() { config.SearchProviderCatalogue = saved }
}

// TestCatalogueWalk_AppendedProviderIsWarnableAndSelectable is spec test 48:
// a provider appended to the catalogue — and nothing else — is warnable and
// selectable with no other edit.
func TestCatalogueWalk_AppendedProviderIsWarnableAndSelectable(t *testing.T) {
	defer appendTestProviderCatalogue()()

	// Warnable: enabled (Exa fields on) with no key — the warning must name
	// the appended section, derived from the walk.
	cfg := &config.WebToolsConfig{}
	cfg.Exa.Enabled = true
	opts := WebSearchToolOptions{Roles: func() *config.WebToolsConfig { return cfg }}
	warned := make([]string, 0, 2)
	for _, m := range enabledButKeylessSearchProviders(opts) {
		warned = append(warned, m.name)
	}
	if !slices.Contains(warned, "testprov") {
		t.Fatalf("appended provider not warnable (got %v) — the warning list is not derived from the catalogue", warned)
	}

	// Selectable: a usable appended default resolves as a known, usable id.
	cfg.Exa.APIKeyRef = "TESTPROV_API_KEY"
	t.Setenv("TESTPROV_API_KEY", "k")
	snap := ResolveSearchRoleSnapshot(cfg)
	if !snap.Usable["testprov"] {
		t.Fatal("appended provider not usable in the resolver snapshot — selectable requires the catalogue walk")
	}
	cfg.DefaultProvider = "testprov"
	snap = ResolveSearchRoleSnapshot(cfg)
	if !snap.Usable[cfg.DefaultProvider] {
		t.Fatalf("default %q not usable in the snapshot after append", cfg.DefaultProvider)
	}
}

// TestCatalogueWalk_EveryKeyedProviderIsWarnable is spec test 29's shape:
// walk the catalogue — every keyed provider enabled with no key is named by
// the enabled-but-keyless warning. Exa is covered by the walk without being
// named; a catalogue entry the warning does not derive fails here.
func TestCatalogueWalk_EveryKeyedProviderIsWarnable(t *testing.T) {
	cfg := &config.WebToolsConfig{}
	var want []string
	for _, def := range config.SearchProviderCatalogue {
		if !def.Keyed {
			continue
		}
		def.SetEnabled(cfg, true)
		want = append(want, def.Section)
	}
	if len(want) == 0 {
		t.Fatal("catalogue walk found no keyed providers — the catalogue is empty")
	}

	opts := WebSearchToolOptions{Roles: func() *config.WebToolsConfig { return cfg }}
	got := make([]string, 0, 2)
	for _, m := range enabledButKeylessSearchProviders(opts) {
		got = append(got, m.name)
	}
	for _, section := range want {
		if !slices.Contains(got, section) {
			t.Errorf("enabled-but-keyless warning missing enabled+keyless provider %q (got %v)", section, got)
		}
	}
}
