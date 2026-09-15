// Omnipus — FR-143's pinned grammar, ADVANCED 2026-09-01 to carry the Bases
// reference's whole "Date type › Fields" table.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"sort"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WHAT THIS FILE IS FOR
//
// FR-143 pins the formula grammar to a DATED snapshot of Obsidian's Bases
// reference, and says in as many words that adopting a newer snapshot is a
// SPEC REVISION with its own diff, never a silent code change. Two agents in
// a row correctly REFUSED to add `.year` and `.month` on exactly those
// grounds, and both were right to: a grammar widened to make one view import
// is a grammar that means nothing by the following week.
//
// The pin was then advanced deliberately, on the founder's ruling: dated
// (2026-09-01), sourced (https://obsidian.md/help/bases/functions), written
// into FR-143 and ADR-068 §D24.3a, and adopting the Date field table WHOLE
// rather than the two rows that unblocked `Deals::Closing This Month`.
//
// THIS FILE IS THE WALL THE NEXT AGENT HITS. TestFormula_AccessorSurfaceIsPinned
// asserts the accessor set EXACTLY — not "contains", not "at least" — so any
// future addition, however obviously correct, fails a test that names the pin
// and the procedure. That is the point. If you are here because you added an
// accessor and this test went red: the test is not in your way, it is the
// requirement. Advance the pin in the spec and the ADR, then move this list.
//
// Every expected value below is derived BY HAND from the reference's own words
// (quoted at each assertion) and from the fixture clock, never read off the
// implementation.
// ---------------------------------------------------------------------------

// TestFormula_AccessorSurfaceIsPinned is the closed parenless-accessor set,
// written down where a diff can see it.
//
// The two halves are different claims and both matter. The first is that the
// set is EXACTLY these thirteen names — an accessor silently gained is the
// drift FR-143 exists to prevent. The second is that each date field is typed
// the way the reference's table types it: receiver `date`, result `number`,
// and a whole number rather than a scaled one.
func TestFormula_AccessorSurfaceIsPinned(t *testing.T) {
	// Transcribed from the two pinned sources, NOT from accessorFields.
	//
	//   Date type › Fields (functions reference, fetched 2026-09-01) — the
	//   seven singular fields, every one `number`.
	//
	//   Duration Type + List Functions (syntax reference, fetched 2026-08-30)
	//   — the five plural totals and `list.length`. Still pinned at the older
	//   fetch: see the note in accessorFields about what the 2026-09-01 fetch
	//   showed and why its duration restructuring was NOT adopted here.
	wantDateFields := []string{
		"year", "month", "day", "hour", "minute", "second", "millisecond",
	}
	wantOtherFields := []string{
		"days", "hours", "minutes", "seconds", "milliseconds", // duration totals
		"length", // list.length
	}

	t.Run("the accessor set is exactly the pinned snapshot", func(t *testing.T) {
		want := map[string]bool{}
		for _, n := range append(append([]string{}, wantDateFields...), wantOtherFields...) {
			want[n] = true
		}

		var missing, extra []string
		for n := range want {
			if _, ok := accessorFields[n]; !ok {
				missing = append(missing, n)
			}
		}
		for n := range accessorFields {
			if !want[n] {
				extra = append(extra, n)
			}
		}
		sort.Strings(missing)
		sort.Strings(extra)

		if len(missing) != 0 {
			t.Errorf("the pinned snapshot defines %v and the grammar does not accept them — "+
				"a documented field that refuses is a view the founder cannot import",
				missing)
		}
		if len(extra) != 0 {
			t.Errorf("the grammar accepts %v, which no pinned snapshot lists.\n"+
				"FR-143: the grammar is CLOSED, and adopting a newer snapshot is a SPEC REVISION "+
				"with its own diff — never a silent code change. If these are genuinely documented "+
				"upstream, advance the pin in FR-143 (docs/internal/specs/vault-records-spec-2026-08-25.md) "+
				"and ADR-068 §D24.3a with the fetch date, then add them here. Do not delete this check.",
				extra)
		}
	})

	t.Run("every date field is typed as the reference's table types it", func(t *testing.T) {
		// "| `date.year` | `number` | The year of the date |" — and the same
		// shape for all seven rows. Receiver `date`, result `number`.
		//
		// Scale 0 is the load-bearing half. A calendar component is a WHOLE
		// number: there is no such thing as month 3.5. Declaring the default
		// fractional scale instead would render `2019` as `2019.0000000000`
		// and, worse, would let a rounding label attach to a value that can
		// never be rounded.
		for _, name := range wantDateFields {
			rule, ok := accessorFields[name]
			if !ok {
				continue // reported by the subtest above
			}
			if rule.receiver != FormulaDate {
				t.Errorf("`.%s` is a DATE field in the reference's table; its receiver rule is %v", name, rule.receiver)
			}
			if rule.result != FormulaNumber {
				t.Errorf("the reference types `date.%s` as `number`; the rule says %v", name, rule.result)
			}
			if rule.scale != 0 {
				t.Errorf("`.%s` is a calendar component and can only ever be a whole number; declared scale %d", name, rule.scale)
			}
			if rule.requiresMany {
				t.Errorf("`.%s` reads a single date, not a list", name)
			}
		}
	})
}

// TestFormula_DateFieldsAreCalendarComponents asserts the seven values.
//
// These are COMPONENTS, which is the opposite of what the duration fields next
// door are. `.second` is the seconds place of a clock reading and can only be
// 0–59; `.seconds` is a whole span expressed in seconds and is unbounded. A
// single instant with a distinct, non-zero value in every position is what
// makes the difference visible: if any field were wired to the wrong extractor
// — or to a duration divisor — it would land on another field's number and the
// assertion would name which.
func TestFormula_DateFieldsAreCalendarComponents(t *testing.T) {
	c := fixtureCandidate{}

	// 2019-11-04T21:08:07.045Z, chosen so no two fields share a value and none
	// is zero. Read straight off the timestamp, per the reference's own
	// descriptions: "The year of the date", "The month of the date (1–12)",
	// "The day of the month", "The hour (0–23)", "The minute (0–59)", "The
	// second (0–59)", "The millisecond (0–999)".
	const instant = `date("2019-11-04T21:08:07.045Z")`
	for _, tc := range []struct{ field, want, why string }{
		{"year", "2019", "the year of the date"},
		{"month", "11", "November is month 11 — the table says 1–12, so it is NOT zero-based"},
		{"day", "4", "the day of the month, not the day of the week or of the year"},
		{"hour", "21", "the hour, 0–23, so 21 and not 9"},
		{"minute", "8", "the minute, 0–59"},
		{"second", "7", "the second, 0–59 — the seconds PLACE, not the elapsed seconds"},
		{"millisecond", "45", ".045 of a second is 45 milliseconds, 0–999"},
	} {
		src := instant + "." + tc.field
		res := evalOne(t, src, c)
		if got := renderNumber(t, res); got != tc.want {
			t.Errorf("%s = %s, want %s — %s", src, got, tc.want, tc.why)
		}
		if res.Scale != 0 {
			t.Errorf("%s crossed the boundary at scale %d; a calendar component is a whole number", src, res.Scale)
		}
	}
}

// TestFormula_DateFieldsReadTheFixtureClockNotTheWallClock covers `now()` and
// `today()`, the two producers the blocked view actually uses.
//
// It is a separate test from the literal above because it can fail in a way
// the literal cannot: a field that read the wall clock instead of the query's
// snapshotted instant would still return a plausible year, and would still
// pass every assertion made against a `date("…")` literal.
func TestFormula_DateFieldsReadTheFixtureClockNotTheWallClock(t *testing.T) {
	c := fixtureCandidate{}

	// formulaTestNow() is 2019-03-07 14:30:00 UTC, deliberately far in the
	// past. `today()` is that instant truncated to its day, so its clock
	// fields are zero and its calendar fields are unchanged.
	for _, tc := range []struct{ src, want, why string }{
		{"now().year", "2019", "the snapshotted instant's year, not this year"},
		{"now().month", "3", "March"},
		{"now().day", "7", "the 7th"},
		{"now().hour", "14", "14:30 UTC"},
		{"now().minute", "30", "14:30 UTC"},
		{"today().year", "2019", "today() keeps the calendar date"},
		{"today().month", "3", "the same"},
		{"today().day", "7", "the same"},
		{"today().hour", "0", "today() drops the time, so its hour is midnight — not 14"},
		{"today().minute", "0", "the same"},
	} {
		res := evalOne(t, tc.src, c)
		if got := renderNumber(t, res); got != tc.want {
			t.Errorf("%s = %s, want %s — %s", tc.src, got, tc.want, tc.why)
		}
	}
}

// TestFormula_ClosingThisYearDiscriminates is the view this revision was made
// for: `Deals::Closing This Month`, whose filter is `date(close_date).year ==
// today().year`.
//
// PARSING IS NOT THE CLAIM. A filter that parses and then matches every record
// is precisely the FR-105 failure this project treats as worse than a refusal:
// an imported view must never return MORE rows than the Obsidian original. So
// the assertion is a DISCRIMINATING PAIR — one record inside the year and one
// outside — and the out-of-year record must be excluded. A `.year` wired to a
// constant, or to the receiver's own year on both sides, passes a "does it
// parse" test and fails this one.
func TestFormula_ClosingThisYearDiscriminates(t *testing.T) {
	schema := formulaFixtureSchema()
	due := schema.Properties["due"]

	// The fixture clock is 2019, so 2019 dates are in-year and others are not.
	for _, tc := range []struct {
		name string
		date string
		want bool
		why  string
	}{
		{"same year, later month", "2019-06-15", true, "June 2019 is in 2019"},
		{"same year, earlier month", "2019-01-02", true, "January 2019 is too — the filter is the YEAR, not the month"},
		{"next year, one day later", "2020-01-01", false, "one day past the year boundary is OUT"},
		{"previous year, one day earlier", "2018-12-31", false, "one day before it is OUT too"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixtureCandidate{props: map[string]PropertyValue{
				"due": {
					Property: due, State: StatePresent,
					Values: []TypedValue{tvDate(t, tc.date)},
				},
			}}
			res := evalOne(t, "date(due).year == today().year", c)
			vals := res.Values()
			if len(vals) != 1 {
				t.Fatalf("expected one boolean, got %d values", len(vals))
			}
			if got := vals[0].Bool; got != tc.want {
				t.Errorf("`date(due).year == today().year` with due=%s gave %v, want %v — %s",
					tc.date, got, tc.want, tc.why)
			}
		})
	}
}

// TestFormula_SingularIsADateFieldAndPluralIsADuration pins the distinction the
// reference itself draws, in both directions.
//
// This is the mistake the accessor table's receiver rule exists to catch, and
// the revision made it newly reachable: before it, `.day` did not exist at all,
// so `(a - b).day` was an unknown-field refusal. Now both names exist and only
// the receiver tells them apart. Getting it wrong must be a REFUSAL naming the
// receiver, never a plausible number — `(today() - due).day` silently answering
// with a day-of-month would be a wrong answer no reader could detect.
func TestFormula_SingularIsADateFieldAndPluralIsADuration(t *testing.T) {
	schema := formulaFixtureSchema()

	t.Run("a duration has no singular calendar fields", func(t *testing.T) {
		for _, tc := range []struct{ src, why string }{
			{"(today() - due).year", "a span of time has no year"},
			{"(today() - due).month", "nor a month"},
			{"(today() - due).day", "`.day` is the day of the MONTH; a duration has no month"},
			{"(today() - due).hour", "`.hour` is a clock reading; `.hours` is the total"},
			{"(today() - due).minute", "the same"},
			{"(today() - due).second", "the same"},
			{"(today() - due).millisecond", "the same"},
		} {
			if _, errs := ValidateFormulaSet(map[string]string{"f": tc.src}, schema); len(errs) == 0 {
				t.Errorf("%q must be REFUSED (%s) — a duration answering a calendar field is a wrong answer with no error", tc.src, tc.why)
			}
		}
	})

	t.Run("a date has no plural total fields", func(t *testing.T) {
		for _, tc := range []struct{ src, why string }{
			{"due.days", "`.days` is a duration total, not a date field"},
			{"due.hours", "the same"},
			{"due.minutes", "the same"},
			{"due.seconds", "the same"},
			{"due.milliseconds", "the same"},
		} {
			if _, errs := ValidateFormulaSet(map[string]string{"f": tc.src}, schema); len(errs) == 0 {
				t.Errorf("%q must be REFUSED (%s)", tc.src, tc.why)
			}
		}
	})

	t.Run("the two produce genuinely different numbers", func(t *testing.T) {
		// The refusals above would all still pass if `.second` and `.seconds`
		// were wired to the same extractor and merely gated on receiver type.
		// This is the assertion that separates them by VALUE.
		//
		// 2019-03-07 14:30:00Z (the fixture clock) minus 2019-03-07 00:00:00Z
		// is 14.5 hours. As a TOTAL that is 52,200 seconds. The seconds PLACE
		// of 14:30:00 is 0. Two different questions, two different answers.
		c := fixtureCandidate{}
		total := evalOne(t, `(now() - date("2019-03-07T00:00:00Z")).seconds`, c)
		if got := renderNumber(t, total); got != "52200" {
			t.Errorf("`.seconds` is the TOTAL seconds in the span: 14.5h × 3600 = 52200, got %s", got)
		}
		place := evalOne(t, "now().second", c)
		if got := renderNumber(t, place); got != "0" {
			t.Errorf("`.second` is the seconds PLACE of 14:30:00, which is 0, got %s", got)
		}
	})
}

// TestFormula_DateFieldAbsenceIsNotYearZero is R-14 over the new fields.
//
// The plausible wrong answer here is specific and nasty. A date that is not
// there has a Go zero value, and the zero time's `.year` is 1 — a real number
// that would flow into a comparison and answer FALSE with no problem reported.
// `date(due).year == today().year` over a record with no `due` must be ABSENT,
// not "year 1, therefore not this year".
func TestFormula_DateFieldAbsenceIsNotYearZero(t *testing.T) {
	schema := formulaFixtureSchema()
	due := schema.Properties["due"]
	c := fixtureCandidate{props: map[string]PropertyValue{"due": absentValue(due)}}

	for _, field := range []string{"year", "month", "day", "hour", "minute", "second", "millisecond"} {
		src := "due." + field
		res := evalOne(t, src, c)
		if !res.Absent {
			t.Errorf("R-14: %s over an absent date must be ABSENT; got %v — "+
				"the zero time would answer 1 here, which is a wrong number rather than no number",
				src, res.Values())
		}
	}

	// And the same through the whole view expression, which is where an
	// absence that became a number would actually do its damage.
	//
	// The claim here is NOT that the comparison is absent. This package's
	// established rule — pre-existing and shared by `due == today()`,
	// `amount == 1` and `amount > 1` alike — is that absence propagates
	// through VALUES and then becomes FALSE at the comparison, because a
	// filter has to decide include-or-exclude and "exclude" is the only safe
	// direction. FR-105 is what makes it the safe one: an imported view must
	// never return MORE rows than the Obsidian original, so a record with no
	// close date must not appear in "closing this year".
	//
	// So the assertion is that it is FALSE and specifically never TRUE. If
	// `.year` over an absent date returned the zero time's year of 1, this
	// would still be false and this assertion would not catch it — which is
	// why the per-field absence checks above are the other half, and why both
	// halves are here.
	res := evalOne(t, "date(due).year == today().year", c)
	vals := res.Values()
	if res.Absent || len(vals) != 1 {
		t.Fatalf("the filter must produce one boolean over a record with no date; got absent=%v values=%v", res.Absent, vals)
	}
	if vals[0].Bool {
		t.Errorf("FR-105: a record with no date MATCHED `date(due).year == today().year` — " +
			"an imported view must never return more rows than the Obsidian original")
	}
}

// TestFormula_TheReferenceDefinesNoOtherDateField is the exclusion half of
// "adopt the table whole".
//
// The ruling that advanced the pin asked which of a FAMILY to adopt and why
// the rest were excluded. The honest answer is that there is no rest: the
// reference's Date table has exactly seven rows and defines no `.week`,
// `.quarter`, `.dayOfWeek` or `.dayOfYear`. Each of these exists in some other
// expression language, which is exactly why a closed grammar has to refuse
// them BY NAME rather than by accident — and why this test exists rather than
// a sentence in a comment.
func TestFormula_TheReferenceDefinesNoOtherDateField(t *testing.T) {
	schema := formulaFixtureSchema()

	for _, tc := range []struct{ src, why string }{
		{"due.week", "no `.week` row in the table"},
		{"due.quarter", "no `.quarter` row — quarters are a reporting idea, not a documented field"},
		{"due.dayOfWeek", "no `.dayOfWeek` row"},
		{"due.weekday", "nor `.weekday` under any spelling"},
		{"due.dayOfYear", "no `.dayOfYear` row"},
		{"due.years", "plural is the duration family, and durations have no year total either"},
		{"due.months", "the same — `.months` is in neither table"},
		{"due.weeks", "the same"},
		{"due.date", "`date()` is a METHOD; the parenless spelling is not a field"},
		{"due.timestamp", "not a documented field"},
	} {
		_, errs := ValidateFormulaSet(map[string]string{"f": tc.src}, schema)
		if len(errs) == 0 {
			t.Errorf("%q must be refused (%s) — a grammar that quietly grows is a moving target nobody notices moving", tc.src, tc.why)
			continue
		}
		// The refusal must QUOTE what was written and say what would have been
		// accepted — FR-024's posture. An undefined field is not recognised as
		// a mistyped accessor (the parser only treats a trailing segment as an
		// accessor when it IS one), so it surfaces as an unresolvable name and
		// the message names the resolvable shapes instead. Either way the
		// author is told; a bare "invalid" would not do.
		msg := strings.Join(formulaErrorMessages(errs), " ")
		if !strings.Contains(msg, tc.src) {
			t.Errorf("the refusal of %q must quote what was written; got %q", tc.src, msg)
		}
		if !strings.Contains(msg, "expected") {
			t.Errorf("the refusal of %q must say what WOULD have been accepted; got %q", tc.src, msg)
		}
	}
}
