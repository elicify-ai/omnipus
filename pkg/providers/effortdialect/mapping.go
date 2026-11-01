// mapping.go — the effort mapping table (effort-levels-spec-r10.md,
// "Mapping table (DE-5)", dataset D2, FR-046, FR-047, FR-058).
//
// Go twin of src/lib/effort/mapEffort.ts; both are pinned by the one shared
// fixture src/lib/effort/fixtures/effort-mapping-d2.json (SC-002: identical
// results for every row).
//
// Rules, in order:
//   - Not set ("") and Provider default are never changed.
//   - A target that does not reason clears the value (Not set), no note.
//   - Off ("off"/"none"): `none` if offered, else the switch's `off`, else
//     blocked (levels: pick a level, nearest marked; nothing sendable: "Let the
//     provider decide" or Cancel).
//   - On: the switch's `on`, else the level nearest rank 3.
//   - A ranked level: exact, else nearest; switch only → `on`.
//   - An unranked name: exact only, else Provider default with a note (never On).
//   - Empty candidate: an On or ranked source whose target offers no eligible
//     destination (only `none` or only unranked names) → Provider default with
//     the mapping explanation. A non-Off value never maps into `none`.
//
// "Nearest" ties go to the lower level.

package effortdialect

import (
	"slices"
	"strings"
)

// onRank is the rank an On source maps nearest to (medium).
const onRank = 3

// Target is the SERVED capability of the candidate (provider, model)
// pair — already filtered by the encoder table, so "reasons but nothing
// sendable" is Reasons with no levels and no switch (spec DE-5 target kinds).
type Target struct {
	Levels  []string // level names as published, may include "none"
	Switch  bool
	Reasons bool // a served reasoning object is present
}

// Mapping is the outcome of the mapping table for one (source, target).
type Mapping struct {
	// Value is the mapped stored value for a picker switch: a level name,
	// "on", "off", "provider_default", or "" for Not set / cleared.
	Value string
	// Blocked is true when the switch must not complete silently: the picker
	// shows the blocked step and REST/tools refuse; at request time nothing
	// is sent.
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

// NoteKinds enumerates the stable note kinds MapEffort can return.
var NoteKinds = []string{
	"mapped", "mapped_on", "switch_only", "nothing_sendable",
	"empty_candidate", "unranked_default", "not_supported",
}

// IsOff reports whether a stored value means Off (FR-044 normalisation).
func IsOff(v string) bool { return v == ValueOff || v == ValueNone }

func mappingResult(value, kind string) Mapping {
	return Mapping{Value: value, NoteKind: kind}
}

// nearestLevel returns the offered ranked level nearest rank, excluding
// `none`; ties go to the lower level; "" when no level qualifies.
func nearestLevel(levels []string, rank int) string {
	best, bestDist, bestRank := "", -1, 0
	for _, l := range levels {
		r, ok := Rank(l)
		if !ok || l == ValueNone {
			continue
		}
		d := r - rank
		if d < 0 {
			d = -d
		}
		if bestDist < 0 || d < bestDist || (d == bestDist && r < bestRank) {
			best, bestDist, bestRank = l, d, r
		}
	}
	return best
}

func mapOff(target Target, nothingSendable bool) Mapping {
	if slices.Contains(target.Levels, ValueNone) {
		return mappingResult(ValueNone, "")
	}
	if target.Switch {
		return mappingResult(ValueOff, "")
	}
	if nothingSendable {
		return Mapping{Blocked: true, ProviderDefaultEscape: true, NoteKind: "nothing_sendable"}
	}
	return Mapping{Blocked: true, Nearest: nearestLevel(target.Levels, 0)}
}

// MapEffort applies the spec mapping table (DE-5) to one source value and one
// served target capability.
func MapEffort(source string, target Target) Mapping {
	if source == "" || source == ValueProviderDefault {
		return mappingResult(source, "")
	}
	if !target.Reasons {
		return mappingResult("", "")
	}
	nothingSendable := len(target.Levels) == 0 && !target.Switch
	if IsOff(source) {
		return mapOff(target, nothingSendable)
	}
	if nothingSendable {
		return mappingResult(ValueProviderDefault, "nothing_sendable")
	}
	if source == ValueOn {
		if target.Switch {
			return mappingResult(ValueOn, "")
		}
		if n := nearestLevel(target.Levels, onRank); n != "" {
			return mappingResult(n, "mapped_on")
		}
		return mappingResult(ValueProviderDefault, "empty_candidate")
	}
	rank, ranked := Rank(source)
	if !ranked {
		// Unranked name: exact only, never On.
		if slices.Contains(target.Levels, source) {
			return mappingResult(source, "")
		}
		return mappingResult(ValueProviderDefault, "unranked_default")
	}
	if slices.Contains(target.Levels, source) {
		return mappingResult(source, "")
	}
	if n := nearestLevel(target.Levels, rank); n != "" {
		return mappingResult(n, "mapped")
	}
	if target.Switch {
		return mappingResult(ValueOn, "switch_only")
	}
	return mappingResult(ValueProviderDefault, "empty_candidate")
}

// Rank returns the spec rank of a named level (none 0 … max 6);
// unranked names have no rank (ok=false).
func Rank(level string) (int, bool) {
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
