// Omnipus — ToolSearch ambiguity band (ADR-071 D2, §3.2)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// This file implements the multi-candidate promotion logic execSearchAndLoad
// (tools_tool.go) runs over the policy-filtered, rank-ordered match list
// §3.2.2 (CRIT-201) produces. Kept separate from tools_tool.go because it is
// D2's own decision, layered on top of D3's filter-and-truncate rewrite.

package tools

// Tuning constants for the ambiguity test (ADR-071 §3.2). Unexported: these
// are heuristic parameters, not operator-facing policy — no operator can
// tune them intelligently without usage data the two ToolSearch detection
// counters (omnipus_toolsearch_zero_result_total,
// omnipus_toolsearch_no_followup_total) don't yet exist to produce.
const (
	// searchAmbiguityRatio is the "confident band" ratio (rule 1, §3.2). A
	// candidate scoring at or above this fraction of the top hit's score is
	// unambiguously plausible and is promoted regardless of category or
	// administrative status — narrowing this clause would defeat the
	// feature on the case it exists for.
	searchAmbiguityRatio = 0.80

	// searchCrossCategoryRatio is the speculative "cross-category near-miss"
	// ratio (rule 2, §3.2). A candidate scoring at or above this fraction of
	// the top hit's score, in a DIFFERENT Tool.Category() than the top hit,
	// is a plausible enough alternate reading of the query to promote —
	// unless it is administrative or "ask"-gated (§3.2's narrowing).
	searchCrossCategoryRatio = 0.50

	// searchMaxAutoLoad caps the total number of tools promoted by one
	// query, including the top hit.
	searchMaxAutoLoad = 3

	// searchCanLoadScanCap bounds the number of canLoad policy lookups
	// execSearchAndLoad performs while filtering the ranked list (§3.2.2).
	// It can only ever truncate the loadable list earlier — it never admits
	// a denied name — so an agent whose policy allows almost nothing still
	// gets a bounded, not unbounded, scan.
	searchCanLoadScanCap = 50
)

// toolSearchCandidate is one rank-ordered, policy-loadable ToolSearch result,
// carrying the extra fields (score, category, askPolicy) the ambiguity test
// needs beyond the {name, description} pair the wire response (
// ToolSearchResult) exposes.
type toolSearchCandidate struct {
	name        string
	description string
	score       float32
	category    ToolCategory
	// askPolicy is true when the calling agent's resolved policy for this
	// tool is "ask" (requires user confirmation before execution) — signaled
	// by canLoad returning CanLoadAskPolicyPrefix as its reason alongside
	// ok=true. Used only to narrow the speculative cross-category clause
	// (§3.2); the tool is otherwise fully loadable.
	askPolicy bool
}

// selectSearchPromotionCandidates implements ADR-071 §3.2's ambiguity test
// over loadable — already rank-ordered (best score first) and already
// policy-filtered by execSearchAndLoad. Returns the tool(s) to auto-load:
//
//   - 0 or 1 entries in loadable: returned as-is, no comparison runs.
//   - Otherwise: loadable[0] (the top hit) plus every subsequent candidate
//     that qualifies under either rule, in rank order, capped at
//     searchMaxAutoLoad total. If nothing qualifies, the top hit alone is
//     returned — today's behavior, byte for byte.
//
// Because loadable is already rank-sorted, "the two highest-scoring
// qualifiers" (ADR-071 §3.2's tie-break for more qualifiers than the cap
// admits) falls out of iterating in order and stopping at the cap — no
// separate sort or selection step is needed.
func selectSearchPromotionCandidates(loadable []toolSearchCandidate) []toolSearchCandidate {
	if len(loadable) <= 1 {
		return loadable
	}

	top := loadable[0]
	promoted := make([]toolSearchCandidate, 0, searchMaxAutoLoad)
	promoted = append(promoted, top)

	for _, c := range loadable[1:] {
		if len(promoted) >= searchMaxAutoLoad {
			break
		}
		if candidateQualifies(c, top) {
			promoted = append(promoted, c)
		}
	}
	return promoted
}

// candidateQualifies applies ADR-071 §3.2's two-rule ambiguity test to a
// single runner-up candidate c against the top hit.
func candidateQualifies(c, top toolSearchCandidate) bool {
	// Rule 1 — confident score band: unrestricted. A candidate scoring
	// within 20% of the top hit is genuinely plausible regardless of
	// category or administrative/ask status (ADR-071 §3.2: "the tighter
	// score-band clause is unrestricted... excluding it would defeat the
	// feature").
	if c.score >= searchAmbiguityRatio*top.score {
		return true
	}

	// Rule 2 — speculative cross-category near-miss: narrowed. Excludes
	// administrative tools and tools whose resolved policy requires
	// confirmation ("ask") — the speculative clause is the one that carries
	// this cost, per ADR-071 §3.2's "One narrowing is adopted, cheaply."
	if c.score >= searchCrossCategoryRatio*top.score && c.category != top.category {
		if c.askPolicy || isAdministrativeToolName(c.name) {
			return false
		}
		return true
	}

	return false
}
