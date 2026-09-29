// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Founder decision (2026-09-29, recorded in the #1055/#1056 fix-red pack):
// "Key save switches on" — saving a key for a keyed search provider also
// sets that provider's tools.web.<section>.enabled to true in config.json.
// Before this decision, applySearchIntegrationRoles
// (rest_integrations_roles.go) wrote api_key_ref on a key-only save but left
// `enabled` untouched, so a keyed provider (Tavily, Brave, Exa, Perplexity,
// Baidu, GLM) could never become usable from the new Settings screen: the
// tool-usability test (pkg/config/web_search_roles.go::UsableSearchProvider)
// requires enabled==true AND a resolving key, and the only place `enabled`
// was ever set to true was a DEFAULT-role save (setActive), which the new
// screen refuses to offer for a provider the catalogue does not yet report
// usable — a deadlock.
//
// This file pins the fix's exact, narrow scope: a key-only save (keySet,
// no active, no fallback) enables the provider's own section and nothing
// else — it does NOT assign a role (default_provider / fallback_provider
// stay byte-identical) and does NOT stamp roles_migrated_at (existing rule,
// applySearchIntegrationRoles's doc comment: "a key-only save stamps
// nothing" — unchanged by this decision).
//
// Oracle: the founder decision text above (dispatch to qa-lead, 2026-09-29),
// plus the existing, UNCHANGED rules in rest_integrations_roles.go's
// applySearchIntegrationRoles doc comment ("a stored key writes this
// provider's api_key_ref and nothing else — FR-005: no other provider's ref
// is ever deleted"; "a key-only save stamps nothing") and
// pkg/config/web_search_roles.go::UsableSearchProvider (the ONE usability
// test: keyed provider needs Enabled() true AND a resolving key).

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFixKeySave_EnablesKeyedSearchProvider is T1: through the real REST
// handler used for key saves (handleIntegrationProviderUpdate, called via
// HandleIntegrationProviders' PUT dispatch — the same path putRoles/
// putIntegration drive in rest_integrations_roles_test.go), a key-only save
// for a keyed search provider whose section starts disabled must:
//
//   - flip that provider's own tools.web.<section>.enabled to true on disk;
//   - keep api_key_ref set to the provider's credential ref;
//   - report GET /api/v1/integrations/providers' row for that provider as
//     usable == true (once the saved key also resolves, as it would after
//     the production reload's InjectFromConfig — simulated here exactly as
//     the existing roles tests do, via t.Setenv before the PUT);
//   - leave default_provider, fallback_provider and roles_migrated_at
//     byte-identical to their pre-save values (no role assignment, no
//     migration-marker stamp — the key-only save rule is otherwise
//     unchanged);
//   - leave every OTHER provider's `enabled` flag and `api_key_ref`
//     untouched (FR-005's "no other provider's ref is ever deleted",
//     extended by the founder decision to "no other provider's enabled flag
//     is ever touched either").
//
// Table-driven over Tavily and Brave — two independent catalogue sections
// (config.SearchProviderCatalogue) — so the fix is proven generic across the
// catalogue, not hand-fixed for one provider id.
func TestFixKeySave_EnablesKeyedSearchProvider(t *testing.T) {
	const bystanderSection = "perplexity"

	for _, tc := range []struct {
		name    string
		id      string // catalogue id == PUT path segment
		section string // tools.web sub-object key
		credRef string // the api_key_ref value a save must write
	}{
		{name: "Tavily", id: "tavily", section: "tavily", credRef: "TAVILY_API_KEY"},
		{name: "Brave", id: "brave", section: "brave", credRef: "BRAVE_API_KEY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, user, cfg := newRolesTestAPI(t)

			// Decided install (roles_migrated_at present): DuckDuckGo is the
			// default and usable on its own (keyless), so the response is
			// well-formed independent of this test's provider. The target
			// provider's section starts present but switched OFF, with no
			// api_key_ref yet — exactly the state the founder decision names
			// ("a keyed provider ... could never become usable"). A
			// bystander keyed provider (Perplexity, distinct from both table
			// rows) starts OFF too, to prove the fix does not touch anyone
			// else's section.
			web := map[string]any{
				"default_provider":  "duckduckgo",
				"fallback_provider": "none",
				"roles_migrated_at": rolesMarker,
				"duckduckgo":        map[string]any{"enabled": true},
				tc.section:          map[string]any{"enabled": false},
				bystanderSection:    map[string]any{"enabled": false},
			}
			writeRolesWebConfig(t, api, web)

			// Simulate the reload's InjectFromConfig having made the just-
			// saved key resolve via the process environment — the same
			// technique rest_integrations_roles_test.go's own PUT tests use
			// (e.g. TestIntegrationPut_MaterializesAutomaticFallback,
			// TestIntegrationPut_DefaultKeyUnresolvedAfterReload_400KeepsWrites).
			// The oracle here is UsableSearchProvider's own rule: Enabled()
			// true AND APIKey() non-empty.
			t.Setenv(tc.credRef, "k-live-"+tc.id)
			wireRolesReload(t, cfg, api, nil, nil)

			body := `{"kind":"search","api_key":"k-raw-` + tc.id + `"}`
			w := putRoles(t, api, user, tc.id, body)
			require.Equal(t, http.StatusOK, w.Code,
				"key-only save for %s must succeed: body=%s", tc.id, w.Body.String())

			gotWeb := readRolesWebConfig(t, api)

			sec := roleSection(t, gotWeb, tc.section)
			assert.Equal(t, true, sec["enabled"],
				"founder decision 2026-09-29 ('key save switches on'): "+
					"a key-only save for %s must set tools.web.%s.enabled=true", tc.id, tc.section)
			assert.Equal(t, tc.credRef, sec["api_key_ref"],
				"the saved key's ref must still be written to %s.api_key_ref", tc.section)

			assert.Equal(t, "duckduckgo", gotWeb["default_provider"],
				"a key-only save must not assign the default role")
			assert.Equal(t, "none", gotWeb["fallback_provider"],
				"a key-only save must not assign the fallback role")
			assert.Equal(t, rolesMarker, gotWeb["roles_migrated_at"],
				"a key-only save must not stamp roles_migrated_at (existing rule, unchanged)")

			// Negative case: no OTHER provider's enabled flag or ref moves.
			bystander := roleSection(t, gotWeb, bystanderSection)
			assert.Equal(t, false, bystander["enabled"],
				"a key-only save for %s must not switch on the unrelated %s section",
				tc.id, bystanderSection)
			_, bystanderHasRef := bystander["api_key_ref"]
			assert.False(t, bystanderHasRef,
				"a key-only save for %s must not write an api_key_ref for %s (FR-005)",
				tc.id, bystanderSection)
			duckduckgoSec := roleSection(t, gotWeb, "duckduckgo")
			assert.Equal(t, true, duckduckgoSec["enabled"],
				"a key-only save for %s must not disable the unrelated duckduckgo section", tc.id)

			resp := getIntegrationList(t, api, user)
			row := searchRow(t, resp, tc.id)
			require.NotNil(t, row.Usable, "%s row must report usable (search rows only)", tc.id)
			assert.True(t, *row.Usable,
				"GET usable for %s must be true once the key save switched it on and the key resolves", tc.id)
		})
	}
}
