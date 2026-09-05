// Omnipus — the manifest tier partition must cover the whole builtin catalog
// (ADR-075 D2 FR-036).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// The four manifest tiers decide what an agent can SEE: Tier 1 is sent in full
// every turn, Tier 2 is previewed as one line, Tier 3 is reachable only
// through ToolSearch, and the infra tier is ToolSearch itself.
//
// THE SUBTLETY THAT DECIDES HOW THIS TEST HAD TO BE WRITTEN. Tier 3 is the
// RESIDUAL: pkg/tools has no SearchOnlyManifestToolNames() and deliberately
// never will, because a lazy tool that is not in the previewed set resolves to
// search-only by default. So "the union of the four tiers equals the catalog"
// is, taken literally, a tautology — define Tier 3 as everything left over and
// the two sets are equal no matter what the fixtures say. Written that way,
// FR-036 would be a test that cannot fail.
//
// The non-vacuous form is the two directions that CAN disagree:
//
//	catalog -> tier   every registered builtin resolves through the production
//	                  classifier to exactly one tier, and Tier 3 membership is
//	                  therefore a decision rather than an accident.
//	tier -> catalog   every name in the three EXPLICIT sets is a tool that
//	                  actually exists. A retired tool leaves its tier entry
//	                  behind; a typo'd entry sits in Tier 1 or 2 while the tool
//	                  it was meant to name falls into the Tier 3 residual, and
//	                  nothing anywhere looks wrong.
//
// It lives in pkg/gateway because this is the only package that imports both
// pkg/tools and the browser + sysagent metadata catalogs, so
// buildKnownBuiltinToolNames — the real registered-builtin universe the
// tool-policy coverage validator is fed — is reachable here and nowhere else.

package gateway

import (
	"sort"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestManifestTierPartition_CoversRegisteredBuiltinCatalog asserts the two
// directions above, plus pairwise disjointness of the three explicit sets.
func TestManifestTierPartition_CoversRegisteredBuiltinCatalog(t *testing.T) {
	catalog := buildKnownBuiltinToolNames()
	if len(catalog) == 0 {
		t.Fatal("buildKnownBuiltinToolNames() is empty — every assertion below would be vacuous")
	}

	// --- direction 1: every explicit tier entry is a registered builtin ---
	explicit := make(map[string]string, len(catalog))
	add := func(setName string, names []string) {
		if len(names) == 0 {
			t.Errorf("the %q tier set is empty — a tier that names nothing cannot be checked "+
				"against anything", setName)
		}
		for _, n := range names {
			if prior, ok := explicit[n]; ok {
				t.Errorf("tool %q is in both the %q and %q tiers. The tiers must be a PARTITION: "+
					"a tool with two visibility answers gets whichever the lookup order happens to "+
					"reach first", n, prior, setName)
				continue
			}
			explicit[n] = setName
		}
	}
	add("full", tools.FullManifestToolNames())
	add("previewed", tools.PreviewedLazyToolNames())
	add("infra", tools.InfraManifestToolNames())

	var notRegistered []string
	for name := range explicit {
		if _, ok := catalog[name]; !ok {
			notRegistered = append(notRegistered, name)
		}
	}
	sort.Strings(notRegistered)
	if len(notRegistered) > 0 {
		t.Errorf("%d name(s) sit in an explicit manifest tier but are NOT registered builtin "+
			"tools: %v.\nEither a tool was retired and its tier entry outlived it, or a fixture has "+
			"a typo — in which case the tool it was meant to name has silently fallen into the "+
			"Tier 3 residual instead of the tier someone chose for it.",
			len(notRegistered), notRegistered)
	}

	// --- direction 2: every registered builtin resolves to exactly one tier ---
	counts := map[string]int{}
	for name := range catalog {
		switch tier := tools.ToolManifestTier(name); tier {
		case tools.ManifestFull:
			counts["full"]++
			if got := explicit[name]; got != "full" {
				t.Errorf("%q resolves to ManifestFull but the explicit sets record it as %q", name, got)
			}
		case tools.ManifestInfra:
			counts["infra"]++
			if got := explicit[name]; got != "infra" {
				t.Errorf("%q resolves to ManifestInfra but the explicit sets record it as %q", name, got)
			}
		case tools.ManifestLazy:
			switch vis := tools.ToolManifestVisibility(name); vis {
			case tools.ManifestPreviewed:
				counts["previewed"]++
				if got := explicit[name]; got != "previewed" {
					t.Errorf("%q resolves to previewed but the explicit sets record it as %q", name, got)
				}
			case tools.ManifestSearchOnly:
				counts["search-only"]++
				if got, ok := explicit[name]; ok {
					t.Errorf("%q resolves to search-only yet appears in the explicit %q set — the "+
						"two disagree about how discoverable this tool is", name, got)
				}
			default:
				t.Errorf("%q has an unrecognised manifest visibility %v", name, vis)
			}
		default:
			t.Errorf("%q has an unrecognised manifest tier %v — it belongs to no tier at all, so "+
				"nothing has decided whether an agent can see it", name, tier)
		}
	}

	// Every tier must be non-empty, which is what stops the loop above from
	// passing on a build where the classifier answered "search-only" for the
	// entire catalog.
	for _, tier := range []string{"full", "previewed", "infra", "search-only"} {
		if counts[tier] == 0 {
			t.Errorf("no registered builtin tool resolves to the %q tier", tier)
		}
	}
	if total := counts["full"] + counts["previewed"] + counts["infra"] + counts["search-only"]; total != len(catalog) {
		t.Errorf("the four tiers account for %d tools but the catalog holds %d", total, len(catalog))
	}
}
