// Omnipus — ADR-068 D3: turning a lexical frontmatter value into a typed one.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
	"math"
	"strings"
	"time"

	"golang.org/x/text/cases"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// The schema says what a property IS (schema.go). The frontmatter says what a
// note WROTE (frontmatter.go). This file is the join: it takes one lexical
// value and one declaration and produces either a typed value or a rejection
// that names the expected shape.
//
// Two rules govern every function here:
//
//  1. NOTHING IS COERCED. A value that does not conform is rejected, never
//     nudged into shape. §8 R-4: a non-conforming value compares false for
//     every operator AND the record is reported. Silence is the defect.
//
//  2. NOTHING BECOMES A FLOAT. `decimal` parses from source text straight
//     into Decimal (decimal.go) and `integer` into an int64 — neither ever
//     touches a float64. FR-020b is a promise about the whole path, and this
//     is the only place in the path that touches numeric text.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// CASE FOLDING — the ONE function (FR-011a)
//
// FR-011a  text, enum and the path side of a relation compare
//          case-INSENSITIVELY, in FULL Unicode.
//
// The rule is one line; the reason it needs this much commentary is that the two
// obvious ways to write it are both WRONG, and they are wrong in OPPOSITE
// directions, so neither one's failures reveal the other's. Executed against
// golang.org/x/text v0.41.0:
//
//	pair                 strings.ToLower   strings.EqualFold   cases.Fold
//	straße / STRASSE     false             false               TRUE
//	σίσυφος / ΣΊΣΥΦΟΣ    false             true                TRUE
//	istanbul / İSTANBUL  true              false               FALSE
//
// Row 1 is German ß, which needs FULL folding (ß → ss). The Go standard
// library performs only SIMPLE folding — a rune-for-rune map — so neither
// stdlib function can ever match it, however the call is arranged.
//
// Row 2 is Greek final sigma, where the two stdlib functions DISAGREE WITH
// EACH OTHER. That is the reason neither is a defensible default: a reviewer
// who checks one and is satisfied has checked the one that happens to be
// right for their fixture.
//
// Row 3 is the Turkish dotted capital İ, and cases.Fold's FALSE is the
// CORRECT answer, not a gap. Dotted İ and plain i are different letters in
// Turkish; folding them together is the classic Turkish-I bug. ToLower's
// `true` there is a WRONG MATCH — the failure direction nobody notices,
// because a wrong match looks like a feature. AC-8.9e asserts it as a
// negative, with the reason inside the assertion message so that the next
// reader does not "fix" it.
//
// TWO CONSEQUENCES A CALLER MUST KNOW:
//
//  1. cases.Fold() is the documented exception to the general Caser rule that
//     a Caser is stateful and must not be shared. Its own documentation
//     (golang.org/x/text/cases/cases.go:86-87): "The returned Caser is
//     stateless and safe to use concurrently by multiple goroutines." That
//     sentence is LOAD-BEARING here — it is what permits the package-level
//     `folder` below, which every comparison in the package shares. Without
//     it this would need a sync.Pool or a per-call construction.
//
//  2. FOLDING CHANGES RUNE COUNT. `straße` is 6 runes and folds to `strasse`,
//     which is 7; `ﬁle` is 3 and folds to `file`, which is 4. Any rule
//     counting characters — LIKE's `_`, which matches exactly one character —
//     is therefore defined against the FOLDED subject and the FOLDED pattern,
//     never the raw text, or `_` would mean a different number of characters
//     on each side of the same comparison.
//
// COST, STATED RATHER THAN GLOSSED. golang.org/x/text was an INDIRECT
// dependency before this change and is promoted to direct. No new module and
// no CGo, so Hard Constraint #1 and #2 hold — but the `cases` subpackage and
// its transform/language siblings are not free: linking them into a
// previously-cases-free binary measured +443.8 KiB (Go 1.26, darwin/arm64,
// CGO_ENABLED=0, minimal program with and without the import).
// ---------------------------------------------------------------------------

// folder is the single shared Caser. See consequence (1) above for why one
// package-level value is safe: cases.Fold() is documented stateless and
// concurrency-safe, unlike every other Caser this package could have built.
var folder = cases.Fold()

// FoldKey returns the full-Unicode case-folded form of s — the comparison key for
// every case-insensitive rule in this package (FR-011a), and the SORT KEY for
// R-5's lexical enum ordering.
//
// It is the ONLY permitted way to fold text here. strings.ToLower and
// strings.EqualFold are forbidden for text comparison; fold_test.go asserts
// that this function agrees with NEITHER of them across the six AC-8.9 pairs,
// which is what makes the test unfaked: an implementation that folded nothing,
// or that delegated to either stdlib function, fails a named cell.
//
// The returned string is NOT normalized (cases.Fold does not normalize and may
// not preserve a normal form) and is never rendered — FR-011c: what a report
// shows is always the file's own spelling. This is a key, not a display form.
func FoldKey(s string) string {
	return folder.String(s)
}

// FoldEqual reports whether a and b are the same text under full Unicode case
// folding — R-10's `=` on text, R-5's enum resolution and R-8's relation-PATH
// comparison.
//
// It is deliberately NOT used for a relation IDENTIFIER: R-8 splits those, and
// identifiers compare byte-exactly, because folding would make `CO-0142` and
// `co-0142` one key and two legitimately distinct targets could then not
// coexist.
func FoldEqual(a, b string) bool {
	return FoldKey(a) == FoldKey(b)
}

// FoldLess is R-5's total order over text: byte-lexical over the FOLDED key,
// with ties broken on the RAW bytes.
//
// Both halves are decisions, not defaults:
//
//   - The folded key, because byte order over raw values puts every
//     capitalised value before every lowercase one — executed, `"Won" < "lost"`
//     is TRUE on raw bytes and FALSE folded. A corpus that FR-011 deliberately
//     permits to hold `Won`, `won` and `WON` as ONE value would otherwise
//     render in THREE places in a sorted result while group_by collapsed them
//     into ONE group. Sorting on the folded key makes ordering, equality and
//     grouping agree.
//
//   - The raw-byte tie-break, because without it the order is only a partial
//     one and equal-folding values come out in whatever order the sort
//     happened to leave them. SC-014 asserts a byte-identical result across a
//     rebuild, and R-11 requires determinism across runs; a total order is how
//     both are met.
func FoldLess(a, b string) bool {
	fa, fb := FoldKey(a), FoldKey(b)
	if fa != fb {
		return fa < fb
	}
	return a < b
}

// FoldCompare is FoldLess as a three-way comparison, for callers that need to
// feed sort.Slice or a comparator returning -1/0/+1. It is the same total
// order: folded key first, raw bytes as the tie-break.
func FoldCompare(a, b string) int {
	fa, fb := FoldKey(a), FoldKey(b)
	switch {
	case fa < fb:
		return -1
	case fa > fb:
		return 1
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// TypedValue is one conforming value of one declared property.
type TypedValue struct {
	Type PropertyType
	// Raw is the source text exactly as the file had it, kept so a report can
	// quote what the operator actually wrote rather than a re-rendering of it.
	Raw string

	// Text carries a `text` value.
	Text string
	// Enum carries the DECLARED enum value a written value resolved to
	// (FR-011a). It is the schema's spelling, not the file's — the file's is in
	// Raw — so grouping and equality agree even when three notes spell one
	// state three ways.
	Enum EnumValue
	// Link carries a `relation` or `person` value.
	Link Wikilink
	// Date carries a `date` value.
	Date DateValue
	// Number carries an `integer` or a `decimal` value, exactly. §8 R-1 makes
	// the two ONE declared type for comparison, so they share one field and
	// `3 = 3.0` is true; the declared type decides the BOUNDS, not a separate
	// comparison domain.
	//
	// For an `integer` the Decimal always has scale 0 and always fits int64 —
	// Int64 returns it without loss. For a `decimal` the scale is bounded by
	// maxDecimalScale and the magnitude is unbounded.
	Number Decimal
}

// String renders a value for a report.
func (v TypedValue) String() string {
	switch v.Type {
	case TypeEnum:
		return v.Enum.Name
	case TypeRelation, TypePerson:
		return v.Link.String()
	case TypeDate:
		return v.Date.String()
	case TypeInteger, TypeDecimal:
		return v.Number.String()
	}
	return v.Text
}

// Wikilink is D5.1's on-disk relation form: a quoted wikilink.
type Wikilink struct {
	// Target is the note name the link points at — the join key before the
	// index resolves it to a record ID.
	Target string
	// Heading is an optional `#section` suffix.
	Heading string
	// Display is an optional `|alias`. It is NEVER identity: §8 R-8 compares
	// relations by target, never by display text.
	Display string
	// Raw is the link exactly as written.
	Raw string
}

func (w Wikilink) String() string { return w.Raw }

// DateValue is a day or an instant. D3/§8 R-7: both are the same declared type
// and compare directly, so a day is held as an instant at midnight UTC with a
// flag recording which was written.
type DateValue struct {
	Instant time.Time
	// HasTime distinguishes `2026-08-25` from `2026-08-25T09:30:00Z` for
	// rendering. It does NOT affect comparison.
	HasTime bool
}

func (d DateValue) String() string {
	if d.HasTime {
		return d.Instant.Format(time.RFC3339)
	}
	return d.Instant.Format("2006-01-02")
}

// ValueError is a non-conforming value, with everything a report needs.
//
// FR-006/FR-011/FR-042 all demand the same thing in different words: say what
// was expected. A report that says only "invalid" is a failure of the whole
// design, so this type makes Expected non-optional in practice.
type ValueError struct {
	// Reason is the human sentence.
	Reason string
	// Expected is the shape that would have been accepted.
	Expected string
	// Got is what the file actually held.
	Got string
	// Permitted is the closed set, where one exists (FR-011's enum values).
	Permitted []string
	// Code classifies the fault for callers that branch on it.
	Code FindingCode
}

func (e *ValueError) Error() string {
	msg := e.Reason
	if e.Expected != "" {
		msg += "; expected " + e.Expected
	}
	if len(e.Permitted) > 0 {
		msg += "; permitted values are " + strings.Join(e.Permitted, ", ")
	}
	return msg
}

// ParseValue converts one scalar frontmatter node into a typed value for a
// declared property. It handles ONE element: arity is the caller's business
// (validate.go), because a list's elements each come through here.
func ParseValue(p *Property, n Node) (TypedValue, *ValueError) {
	switch p.Type {
	case TypeText:
		return parseTextValue(p, n)
	case TypeEnum:
		return parseEnumValueNode(p, n)
	case TypeRelation, TypePerson:
		return parseLinkValue(p, n)
	case TypeDate:
		return parseDateValue(p, n)
	case TypeInteger:
		return parseIntegerValue(p, n)
	case TypeDecimal:
		return parseDecimalValue(p, n)
	}
	return TypedValue{}, &ValueError{
		Code:     FindingUnsupportedType,
		Reason:   fmt.Sprintf("property %q declares unsupported type %q", p.Name, p.Type),
		Expected: p.ExpectedShape(),
	}
}

func mustBeScalar(p *Property, n Node) *ValueError {
	if n.Kind == KindScalar {
		return nil
	}
	return &ValueError{
		Code:     FindingWrongShape,
		Reason:   fmt.Sprintf("property %q holds %s", p.Name, n.Kind),
		Expected: p.ExpectedShape(),
		Got:      n.Kind.String(),
	}
}

func parseTextValue(p *Property, n Node) (TypedValue, *ValueError) {
	if err := mustBeScalar(p, n); err != nil {
		return TypedValue{}, err
	}
	// D3: text is "never validated". An empty string is a VALUE, distinct from
	// absent (DS-1, §8 R-3), and is accepted here without comment.
	return TypedValue{Type: TypeText, Raw: n.Text, Text: n.Text}, nil
}

func parseEnumValueNode(p *Property, n Node) (TypedValue, *ValueError) {
	if err := mustBeScalar(p, n); err != nil {
		return TypedValue{}, err
	}
	declared, ok := p.ResolveEnum(n.Text)
	if !ok {
		// FR-011 — reject, listing the permitted values. Matching is
		// case-INSENSITIVE in full Unicode (FR-011a), so `Active` RESOLVES to a
		// declared `active` and only a value that matches none of them is
		// rejected. The message says so, because "not one of the declared
		// values" over a case difference would send the author to fix the one
		// thing that is not wrong.
		return TypedValue{}, &ValueError{
			Code:      FindingEnumNotPermitted,
			Reason:    fmt.Sprintf("property %q holds %q, which is not one of the declared values for this enum (matching ignores case)", p.Name, n.Text),
			Expected:  p.ExpectedShape(),
			Got:       n.Text,
			Permitted: p.PermittedValues(),
		}
	}
	// Raw keeps the file's own spelling — FR-011c renders that, never the
	// declared one — while Enum carries the DECLARED value, which is what
	// equality and grouping compare.
	return TypedValue{Type: TypeEnum, Raw: n.Text, Enum: declared}, nil
}

func parseLinkValue(p *Property, n Node) (TypedValue, *ValueError) {
	if err := mustBeScalar(p, n); err != nil {
		return TypedValue{}, err
	}
	link, ok := ParseWikilink(n.Text)
	if !ok {
		return TypedValue{}, &ValueError{
			Code:     FindingNotAWikilink,
			Reason:   fmt.Sprintf("property %q holds %q, which is not a wikilink", p.Name, n.Text),
			Expected: p.ExpectedShape(),
			Got:      n.Text,
		}
	}
	return TypedValue{Type: p.Type, Raw: n.Text, Link: link}, nil
}

// ParseWikilink reads `[[Target]]`, `[[Target#Heading]]`, `[[Target|Display]]`
// or a combination. It returns ok=false for anything that is not a wikilink.
//
// D5.1 is why this shape and not an Omnipus-specific encoding: remove Omnipus
// and the relation is still a working link in the operator's editor.
func ParseWikilink(text string) (Wikilink, bool) {
	s := strings.TrimSpace(text)
	if !strings.HasPrefix(s, "[[") || !strings.HasSuffix(s, "]]") || len(s) <= 4 {
		return Wikilink{}, false
	}
	inner := s[2 : len(s)-2]
	if strings.Contains(inner, "[[") || strings.Contains(inner, "]]") {
		return Wikilink{}, false
	}
	w := Wikilink{Raw: s}
	if i := strings.Index(inner, "|"); i >= 0 {
		w.Display = strings.TrimSpace(inner[i+1:])
		inner = inner[:i]
	}
	if i := strings.Index(inner, "#"); i >= 0 {
		w.Heading = strings.TrimSpace(inner[i+1:])
		inner = inner[:i]
	}
	w.Target = strings.TrimSpace(inner)
	if w.Target == "" {
		return Wikilink{}, false
	}
	return w, true
}

// dateLayouts are accepted in order. A bare day is the common case; the
// RFC-3339 forms cover an instant. Nothing else is accepted — a date stored as
// free text is exactly the failure D3 names ("last_contacted stored as text,
// silently unmatchable"), so accepting "25 Aug 2026" here would recreate it.
var dateLayouts = []struct {
	layout  string
	hasTime bool
}{
	{"2006-01-02", false},
	{time.RFC3339, true},
	{"2006-01-02T15:04:05", true},
	{"2006-01-02 15:04:05", true},
	{"2006-01-02T15:04", true},
	{"2006-01-02 15:04", true},
}

func parseDateValue(p *Property, n Node) (TypedValue, *ValueError) {
	if err := mustBeScalar(p, n); err != nil {
		return TypedValue{}, err
	}
	s := strings.TrimSpace(n.Text)
	for _, l := range dateLayouts {
		t, err := time.Parse(l.layout, s)
		if err == nil {
			return TypedValue{Type: TypeDate, Raw: n.Text, Date: DateValue{Instant: t.UTC(), HasTime: l.hasTime}}, nil
		}
	}
	return TypedValue{}, &ValueError{
		Code:     FindingNotADate,
		Reason:   fmt.Sprintf("property %q holds %q, which is not a valid date", p.Name, n.Text),
		Expected: p.ExpectedShape(),
		Got:      n.Text,
	}
}

// parseDecimalValue reads a `decimal`: an exact number of arbitrary magnitude,
// with at most maxDecimalScale fractional places.
//
// The bound is maxDecimalScale (100) and it is DELIBERATELY GENEROUS. The
// retired `money` type bounded scale at 12 — a currency-shaped limit for a type
// that is not currency-shaped — and ADR-068 D3 is explicit that the bound dies
// with money and must not be inherited: "make sure its precision after digits
// is high enough to be precise".
//
// A value beyond the bound is REFUSED, naming the bound and its own scale. It
// is never rounded to fit: rounding to satisfy a bound is a silent change to a
// number, which is the whole class of failure this type exists to close.
func parseDecimalValue(p *Property, n Node) (TypedValue, *ValueError) {
	if err := mustBeScalar(p, n); err != nil {
		return TypedValue{}, err
	}
	d, err := ParseDecimal(n.Text)
	if err != nil {
		// DS-1's `PLACEHOLDER — unknown` in a numeric property lands here, and
		// FR-026 requires the RECORD to be named when this excludes it from an
		// aggregate. The caller attaches the record; this names the value.
		//
		// The parser's own error is carried through rather than flattened to
		// "is not a number", because it is the half that says WHICH rule was
		// broken — a thousands separator, an exponent out of range, a scale
		// past the bound — and the operator's fix differs for each.
		return TypedValue{}, &ValueError{
			Code:     FindingNotANumber,
			Reason:   fmt.Sprintf("property %q holds %q, which is not a decimal: %v", p.Name, n.Text, err),
			Expected: p.ExpectedShape(),
			Got:      n.Text,
		}
	}
	return TypedValue{Type: TypeDecimal, Raw: n.Text, Number: d}, nil
}

// parseIntegerValue reads an `integer`: a signed 64-bit whole number.
//
// TWO REFUSALS, and they are different faults with different fixes, so they get
// different sentences:
//
//	FRACTIONAL   `3.5` in an integer property. The author declared a whole
//	             number and wrote a fraction. Truncating to 3 or rounding to 4
//	             would both be a silent change to the value, so neither happens
//	             — the remedy named is to declare the property `decimal`.
//	OUT OF RANGE `9223372036854775808` is one past int64. It is REFUSED, naming
//	             the bound. This is D3's "a large identifier silently truncated"
//	             — SQLite saturates such a CAST to MaxInt64 without a word, and
//	             a binary float would round it; neither is acceptable, so the
//	             answer is a refusal.
//
// The value is parsed through ParseDecimal, not strconv.ParseInt, so an integer
// and a decimal accept exactly the same NOTATION (R-1 makes them one comparison
// type, and two parsers would eventually disagree about what a number looks
// like). The int64 range is then a bound applied on top, not a second grammar.
func parseIntegerValue(p *Property, n Node) (TypedValue, *ValueError) {
	if err := mustBeScalar(p, n); err != nil {
		return TypedValue{}, err
	}
	d, err := ParseDecimal(n.Text)
	if err != nil {
		return TypedValue{}, &ValueError{
			Code:     FindingNotANumber,
			Reason:   fmt.Sprintf("property %q holds %q, which is not an integer: %v", p.Name, n.Text, err),
			Expected: p.ExpectedShape(),
			Got:      n.Text,
		}
	}
	whole, exact := d.Int64()
	switch {
	case !exact && d.IsFractional():
		return TypedValue{}, &ValueError{
			Code:     FindingIntegerNotWhole,
			Reason:   fmt.Sprintf("property %q holds %q, which is not a whole number; an integer property is never rounded or truncated to fit, so either write a whole number or declare the property `decimal`", p.Name, n.Text),
			Expected: p.ExpectedShape(),
			Got:      n.Text,
		}
	case !exact:
		return TypedValue{}, &ValueError{
			Code:     FindingIntegerOutOfRange,
			Reason:   fmt.Sprintf("property %q holds %q, which is outside the range of a 64-bit integer (%d to %d); it is refused rather than truncated or widened to a float", p.Name, n.Text, math.MinInt64, math.MaxInt64),
			Expected: p.ExpectedShape(),
			Got:      n.Text,
		}
	}
	// Re-made at scale 0 from the int64, so an integer value is canonical
	// however it was spelled: `3`, `3.0` and `3e0` all become the same Decimal,
	// and R-1's "3 = 3.0 is true" holds without the comparator special-casing
	// the pair.
	return TypedValue{Type: TypeInteger, Raw: n.Text, Number: DecimalFromInt64(whole, 0)}, nil
}
