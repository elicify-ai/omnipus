// Omnipus — the seam that lets the candidate stream reuse the ONE matching
// layer instead of growing a second one.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import "testing"

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// MatchWith takes a Record and resolves the left operand out of its
// frontmatter. The candidate-stream evaluation path (pkg/records/propindex)
// has no frontmatter — its values are decoded from the index by
// StoredProp.Typed — so without a seam it would have to re-implement the
// four-case ladder: the unary two, "could not compare" excluded-and-reported,
// R-2 absence with FR-008's re-inclusion, and the oracle's answer.
//
// A second MATCHING layer is the same class of defect as a second COMPARATOR,
// and filter.go's header bans the latter for reasons that apply verbatim to the
// former: the verified copy sits off the query path while the unverified one
// does the real filtering.
//
// So MatchWith and MatchValue must be the same code, and this file is what
// makes that a property rather than an intention. Its mutation proof is:
// break ONE branch of PreparedFilter.MatchValue and BOTH entry points must
// fail. If only one fails, a copy has grown back.
// ---------------------------------------------------------------------------

// TestMatchValue_AgreesWithMatchWithOnEveryLadderCase drives both entry points
// over the same inputs and requires identical verdicts.
//
// The corpus deliberately reaches all four cases of the ladder AND both
// polarities of Negate, because three of the four differ from each other only
// in the negation they apply.
func TestMatchValue_AgreesWithMatchWithOnEveryLadderCase(t *testing.T) {
	_, sc := filterSchema(t)

	records := map[string]Record{
		"absent":  ParseRecord("absent.md", []byte("---\ntype: widget\nname: A\n---\n")),
		"null":    ParseRecord("null.md", []byte("---\ntype: widget\nname: N\nstatus:\n---\n")),
		"done":    ParseRecord("done.md", []byte("---\ntype: widget\nname: D\nstatus: done\n---\n")),
		"todo":    ParseRecord("todo.md", []byte("---\ntype: widget\nname: T\nstatus: todo\n---\n")),
		"corrupt": ParseRecord("corrupt.md", []byte("---\ntype: widget\nname: C\nstatus: shipped\n---\n")),
		"number":  ParseRecord("num.md", []byte("---\ntype: widget\nname: X\ncount: 7\n---\n")),
		"list":    ParseRecord("list.md", []byte("---\ntype: widget\nname: L\nsegment: [vendor, partner]\n---\n")),
	}

	filters := []Filter{
		// (1) the unary two — the only operators absence does not make false.
		{Property: "status", Op: OpIsNull},
		{Property: "status", Op: OpIsNotNull},
		{Property: "status", Op: OpIsNull, Negate: true},
		// (4) and (3) — a plain leaf, then the same leaf negated, which is the
		// pair FR-008's re-inclusion sits between.
		{Property: "status", Op: OpEqual, Literal: "done", LiteralGiven: true},
		{Property: "status", Op: OpEqual, Literal: "done", LiteralGiven: true, Negate: true},
		{Property: "status", Op: OpEqual, Literal: "done", LiteralGiven: true, Negate: true, ExcludeAbsent: true},
		// `<>` is a LEAF, not the negation flag — R-2 governs it like any other.
		{Property: "status", Op: OpNotEqual, Literal: "done", LiteralGiven: true},
		// ordering and set membership, over a conforming and an absent side.
		{Property: "count", Op: OpGreater, Literal: "3", LiteralGiven: true},
		{Property: "count", Op: OpGreater, Literal: "3", LiteralGiven: true, Negate: true},
		{Property: "status", Op: OpIn, Literals: []string{"todo", "doing"}},
		// (2) element-wise over a `many` property, and a pattern.
		{Property: "segment", Op: OpIn, Literals: []string{"partner"}},
		{Property: "name", Op: OpLike, Literal: "%", LiteralGiven: true},
	}

	var c Comparator
	for _, f := range filters {
		for name, rec := range records {
			viaRecord, errRecord := f.MatchWith(c, sc, rec)

			// The left operand MatchValue is given is resolved through the same
			// ResolveProperty MatchWith uses. That is the point: the two paths
			// must differ ONLY in how the operand arrived, never in what is
			// decided about it.
			prop, ok := sc.Property(f.Property)
			if !ok {
				t.Fatalf("the fixture schema has no property %q", f.Property)
			}
			viaValue, errValue := f.MatchValue(c, sc, ResolveProperty(rec, prop))

			if (errRecord == nil) != (errValue == nil) {
				t.Fatalf("filter %+v on %s: MatchWith err=%v but MatchValue err=%v",
					f, name, errRecord, errValue)
			}
			if errRecord != nil {
				continue
			}
			if viaRecord.Matched != viaValue.Matched {
				t.Errorf(
					"filter %+v on record %q: MatchWith says Matched=%v, MatchValue says %v.\n"+
						"These must be ONE implementation. A disagreement means the four-case ladder "+
						"has been copied, which is the defect a second comparator would be.",
					f, name, viaRecord.Matched, viaValue.Matched)
			}
			if viaRecord.State != viaValue.State {
				t.Errorf("filter %+v on record %q: State %v vs %v", f, name, viaRecord.State, viaValue.State)
			}
			if len(viaRecord.ComparisonProblems) != len(viaValue.ComparisonProblems) {
				t.Errorf("filter %+v on record %q: %d comparison problems vs %d",
					f, name, len(viaRecord.ComparisonProblems), len(viaValue.ComparisonProblems))
			}
		}
	}
}

// TestPreparedFilter_ValidatesOnceAndRefusesTheSameWay asserts the prepared
// form refuses exactly what Validate refuses, at Prepare time rather than per
// record.
//
// FR-023 requires a filter to be checked BEFORE any record is touched. A
// prepared filter that deferred its refusal to the first record would satisfy
// the letter and lose the requirement: a query naming an unknown property would
// then be reported once per candidate, or — worse — reported as a per-record
// problem rather than a rejected query, which is FR-024's silent-empty-result
// failure wearing a different hat.
func TestPreparedFilter_ValidatesOnceAndRefusesTheSameWay(t *testing.T) {
	_, sc := filterSchema(t)

	for _, f := range []Filter{
		{Property: "nonexistent", Op: OpEqual, Literal: "x", LiteralGiven: true},
		{Property: "status", Op: "CONTAINS", Literal: "x", LiteralGiven: true},
		{Property: "status", Op: OpIsNull, Literal: "x", LiteralGiven: true},
		{Property: "status", Op: OpIn},
		{Property: "count", Op: OpEqual, Literal: "not-a-number", LiteralGiven: true},
	} {
		_, prepErr := f.Prepare(sc)
		if prepErr == nil {
			t.Errorf("filter %+v was PREPARED without complaint; Validate refuses it, so Prepare must too "+
				"— FR-023 requires the refusal before any record is touched", f)
			continue
		}

		_, _, valErr := f.Validate(sc)
		if valErr == nil || valErr.Error() != prepErr.Error() {
			t.Errorf("filter %+v: Prepare said %q but Validate said %q; the refusal must be the SAME one, "+
				"not a second wording of it", f, prepErr, valErr)
		}
	}
}

// TestPreparedFilter_ReusedAcrossRecordsMatchesPerRecordValidation is the
// performance seam's correctness obligation.
//
// PreparedFilter exists so a filter is validated once and applied to a
// population bounded only by FR-064's B1 (50,000). That is only sound if
// reusing it decides identically to re-validating every time — otherwise the
// optimisation has changed an answer, which is precisely the trade this
// codebase does not make.
func TestPreparedFilter_ReusedAcrossRecordsMatchesPerRecordValidation(t *testing.T) {
	_, sc := filterSchema(t)
	f := Filter{Property: "status", Op: OpEqual, Literal: "done", LiteralGiven: true, Negate: true}

	pf, err := f.Prepare(sc)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	var c Comparator
	for _, src := range []string{
		"---\ntype: widget\nname: A\n---\n",
		"---\ntype: widget\nname: D\nstatus: done\n---\n",
		"---\ntype: widget\nname: T\nstatus: todo\n---\n",
		"---\ntype: widget\nname: C\nstatus: shipped\n---\n",
	} {
		rec := ParseRecord("r.md", []byte(src))
		reused := pf.MatchValue(c, ResolveProperty(rec, pf.Property))

		fresh, err := f.MatchWith(c, sc, rec)
		if err != nil {
			t.Fatalf("MatchWith: %v", err)
		}
		if reused.Matched != fresh.Matched || reused.State != fresh.State {
			t.Errorf("reusing a PreparedFilter changed the answer for %q: reused %v/%v, fresh %v/%v",
				src, reused.Matched, reused.State, fresh.Matched, fresh.State)
		}
	}
}
