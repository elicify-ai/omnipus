// Omnipus — view-kinds-design-2026-09-03 §6.1: knowledge_configure op=create_view,
// the COMPOSER path.
//
// # Why this is a separate file from knowledge_configure.go
//
// knowledge_configure.go's own header explains why the logic half and the
// tool-adapter half of this package's control plane live together rather
// than being split into a pkg/tools boundary. That reasoning is about NOT
// separating validation-and-persistence from the tool surface — it says
// nothing about file layout, and knowledge_restructure already splits its
// trash/restore logic into its own file (knowledge_restructure_trash.go) for
// the ordinary reason: one 1000+-line file per concern is more than one
// reviewer wants to hold in their head at once. This file is create_view's
// concern: everything write_view already validates and persists (ParseView,
// ValidateViewAgainstSchemas, the tier-1 lock, the audit record, the cascade
// block) is REUSED, not reimplemented — this file's own job is exactly the
// part write_view does not have: turning eight named "kinds" plus a handful
// of property bindings into the `parts` stack §2.3's table describes, and
// refusing before any of it reaches disk when a gate in §3 is not met.
//
// # The gates, and why each is a NAMED function
//
// design §3 states six gates, G1..G6, as "the whole of the tool's judgement".
// Each below is its own named, individually callable function for the reason
// the task that produced this file states directly: a gate that is provably
// tested is a gate a mutation test can catch someone quietly deleting. A
// gate folded into one big composePartsForKind switch would still WORK, but
// nobody reviewing a diff six months from now could point at "this is where
// G4 lives" — they would have to re-derive it from the switch's shape.
//
// G1 (kind offered only when required properties exist) is not one function
// — it is one function PER KIND, because each kind's requirement is a
// DIFFERENT property type with a DIFFERENT near-miss story (board's is "an
// enum with too many values"; tiles' — per D5, design §9 — is that NO
// property type is eligible yet, so it refuses unconditionally rather than
// naming a near miss). Naming them gateG1RequireDate / …Image / …Choice /
// …GroupProperty keeps each testable and keeps a near-miss message next to
// the requirement it is a near miss OF.
//
// G2 (never a combined total across units) and G3 (the composer must RECORD
// the exclusion, even though enforcing it is the renderer's job) are two
// different obligations that happen to share one input — a number property's
// declared UnitProperty — and they are kept as two functions on purpose:
// gateG2UnitConsistency decides whether the REQUEST is allowed at all
// (refuses an explicit attempt to override or drop the pairing);
// setPartUnit is the separate act of RECORDING the pairing onto the emitted
// part. Folding them into one function would make it possible to validate
// the request correctly while forgetting to stamp the field the renderer
// depends on — which is exactly the silent-wrong-number failure this whole
// design exists to close off, one layer further in.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// createViewKindNames lists the eight view kinds in view-kinds-design's own
// §2.3 order — records.viewKindNames says the same thing but is unexported,
// and duplicating the eight literals (rather than importing an unexported
// symbol across a package boundary that does not exist) keeps this file
// honest about which package actually owns the enum: generated.ViewDefKind*.
var createViewKindNames = []string{
	string(generated.ViewDefKindTable),
	string(generated.ViewDefKindList),
	string(generated.ViewDefKindTiles),
	string(generated.ViewDefKindBoard),
	string(generated.ViewDefKindCalendar),
	string(generated.ViewDefKindSummary),
	string(generated.ViewDefKindTrend),
	string(generated.ViewDefKindBreakdown),
}

// createViewArgNames is create_view's own flat argument surface (design
// §6.1: "Arguments (flat, no nested definition)"). These are added to the
// package-wide configureArgNames list (knowledge_configure.go), which is how
// unknownArgs refuses a misspelled or invented argument by name for every op
// in this tool, create_view included.
var createViewArgNames = []string{
	"kind", "filter", "number", "unit", "date", "image", "choice", "group_by", "columns", "sort", "limit",
}

// ---------------------------------------------------------------------------
// Argument parsing — the JSON-shaped tool-call arguments become typed
// bindings before any gate looks at them.
// ---------------------------------------------------------------------------

// createViewBindings is create_view's parsed, kind-agnostic argument set. Not
// every field is used by every kind — which ones a given kind reads is
// composePartsForKind's question, not this one's.
type createViewBindings struct {
	number string
	// unitGiven distinguishes "unit was not mentioned" from "unit was set to
	// the empty string", because G2 treats those two differently: an absent
	// `unit` lets the composer apply the schema's own pairing automatically,
	// while an EXPLICIT blank is a request to drop it — the shape "asks for a
	// combined total across units" takes when a number totals per unit.
	unit      string
	unitGiven bool
	date      string
	image     string
	choice    string
	groupBy   []string
	columns   []string

	filter any
	sort   any
	limit  any
	// limitGiven distinguishes "no limit argument" from "limit explicitly
	// null" — mirrors unitGiven for the same reason.
	limitGiven bool
}

// parseCreateViewBindings reads create_view's flat arguments into
// createViewBindings, refusing only what is malformed at the ARGUMENT level
// (wrong JSON shape). Whether a binding names a real, correctly-typed
// property is every gate function's job below, not this one's — this
// function never touches a schema.
func parseCreateViewBindings(args map[string]any) (createViewBindings, string) {
	var b createViewBindings
	b.number = strings.TrimSpace(stringArg(args["number"]))
	if raw, has := args["unit"]; has {
		b.unitGiven = true
		b.unit = strings.TrimSpace(stringArg(raw))
	}
	b.date = strings.TrimSpace(stringArg(args["date"]))
	b.image = strings.TrimSpace(stringArg(args["image"]))
	b.choice = strings.TrimSpace(stringArg(args["choice"]))

	groupBy, gerr := stringListArg(args["group_by"], "group_by")
	if gerr != "" {
		return b, gerr
	}
	b.groupBy = groupBy

	columns, cerr := stringListArg(args["columns"], "columns")
	if cerr != "" {
		return b, cerr
	}
	b.columns = columns

	b.filter = args["filter"]
	b.sort = args["sort"]
	if raw, has := args["limit"]; has {
		b.limit = raw
		b.limitGiven = true
	}
	return b, ""
}

// stringListArg coerces a create_view binding argument that may arrive as
// either a single string or a JSON array of strings — group_by's shape is
// one property for summary/trend and two for breakdown (design §6.1,
// gateG5Grouping), and `columns` is naturally a list. A blank string or a
// blank list element is refused rather than silently dropped: an author who
// wrote something meant it, per the same posture mergeDeclaredType takes for
// a declared-vs-implied conflict.
func stringListArg(raw any, argName string) ([]string, string) {
	switch v := raw.(type) {
	case nil:
		return nil, ""
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil, ""
		}
		return []string{s}, ""
	case []any:
		out := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Sprintf("'%s[%d]' must be a string, found %T", argName, i, item)
			}
			s = strings.TrimSpace(s)
			if s == "" {
				return nil, fmt.Sprintf("'%s[%d]' is blank", argName, i)
			}
			out = append(out, s)
		}
		return out, ""
	default:
		return nil, fmt.Sprintf("'%s' must be a string or a list of strings, found %T", argName, raw)
	}
}

// ---------------------------------------------------------------------------
// G1 — a kind is offered only when the collection has the properties it
// requires; a refusal names the missing property and any near-miss
// candidate.
// ---------------------------------------------------------------------------

// propertiesOfType lists a schema's own properties of exactly one declared
// type, in declaration order — the candidate list a G1 refusal names when the
// binding the agent gave was blank rather than wrong.
func propertiesOfType(schema *records.Schema, t records.PropertyType) []string {
	var out []string
	for _, name := range schema.PropertyNames() {
		if p, ok := schema.Property(name); ok && p.Type == t {
			out = append(out, name)
		}
	}
	return out
}

// gateG1RequireDate is G1 for `calendar` and the date half of `trend`: the
// bound property must exist and must be declared `date`.
func gateG1RequireDate(schema *records.Schema, kindLabel, propName string) (*records.Property, string) {
	if schema == nil {
		return nil, fmt.Sprintf(
			"kind=%s requires a declared record type with a date property; 'type' was not given", kindLabel)
	}
	if propName == "" {
		cands := propertiesOfType(schema, records.TypeDate)
		if len(cands) == 0 {
			return nil, fmt.Sprintf(
				"kind=%s needs a date property; record type %q declares none", kindLabel, schema.Type)
		}
		return nil, fmt.Sprintf(
			"kind=%s needs 'date' naming one of record type %q's date properties: %s",
			kindLabel, schema.Type, strings.Join(cands, ", "))
	}
	prop, ok := schema.Property(propName)
	if !ok {
		return nil, fmt.Sprintf("record type %q declares no property %q; declared: %s",
			schema.Type, propName, strings.Join(schema.PropertyNames(), ", "))
	}
	if prop.Type != records.TypeDate {
		msg := fmt.Sprintf("property %q is %s, not a date; kind=%s needs a date property",
			propName, prop.Type, kindLabel)
		if cands := propertiesOfType(schema, records.TypeDate); len(cands) > 0 {
			msg += fmt.Sprintf(" (candidates: %s)", strings.Join(cands, ", "))
		} else {
			msg += fmt.Sprintf("; record type %q declares no date property at all", schema.Type)
		}
		return nil, msg
	}
	return prop, ""
}

// gateG1RequireImage is G1 for `tiles`.
//
// D5 (design §9, ratified 2026-09-03, commit ac787a307 — BEFORE this file):
// records.PropertyTypes (schema.go) is the CLOSED eight-type set, and it has
// no image-capable type yet. Binding tiles to TypeText (option (a) in D5) was
// explicitly REJECTED — it would make tiles available on every vault and
// attach rendering behaviour to unvalidated strings. So this gate calls the
// ONE shared eligibility switch, view_kinds.go's ImageEligible, which is the
// exact function knowledge_describe's discovery block (RenderAvailableViews)
// also calls for the same question. ImageEligible returns false for every
// type today, so this refuses unconditionally, with the SAME wording
// (imageIneligibleReason) the discovery block gives — the two paths cannot
// disagree, because there is only the one function to disagree with. The day
// an image-capable property type lands in the records layer, flipping
// ImageEligible is the whole of what makes both paths agree that tiles is
// available — nothing here changes.
func gateG1RequireImage(schema *records.Schema, propName string) (*records.Property, string) {
	if schema == nil {
		return nil, fmt.Sprintf("kind=tiles requires a declared record type; 'type' was not given (%s)", imageIneligibleReason)
	}
	for _, name := range schema.PropertyNames() {
		p, ok := schema.Property(name)
		if !ok || !ImageEligible(p.Type) {
			continue
		}
		if propName != "" && propName != p.Name {
			continue
		}
		return p, ""
	}
	return nil, fmt.Sprintf("kind=tiles: %s", imageIneligibleReason)
}

// gateG1RequireChoice is G1 for `board`: the bound property must be an enum
// of AT MOST maxBoardEnumValues declared values (view_kinds.go — the SAME
// bound boardAvailability's discovery-path check reads, so a near-miss
// message here can never cite a different threshold than knowledge_describe
// does). The near-miss wording — "board needs an enum with ≤ 8 values;
// `status` has 26" — is design §3 G1's own worked example, and the bound in
// it is built from the constant rather than retyped, so a future change to
// maxBoardEnumValues moves this wording with it instead of leaving a second
// hardcoded "8" behind.
func gateG1RequireChoice(schema *records.Schema, propName string) (*records.Property, string) {
	if schema == nil {
		return nil, fmt.Sprintf("kind=board requires a declared record type with an enum property of at most %d values; 'type' was not given", maxBoardEnumValues)
	}
	if propName == "" {
		if eligible := boardEligibleEnums(schema); len(eligible) > 0 {
			return nil, fmt.Sprintf(
				"kind=board needs 'choice' naming one of record type %q's board-eligible enum properties: %s",
				schema.Type, strings.Join(eligible, ", "))
		}
		if report := enumPropertiesReport(schema); len(report) > 0 {
			return nil, fmt.Sprintf("board needs an enum with ≤ %d values; %s", maxBoardEnumValues, strings.Join(report, ", "))
		}
		return nil, fmt.Sprintf(
			"board needs an enum with ≤ %d values; record type %q declares no enum property", maxBoardEnumValues, schema.Type)
	}
	prop, ok := schema.Property(propName)
	if !ok {
		return nil, fmt.Sprintf("record type %q declares no property %q; declared: %s",
			schema.Type, propName, strings.Join(schema.PropertyNames(), ", "))
	}
	if prop.Type != records.TypeEnum {
		return nil, fmt.Sprintf(
			"property %q is %s, not enum; board needs an enum with ≤ %d values", propName, prop.Type, maxBoardEnumValues)
	}
	if len(prop.Values) > maxBoardEnumValues {
		return nil, fmt.Sprintf("board needs an enum with ≤ %d values; %q has %d", maxBoardEnumValues, propName, len(prop.Values))
	}
	return prop, ""
}

// enumPropertiesReport lists every enum property with its value count, in
// declaration order — the raw material a "no board-eligible enum, but here
// is what you do have" refusal names.
func enumPropertiesReport(schema *records.Schema) []string {
	var out []string
	for _, name := range schema.PropertyNames() {
		if p, ok := schema.Property(name); ok && p.Type == records.TypeEnum {
			out = append(out, fmt.Sprintf("%q has %d", name, len(p.Values)))
		}
	}
	return out
}

// boardEligibleEnums lists enum properties with at most maxBoardEnumValues
// declared values — the set `choice` may legally name when the agent left it
// blank.
func boardEligibleEnums(schema *records.Schema) []string {
	var out []string
	for _, name := range schema.PropertyNames() {
		if p, ok := schema.Property(name); ok && p.Type == records.TypeEnum && len(p.Values) <= maxBoardEnumValues {
			out = append(out, name)
		}
	}
	return out
}

// gateG1RequireGroupProperty is G1 for a `group_by` entry on `summary`,
// `trend` and `breakdown` (design's "groupable" requirement): the property
// must exist. No type is excluded — the design's own §2.2 table requires
// only "two groupable properties" for breakdown, naming no closed set of
// eligible types, unlike board's enum-only requirement or tiles' text-only
// one.
func gateG1RequireGroupProperty(schema *records.Schema, kindLabel, propName string) (*records.Property, string) {
	if schema == nil {
		return nil, fmt.Sprintf("kind=%s requires a declared record type; 'type' was not given", kindLabel)
	}
	if propName == "" {
		return nil, fmt.Sprintf("kind=%s needs 'group_by' naming a property to group by", kindLabel)
	}
	prop, ok := schema.Property(propName)
	if !ok {
		return nil, fmt.Sprintf("record type %q declares no property %q; declared: %s",
			schema.Type, propName, strings.Join(schema.PropertyNames(), ", "))
	}
	return prop, ""
}

// ---------------------------------------------------------------------------
// G4 — text is never accepted as a number binding, even when its values
// parse as numbers.
// ---------------------------------------------------------------------------

// gateG4NumberBinding is G1's "required property" check AND G4's "never
// text" refusal for the one binding both apply to: `number`. It is the sole
// entry point every number-bearing kind (summary, trend, breakdown) calls,
// so the two rules can never drift apart between kinds.
//
// edit_record_type is named as the remedy because it is the ONLY property-
// conversion path this tool has (design §3 G4: "the refusal offers the
// existing property-conversion path") — there is no dedicated "convert this
// property's type" operation, and inventing a second way to say the same
// thing edit_record_type already says would be exactly the two-authorities
// problem UnitProperty's own doc comment warns against, one layer over.
func gateG4NumberBinding(schema *records.Schema, propName string) (*records.Property, string) {
	if schema == nil {
		return nil, "'number' requires a declared record type; 'type' was not given"
	}
	if propName == "" {
		cands := append(propertiesOfType(schema, records.TypeInteger), propertiesOfType(schema, records.TypeDecimal)...)
		sort.Strings(cands)
		if len(cands) == 0 {
			return nil, fmt.Sprintf("'number' is required; record type %q declares no integer or decimal property", schema.Type)
		}
		return nil, fmt.Sprintf("'number' is required; candidates on %q: %s", schema.Type, strings.Join(cands, ", "))
	}
	prop, ok := schema.Property(propName)
	if !ok {
		return nil, fmt.Sprintf("record type %q declares no property %q; declared: %s",
			schema.Type, propName, strings.Join(schema.PropertyNames(), ", "))
	}
	if prop.Type == records.TypeInteger || prop.Type == records.TypeDecimal {
		return prop, ""
	}
	if prop.Type == records.TypeText {
		return nil, fmt.Sprintf(
			"property %q is text, not a number, even though its values may look numeric; a text property is never "+
				"accepted as a number binding (design §3 G4) — use knowledge_configure op=edit_record_type to declare "+
				"%q as integer or decimal first", propName, propName)
	}
	return nil, fmt.Sprintf("property %q is %s, not a number; 'number' must name an integer or decimal property",
		propName, prop.Type)
}

// ---------------------------------------------------------------------------
// G2 — a number-with-unit totals once per unit value, never combined; an
// explicit request to override or drop the pairing is refused.
// ---------------------------------------------------------------------------

// gateG2UnitConsistency decides whether the REQUEST is legal — never whether
// it is recorded, which is setPartUnit's separate job (see this file's own
// header for why the two are kept apart).
//
// Three outcomes:
//
//   - the property declares no unit_property, and `unit` was not given (or
//     was given empty): "" ("no unit applies") — the ordinary case.
//   - the property declares no unit_property, but `unit` WAS given something:
//     refused — there is no pairing for the argument to name.
//   - the property declares a unit_property, and `unit` is either absent or
//     names the SAME property: the declared pairing, applied automatically.
//   - the property declares a unit_property, and `unit` names something ELSE
//     (including an explicit blank): refused — this is "a request that
//     explicitly asks for a cross-unit total" (design §3 G2), because the
//     only way to ask for one through this argument is to try to detach the
//     number from its declared unit.
func gateG2UnitConsistency(prop *records.Property, b createViewBindings) (unit, refusal string) {
	if prop.UnitProperty == "" {
		if b.unitGiven && b.unit != "" {
			return "", fmt.Sprintf(
				"property %q declares no unit_property; 'unit' cannot be supplied for it — its total is already one combined figure",
				prop.Name)
		}
		return "", ""
	}
	if b.unitGiven && b.unit != prop.UnitProperty {
		return "", fmt.Sprintf(
			"property %q totals once per value of its declared unit %q (design §3 G2); 'unit' was set to %q, which "+
				"asks for one combined figure across units — refused, because a sum across units is a wrong number "+
				"that looks right. Omit 'unit' (the pairing is applied automatically) or set it to %q",
			prop.Name, prop.UnitProperty, orNone(b.unit), prop.UnitProperty)
	}
	return prop.UnitProperty, ""
}

func orNone(s string) string {
	if s == "" {
		return "(empty)"
	}
	return s
}

// setPartUnit is G3's composer-side half: RECORD, on the part, which
// property carries the unit — the design's own words are "the composer must
// record in the part … that unit-less rows are excluded from totals", and
// ViewPart.Unit (Phase 1) is that field. It is deliberately its own function,
// separate from gateG2UnitConsistency, so a caller can validate the request
// correctly and still forget to stamp the part — which is a distinct,
// separately mutation-testable defect from validating the request wrong.
func setPartUnit(part map[string]any, unit string) {
	if unit != "" {
		part["unit"] = unit
	}
}

// ---------------------------------------------------------------------------
// G5 — grouping is one property; two grouping fields for a non-breakdown
// kind are refused, pointing at breakdown.
// ---------------------------------------------------------------------------

// gateG5Grouping checks group_by's SHAPE against the kind before any
// property name is looked up — a malformed grouping request is refused in
// one vocabulary regardless of which kind receives it, rather than as a
// side effect of some other gate tripping first.
func gateG5Grouping(kind generated.ViewDefKind, groupBy []string) string {
	switch kind {
	case generated.ViewDefKindBreakdown:
		if len(groupBy) != 2 {
			return fmt.Sprintf("breakdown needs exactly two grouping properties (group_by); got %d", len(groupBy))
		}
		if groupBy[0] == groupBy[1] {
			return fmt.Sprintf("breakdown needs two DIFFERENT grouping properties; both were %q", groupBy[0])
		}
		return ""
	case generated.ViewDefKindSummary, generated.ViewDefKindTrend:
		if len(groupBy) > 1 {
			return fmt.Sprintf(
				"kind=%s groups by at most one property (Obsidian parity, design §3 G5); got %d (%s) — "+
					"two-way grouping is what kind=breakdown is for",
				string(kind), len(groupBy), strings.Join(groupBy, ", "))
		}
		return ""
	default:
		if len(groupBy) > 0 {
			return fmt.Sprintf(
				"kind=%s does not use grouping; omit group_by (it applies to summary, trend and breakdown)", string(kind))
		}
		return ""
	}
}

// ---------------------------------------------------------------------------
// The composer itself — one function per kind, matching design §2.3's table
// column for column.
// ---------------------------------------------------------------------------

// composePartsForKind is G6's other half: it returns EITHER a complete part
// stack OR a refusal, never a partial one — every gate above runs to
// completion (or short-circuits on its own refusal) before this function
// returns, and the caller (execCreateView) writes nothing until this
// function returns no refusal at all.
func composePartsForKind(kind generated.ViewDefKind, schema *records.Schema, b createViewBindings) ([]map[string]any, string) {
	if g5 := gateG5Grouping(kind, b.groupBy); g5 != "" {
		return nil, g5
	}

	switch kind {
	case generated.ViewDefKindTable:
		return []map[string]any{{"part": "table"}}, ""

	case generated.ViewDefKindList:
		return []map[string]any{{"part": "list"}}, ""

	case generated.ViewDefKindTiles:
		prop, refusal := gateG1RequireImage(schema, b.image)
		if refusal != "" {
			return nil, refusal
		}
		return []map[string]any{{"part": "tiles", "image": prop.Name}}, ""

	case generated.ViewDefKindBoard:
		prop, refusal := gateG1RequireChoice(schema, b.choice)
		if refusal != "" {
			return nil, refusal
		}
		return []map[string]any{{"part": "columns", "choice": prop.Name}}, ""

	case generated.ViewDefKindCalendar:
		prop, refusal := gateG1RequireDate(schema, "calendar", b.date)
		if refusal != "" {
			return nil, refusal
		}
		return []map[string]any{{"part": "calendar", "date": prop.Name}}, ""

	case generated.ViewDefKindSummary:
		numProp, refusal := gateG4NumberBinding(schema, b.number)
		if refusal != "" {
			return nil, refusal
		}
		unit, refusal := gateG2UnitConsistency(numProp, b)
		if refusal != "" {
			return nil, refusal
		}
		if len(b.groupBy) == 1 {
			if _, refusal := gateG1RequireGroupProperty(schema, "summary", b.groupBy[0]); refusal != "" {
				return nil, refusal
			}
		}

		figures := map[string]any{"part": "figures", "number": numProp.Name, "aggregate": "sum"}
		setPartUnit(figures, unit)

		table := map[string]any{"part": "table"}
		setPartUnit(table, unit)
		if len(b.groupBy) == 1 {
			table["grouping"] = []map[string]any{{"property": b.groupBy[0]}}
			table["subtotals"] = map[string]any{numProp.Name: "sum"}
		}
		return []map[string]any{figures, table}, ""

	case generated.ViewDefKindTrend:
		dateProp, refusal := gateG1RequireDate(schema, "trend", b.date)
		if refusal != "" {
			return nil, refusal
		}
		numProp, refusal := gateG4NumberBinding(schema, b.number)
		if refusal != "" {
			return nil, refusal
		}
		unit, refusal := gateG2UnitConsistency(numProp, b)
		if refusal != "" {
			return nil, refusal
		}
		if len(b.groupBy) == 1 {
			if _, refusal := gateG1RequireGroupProperty(schema, "trend", b.groupBy[0]); refusal != "" {
				return nil, refusal
			}
		}

		figures := map[string]any{"part": "figures", "number": numProp.Name, "aggregate": "sum"}
		setPartUnit(figures, unit)

		chart := map[string]any{"part": "chart", "date": dateProp.Name, "number": numProp.Name}
		setPartUnit(chart, unit)

		table := map[string]any{"part": "table"}
		if len(b.groupBy) == 1 {
			table["grouping"] = []map[string]any{{"property": b.groupBy[0]}}
		}
		return []map[string]any{figures, chart, table}, ""

	case generated.ViewDefKindBreakdown:
		numProp, refusal := gateG4NumberBinding(schema, b.number)
		if refusal != "" {
			return nil, refusal
		}
		unit, refusal := gateG2UnitConsistency(numProp, b)
		if refusal != "" {
			return nil, refusal
		}
		group1, refusal := gateG1RequireGroupProperty(schema, "breakdown", b.groupBy[0])
		if refusal != "" {
			return nil, refusal
		}
		group2, refusal := gateG1RequireGroupProperty(schema, "breakdown", b.groupBy[1])
		if refusal != "" {
			return nil, refusal
		}

		figures := map[string]any{"part": "figures", "number": numProp.Name, "aggregate": "sum"}
		setPartUnit(figures, unit)

		crosstab := map[string]any{
			"part":      "crosstab",
			"number":    numProp.Name,
			"aggregate": "sum",
			"grouping": []map[string]any{
				{"property": group1.Name},
				{"property": group2.Name},
			},
		}
		setPartUnit(crosstab, unit)
		return []map[string]any{figures, crosstab}, ""

	default:
		// Unreachable on the normal path — execCreateView checks
		// generated.ViewDefKind.Valid() before this function is ever called —
		// but a refusal here rather than a panic is cheap insurance against a
		// future caller that skips that check.
		return nil, fmt.Sprintf("kind %q is not one of the eight declared view kinds; permitted: %s",
			string(kind), strings.Join(createViewKindNames, ", "))
	}
}

// ---------------------------------------------------------------------------
// The tool entry point
// ---------------------------------------------------------------------------

// execCreateView is knowledge_configure op=create_view.
//
// G6 IN ONE SENTENCE: every gate above runs, and composePartsForKind either
// returns a complete stack or this function returns the refusal — the write
// call at the bottom is reached ONLY after that, and after ParseView and
// ValidateViewAgainstSchemas ALSO accept the assembled result. There is no
// path from "a gate objected" to "a file changed".
func (t *ConfigureTool) execCreateView(target mutationTarget, args map[string]any) *tools.ToolResult {
	root := target.collection.Root()
	viewName := strings.TrimSpace(stringArg(args["view"]))
	if viewName == "" {
		return t.deps.refuse(authorOpConfigure, target, nil, "'view' is required for create_view")
	}

	kindStr := strings.TrimSpace(stringArg(args["kind"]))
	if kindStr == "" {
		return t.deps.refuse(authorOpConfigure, target, nil,
			"'kind' is required for create_view; one of "+strings.Join(createViewKindNames, ", "))
	}
	kind := generated.ViewDefKind(kindStr)
	if !kind.Valid() {
		return t.deps.refuse(authorOpConfigure, target, nil, fmt.Sprintf(
			"kind %q is not one of the eight declared view kinds; permitted: %s",
			kindStr, strings.Join(createViewKindNames, ", ")))
	}

	typeName := strings.TrimSpace(stringArg(args["type"]))

	schemas, _, lerr := records.LoadSchemas(root)
	if lerr != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "create_view: loading record schemas: "+lerr.Error())
	}
	var schema *records.Schema
	if typeName != "" {
		sc, ok := schemas.Get(typeName)
		if !ok {
			return t.deps.refuse(authorOpConfigure, target, nil, fmt.Sprintf(
				"no record type %q is declared; declared types: %s", typeName, joinOrNone(schemas.Types())))
		}
		schema = sc
	}

	bindings, berr := parseCreateViewBindings(args)
	if berr != "" {
		return t.deps.refuse(authorOpConfigure, target, nil, "create_view: "+berr)
	}

	parts, refusal := composePartsForKind(kind, schema, bindings)
	if refusal != "" {
		// G6: nothing has been written yet. Refusing here is the whole of
		// G6's guarantee — see this function's own doc comment.
		return t.deps.refuse(authorOpConfigure, target, nil, "create_view: "+refusal)
	}

	defMap := map[string]any{
		"name":  viewName,
		"kind":  string(kind),
		"parts": parts,
	}
	if typeName != "" {
		defMap["type"] = typeName
	}
	if bindings.filter != nil {
		defMap["filter"] = bindings.filter
	}
	if len(bindings.columns) > 0 {
		defMap["properties"] = bindings.columns
	}
	if bindings.sort != nil {
		defMap["sort"] = bindings.sort
	}
	if bindings.limitGiven {
		defMap["limit"] = bindings.limit
	}

	// From here on this is deliberately the SAME sequence execWriteView runs
	// (knowledge_configure.go), so schema validation, the tier-1 lock, the
	// audit record and "will knowledge_find actually serve this" all stay
	// identical between the composer path and the raw escape hatch — see
	// this file's own header.
	yamlBytes, merr := marshalDefinition(defMap)
	if merr != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "create_view: "+merr.Error())
	}
	viewPath := filepath.Join(records.ViewsDir(root), viewName+".yaml")
	parsed, rej := records.ParseView(viewPath, yamlBytes)
	if rej != nil {
		// Reaching a REJECTION here (rather than a gate refusal above) means
		// this composer assembled a shape ParseView itself does not accept —
		// a bug in this file, not an agent's bad request. G6 still holds:
		// nothing is written.
		return t.deps.refuse(authorOpConfigure, target, nil, "create_view: "+rej.Reason)
	}
	if rej := records.ValidateViewAgainstSchemas(parsed, schemas); rej != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "create_view: "+rej.Reason)
	}

	if werr := overwriteControlPlaneFile(target, viewPath, yamlBytes); werr != nil {
		return t.deps.refuse(authorOpConfigure, target, []string{relControlPlanePath(root, viewPath)}, "create_view: "+werr.Error())
	}

	t.deps.record(AuthorAuditRecord{
		Operation: authorOpConfigure, Outcome: AuthorOutcomeApplied,
		AgentID: target.agentID, WorkspaceID: target.workspaceID,
		Collection: target.col.Name, Root: root,
		Paths: []string{relControlPlanePath(root, viewPath)}, At: t.deps.now(),
	})

	viewType := ""
	if parsed.Def.Type != nil {
		viewType = *parsed.Def.Type
	}
	return tools.NewToolResult(RenderConfigure(ConfigureData{
		Op: opCreateView, Name: viewName, Path: relControlPlanePath(root, viewPath),
		ViewType: viewType, Kind: string(kind),
		PartsSummary: summarizeParts(parts),
		Unservable:   t.serveRefusalFor(root, schemas, viewName),
	}))
}

// ---------------------------------------------------------------------------
// Reading back what was built — design §6.1: "the answer with … the
// assembled stack so the agent can read back what it built."
// ---------------------------------------------------------------------------

func summarizeParts(parts []map[string]any) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, summarizePart(p))
	}
	return out
}

func summarizePart(part map[string]any) string {
	name, _ := part["part"].(string)
	keys := make([]string, 0, len(part))
	for k := range part {
		if k == "part" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return name
	}
	kv := make([]string, 0, len(keys))
	for _, k := range keys {
		kv = append(kv, fmt.Sprintf("%s=%s", k, formatPartValue(part[k])))
	}
	return fmt.Sprintf("%s(%s)", name, strings.Join(kv, ", "))
}

func formatPartValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []string:
		return strings.Join(t, ", ")
	case []map[string]any:
		names := make([]string, 0, len(t))
		for _, g := range t {
			if p, ok := g["property"].(string); ok {
				names = append(names, p)
			}
		}
		return strings.Join(names, "+")
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s:%v", k, t[k]))
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprintf("%v", v)
	}
}
