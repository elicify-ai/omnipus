package tools

// web_search_fix_roles_table_test.go — gate round 1 finding K6: SC-001 /
// spec test 45 (ADR-096 review CRIT-004): the R-table's total coverage.
// The cross product of (default usable/unusable/unknown/absent) ×
// (fallback absent/none/same/other-usable/other-unusable/unknown) ×
// (DuckDuckGo usable/not) — 48 combinations — each lands in exactly one
// R-row, and the resolver tuple and the runtime outcome are the spec's.
// Expectations derive from the spec's R-table (first-match-wins) and the
// Definitions section's usability test, encoded in specRoles below — NOT
// from resolveRoles.

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// specRoles is the spec's R-table as a function — the K6 oracle. Encoded
// from the spec's table text ("The rows are evaluated in order. The first
// row whose condition holds wins") and the Definitions usability test
// (switched on, a required key resolves, base URL non-empty), not from the
// code under test. Returns the resolved fallback id, the automatic flag,
// the R5 ignored flag, and the row that fired.
//
// Table order note: R5's condition (fallback id equals default id) is
// evaluated before R6/R7, so it fires first on equal ids even when the
// default is unusable; its payload duty (fallback_ignored_reason) is a
// Settings concern for a STORED default, so the all-empty undecided state
// must not read as ignored.
func specRoles(dState, fState string, ddgUsable bool) (fb string, auto bool, ignored bool, row string) {
	if dState != "usable" {
		// R9 folds unknown/absent defaults into the not-usable family:
		// "treated as not usable (R6 or R7). The error names the unknown id."
		if fState == "same" {
			return "", false, dState != "absent", "R5"
		}
		if fState == "other-usable" {
			// R4 resolves the fallback role; R6 governs the call (default
			// not usable + R4 produced a usable fallback → the fallback
			// runs, and the result says the default was not called).
			return "perplexity", false, false, "R6"
		}
		// R6 needs an R3/R4 fallback; R3 cannot fire without a usable
		// default (its third conjunct). Everything else is R7: nobody
		// runs. A known-unusable fallback REMAINS a listed role — R7's
		// "the error names each role as 'not called'" — so the tuple keeps
		// it (fb="brave"); an unknown fallback id is not a role at all.
		if fState == "other-unusable" {
			return "brave", false, false, "R7"
		}
		return "", false, false, "R7"
	}
	// The default is the usable "tavily".
	switch fState {
	case "none":
		// R1 lists "none" among its no-fallback forms; R2 is the specific
		// row. Same outcome: the default only, no second try.
		return "", false, false, "R1/R2"
	case "same":
		// R5.
		return "", false, true, "R5"
	case "absent":
		// R3 first-match: absent + DDG usable + usable default ≠ DDG.
		// Without a usable DDG: R1 ("absent with no automatic fallback");
		// R8 names the same state.
		if ddgUsable {
			return "duckduckgo", true, false, "R3"
		}
		return "", false, false, "R1/R8"
	case "other-usable":
		// R4.
		return "perplexity", false, false, "R4"
	case "other-unusable":
		// R4b: a listed role, never called (the review's CRIT-004 fix).
		return "brave", false, false, "R4b"
	default:
		// An unknown fallback id is not a usable id: R1's no-fallback
		// forms apply and it is not a provider role at all (nothing to
		// list; R4b covers KNOWN unusable ids only).
		return "", false, false, "R1/unknown-fb"
	}
}

// TestFixK6_RolesCrossProduct walks the 48-combination cross product. For
// each: the resolver tuple must equal the spec's tuple, and one runtime run
// must serve from the row's provider (or fail with nobody called for R7/R5
// rows). A fresh fixture per combination keeps env, servers and hit
// counters isolated.
func TestFixK6_RolesCrossProduct(t *testing.T) {
	dStates := []string{"usable", "unusable", "unknown", "absent"}
	fStates := []string{"absent", "none", "same", "other-usable", "other-unusable", "unknown"}
	allIDs := []string{"tavily", "ddg", "brave", "perplexity", "glm", "exa", "baidu"}

	for _, dState := range dStates {
		for _, fState := range fStates {
			for _, ddgOn := range []bool{true, false} {
				ddgLabel := "off"
				if ddgOn {
					ddgLabel = "on"
				}
				t.Run(dState+"/"+fState+"/ddg="+ddgLabel, func(t *testing.T) {
					fx := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
						switch dState {
						case "unusable":
							c.Tavily.APIKeyRef = envRefMissingTav
						case "unknown":
							c.DefaultProvider = "altavista"
						case "absent":
							c.DefaultProvider = ""
						}
						switch fState {
						case "none":
							c.FallbackProvider = config.SearchProviderNone
						case "same":
							c.FallbackProvider = c.DefaultProvider
						case "other-usable":
							c.FallbackProvider = config.SearchProviderPerplexity
							c.Perplexity = config.PerplexityConfig{Enabled: true, APIKeyRef: envRefPerplexity}
						case "other-unusable":
							c.FallbackProvider = config.SearchProviderBrave
							c.Brave = config.BraveConfig{Enabled: true, APIKeyRef: envRefMissingBrav}
						case "unknown":
							// A different unknown id than the unknown
							// default's: the cross-product's "unknown
							// fallback" state is a fallback id not in the
							// catalogue, distinct from the default.
							c.FallbackProvider = "lycos"
						}
						c.DuckDuckGo.Enabled = ddgOn
					}, nil)

					// 1. The resolver tuple equals the spec's tuple.
					entries := resolveRoles(fx.cfg)
					wantFb, wantAuto, wantIgnored, row := specRoles(dState, fState, ddgOn)
					if entries.fallbackID != wantFb ||
						entries.fallbackAuto != wantAuto ||
						entries.ignoredSameAsDefault != wantIgnored {
						t.Fatalf("row %s: resolver (fb=%q auto=%v ignored=%v), want (fb=%q auto=%v ignored=%v)",
							row, entries.fallbackID, entries.fallbackAuto, entries.ignoredSameAsDefault,
							wantFb, wantAuto, wantIgnored)
					}

					// 2. Runtime: the row decides who answers. R5's outcome
					// column is "the configured default": with a usable
					// default it runs; with a not-usable default R5 has
					// resolved no fallback, so nobody runs.
					res := fx.run(map[string]any{"query": "golang"})
					wantServed := ""
					wantErr := false
					switch {
					case row == "R6":
						wantServed = "perplexity"
					case row == "R7":
						wantErr = true
					case row == "R5" && dState != "usable":
						wantErr = true
					default:
						wantServed = "tavily"
					}
					if wantErr && !res.IsError {
						t.Fatalf("row %s: expected nobody to run (tool error), got success:\n%s", row, res.ForLLM)
					}
					if !wantErr && res.IsError {
						t.Fatalf("row %s: expected %q to answer, got error:\n%s", row, wantServed, res.ForLLM)
					}
					for _, id := range allIDs {
						want := 0
						if id == wantServed {
							want = 1
						}
						if got := fx.hitsOf(id); got != want {
							t.Fatalf("row %s: %s hits = %d, want %d", row, id, got, want)
						}
					}
					// R9: the error names the unknown default id.
					if dState == "unknown" && wantErr && !strings.Contains(res.ForLLM, "altavista") {
						t.Fatalf("row %s: R9 error must name the unknown default id, got:\n%s", row, res.ForLLM)
					}
				})
			}
		}
	}
}
