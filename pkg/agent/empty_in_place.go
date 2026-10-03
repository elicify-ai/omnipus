// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// empty_in_place.go — the surviving, still-live pieces of ADR-066 D5's
// original "empty eligible tool results in place" pass: the
// omnipus_context_empties_total counter (FR-023, US-6.AC10), the named
// emptying sites that label it, and the tool-result-share helper (FR-024)
// several window packages still call directly. The D5 pass itself
// (emptyInPlace, eligibleToolResults, floorResultIndices, emptyOldestFirst,
// and their private support helpers) was deleted as dead code: R1 GREEN's
// window-trim refactor removed the last production call site, leaving only
// the counter, the site labels and the share helper with live callers
// (pkg/agent/window_checkpoint.go, pkg/agent/window_trim_checked.go,
// pkg/agent/window_relief_metrics.go, pkg/agent/window_runtime.go,
// pkg/agent/recall_injection.go, pkg/gateway/metrics.go).
package agent

import (
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// contextEmptiesTotal backs omnipus_context_empties_total (FR-023,
// US-6.AC10): tool results emptied in place, summed over every pass.
var contextEmptiesTotal atomic.Int64

// ContextEmptiesTotal returns the context_empties_total counter. Rendered
// by the gateway's /metrics handler (pkg/gateway/metrics.go).
func ContextEmptiesTotal() int64 { return contextEmptiesTotal.Load() }

// Emptying sites, named in the INFO line so an operator can tell a pre-turn
// floor empty (register #3) from a mid-turn one (D6).
const (
	emptyingSitePreTurn = "pre_turn"
	emptyingSiteMidTurn = "mid_turn"
)

// toolResultShareTokens is D6's `share`: the estimator tokens of every
// role:"tool" message in msgs (the second trigger condition, FR-024).
func toolResultShareTokens(msgs []providers.Message) int {
	share := 0
	for _, m := range msgs {
		if m.Role == "tool" {
			share += estimateMessageTokens(m)
		}
	}
	return share
}

// midTurnLineResolver returns the lineOf function for the in-memory slice
// the tool loop builds (pinned core + breadcrumb + window + this turn's
// appended messages).
//
// It is POSITION-AWARE, not id-keyed. The archive is append-only and the
// slice's tool messages appear in the same relative order as their archive
// lines (the window is an archive suffix; the choke point appends this
// turn's results before the slice does), so a single reverse walk that
// CONSUMES each matched line assigns every message its own line — including
// when a provider reuses an id such as call_0 on every turn (B-29b, the very
// reason memory.ProjectionKey is composite).
//
// Resolving by id alone took the most recent line for every message carrying
// that id, so two results sharing an id both resolved to the newer line: the
// mark described the wrong content, and the persisted (id, line) → emptied
// entry made the reload projection blank a DIFFERENT, newer result than the
// live pass emptied — the live/reload byte-identity B-22 requires.
//
// A message that is not on the archive (a system note, a spliced recall
// span) matches nothing, resolves to −1 and consumes no line.
func midTurnLineResolver(archive []memory.ArchivedMessage, msgs []providers.Message) func(int) int {
	lines := make(map[int]int, len(msgs))
	next := len(archive) - 1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "tool" || msgs[i].ToolCallID == "" {
			continue
		}
		found := -1
		for j := next; j >= 0; j-- {
			if archive[j].Role == "tool" && archive[j].ToolCallID == msgs[i].ToolCallID {
				found = j
				break
			}
		}
		lines[i] = found
		if found >= 0 {
			// Consume: an EARLIER message can only own an earlier line.
			next = found - 1
		}
	}
	return func(i int) int {
		if line, ok := lines[i]; ok {
			return line
		}
		return -1
	}
}
