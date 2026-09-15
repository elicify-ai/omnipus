// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package plan

// handover_clamp.go — the single, unbypassable bound on Plan.HandoverText.
//
// WHY THIS FIELD CLAMPS WHERE EVERY OTHER BOUNDED FIELD REJECTS
//
// Title/Goal/Description/Rationale are authored by a human or by an agent at
// an interactive boundary: a "too long" error reaches somebody who can
// shorten the text and try again, so rejecting is the right answer there and
// normalize() still does exactly that.
//
// HandoverText is different in kind. It is SERVER-SET ONLY (see its doc
// comment on Plan), built by the plan engine at a decision point with nobody
// in the loop, and several of its writers embed a string whose length no part
// of this system controls:
//
//   - the judge-unavailability park note embeds the judge's own failure
//     reason — a provider error body, arbitrary length;
//   - the UNMET steering note embeds one provider-authored reason per unmet
//     criterion;
//   - the stall note embeds one member id per blocked/inbox member;
//   - the member-cancel / unreachable-DoD notes embed one member title each;
//   - the rounds-exhausted, correction-budget and supervision-unavailable
//     notes embed the PREVIOUS HandoverText verbatim inside new boilerplate,
//     so a handover sitting just under the bound overflows it by construction
//     on the very next terminal write.
//
// Rejecting those does not get a shorter note from anywhere — it gets NO
// note. The write fails deterministically, every time, for that plan, and the
// engine's bounded retry then ends a plan whose members all SUCCEEDED at
// failed(supervision_unavailable). Losing the whole diagnostic because the
// diagnostic was long is strictly worse than keeping a truncated one.
//
// WHAT THE 8000-RUNE BOUND ACTUALLY DEFENDS (so: kept, not raised)
//
// It is NOT a wire limit: handover_text appears nowhere in contracts/, and
// pkg/gateway's toWirePlan deliberately does not forward it (pinned by
// pkg/gateway/rest_plans_test.go's "HandoverText must stay off the wire").
// It is NOT a store limit either: the plan record is one atomically-rewritten
// JSON file with no size ceiling.
//
// What it defends is an AGENT CONTEXT BUDGET. HandoverText is re-embedded
// verbatim into the supervisor/owner wake prompt that drives the next turn,
// and into the next handover after that. 8000 runes is already ~2k tokens of
// one field of one prompt. Raising the bound would not fix this defect class
// — a provider can return a megabyte — and removing it would let provider
// output of unbounded size into an agent's context window. So the bound
// stays and the HANDLING changes: clamp at write time instead of refusing
// the write.
//
// WHERE THE CLAMP SITS
//
// Two call sites, one helper, idempotent:
//
//  1. Plan.normalize() — the funnel both Store.Create and Store.updateLocked
//     run immediately before persisting, so the *Plan those two RETURN to the
//     caller already matches what went to disk.
//  2. Store.write() — the only function in this package that marshals a Plan
//     to disk, and unexported, so nothing outside pkg/plan can reach disk
//     without passing through it and no future in-package writer can either.
//     This is the site that makes the clamp unbypassable rather than merely
//     available; (1) alone would be a helper a writer could forget.
//
// NOTHING IS SILENTLY DESTROYED
//
// The truncation is head-preserving (every note above leads with what failed)
// and leaves an explicit marker naming how much was elided. The FULL original
// is emitted once, at WARN, by clampPlanHandover below, under the
// "plan: handover text clamped" message with the plan id — the marker points
// there by name. For the judge-unavailability case the raw provider reason
// additionally survives in pkg/agent/plan_engine.go's own ErrorCF park log.

import (
	"fmt"
	"log/slog"
	"unicode/utf8"
)

// handoverTruncationMarkerFmt is appended to a clamped handover. Its three
// arguments are (elided runes, original runes, the bound).
//
// It is part of the persisted text on purpose: the agent that reads this
// handover as its next prompt must be able to tell "the judge said nothing
// more" apart from "the judge said more and we dropped it".
const handoverTruncationMarkerFmt = "\n\n[handover clamped: %d of %d characters elided to fit the %d-character bound. " +
	"The full text was logged at WARN — search the gateway log for \"plan: handover text clamped\" with this plan's id.]"

// ClampHandoverText returns s bounded to maxPlanHandoverRunes runes,
// preserving the HEAD and appending handoverTruncationMarkerFmt when it has
// to cut. It is pure, deterministic and idempotent, and it never splits a
// rune (it cuts on a rune boundary by construction, slicing []rune).
//
// Callers do NOT have to call this to be safe — Store.write clamps
// unconditionally. It is exported for the one thing the write path cannot do
// for a caller: let a writer that DEDUPES against the persisted value (e.g.
// pkg/agent's surfaceStallIfAny, which skips a repeat wake when
// p.HandoverText == the note it just built) compare like for like. A writer
// that compares a raw over-long note against the clamped value on disk will
// never see them match and will re-write and re-wake every tick.
func ClampHandoverText(s string) string {
	// Fast path. A string's byte length is an upper bound on its rune count,
	// so anything this short is certainly within the bound and costs no
	// allocation.
	if len(s) <= maxPlanHandoverRunes {
		return s
	}
	runes := []rune(s)
	total := len(runes)
	if total <= maxPlanHandoverRunes {
		return s
	}
	// Reserve the WIDEST the marker could render: the elided count can never
	// exceed the original count, and a decimal number never renders wider
	// than a larger one, so formatting with (total, total) bounds the marker
	// width from above. Result width is then keep + realMarker <= keep +
	// widest == maxPlanHandoverRunes, guaranteed, without a second pass.
	widest := utf8.RuneCountInString(fmt.Sprintf(handoverTruncationMarkerFmt, total, total, maxPlanHandoverRunes))
	keep := maxPlanHandoverRunes - widest
	if keep < 0 {
		// Unreachable at the current bound (the marker is ~180 runes against
		// 8000) but kept so a future, much smaller bound degrades to
		// "marker only" rather than panicking on a negative slice index.
		keep = 0
	}
	return string(runes[:keep]) + fmt.Sprintf(handoverTruncationMarkerFmt, total-keep, total, maxPlanHandoverRunes)
}

// clampPlanHandover bounds p.HandoverText in place and, when it actually had
// to cut, emits the single WARN record that carries the FULL original text —
// the copy the marker in the persisted note points at.
//
// Logging only on a real cut is what makes the two call sites (normalize,
// write) log once between them rather than twice: by the time write runs, a
// plan that came through normalize is already clamped, so this is a no-op
// there.
func clampPlanHandover(p *Plan) {
	if p == nil {
		return
	}
	clamped := ClampHandoverText(p.HandoverText)
	if clamped == p.HandoverText {
		return
	}
	slog.Warn("plan: handover text clamped",
		"plan_id", p.ID,
		"bound_runes", maxPlanHandoverRunes,
		"original_runes", utf8.RuneCountInString(p.HandoverText),
		"kept_runes", utf8.RuneCountInString(clamped),
		"handover_text_full", p.HandoverText,
	)
	p.HandoverText = clamped
}
