// Omnipus — ADR-068 D13 / spec FR-025, FR-026: the problem list, and the refusal
// that is never an empty success.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultfind

import (
	"errors"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// A REFUSAL CARRIES ITS OWN RESPONSE
//
// Spec 4.1.2 says a refusal arrives as a response with `refused: true` and the
// remedy in `problems` — not as a transport error. The FR-020h contract
// (records.AssertRefusesWhenIndexUnavailable) says the entry point must return a
// non-nil ERROR that unwraps to records.ErrPropertyIndexUnavailable, because an
// entry point returning a successful empty result is the exact bug FR-020h
// exists to close.
//
// Both are right, and they are not in tension once you notice they address
// different readers. So Find returns BOTH: the fully-rendered refusal for the
// model, and the error for the caller. The tool adapter renders the response
// (so the model gets something it can act on) and marks the result as an error
// (so nothing downstream mistakes it for an answer).
//
// The failure this shape rules out is the one that actually happens: returning
// `(emptyResponse, nil)` and letting the operator conclude their vault is empty.
// ---------------------------------------------------------------------------

// Refusal is a query that produced no answer, with the reason and the remedy.
type Refusal struct {
	// Problem is what goes in the response's problem list. Its Fix is not
	// optional: a bound, a bad operator or an unavailable index is never
	// reported without the instruction that resolves it.
	Problem generated.RecordProblem
	// cause is the underlying error when there is one to unwrap — the platform
	// refusal in particular, which errors.Is must still find.
	cause error
}

func (r *Refusal) Error() string {
	msg := r.Problem.Reason
	if r.Problem.Fix != nil && *r.Problem.Fix != "" {
		msg += " — " + *r.Problem.Fix
	}
	return msg
}

// Unwrap keeps errors.Is working through the refusal. records.RequirePropertyIndex's
// error is wrapped and NOT replaced, so the platform name survives to the caller
// and AssertRefusesWhenIndexUnavailable's three checks all pass.
func (r *Refusal) Unwrap() error { return r.cause }

// IsRefusal reports whether err is a vault_find refusal.
func IsRefusal(err error) bool {
	var r *Refusal
	return errors.As(err, &r)
}

// str is the pointer-taking helper the generated optional fields need. It is
// deliberately not variadic and not clever: every optional field on the wire is
// a pointer, and a helper that silently turned "" into nil would erase the
// difference between "no fix" and "a fix that renders as nothing".
func str(s string) *string { return &s }

// problem builds one entry. Reason and fix are both required by the caller's
// discipline rather than by the type, so this is the one place to notice a
// problem being built without a remedy.
func problem(code generated.RecordProblemCode, reason, fix string, records_ ...string) generated.RecordProblem {
	p := generated.RecordProblem{
		Code:    code,
		Reason:  reason,
		Records: append([]string{}, records_...),
	}
	if fix != "" {
		p.Fix = str(fix)
	}
	return p
}

// refuse wraps a problem as the error half of the pair.
func refuse(p generated.RecordProblem, cause error) *Refusal {
	return &Refusal{Problem: p, cause: cause}
}

// ---------------------------------------------------------------------------
// TRANSLATING pkg/records' OWN REFUSALS
//
// records.QueryError already carries the exact wording spec 4.1.2's refusal
// table mandates — the declared property names, the ten supported operators,
// the parameter that does the job instead. This function CLASSIFIES it into a
// wire code; it deliberately does not rewrite the message.
//
// Rewriting was the tempting option and it is the wrong one: two places would
// then own one refusal's wording, and the day the engine's message improves the
// tool's copy would silently go stale while every test still passed.
// ---------------------------------------------------------------------------

// fromQueryError maps a records.QueryError onto the problem the response
// carries, choosing the code from what the engine actually objected to.
func fromQueryError(qe *records.QueryError) generated.RecordProblem {
	reason := qe.Error()
	code := classifyQueryError(qe)

	p := generated.RecordProblem{Code: code, Reason: reason, Records: []string{}}
	if qe.Property != "" {
		p.Property = str(qe.Property)
	}
	if len(qe.ValidNames) > 0 {
		names := append([]string{}, qe.ValidNames...)
		p.Permitted = &names
	}
	p.Fix = str(queryErrorFix(qe, code))
	return p
}

// classifyQueryError picks the wire code. The order matters: the more specific
// causes are tested before the general ones, so `IN` with an empty list is
// reported as an empty IN list rather than as a generic bad value.
func classifyQueryError(qe *records.QueryError) generated.RecordProblemCode {
	r := qe.Reason
	switch {
	case strings.Contains(r, "is not a supported operator"):
		return generated.UnsupportedOperator
	case strings.Contains(r, "matches every value"):
		return generated.EmptyLikePattern
	case strings.Contains(r, "empty list"), strings.Contains(r, "IN was given"):
		return generated.EmptyInList
	case strings.Contains(r, "ordering comparisons are not defined"),
		strings.Contains(r, "holds many values"):
		return generated.OrderingOnManyProperty
	case strings.Contains(r, "has no property"):
		return generated.UnknownProperty
	case strings.Contains(r, "is not defined for"):
		return generated.UnsupportedOperator
	case strings.Contains(r, "is not a valid"):
		// FR-022e — a literal that cannot be read in the declared type. The
		// enum sub-case is reported as an unknown enum VALUE, because the
		// remedy differs: one is "quote it", the other is "use a declared
		// value", and a caller cannot act on the wrong one.
		if len(qe.ValidNames) > 0 {
			return generated.UnknownEnumValue
		}
		return generated.LiteralTypeMismatch
	}
	return generated.TypeMismatch
}

// queryErrorFix names the remedy when the engine's own message did not already
// carry one. It never overrides a remedy the engine supplied.
func queryErrorFix(qe *records.QueryError, code generated.RecordProblemCode) string {
	if qe.Remedy != "" {
		return qe.Remedy
	}
	switch code {
	case generated.UnknownProperty:
		return "call vault_describe to see the declared properties of this record type"
	case generated.UnknownEnumValue:
		return "use one of the permitted values, or IS NULL to select records with no value"
	case generated.LiteralTypeMismatch:
		return "compare against a value written the way the note would write it, " +
			"or use IS NULL / IS NOT NULL"
	case generated.EmptyInList:
		return "supply at least one value, or use IS NULL to select records with no value"
	case generated.EmptyLikePattern:
		return "use IS NOT NULL if you meant \"has a value at all\""
	case generated.OrderingOnManyProperty:
		return "use =, IN or LIKE, which are defined element-wise over a list"
	}
	return "correct the filter and re-run"
}

// ---------------------------------------------------------------------------
// COMPARISON PROBLEMS — one line per RECORD, never one per rule firing
// ---------------------------------------------------------------------------

// comparisonProblem renders one record's failed comparison, naming the record,
// the property, what it holds and what to write instead.
//
// The shape is FR-025's: one record, one reason, one fix, on one line. The
// alternative — "3 records excluded" with a count and no names — is the failure
// mode this whole design exists to remove, because it tells the reader that
// something is wrong and gives them no way to act.
//
// The reason is the oracle's `Detail`, NOT its String(): String() prefixes the
// machine code ("non_conforming_value: ..."), which is the right shape for a
// log line and the wrong one for a sentence a model reads and acts on.
func comparisonProblem(recordID, path string, cp records.ComparisonProblem) generated.RecordProblem {
	id := recordID
	if id == "" {
		id = path
	}
	p := generated.RecordProblem{
		Code:    codeForComparison(cp.Code),
		Reason:  cp.Detail,
		Records: []string{id},
		Paths:   &[]string{path},
	}
	if cp.Property != "" {
		p.Property = str(cp.Property)
	}
	if len(cp.Supported) > 0 {
		names := append([]string{}, cp.Supported...)
		p.Permitted = &names
	}
	switch {
	case cp.Remedy != "":
		p.Fix = str(cp.Remedy)
	case len(cp.Supported) > 0:
		p.Fix = str("use one of: " + strings.Join(cp.Supported, ", "))
	default:
		p.Fix = str("open the note and correct the value, then re-run")
	}
	return p
}

// codeForComparison maps the oracle's rule-level verdict onto a wire code.
//
// The default is comparison_undefined rather than type_mismatch, and the
// distinction is not pedantry: type_mismatch says the value is the wrong type,
// while comparison_undefined says the comparison could not be MADE — which also
// covers an operator no rule defines for the declared type. Reporting the second
// as the first sends the reader to fix a value that is perfectly fine.
//
// It switches on the EXPORTED constants rather than on their string spellings.
// A literal here would keep compiling after pkg/records renamed a code, and this
// function would then silently classify every one of them as the default.
func codeForComparison(c records.ComparisonProblemCode) generated.RecordProblemCode {
	switch c {
	case records.CompareNonConforming:
		return generated.TypeMismatch
	case records.CompareRelationUnresolved:
		return generated.DanglingRelation
	case records.CompareArityNotDefined:
		return generated.OrderingOnManyProperty
	case records.CompareOperatorNotDefined, records.CompareUnknownOperator:
		return generated.UnsupportedOperator
	}
	return generated.ComparisonUndefined
}

// findingProblem renders a records.Finding — the resolver's own account of a
// value it could not read — as a problem line.
func findingProblem(recordID, path string, f records.Finding) generated.RecordProblem {
	id := recordID
	if id == "" {
		id = path
	}
	reason := f.Reason
	if f.Got != "" {
		reason = fmt.Sprintf("%s (found %s)", reason, f.Got)
	}
	p := generated.RecordProblem{
		Code:    generated.TypeMismatch,
		Reason:  reason,
		Records: []string{id},
		Paths:   &[]string{path},
	}
	if f.Property != "" {
		p.Property = str(f.Property)
	}
	if len(f.Permitted) > 0 {
		names := append([]string{}, f.Permitted...)
		p.Permitted = &names
	}
	if f.Expected != "" {
		p.Expected = str(f.Expected)
		p.Fix = str("write " + f.Expected)
	} else {
		p.Fix = str("open the note and correct the value, then re-run")
	}
	return p
}
