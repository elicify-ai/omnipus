// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// default_model_catalog_boot_order_test.go — regression coverage for the
// production defect reported 2026-08-28: a founder set
// agents.defaults.default_model to a (provider, model) pair his REFRESHED
// $OMNIPUS_HOME/providers_catalog.json genuinely served (the SPA's picker
// offered it), but the gateway repeatedly failed to boot with
//
//	error creating provider: default model "zai-coding-plan/glm-5.3-flash"
//	not found in providers
//
// Root cause: RunContextWithOptions called createStartupProvider (which
// resolves the default model against providers.ProviderCatalog()) BEFORE
// catalog.Boot + providers.SetCatalog ever ran. providers.ProviderCatalog()
// falls back to the embedded-only snapshot whenever nothing has been
// installed yet (pkg/providers/catalog_source.go), so createStartupProvider
// could never see the richer, already-booted catalog the rest of the
// process (NewAgentLoop's ADR-066 ResolveWindow rung 5, the REST default-
// model PUT) resolves against moments later.
//
// The fix extracted the ordering-sensitive lines into
// installProviderCatalogAndStartupProvider (catalog.Boot + SetCatalog,
// THEN createStartupProvider) precisely so this file can pin the order by
// calling the real function, not by re-implementing it — swapping the two
// steps back inside that function (the exact pre-fix defect) makes
// TestInstallProviderCatalogAndStartupProvider_ResolvesFromDisk fail.
package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// richerCatalogBytes parses the REAL embedded snapshot (catalog.EmbeddedSnapshot
// — the same bytes providers.ProviderCatalog() falls back to when nothing is
// installed) and returns document bytes that are identical except: (a) one
// extra model, cloned from the provider's own first model row, appended
// under providerID with id newModelID, and (b) a version strictly newer (an
// appended ".999" same-day re-release component, valid per catalog's
// vYYYY.M.D[.N] grammar and Version.Compare) — so catalog.Boot's "strictly
// newer persisted document wins" rule always picks this document over the
// embedded snapshot. This test is scoped to the ORDERING bug (Defect 1), not
// the separate version-tie question under active investigation (Defect 2),
// so it deliberately avoids that edge case.
func richerCatalogBytes(t *testing.T, providerID, newModelID string) []byte {
	t.Helper()

	var doc map[string]any
	if err := json.Unmarshal(catalog.EmbeddedSnapshot, &doc); err != nil {
		t.Fatalf("unmarshal embedded snapshot: %v", err)
	}

	ver, _ := doc["version"].(string)
	if ver == "" {
		t.Fatalf("embedded snapshot has no version string")
	}
	doc["version"] = ver + ".999"

	provsAny, _ := doc["providers"].([]any)
	found := false
	for _, pAny := range provsAny {
		p, ok := pAny.(map[string]any)
		if !ok || p["id"] != providerID {
			continue
		}
		modelsAny, _ := p["models"].([]any)
		if len(modelsAny) == 0 {
			t.Fatalf("embedded snapshot provider %q has no models to clone from", providerID)
		}
		template, ok := modelsAny[0].(map[string]any)
		if !ok {
			t.Fatalf("embedded snapshot provider %q model[0] is not an object", providerID)
		}
		clone := make(map[string]any, len(template))
		for k, v := range template {
			clone[k] = v
		}
		clone["id"] = newModelID
		clone["name"] = newModelID
		p["models"] = append(modelsAny, clone)
		found = true
		break
	}
	if !found {
		t.Fatalf("embedded snapshot has no provider %q — pick a real provider id from data/providers_catalog.json", providerID)
	}

	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal richer document: %v", err)
	}
	return data
}

// TestInstallProviderCatalogAndStartupProvider_ResolvesFromDisk is the
// revert-proof for the boot-order fix. It writes a richer, refreshed
// providers_catalog.json to disk (mirroring $OMNIPUS_HOME/providers_catalog.json)
// and calls installProviderCatalogAndStartupProvider — the REAL function
// RunContextWithOptions calls — exactly as boot does: Boot reads the file
// from disk, THEN createStartupProvider resolves the default model.
//
// If installProviderCatalogAndStartupProvider's internal order is reverted
// to the pre-fix order (createStartupProvider called before catalog.Boot /
// providers.SetCatalog), this test fails with the exact production error:
// `default model "zai-coding-plan/<model>" not found in providers` — because
// providers.ProviderCatalog() would still be serving the embedded-only
// fallback, which does not carry the synthetic model this test adds.
func TestInstallProviderCatalogAndStartupProvider_ResolvesFromDisk(t *testing.T) {
	const providerID = "zai-coding-plan"
	const newModel = "test-only-catalog-boot-order-flash"

	t.Cleanup(func() { providers.SetCatalog(nil) })
	providers.SetCatalog(nil)

	// Sanity: the synthetic model must not already resolve via the embedded
	// snapshot fallback — otherwise this test would not exercise the bug.
	if providers.ProviderCatalog().Resolve(providerID, newModel).Found() {
		t.Fatalf("sanity check failed: %q/%q unexpectedly already exists in the embedded snapshot fallback — "+
			"pick a different newModel id", providerID, newModel)
	}

	homePath := t.TempDir()
	catalogPath := filepath.Join(homePath, catalog.PersistedFileName)
	if err := os.WriteFile(catalogPath, richerCatalogBytes(t, providerID, newModel), 0o600); err != nil {
		t.Fatalf("write %s: %v", catalogPath, err)
	}

	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{Provider: providerID, Model: "default"},
		},
	}
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Provider: providerID, Model: newModel}

	_, _, gotCatalog, err := installProviderCatalogAndStartupProvider(context.Background(), homePath, cfg, false)
	if err != nil && strings.Contains(err.Error(), "not found in providers") {
		t.Fatalf("installProviderCatalogAndStartupProvider must resolve %q/%q from the persisted "+
			"providers_catalog.json — got the pre-fix boot-order failure: %v", providerID, newModel, err)
	}
	// A different failure (missing credential — this test wires no
	// credential store) is expected and fine; it proves ResolveDefaultModelRow
	// found the row and moved on to actually building a transport.
	if err != nil && !strings.Contains(err.Error(), "api_key") {
		t.Fatalf("installProviderCatalogAndStartupProvider failed for an unexpected reason "+
			"(expected either success or a missing-credential error): %v", err)
	}
	if !gotCatalog.Resolve(providerID, newModel).Found() {
		t.Fatalf("the catalog installProviderCatalogAndStartupProvider booted does not carry %q/%q; "+
			"it should have been read from %s", providerID, newModel, catalogPath)
	}
}

// TestCreateStartupProvider_PreFixOrderReproducesTheDefect is the negative
// control: it replicates the PRE-FIX call order directly — createStartupProvider
// invoked while providers.ProviderCatalog() still has nothing installed,
// exactly as RunContextWithOptions used to do before catalog.Boot moved
// ahead of it — and confirms it fails with the exact production error text.
// It exists so the "why the order matters" claim in
// installProviderCatalogAndStartupProvider's doc comment is verified
// directly, mirroring boot_order_test.go's own
// TestBootOrder_OldOrderWouldLoseDataModelInitLogs pattern.
func TestCreateStartupProvider_PreFixOrderReproducesTheDefect(t *testing.T) {
	const providerID = "zai-coding-plan"
	const newModel = "test-only-catalog-boot-order-flash-2"

	t.Cleanup(func() { providers.SetCatalog(nil) })
	providers.SetCatalog(nil) // nothing installed yet — the pre-fix state

	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{Provider: providerID, Model: "default"},
		},
	}
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Provider: providerID, Model: newModel}

	_, _, err := createStartupProvider(cfg, false)
	if err == nil {
		t.Fatal("createStartupProvider must fail when nothing has been installed via providers.SetCatalog yet " +
			"(pre-fix boot order) — got a nil error; this negative control is broken")
	}
	wantSubstr := `default model "` + providerID + `/` + newModel + `" not found in providers`
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Fatalf("createStartupProvider error = %q, want it to contain %q "+
			"(the exact production error text from the founder's repro)", err.Error(), wantSubstr)
	}
}

// TestCreateStartupProvider_UnknownModelStillRejected is the guard-still-
// bites proof: even with a richer catalog installed, a model that genuinely
// does not exist anywhere — not the row's exact Model, not its Models[]
// list, not the served catalog — must still be refused. The boot-order fix
// must never turn into "trust whatever the operator typed".
func TestCreateStartupProvider_UnknownModelStillRejected(t *testing.T) {
	const providerID = "zai-coding-plan"
	const bogusModel = "definitely-does-not-exist-anywhere-vX"

	t.Cleanup(func() { providers.SetCatalog(nil) })

	rich, err := catalog.NewCatalog(richerCatalogBytes(t, providerID, "some-other-real-addition"))
	if err != nil {
		t.Fatalf("catalog.NewCatalog: %v", err)
	}
	providers.SetCatalog(rich)

	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{Provider: providerID, Model: "default"},
		},
	}
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Provider: providerID, Model: bogusModel}

	_, _, gotErr := createStartupProvider(cfg, false)
	if gotErr == nil {
		t.Fatal("createStartupProvider must reject a model the catalog does not carry — got nil error")
	}
	wantSubstr := `default model "` + providerID + `/` + bogusModel + `" not found in providers`
	if !strings.Contains(gotErr.Error(), wantSubstr) {
		t.Fatalf("createStartupProvider error = %q, want it to contain %q", gotErr.Error(), wantSubstr)
	}
}
