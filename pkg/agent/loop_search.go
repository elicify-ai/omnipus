// loop_search.go: Dynamic tool load and search promotion

package agent

import (
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// markToolsLoaded records names as loaded for the given session (manifest
// optimization). Creates the inner map on first call for a session.
// Safe for concurrent access — protected by loadedToolsMu.
func (al *AgentLoop) markToolsLoaded(sessionID string, names []string) {
	if sessionID == "" || len(names) == 0 {
		return
	}
	al.loadedToolsMu.Lock()
	defer al.loadedToolsMu.Unlock()
	if al.loadedTools[sessionID] == nil {
		al.loadedTools[sessionID] = make(map[string]bool, len(names))
	}
	for _, n := range names {
		al.loadedTools[sessionID][n] = true
	}
}

// sessionLoadedTools returns a copy of the loaded-tool set for sessionID.
// Returns an empty map when sessionID is empty or has no entries.
// Safe for concurrent access — protected by loadedToolsMu.
func (al *AgentLoop) sessionLoadedTools(sessionID string) map[string]bool {
	if sessionID == "" {
		return map[string]bool{}
	}
	al.loadedToolsMu.Lock()
	defer al.loadedToolsMu.Unlock()
	src := al.loadedTools[sessionID]
	if len(src) == 0 {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// recordPendingSearchPromotions records each newly query-path-promoted name
// in names against bucket's current turn index, for later no-followup
// detection (ADR-071 §4.3.1a). Only ever called from the markLoaded closure
// when tools.IsSearchPromotion(ctx) is true — an exact-name `names` load
// must never reach here (FR-038a). No-op for an empty bucket or names.
// Safe for concurrent access — protected by loadedToolsMu.
func (al *AgentLoop) recordPendingSearchPromotions(bucket string, names []string) {
	if bucket == "" || len(names) == 0 {
		return
	}
	al.loadedToolsMu.Lock()
	defer al.loadedToolsMu.Unlock()
	if al.pendingSearchPromotions[bucket] == nil {
		al.pendingSearchPromotions[bucket] = make(map[string]int, len(names))
	}
	turn := al.bucketTurnCounter[bucket]
	for _, n := range names {
		al.pendingSearchPromotions[bucket][n] = turn
	}
}

// clearPendingSearchPromotion deletes bucket's pending-discovery record for
// name, if any, because it is about to be invoked (ADR-071 §4.3.1a "Clear").
// Called from the tool-dispatch site on every call regardless of tier — a
// no-op map delete when there is no pending entry for name.
// Safe for concurrent access — protected by loadedToolsMu.
func (al *AgentLoop) clearPendingSearchPromotion(bucket, name string) {
	if bucket == "" || name == "" {
		return
	}
	al.loadedToolsMu.Lock()
	defer al.loadedToolsMu.Unlock()
	if pending := al.pendingSearchPromotions[bucket]; pending != nil {
		delete(pending, name)
	}
}

// searchPromotionHorizonTurns is the number of turns a query-path ToolSearch
// promotion may sit unused before it counts toward
// omnipus_toolsearch_no_followup_total (ADR-071 §4.3.1a, FR-038).
//
// This is a NEW, INDEPENDENT literal — it MUST NOT be derived from, coupled
// to, or merged with cfg.Tools.MCP.Discovery.TTL, whose default is also 5.
// The equal value is a coincidence of two separate choices: the MCP TTL
// decides when an externally-provided tool stops being callable; this
// horizon decides only when an unused static discovery is COUNTED, and
// withdraws nothing from anyone. They are also not operator-equivalent — the
// MCP TTL is operator-configurable and an operator who has tuned it away
// from 5 must not see this horizon move with it. Conflating the two is the
// exact defect ADR-071 §1.1.1 records as its own worst mistake.
const searchPromotionHorizonTurns = 5

// tickSearchPromotionHorizon advances bucket's turn counter by one and
// sweeps its pendingSearchPromotions entries for staleness (ADR-071
// §4.3.1a). Called exactly once per real conversational turn — after
// runTurn's turnLoop for-loop naturally exits, deliberately NOT inside that
// loop (unlike the unrelated per-round ts.agent.Tools.TickTTL() call it used
// to sit next to) — one existing call site. Any entry whose
// recorded turn index is more than searchPromotionHorizonTurns turns old
// increments omnipus_toolsearch_no_followup_total exactly once and is
// deleted (deleting on fire is what makes it fire exactly once per wasted
// promotion, not every turn thereafter). Purely observational: nothing is
// evicted from loadedTools, nothing changes about which tools are callable.
// No-op for the empty bucket.
func (al *AgentLoop) tickSearchPromotionHorizon(bucket string) {
	if bucket == "" {
		return
	}
	al.loadedToolsMu.Lock()
	al.bucketTurnCounter[bucket]++
	current := al.bucketTurnCounter[bucket]
	pending := al.pendingSearchPromotions[bucket]
	var stale int
	for name, recordedTurn := range pending {
		if current-recordedTurn > searchPromotionHorizonTurns {
			delete(pending, name)
			stale++
		}
	}
	al.loadedToolsMu.Unlock()

	for i := 0; i < stale; i++ {
		tools.RecordToolSearchNoFollowUp()
	}
}
