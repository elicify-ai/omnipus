// Omnipus — view-kinds-design-2026-09-03 §2.3 / §6.2: the availability rules
// for the eight closed view kinds, shared between knowledge_describe's
// discovery block (§6.2, wired in knowledge_describe.go) and
// knowledge_configure's create_view composer gates (§6.1, G1).
//
// WHY THIS IS A NEW FILE, NOT PART OF knowledge_configure.go
//
// knowledge_configure.go's op=create_view implementation is a SEPARATE, and
// concurrent, piece of work — a different agent owns that file for the
// composer itself. The design's own point in putting a discovery block next
// to a composer (§6.3: "the agent asks, it does not remember") only holds if
// the two AGREE: a kind knowledge_describe calls available must never be one
// the composer then refuses, and a refusal reason knowledge_describe names
// must be the exact reason the composer would give for the same schema. The
// only way to guarantee that without a second hand-copied rulebook is to
// write the rule ONCE, here, and have both callers read it. This file is that
// one rulebook; knowledge_describe.go calls into it and knowledge_configure.go
// is expected to, once create_view lands.
//
// EVERY RULE BELOW IS TRANSCRIBED FROM design §2.3's TABLE, EXACTLY. Nothing
// here invents a ninth condition — see D5 (§9) for the one place the design
// itself had to be corrected (there is no image-capable property type yet,
// so `tiles` is wired off, not worked around).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// The eight kinds, in design §2.3's own table order
// ---------------------------------------------------------------------------

// The eight kind names, re-exported from the generated ViewDefKind*
// constants (not a second set of string literals) so a ninth kind, should the
// closed set ever change, fails a build here rather than drifting silently.
// Callers outside this package (knowledge_configure.go's future create_view,
// tests) spell a kind as ViewKindBoard rather than the generated type's own
// longer name.
const (
	ViewKindTable     = string(generated.ViewDefKindTable)
	ViewKindList      = string(generated.ViewDefKindList)
	ViewKindTiles     = string(generated.ViewDefKindTiles)
	ViewKindBoard     = string(generated.ViewDefKindBoard)
	ViewKindCalendar  = string(generated.ViewDefKindCalendar)
	ViewKindSummary   = string(generated.ViewDefKindSummary)
	ViewKindTrend     = string(generated.ViewDefKindTrend)
	ViewKindBreakdown = string(generated.ViewDefKindBreakdown)
)

// ViewKindOrder lists the eight view kinds in the order design §2.3's table
// declares them — the order knowledge_describe's "available views" block
// renders in, and the order the compressed tool-description text below names
// them in.
var ViewKindOrder = []string{
	ViewKindTable, ViewKindList, ViewKindTiles, ViewKindBoard,
	ViewKindCalendar, ViewKindSummary, ViewKindTrend, ViewKindBreakdown,
}

// maxBoardEnumValues is design §2.3's "≤ 8 values" bound for `board`.
const maxBoardEnumValues = 8

// boardEnumEligible is the ONE place the board bound is APPLIED, as
// ImageEligible is the one place tiles' eligibility is decided. Both the
// discovery block below and knowledge_configure's create_view gate
// (gateG1RequireChoice, knowledge_configure_create_view.go) call it, so the
// set of enums knowledge_describe calls board-available and the set the
// composer accepts are one set by construction — not two comparisons that
// happen to read the same constant today.
func boardEnumEligible(p *records.Property) bool {
	return p != nil && len(p.Values) <= maxBoardEnumValues
}

// ---------------------------------------------------------------------------
// D5 — the one place the design's own §2.1 was wrong, corrected 2026-09-03
//
// records.PropertyTypes is a closed set of EIGHT: text, enum, relation, date,
// integer, decimal, person, checkbox (pkg/records/schema.go). There is no
// "file" or "image" type. design §2.1 originally claimed one already existed
// in the records layer; it does not, and D5 (§9) is the ruling on what to do
// about it: `tiles` ships gated off rather than binding its requirement to a
// type that was never declared for the purpose (binding it to `text` would
// make tiles available on every vault and attach rendering behaviour to
// unvalidated strings — rejected for the record in D5).
// ---------------------------------------------------------------------------

// ImageEligible reports whether a declared property TYPE can back `tiles`
// (design §2.3: "an image-capable property"). It is the ONE switch this
// codebase consults for that question — knowledge_describe's availability
// block below calls it, and knowledge_configure's create_view gate for
// kind=tiles is meant to call the exact same function, so the two paths can
// never disagree about which property types qualify.
//
// It returns false for every type today, by design (D5): no property type in
// records.PropertyTypes is image-capable yet. The day a file/image property
// type lands in the records layer, flipping this one function is the whole of
// what makes `tiles` available — nothing in knowledge_describe.go or (once
// written) knowledge_configure.go needs to change.
func ImageEligible(t records.PropertyType) bool {
	return false
}

// imageIneligibleReason is D5's refusal wording, verbatim, for both the
// discovery block and (once wired) the composer's create_view refusal.
const imageIneligibleReason = "no image-capable property type exists yet"

// ---------------------------------------------------------------------------
// Per-kind availability
// ---------------------------------------------------------------------------

// ViewKindAvailability describes whether one of the eight view kinds can be
// authored against one record type right now, and why.
type ViewKindAvailability struct {
	// Kind is one of ViewKindOrder's eight values.
	Kind string
	// Available is design §2.3's gate: does this record type declare what the
	// kind's row requires.
	Available bool
	// Bindings names, in design §6.1's own argument vocabulary
	// (number/date/choice/group/unit), the properties that satisfy the
	// requirement — e.g. "number: amount, unit: currency". Empty when the
	// kind needs no specific property (`table`, `list`) or is unavailable.
	Bindings string
	// Missing names exactly what requirement is absent (design §2.3's G1:
	// "a refusal names the missing property and lists candidate properties if
	// any near-miss exists"). Empty when Available is true.
	Missing string
}

// ViewKindAvailabilityFor computes all eight kinds' availability against one
// record type's schema, in ViewKindOrder.
func ViewKindAvailabilityFor(sc *records.Schema) []ViewKindAvailability {
	out := make([]ViewKindAvailability, 0, len(ViewKindOrder))
	numbers := propertiesOfTypes(sc, records.TypeInteger, records.TypeDecimal)
	dates := propertiesOfTypes(sc, records.TypeDate)

	for _, kind := range ViewKindOrder {
		switch kind {
		case string(generated.ViewDefKindTable), string(generated.ViewDefKindList):
			// design §2.3: "anything" — every record type qualifies, even one
			// declaring no properties at all.
			out = append(out, ViewKindAvailability{Kind: kind, Available: true})
		case string(generated.ViewDefKindTiles):
			out = append(out, ViewKindAvailability{
				Kind: kind, Available: false, Missing: imageIneligibleReason,
			})
		case string(generated.ViewDefKindBoard):
			out = append(out, boardAvailability(sc))
		case string(generated.ViewDefKindCalendar):
			out = append(out, calendarAvailability(dates))
		case string(generated.ViewDefKindSummary):
			out = append(out, summaryAvailability(numbers))
		case string(generated.ViewDefKindTrend):
			out = append(out, trendAvailability(dates, numbers))
		case string(generated.ViewDefKindBreakdown):
			out = append(out, breakdownAvailability(sc, numbers))
		}
	}
	return out
}

// propertiesOfTypes lists a schema's declared properties whose type is one of
// those given, in declaration order — the order every other listing in
// knowledge_describe.go already renders in (renderProperties), so a reader
// sees candidates in the order they wrote them.
func propertiesOfTypes(sc *records.Schema, types ...records.PropertyType) []*records.Property {
	if sc == nil {
		return nil
	}
	var out []*records.Property
	for _, name := range sc.PropertyNames() {
		p, ok := sc.Property(name)
		if !ok {
			continue
		}
		for _, t := range types {
			if p.Type == t {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// numberBinding renders one number property's design §6.1 binding text —
// "number: amount", plus ", unit: currency" when the property declares a
// companion unit property (§5).
func numberBinding(p *records.Property) string {
	s := "number: " + p.Name
	if p.UnitProperty != "" {
		s += ", unit: " + p.UnitProperty
	}
	return s
}

// board — design §2.3: "an enum property with ≤ 8 values".
func boardAvailability(sc *records.Schema) ViewKindAvailability {
	enums := propertiesOfTypes(sc, records.TypeEnum)
	if len(enums) == 0 {
		return ViewKindAvailability{
			Kind: string(generated.ViewDefKindBoard), Available: false,
			Missing: "no enum property declared",
		}
	}
	for _, p := range enums {
		if boardEnumEligible(p) {
			return ViewKindAvailability{
				Kind: string(generated.ViewDefKindBoard), Available: true,
				Bindings: "choice: " + p.Name,
			}
		}
	}
	// G1: "lists candidate properties if any near-miss exists" — the
	// declared enum closest to qualifying is the one with the FEWEST values,
	// ties broken by declaration order (propertiesOfTypes' own order).
	nearest := enums[0]
	for _, p := range enums[1:] {
		if len(p.Values) < len(nearest.Values) {
			nearest = p
		}
	}
	return ViewKindAvailability{
		Kind: string(generated.ViewDefKindBoard), Available: false,
		Missing: fmt.Sprintf("no enum with ≤ %d values; %q has %d", maxBoardEnumValues, nearest.Name, len(nearest.Values)),
	}
}

// calendar — design §2.3: "a date property".
func calendarAvailability(dates []*records.Property) ViewKindAvailability {
	if len(dates) == 0 {
		return ViewKindAvailability{
			Kind: string(generated.ViewDefKindCalendar), Available: false,
			Missing: "no date property declared",
		}
	}
	return ViewKindAvailability{
		Kind: string(generated.ViewDefKindCalendar), Available: true,
		Bindings: "date: " + dates[0].Name,
	}
}

// summary — design §2.3: "a number property".
func summaryAvailability(numbers []*records.Property) ViewKindAvailability {
	if len(numbers) == 0 {
		return ViewKindAvailability{
			Kind: string(generated.ViewDefKindSummary), Available: false,
			Missing: "no number property declared",
		}
	}
	return ViewKindAvailability{
		Kind: string(generated.ViewDefKindSummary), Available: true,
		Bindings: numberBinding(numbers[0]),
	}
}

// trend — design §2.3: "a date + a number". Unavailable wording matches
// design §6.2's own worked example verbatim ("no number tracked over a
// date") regardless of which of the two is the one actually missing — the
// design states the pairing as one requirement, not two independent ones.
func trendAvailability(dates, numbers []*records.Property) ViewKindAvailability {
	if len(dates) == 0 || len(numbers) == 0 {
		return ViewKindAvailability{
			Kind: string(generated.ViewDefKindTrend), Available: false,
			Missing: "no number tracked over a date",
		}
	}
	return ViewKindAvailability{
		Kind: string(generated.ViewDefKindTrend), Available: true,
		Bindings: "date: " + dates[0].Name + ", " + numberBinding(numbers[0]),
	}
}

// breakdown — design §2.3: "two groupable properties + a number". Grouping
// itself carries no type restriction anywhere in the records layer (view.go's
// grouping validation checks only that the named property exists on the
// type, never its kind — the same rule `table`/`list` read as "anything"
// here reads as "any OTHER two properties"), so "groupable" is every
// declared property except the one bound as the number.
func breakdownAvailability(sc *records.Schema, numbers []*records.Property) ViewKindAvailability {
	if len(numbers) == 0 {
		return ViewKindAvailability{
			Kind: string(generated.ViewDefKindBreakdown), Available: false,
			Missing: "no number property declared",
		}
	}
	number := numbers[0]
	var groupCandidates []*records.Property
	for _, name := range sc.PropertyNames() {
		p, ok := sc.Property(name)
		if !ok || p.Name == number.Name {
			continue
		}
		groupCandidates = append(groupCandidates, p)
	}
	if len(groupCandidates) < 2 {
		return ViewKindAvailability{
			Kind: string(generated.ViewDefKindBreakdown), Available: false,
			Missing: fmt.Sprintf(
				"needs two other properties to group by besides %q; %d available",
				number.Name, len(groupCandidates)),
		}
	}
	return ViewKindAvailability{
		Kind: string(generated.ViewDefKindBreakdown), Available: true,
		Bindings: fmt.Sprintf("%s, group: %s, group: %s",
			numberBinding(number), groupCandidates[0].Name, groupCandidates[1].Name),
	}
}

// ---------------------------------------------------------------------------
// Rendering — the one line knowledge_describe.go's TYPES section adds per
// record type (design §6.2)
// ---------------------------------------------------------------------------

// RenderAvailableViews renders design §6.2's discovery line for one record
// type: every kind, available ones with their bindings, unavailable ones with
// what they are missing — the exact worked format §6.2 itself shows:
//
//	views you can create here: table, list, summary (number: amount, unit:
//	currency), board — NO (no enum with ≤ 8 values; `status` has 26), trend
//	— NO (no number tracked over a date), ...
//
// One line, comma-joined, matching the design document's own rendering
// rather than inventing a table/list layout knowledge_describe does not use
// anywhere else for a per-kind breakdown.
func RenderAvailableViews(sc *records.Schema) string {
	kinds := ViewKindAvailabilityFor(sc)
	parts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		parts = append(parts, renderOneViewKind(k))
	}
	return "views you can create here: " + strings.Join(parts, ", ")
}

func renderOneViewKind(k ViewKindAvailability) string {
	if !k.Available {
		return fmt.Sprintf("%s — NO (%s)", k.Kind, k.Missing)
	}
	if k.Bindings == "" {
		return k.Kind
	}
	return fmt.Sprintf("%s (%s)", k.Kind, k.Bindings)
}

// ---------------------------------------------------------------------------
// The compressed form for knowledge_configure's own tool description
// (design §6.2: "the same block appears in the tool's own schema description
// in compressed form ... because the tool description is what is in front of
// the agent at call time")
//
// THESE ARE DELIBERATELY EXPORTED, READY-TO-PASTE STRINGS, NOT WIRED IN HERE.
// knowledge_configure.go's op=create_view landing is a separate, concurrent
// piece of work (see this file's header) — wiring these into
// ConfigureTool.Description() / Parameters() is that work's job, in one call
// each, so the description text is derived from the same rulebook this file
// already is rather than hand-transcribed a second time (the drift
// knowledge_configure.go's own configurePropertyTypeSentence /
// configureOperatorSentence comment block already warns against for the
// property-type and operator sentences next to these).
// ---------------------------------------------------------------------------

// viewKindRequirementPhrase is design §2.3's own "Offered only when the
// collection has" column, one phrase per kind, generic (never naming a
// specific vault's properties — that half is ViewKindAvailability's job).
var viewKindRequirementPhrase = map[string]string{
	string(generated.ViewDefKindTable):     "any collection",
	string(generated.ViewDefKindList):      "any collection",
	string(generated.ViewDefKindTiles):     imageIneligibleReason,
	string(generated.ViewDefKindBoard):     fmt.Sprintf("an enum property with ≤ %d values", maxBoardEnumValues),
	string(generated.ViewDefKindCalendar):  "a date property",
	string(generated.ViewDefKindSummary):   "a number property",
	string(generated.ViewDefKindTrend):     "a date property and a number property",
	string(generated.ViewDefKindBreakdown): "two other properties and a number property",
}

// ConfigureCreateViewDescriptionFragment is ready-to-paste prose for
// ConfigureTool.Description() (pkg/knowledge/knowledge_configure.go) once
// op=create_view lands: names the op, the eight kinds, and each kind's
// requirement in compressed form, exactly as design §6.2 requires the tool
// schema itself to state. Wire it in with one call — do not hand-transcribe
// the kind list a second time.
var ConfigureCreateViewDescriptionFragment = buildCreateViewDescriptionFragment()

func buildCreateViewDescriptionFragment() string {
	parts := make([]string, 0, len(ViewKindOrder))
	for _, kind := range ViewKindOrder {
		parts = append(parts, fmt.Sprintf("%s (%s)", kind, viewKindRequirementPhrase[kind]))
	}
	return "create_view composes one of the 8 named view kinds: " +
		strings.Join(parts, ", ") + ". Call knowledge_describe on the record " +
		"type first — it states, per type, which of these are actually " +
		"available and which properties satisfy them; a kind requested here " +
		"that the type does not support is refused naming the missing " +
		"requirement, never partially written."
}

// ConfigureWriteViewSteerLine is the one line design §6.1 asks write_view's
// own description to gain: "write_view's tool description gains one line
// steering agents to create_view for the common cases." Wire it into
// knowledge_configure.go's write_view description alongside
// ConfigureCreateViewDescriptionFragment.
const ConfigureWriteViewSteerLine = "For a table/list/tiles/board/calendar/" +
	"summary/trend/breakdown, prefer op=create_view over hand-writing a " +
	"`definition` here — it validates the fields against this record type's " +
	"declared properties and refuses naming exactly what is missing, rather " +
	"than writing a view that fails once queried. write_view remains the raw " +
	"escape hatch for anything create_view's closed set does not cover."
