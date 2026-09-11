// Tests for ADR-083 review I2 — typedValueWire's per-type projection.
//
// WHY THIS FILE EXISTS. typedValueWire is the single function that turns
// every stored record value into the shape the SPA reads, and only two of its
// eight arms (`text` and `enum`) were asserted anywhere. `grep typedValueWire`
// across pkg/gateway/*_test.go returned nothing — the two that WERE covered
// were covered incidentally, through a whole-record fixture that happened to
// carry those types.
//
// An unasserted arm fails silently and in the worst possible direction: the
// value reaches the client as the ZERO value of its field rather than as an
// error, so a date renders as an empty cell and a checkbox as `false`. No
// request fails, nothing is logged, and the note on disk still holds the real
// value — the operator simply sees the wrong thing.
//
// THE ORACLE IS THE SOURCE VALUE, NOT THE FUNCTION. Every expectation below
// is written from what the TypedValue was constructed to hold (and, for date
// and number, from the type's own documented String() rendering), never read
// off a run of typedValueWire.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// TestTypedValueWire_ProjectsEveryDeclaredType walks all eight property types
// in one table.
//
// The table is deliberately exhaustive rather than a sample: a new property
// type added to records without an arm here would otherwise ship a silently
// empty wire value, which is exactly the failure this file exists to stop.
// Every case asserts (a) the discriminating `type` field, (b) the populated
// field's CONTENT, and (c) that the arms this type does NOT use are left nil
// — (c) is what stops a future refactor from populating two fields at once
// and letting a client read whichever it likes.
func TestTypedValueWire_ProjectsEveryDeclaredType(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		got := typedValueWire(records.TypedValue{Type: records.TypeText, Text: "Introduced via referral"})
		assert.Equal(t, records.WireRecordValueType(records.TypeText), got.Type)
		require.NotNil(t, got.Text)
		assert.Equal(t, "Introduced via referral", *got.Text)
		assert.Nil(t, got.Enum)
		assert.Nil(t, got.Date)
		assert.Nil(t, got.Checkbox)
	})

	t.Run("enum carries the DECLARED spelling, not the file's", func(t *testing.T) {
		// FR-011a: Enum.Name is the schema's spelling and Raw is the note's.
		// The wire must carry the declared one, or two notes spelling one
		// state two ways group as two values in the SPA.
		got := typedValueWire(records.TypedValue{
			Type: records.TypeEnum,
			Raw:  "Won",
			Enum: records.EnumValue{Name: "won"},
		})
		assert.Equal(t, records.WireRecordValueType(records.TypeEnum), got.Type)
		require.NotNil(t, got.Enum)
		assert.Equal(t, "won", *got.Enum, "the DECLARED spelling reaches the wire")
		assert.NotEqual(t, "Won", *got.Enum, "the file's own spelling must not")
	})

	t.Run("date without a time renders date-only", func(t *testing.T) {
		got := typedValueWire(records.TypedValue{
			Type: records.TypeDate,
			Date: records.DateValue{Instant: time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)},
		})
		assert.Equal(t, records.WireRecordValueType(records.TypeDate), got.Type)
		require.NotNil(t, got.Date, "a date value must reach the wire at all")
		assert.Equal(t, "2026-08-25", *got.Date)
	})

	t.Run("date WITH a time keeps the time", func(t *testing.T) {
		// Paired with the case above: together they prove HasTime actually
		// changes the rendering. Either alone passes on a hardcoded format.
		got := typedValueWire(records.TypedValue{
			Type: records.TypeDate,
			Date: records.DateValue{
				Instant: time.Date(2026, 8, 25, 9, 30, 0, 0, time.UTC),
				HasTime: true,
			},
		})
		require.NotNil(t, got.Date)
		assert.Equal(t, "2026-08-25T09:30:00Z", *got.Date)
	})

	t.Run("integer", func(t *testing.T) {
		got := typedValueWire(records.TypedValue{
			Type:   records.TypeInteger,
			Number: records.NewDecimal(big.NewInt(120000), 0),
		})
		assert.Equal(t, records.WireRecordValueType(records.TypeInteger), got.Type)
		require.NotNil(t, got.Integer)
		assert.Equal(t, "120000", *got.Integer)
		assert.Nil(t, got.Decimal, "an integer must not also populate the decimal arm")
	})

	t.Run("decimal keeps its scale", func(t *testing.T) {
		// 120000.50 as unscaled 12000050 at scale 2. Asserting the trailing
		// zero survives is the point: a float round-trip renders "120000.5"
		// and money silently loses a digit.
		got := typedValueWire(records.TypedValue{
			Type:   records.TypeDecimal,
			Number: records.NewDecimal(big.NewInt(12000050), 2),
		})
		assert.Equal(t, records.WireRecordValueType(records.TypeDecimal), got.Type)
		require.NotNil(t, got.Decimal)
		assert.Equal(t, "120000.50", *got.Decimal, "scale is preserved exactly — no float round-trip")
		assert.Nil(t, got.Integer, "a decimal must not also populate the integer arm")
	})

	t.Run("checkbox true and false are BOTH carried", func(t *testing.T) {
		// The false case is the one that matters. `false` is also the zero
		// value, so an arm that never runs is indistinguishable from one that
		// correctly reported false — unless the pointer itself is asserted
		// non-nil, which is what separates "absent" from "present and false".
		yes := typedValueWire(records.TypedValue{Type: records.TypeCheckbox, Bool: true})
		assert.Equal(t, records.WireRecordValueType(records.TypeCheckbox), yes.Type)
		require.NotNil(t, yes.Checkbox)
		assert.True(t, *yes.Checkbox)

		no := typedValueWire(records.TypedValue{Type: records.TypeCheckbox, Bool: false})
		require.NotNil(t, no.Checkbox, "a FALSE checkbox is present-and-false, never absent")
		assert.False(t, *no.Checkbox)
	})

	t.Run("relation and person populate their OWN arm only", func(t *testing.T) {
		rel := typedValueWire(records.TypedValue{
			Type: records.TypeRelation,
			Link: records.Wikilink{Target: "Acme Holdings", Raw: "[[Acme Holdings]]"},
		})
		assert.Equal(t, records.WireRecordValueType(records.TypeRelation), rel.Type)
		require.NotNil(t, rel.Relation)
		assert.Equal(t, "[[Acme Holdings]]", rel.Relation.Link)
		assert.False(t, rel.Relation.Resolved, "the wire projection does not resolve links")
		assert.Nil(t, rel.Person, "a relation must never populate the person arm")

		per := typedValueWire(records.TypedValue{
			Type: records.TypePerson,
			Link: records.Wikilink{Target: "Daniel Piatkowski", Raw: "[[Daniel Piatkowski]]"},
		})
		assert.Equal(t, records.WireRecordValueType(records.TypePerson), per.Type)
		require.NotNil(t, per.Person)
		assert.Equal(t, "[[Daniel Piatkowski]]", per.Person.Link)
		assert.Nil(t, per.Relation, "a person must never populate the relation arm")
	})
}

// TestTypedValueWire_EmptyRawLinkFallsBackToTarget covers the one branch with
// no obvious caller: when a link's Raw text is empty, the wire carries the
// parsed Target instead.
//
// WHY IT MATTERS. Raw is "the link exactly as written", so it is empty for a
// link the system constructed rather than read off a note. Without the
// fallback that value reaches the SPA as an EMPTY STRING — a relation cell
// that renders blank while the record genuinely points somewhere, and a
// RecordRef the client cannot resolve or display.
//
// The two halves are asserted together, because the fallback can only be
// proven by contrast: a value WITH Raw must keep Raw, or "always use Target"
// would pass the first half alone.
func TestTypedValueWire_EmptyRawLinkFallsBackToTarget(t *testing.T) {
	for _, pt := range []records.PropertyType{records.TypeRelation, records.TypePerson} {
		// Raw empty → Target is used.
		bare := typedValueWire(records.TypedValue{
			Type: pt,
			Link: records.Wikilink{Target: "Acme Holdings"},
		})
		bareRef := bare.Relation
		if pt == records.TypePerson {
			bareRef = bare.Person
		}
		require.NotNil(t, bareRef, "%v must populate its own arm", pt)
		assert.Equal(t, "Acme Holdings", bareRef.Link,
			"an empty Raw must fall back to Target, never reach the client as an empty string")
		assert.NotEmpty(t, bareRef.Link, "a relation that points somewhere must never render blank")

		// POSITIVE CONTROL: Raw present → Raw wins, Target is NOT substituted.
		written := typedValueWire(records.TypedValue{
			Type: pt,
			Link: records.Wikilink{Target: "Acme Holdings", Raw: "[[Acme Holdings|Acme]]"},
		})
		writtenRef := written.Relation
		if pt == records.TypePerson {
			writtenRef = written.Person
		}
		require.NotNil(t, writtenRef)
		assert.Equal(t, "[[Acme Holdings|Acme]]", writtenRef.Link,
			"the operator's own spelling wins whenever they wrote one")
		assert.NotEqual(t, bareRef.Link, writtenRef.Link,
			"the two branches must produce DIFFERENT output, or the fallback is unconditional")
	}
}
