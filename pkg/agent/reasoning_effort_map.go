// reasoning_effort_map.go — RED SCAFFOLDING for the effort-levels feature
// (train T7, branch landing/effort). Spec:
// /workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md
// (section "Mapping table (DE-5)", dataset D2, FR-046, FR-058).
//
// This file is written by the RED test author so the failing tests compile and
// fail on behaviour. The GREEN implementation replaces the bodies of these
// functions with the real mapping table; the signatures below are the
// contract the tests pin. Every stub result is a sentinel that can never be a
// legal mapping outcome, so every mapping assertion fails until the real
// table lands. The note strings are the exact spec texts; the note KINDS are
// what the shared D2 fixture pins.
//
// Mapping table (DE-5), restated for the implementation:
//   normalise: off/none = Off, on = On
//   ranks: none 0 < minimal 1 < low 2 < medium 3 < high 4 < xhigh 5 < max 6
//   nearest ties go to the LOWER level; a non-Off source never maps into none.

package agent

import "strings"

// EffortTarget is the SERVED capability of the candidate (provider, model)
// pair — already filtered by the encoder table, so "reasons but nothing
// sendable" is Reasons with no levels and no switch (spec DE-5 target kinds).
type EffortTarget struct {
	Levels  []string // level names as published, may include "none"
	Switch  bool
	Reasons bool // a served reasoning object is present
}

// EffortMapping is the outcome of the mapping table for one (source, target).
type EffortMapping struct {
	// Value is the mapped stored value for a picker switch: a level name,
	// "on", "off", "provider_default", or "" for Not set / cleared.
	Value string
	// Blocked is true when the switch must not complete silently: the picker
	// shows the blocked step and REST/tools refuse.
	Blocked bool
	// Nearest is the nearest level suggested (marked, not chosen) on the
	// blocked step for a levels target; empty otherwise.
	Nearest string
	// ProviderDefaultEscape is true for the "nothing sendable" blocked kind,
	// where the step offers "Let the provider decide" or Cancel (P-Q2 A).
	ProviderDefaultEscape bool
	// NoteKind is a stable note identifier; "" means no note. Kinds:
	//   mapped            — "Effort set to X. {model} doesn't offer Y."
	//   mapped_on         — "Effort set to X." (On source; nothing missing)
	//   switch_only       — "{model} only turns reasoning on or off." suffix
	//   nothing_sendable  — "Omnipus can't change effort for {model}."
	//   empty_candidate   — "{model} offers no level here that Y maps to."
	//   unranked_default  — "{model} doesn't offer that setting." (never On)
	//   not_supported     — request-time "can't turn reasoning off" (X-paths)
	NoteKind string
}

// MapEffort applies the spec mapping table (DE-5) to one source value and one
// served target capability. RED scaffold: returns a sentinel outcome that
// every D2 row assertion rejects.
func MapEffort(source string, target EffortTarget) EffortMapping {
	_ = source
	_ = target
	return EffortMapping{Value: "__red_scaffold_not_implemented__", NoteKind: "__red_scaffold_not_implemented__"}
}

// EffortNoteKinds enumerates the stable note kinds MapEffort can return.
var EffortNoteKinds = []string{
	"mapped", "mapped_on", "switch_only", "nothing_sendable",
	"empty_candidate", "unranked_default", "not_supported",
}

// EffortSwitchNote renders the picker switch note for a note kind, with the
// model name and the source value interpolated, per the spec's exact strings
// (S-09, S-72, S-73; FR-058). RED scaffold: returns the sentinel text.
func EffortSwitchNote(kind, model, source string) string {
	_ = kind
	_ = model
	_ = source
	return "__red_scaffold_note_not_implemented__"
}

// EffortTurnNote renders the per-turn note ("Sent as Low: {model} doesn't
// offer Medium." / "Effort not sent: …") for a note kind. RED scaffold.
func EffortTurnNote(kind, model, source string) string {
	_ = kind
	_ = model
	_ = source
	return "__red_scaffold_turn_note_not_implemented__"
}

// effortRank returns the spec rank of a named level (none 0 … max 6);
// unranked names have no rank (ok=false).
func effortRank(level string) (int, bool) {
	switch strings.ToLower(level) {
	case "none":
		return 0, true
	case "minimal":
		return 1, true
	case "low":
		return 2, true
	case "medium":
		return 3, true
	case "high":
		return 4, true
	case "xhigh":
		return 5, true
	case "max":
		return 6, true
	}
	return 0, false
}
