// reasoning_effort_map.go — the effort mapping table (effort-levels-spec-r10.md,
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

package agent

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/providers/effortdialect"
)

// The mapping table itself lives in effortdialect (shared with the agent
// tools and the REST writers); these names keep the agent-side spelling.

// Reserved stored effort words (FR-044).
const (
	EffortProviderDefault = effortdialect.ValueProviderDefault
	EffortOn              = effortdialect.ValueOn
	EffortOff             = effortdialect.ValueOff
	effortNone            = effortdialect.ValueNone
)

// EffortTarget is the served capability of the candidate (provider, model).
type EffortTarget = effortdialect.Target

// EffortMapping is the outcome of the mapping table for one (source, target).
type EffortMapping = effortdialect.Mapping

// EffortNoteKinds enumerates the stable note kinds MapEffort can return.
var EffortNoteKinds = effortdialect.NoteKinds

// MapEffort applies the spec mapping table (DE-5).
func MapEffort(source string, target EffortTarget) EffortMapping {
	return effortdialect.MapEffort(source, target)
}

func isEffortOff(v string) bool { return effortdialect.IsOff(v) }

func effortRank(level string) (int, bool) { return effortdialect.Rank(level) }

// EffortLabel is the display label of a stored effort value ("none" reads
// Off, "xhigh" XHigh), the same labels the web UI uses.
func EffortLabel(v string) string {
	switch v {
	case effortNone, EffortOff:
		return "Off"
	case EffortOn:
		return "On"
	case EffortProviderDefault:
		return "Provider default"
	case "xhigh":
		return "XHigh"
	case "":
		return "Not set"
	}
	return strings.ToUpper(v[:1]) + v[1:]
}

// firstValue returns the optional mapped/sent value argument, "" if absent.
func firstValue(value []string) string {
	if len(value) == 0 {
		return ""
	}
	return value[0]
}

// EffortSwitchNote renders the picker switch note for a note kind, with the
// model name and the source value interpolated, per the spec's exact strings
// (S-09, S-72, S-73; FR-058). The `mapped` and `mapped_on` texts name the
// mapped value, so callers pass it as the optional fourth argument; without
// it those two kinds have no text to render and return "".
func EffortSwitchNote(kind, model, source string, value ...string) string {
	v := firstValue(value)
	switch kind {
	case "mapped":
		if v == "" {
			return ""
		}
		return "Effort set to " + EffortLabel(v) + ". " + model + " doesn't offer " + EffortLabel(source) + "."
	case "mapped_on":
		if v == "" {
			return ""
		}
		return "Effort set to " + EffortLabel(v) + "."
	case "switch_only":
		return "Effort set to On. " + model + " only turns reasoning on or off."
	case "nothing_sendable":
		return "Effort set to Provider default. Omnipus can't change effort for " + model + "."
	case "unranked_default":
		return "Effort set to Provider default. " + model + " doesn't offer that setting."
	case "empty_candidate":
		return "Effort set to Provider default. " + model + " offers no level here that " + EffortLabel(source) + " maps to."
	case "not_supported":
		return model + " can't turn reasoning off."
	}
	return ""
}

// EffortTurnNote renders the per-turn note ("Sent as Low: {model} doesn't
// offer Medium." / "Effort not sent: …") for a note kind. The `mapped`,
// `mapped_on` and `switch_only` texts name the value actually sent, passed as
// the optional fourth argument; without it they return "".
func EffortTurnNote(kind, model, source string, sent ...string) string {
	s := firstValue(sent)
	switch kind {
	case "mapped", "mapped_on", "switch_only":
		if s == "" {
			return ""
		}
		return "Sent as " + EffortLabel(s) + ": " + model + " doesn't offer " + EffortLabel(source) + "."
	case "not_supported":
		return "Effort not sent: " + model + " can't turn reasoning off."
	case "empty_candidate":
		return "Effort not sent: " + model + " offers no level here that " + source + " maps to."
	case "nothing_sendable", "unranked_default":
		return "Effort not sent: Omnipus can't change effort for " + model + "."
	}
	return ""
}
