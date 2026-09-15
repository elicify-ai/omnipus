// Omnipus — tests for view_kinds.go: the shared availability rulebook
// view-kinds-design-2026-09-03 §2.3 / §6.2 states, and D5 (design §9) amends
// for `tiles`.
//
// ORACLE: every expected value below is read off the design document — §2.3's
// table for which properties satisfy which kind, and D5 for tiles' permanent
// refusal — never off view_kinds.go's own source. Where the design leaves a
// tie-break unstated (which of several qualifying properties a binding
// names), the fixtures below declare exactly one property of each qualifying
// type, sidestepping the tie-break rather than asserting a choice the design
// does not make.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// mustParseTestSchema parses a schema fixture the same way the vault loader
// does (records.ParseSchema), so a test fixture that would be rejected by the
// real loader fails here rather than silently exercising a shape the product
// can never actually load.
func mustParseTestSchema(t *testing.T, yamlBody string) *records.Schema {
	t.Helper()
	sc, rej := records.ParseSchema("schema.yaml", []byte(yamlBody))
	if rej != nil {
		t.Fatalf("fixture schema was rejected (fixture bug, not the code under test): %s", rej.Reason)
	}
	return sc
}

// availabilityFor is a small lookup helper so a test can assert about one
// kind without depending on ViewKindOrder's position for it.
func availabilityFor(t *testing.T, all []ViewKindAvailability, kind string) ViewKindAvailability {
	t.Helper()
	for _, a := range all {
		if a.Kind == kind {
			return a
		}
	}
	t.Fatalf("ViewKindAvailabilityFor did not report kind %q at all; got: %+v", kind, all)
	return ViewKindAvailability{}
}

// ---------------------------------------------------------------------------
// D5's own acceptance shape: "7-of-8 available plus tiles-unavailable-with-
// this-exact-reason" — a record type declaring a number+unit, a date, an
// enum small enough for `board`, and other properties enough to group by.
// ---------------------------------------------------------------------------

const viewKindsKitchenSinkSchema = `
schema_version: 1
type: invoice
properties:
  status:   { type: enum, values: [draft, sent, paid, overdue] }
  amount:   { type: decimal, unit_property: currency }
  currency: { type: enum, values: [USD, EUR] }
  due_date: { type: date }
  client:   { type: text }
`

func TestViewKindAvailability_KitchenSinkSchema_SevenOfEightAvailable(t *testing.T) {
	sc := mustParseTestSchema(t, viewKindsKitchenSinkSchema)
	all := ViewKindAvailabilityFor(sc)

	if len(all) != len(ViewKindOrder) {
		t.Fatalf("expected all %d kinds reported, got %d: %+v", len(ViewKindOrder), len(all), all)
	}

	wantAvailable := map[string]string{
		// kind -> a substring its Bindings must contain, read off §2.3's
		// requirement column plus the record type's own declared names.
		ViewKindTable:     "",
		ViewKindList:      "",
		ViewKindBoard:     "choice: status",
		ViewKindCalendar:  "date: due_date",
		ViewKindSummary:   "number: amount",
		ViewKindTrend:     "date: due_date",
		ViewKindBreakdown: "number: amount",
	}
	for kind, wantSubstr := range wantAvailable {
		got := availabilityFor(t, all, kind)
		if !got.Available {
			t.Errorf("%s: design §2.3 — this schema declares what %s requires, but it was refused: %q",
				kind, kind, got.Missing)
			continue
		}
		if wantSubstr != "" && !strings.Contains(got.Bindings, wantSubstr) {
			t.Errorf("%s: expected Bindings to name %q, got %q", kind, wantSubstr, got.Bindings)
		}
	}

	// summary/trend/breakdown's number carries a companion unit (design §5) —
	// G2 says a total is per-unit, never combined, and the discovery block's
	// whole point is showing the agent the pairing BEFORE it writes a view
	// that would otherwise total across currencies. "unit: currency" must
	// appear wherever "number: amount" does.
	for _, kind := range []string{ViewKindSummary, ViewKindTrend, ViewKindBreakdown} {
		got := availabilityFor(t, all, kind)
		if !strings.Contains(got.Bindings, "unit: currency") {
			t.Errorf("%s: amount declares unit_property: currency; Bindings must name it, got %q", kind, got.Bindings)
		}
	}

	// breakdown additionally needs two group dimensions distinct from the
	// number it totals (design §2.3: "two groupable properties + a number").
	breakdown := availabilityFor(t, all, ViewKindBreakdown)
	if strings.Count(breakdown.Bindings, "group:") != 2 {
		t.Errorf("breakdown: expected exactly two group bindings, got %q", breakdown.Bindings)
	}

	// D5: tiles is the one kind that MUST be unavailable, and for exactly the
	// reason the design's ruling states — never silently bound to `client`
	// (a `text` property) even though one is declared.
	tiles := availabilityFor(t, all, ViewKindTiles)
	if tiles.Available {
		t.Errorf("D5: tiles must never be available (no image-capable property type exists) — got Available with Bindings %q", tiles.Bindings)
	}
	if tiles.Missing != imageIneligibleReason {
		t.Errorf("D5: tiles' refusal reason must be the D5 wording; got %q, want %q", tiles.Missing, imageIneligibleReason)
	}
}

// ---------------------------------------------------------------------------
// A bare-text record type: table/list only, the other six each naming what
// they lack.
// ---------------------------------------------------------------------------

const viewKindsBareTextSchema = `
schema_version: 1
type: memo
properties:
  title: { type: text }
  notes: { type: text, many: true }
`

func TestViewKindAvailability_BareTextSchema_OnlyTableAndListAvailable(t *testing.T) {
	sc := mustParseTestSchema(t, viewKindsBareTextSchema)
	all := ViewKindAvailabilityFor(sc)

	for _, kind := range []string{ViewKindTable, ViewKindList} {
		got := availabilityFor(t, all, kind)
		if !got.Available {
			t.Errorf("%s: design §2.3 requires nothing (\"anything\"); a bare-text type must qualify, got refused: %q", kind, got.Missing)
		}
	}

	// Every other kind must be UNAVAILABLE, and each must NAME what specific
	// requirement this schema lacks — never a bare "no" with nothing to act
	// on (design §3 G1: "a refusal names the missing property").
	for _, kind := range []string{ViewKindTiles, ViewKindBoard, ViewKindCalendar, ViewKindSummary, ViewKindTrend, ViewKindBreakdown} {
		got := availabilityFor(t, all, kind)
		if got.Available {
			t.Errorf("%s: a schema with only `text` properties declares none of what §2.3 requires; got Available with Bindings %q", kind, got.Bindings)
			continue
		}
		if strings.TrimSpace(got.Missing) == "" {
			t.Errorf("%s: refused with no reason at all — G1 requires the missing requirement to be named", kind)
		}
	}

	// Spot-check that each reason actually names its OWN requirement, not a
	// copy-pasted generic string shared across kinds.
	board := availabilityFor(t, all, ViewKindBoard)
	if !strings.Contains(board.Missing, "enum") {
		t.Errorf("board's refusal must name the missing enum requirement; got %q", board.Missing)
	}
	calendar := availabilityFor(t, all, ViewKindCalendar)
	if !strings.Contains(calendar.Missing, "date") {
		t.Errorf("calendar's refusal must name the missing date requirement; got %q", calendar.Missing)
	}
	summary := availabilityFor(t, all, ViewKindSummary)
	if !strings.Contains(summary.Missing, "number") {
		t.Errorf("summary's refusal must name the missing number requirement; got %q", summary.Missing)
	}
	breakdown := availabilityFor(t, all, ViewKindBreakdown)
	if !strings.Contains(breakdown.Missing, "number") {
		t.Errorf("breakdown's refusal must name the missing number requirement (design lists it as breakdown's own gate); got %q", breakdown.Missing)
	}
}

// ---------------------------------------------------------------------------
// board's own bound: an enum with MORE than 8 declared values is a documented
// near-miss, not a silent "no enum at all" — design §6.2's own worked
// example: "board — NO (no enum with ≤ 8 values; `status` has 26)".
// ---------------------------------------------------------------------------

const viewKindsOversizedEnumSchema = `
schema_version: 1
type: ticket
properties:
  status: { type: enum, values: [new, triaged, assigned, in_progress, blocked, review, testing, staged, done] }
`

func TestViewKindAvailability_OversizedEnum_BoardUnavailableWithTheCountNamed(t *testing.T) {
	sc := mustParseTestSchema(t, viewKindsOversizedEnumSchema)
	all := ViewKindAvailabilityFor(sc)

	board := availabilityFor(t, all, ViewKindBoard)
	if board.Available {
		t.Fatalf("status declares 9 values, over board's ≤8 bound; must be unavailable, got Bindings %q", board.Bindings)
	}
	if !strings.Contains(board.Missing, "status") {
		t.Errorf("board's near-miss message must name the offending property; got %q", board.Missing)
	}
	if !strings.Contains(board.Missing, "9") {
		t.Errorf("board's near-miss message must state the actual count (9); got %q", board.Missing)
	}
	if !strings.Contains(board.Missing, "8") {
		t.Errorf("board's near-miss message must state the bound (8); got %q", board.Missing)
	}
}

// An enum with EXACTLY 8 values is the boundary — design says "≤ 8", so 8
// itself must qualify. A gate that silently used `< 8` would refuse the
// boundary case and nothing above would catch it.
const viewKindsExactlyEightEnumSchema = `
schema_version: 1
type: ticket
properties:
  status: { type: enum, values: [a, b, c, d, e, f, g, h] }
`

func TestViewKindAvailability_EnumWithExactlyEightValues_BoardIsAvailable(t *testing.T) {
	sc := mustParseTestSchema(t, viewKindsExactlyEightEnumSchema)
	all := ViewKindAvailabilityFor(sc)
	board := availabilityFor(t, all, ViewKindBoard)
	if !board.Available {
		t.Fatalf("design §2.3 says \"≤ 8 values\" — an enum with exactly 8 must qualify; got refused: %q", board.Missing)
	}
	if !strings.Contains(board.Bindings, "status") {
		t.Errorf("board's binding must name the qualifying enum; got %q", board.Bindings)
	}
}

// ---------------------------------------------------------------------------
// ImageEligible / D5: no property type is image-capable yet, for every one
// of the eight declared types (schema.go's records.PropertyTypes) — not just
// the ones this file happens to exercise elsewhere.
// ---------------------------------------------------------------------------

func TestImageEligible_D5_NoPropertyTypeQualifiesYet(t *testing.T) {
	for _, pt := range records.PropertyTypes {
		if ImageEligible(pt) {
			t.Errorf("D5 rules tiles gated off entirely; ImageEligible(%q) must be false, was true", pt)
		}
	}
}

// ---------------------------------------------------------------------------
// RenderAvailableViews — the one line knowledge_describe adds per record
// type. Format asserted against design §6.2's own worked example, not
// reconstructed from the renderer.
// ---------------------------------------------------------------------------

func TestRenderAvailableViews_LineShapeMatchesTheDesignsWorkedExample(t *testing.T) {
	sc := mustParseTestSchema(t, viewKindsKitchenSinkSchema)
	line := RenderAvailableViews(sc)

	if !strings.HasPrefix(line, "views you can create here: ") {
		t.Fatalf("expected the design §6.2 header phrase; got: %q", line)
	}
	if strings.Contains(line, "\n") {
		t.Errorf("§6.2 renders one line, comma-joined — got a newline in: %q", line)
	}
	// Kinds render in §2.3's own table order (ViewKindOrder), so a reader
	// comparing this line against the design's table reads down both at once.
	lastIdx := -1
	for _, kind := range ViewKindOrder {
		i := strings.Index(line, kind)
		if i < 0 {
			t.Fatalf("kind %q missing from the rendered line: %q", kind, line)
		}
		if i < lastIdx {
			t.Errorf("kind %q rendered out of §2.3's table order in: %q", kind, line)
		}
		lastIdx = i
	}
	// An available kind with no binding (table/list) renders bare; one that
	// is unavailable renders "kind — NO (reason)" per the worked example.
	if !strings.Contains(line, "table") || !strings.Contains(line, "list") {
		t.Errorf("expected table and list to render bare (no requirement): %q", line)
	}
	if !strings.Contains(line, "tiles — NO (") {
		t.Errorf(`expected "tiles — NO (...)" per D5; got: %q`, line)
	}
}

// A response line built from this file must never contain JSON key syntax
// (FR-072, mirrored from knowledge_describe_test.go's own
// TestDescribe_ResponseIsNeverAJSONDocument) — this is the one function in
// this file whose output reaches the model's response text directly.
func TestRenderAvailableViews_NeverEmitsJSONKeySyntax(t *testing.T) {
	for _, body := range []string{viewKindsKitchenSinkSchema, viewKindsBareTextSchema, viewKindsOversizedEnumSchema} {
		sc := mustParseTestSchema(t, body)
		line := RenderAvailableViews(sc)
		if strings.Contains(line, `":`) {
			t.Errorf("AC-D3 (FR-072): rendered line contains JSON key syntax: %q", line)
		}
	}
}

// ---------------------------------------------------------------------------
// The compressed tool-description text for knowledge_configure's create_view
// (design §6.2's second requirement: "the same block appears in the tool's
// own schema description in compressed form ... because the tool description
// is what is in front of the agent at call time"). These two are exported,
// ready-to-paste strings for ConfigureTool.Description()/Parameters() — see
// this file's own header comment on why they are not wired in here.
// ---------------------------------------------------------------------------

func TestConfigureCreateViewDescriptionFragment_NamesOpAndAllEightKindsWithRequirements(t *testing.T) {
	frag := ConfigureCreateViewDescriptionFragment
	if !strings.Contains(frag, "create_view") {
		t.Fatalf("fragment does not name the op at all: %q", frag)
	}
	wantPerKind := map[string]string{
		ViewKindTable:     "any collection",
		ViewKindList:      "any collection",
		ViewKindTiles:     imageIneligibleReason,
		ViewKindBoard:     "an enum property with ≤ 8 values",
		ViewKindCalendar:  "a date property",
		ViewKindSummary:   "a number property",
		ViewKindTrend:     "a date property and a number property",
		ViewKindBreakdown: "two other properties and a number property",
	}
	for kind, requirement := range wantPerKind {
		if !strings.Contains(frag, kind) {
			t.Errorf("fragment does not name kind %q: %q", kind, frag)
		}
		if !strings.Contains(frag, requirement) {
			t.Errorf("fragment does not state %q's requirement (%q): %q", kind, requirement, frag)
		}
	}
}

func TestConfigureWriteViewSteerLine_PointsToCreateView(t *testing.T) {
	if !strings.Contains(ConfigureWriteViewSteerLine, "create_view") {
		t.Fatalf("write_view steer line never mentions create_view: %q", ConfigureWriteViewSteerLine)
	}
}
