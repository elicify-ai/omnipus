// Omnipus — ADR-068 D3: turning a lexical frontmatter value into a typed one.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
	"sort"
	"strings"
	"time"
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
//  2. NOTHING BECOMES A FLOAT. `number` and `money` parse from source text
//     into Decimal (decimal.go). FR-020b is a promise about the whole path,
//     and this is the only place in the path that touches numeric text.
// ---------------------------------------------------------------------------

// TypedValue is one conforming value of one declared property.
type TypedValue struct {
	Type PropertyType
	// Raw is the source text exactly as the file had it, kept so a report can
	// quote what the operator actually wrote rather than a re-rendering of it.
	Raw string

	// Text carries a `text` value.
	Text string
	// Enum carries an `enum` value, INCLUDING its declared position — FR-010's
	// sort key travels with the value so no later code has to re-derive it and
	// get it wrong.
	Enum EnumValue
	// Link carries a `relation` or `person` value.
	Link Wikilink
	// Date carries a `date` value.
	Date DateValue
	// Number carries a `number` value, exactly.
	Number Decimal
	// Money carries a `money` value, exactly, with its currency attached.
	Money Money
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
	case TypeNumber:
		return v.Number.String()
	case TypeMoney:
		return v.Money.String()
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
	case TypeNumber:
		return parseNumberValue(p, n)
	case TypeMoney:
		return parseMoneyValue(p, n)
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
	pos, ok := p.EnumPosition(n.Text)
	if !ok {
		// FR-011 — reject, listing the permitted values. Matching is
		// EXACT-CASE, so `Active` fails against `active` and says so, rather
		// than quietly creating a second de-facto value (D4).
		return TypedValue{}, &ValueError{
			Code:      FindingEnumNotPermitted,
			Reason:    fmt.Sprintf("property %q holds %q, which is not one of the declared values for this enum (matching is exact, including case)", p.Name, n.Text),
			Expected:  p.ExpectedShape(),
			Got:       n.Text,
			Permitted: p.PermittedValues(),
		}
	}
	return TypedValue{Type: TypeEnum, Raw: n.Text, Enum: p.Values[pos]}, nil
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

func parseNumberValue(p *Property, n Node) (TypedValue, *ValueError) {
	if err := mustBeScalar(p, n); err != nil {
		return TypedValue{}, err
	}
	d, err := ParseDecimal(n.Text)
	if err != nil {
		// DS-1's `PLACEHOLDER — unknown` in a numeric property lands here, and
		// FR-026 requires the RECORD to be named when this excludes it from an
		// aggregate. The caller attaches the record; this names the value.
		return TypedValue{}, &ValueError{
			Code:     FindingNotANumber,
			Reason:   fmt.Sprintf("property %q holds %q, which is not a number", p.Name, n.Text),
			Expected: p.ExpectedShape(),
			Got:      n.Text,
		}
	}
	return TypedValue{Type: TypeNumber, Raw: n.Text, Number: d}, nil
}

// parseMoneyValue accepts the two forms a human actually writes, and rejects
// the one that loses information.
//
//	arr: "349.98 SGD"                                 accepted (scale inferred: 2)
//	arr: "SGD 349.98"                                 accepted
//	arr: {amount: 349.98, currency: SGD}              accepted (scale inferred: 2)
//	arr: {amount: 34998, currency: SGD, scale: 2}     accepted (minor units, O-2)
//	arr: 349.98                                       REJECTED — no currency (FR-012)
//	arr: {amount: 349.98}                             REJECTED — no currency (FR-012)
//	arr: {amount: 349.98, currency: SGD, scale: 2}    REJECTED — ambiguous
//	arr: {amount: 34998, currency: SGD, scal: 2}      REJECTED — unknown key `scal`
//
// That last one is the whole reason the key set is closed. Read only the keys
// it recognises and the parser answers 34998 SGD to a note that says 349.98 —
// a hundredfold error from one dropped letter, with nothing reported. Every
// rejection here names the token the operator actually has to change.
//
// The last one matters: ADR-068 O-2 defines the stored amount as INTEGER MINOR
// UNITS, so `amount` alongside an explicit `scale` must be an integer. Reading
// `349.98` with `scale: 2` as either 349.98 or 3.4998 is a coin toss, and this
// package does not toss coins over money.
//
// Whichever form it arrives in, an accepted money value ends up inside the
// bounds RecordMoney.yaml imposes — scale 0..maxMoneyScale, no exponent — and
// there are two roads to that, both of which must stay closed:
//
//	SCALE INFERRED   the inline forms and {amount, currency} parse their
//	                 amount through parseMoneyAmount (money.go), which applies
//	                 both bounds. Bounding one of these and not the others is
//	                 the defect this arrangement exists to prevent.
//	SCALE DECLARED   {amount, currency, scale} never reaches parseMoneyAmount.
//	                 It cannot breach either bound: the amount must be an
//	                 integer literal (no exponent, no fractional digits) and
//	                 the declared scale is range-checked below.
func parseMoneyValue(p *Property, n Node) (TypedValue, *ValueError) {
	switch n.Kind {
	case KindScalar:
		return parseMoneyScalar(p, n)
	case KindMapping:
		return parseMoneyMapping(p, n)
	}
	return TypedValue{}, &ValueError{
		Code:     FindingWrongShape,
		Reason:   fmt.Sprintf("property %q holds %s", p.Name, n.Kind),
		Expected: p.ExpectedShape(),
		Got:      n.Kind.String(),
	}
}

func parseMoneyScalar(p *Property, n Node) (TypedValue, *ValueError) {
	fields := strings.Fields(strings.TrimSpace(n.Text))
	switch len(fields) {
	case 1:
		// A bare amount. FR-012 is explicit: a money value missing its
		// currency is REJECTED. Assuming a vault currency here is precisely
		// the "two loose fields nothing keeps together" failure D3 names.
		return TypedValue{}, moneyValueError(p, n.Text, &MissingCurrencyError{Amount: fields[0]}, FindingMoneyNoCurrency)
	case 2:
		// Two steps, deliberately. ParseDecimal answers "is this field the
		// amount at all?", which is what decides the currency-first swap below;
		// only then does parseMoneyAmount apply money's own bounds. Collapsing
		// them would make "1e3 USD" look like a currency-first value and report
		// the currency as malformed, which is not where the operator's fix is.
		amountText, currency := fields[0], fields[1]
		_, amountErr := ParseDecimal(amountText)
		if amountErr != nil {
			// "SGD 349.98" is a form a human really writes, so the other order
			// is tried — but ONLY when the other field genuinely IS an amount.
			//
			// Swapping on ANY first-field failure is what made every non-exponent
			// malformation name the wrong token: "1,000 USD" reported `"USD" is
			// not an amount`, sending the operator to fix the one field that was
			// correct while the thousands separator they actually mistyped went
			// unnamed. The swap now has to earn itself.
			if _, err := ParseDecimal(fields[1]); err == nil {
				amountText, currency = fields[1], fields[0]
				amountErr = nil
			}
		}
		if amountErr != nil {
			return TypedValue{}, moneyValueError(p, n.Text, fmt.Errorf("%q is not an amount", amountText), FindingMoneyMalformed)
		}
		d, err := parseMoneyAmount(amountText)
		if err != nil {
			return TypedValue{}, moneyValueError(p, n.Text, err, FindingMoneyMalformed)
		}
		if err := ValidateCurrency(currency); err != nil {
			return TypedValue{}, moneyValueError(p, n.Text, err, FindingMoneyBadCurrency)
		}
		return TypedValue{Type: TypeMoney, Raw: n.Text, Money: Money{Amount: d, Currency: currency}}, nil
	}
	return TypedValue{}, moneyValueError(p, n.Text, fmt.Errorf("%q is not an amount and a currency", n.Text), FindingMoneyMalformed)
}

func parseMoneyMapping(p *Property, n Node) (TypedValue, *ValueError) {
	amountNode, hasAmount := n.Fields["amount"]
	currencyNode, hasCurrency := n.Fields["currency"]
	scaleNode, hasScale := n.Fields["scale"]

	raw := renderMoneyMapping(n)

	// FIRST, before any key is read for its value: a key this parser does not
	// know is a key whose meaning was thrown away.
	//
	// `{amount: 34998, currency: SGD, scal: 2}` used to parse as 34998 SGD —
	// thirty-five thousand dollars where the author wrote three hundred and
	// forty-nine ninety-eight — with no finding and no warning, because the
	// parser read the three keys it recognised and never looked at the fourth.
	// One dropped letter, a hundredfold error, silence.
	//
	// RecordMoney.yaml is `additionalProperties: false`, so the wire refuses
	// exactly this. The Go parser now refuses it too, at the same strictness and
	// naming the key, because an author who writes something meaningful must
	// never be told nothing when we throw it away.
	if unknown := unknownMoneyKeys(n); len(unknown) > 0 {
		return TypedValue{}, moneyValueError(p, raw,
			fmt.Errorf("unknown %s %s in a money value; a money mapping carries only `amount`, `currency` and `scale` (a mistyped key would otherwise be dropped in silence, changing what the value means)",
				pluralise("key", len(unknown)), quoteJoin(unknown)),
			FindingMoneyMalformed)
	}

	switch {
	case !hasAmount || amountNode.Kind == KindNull:
		// FR-007/R-3: an explicit null is ABSENT, so it reports as absent.
		return TypedValue{}, moneyValueError(p, raw, fmt.Errorf("no `amount`"), FindingMoneyMalformed)
	case amountNode.Kind != KindScalar:
		// The amount IS there — saying "no `amount`" here told the operator to
		// add a field they had already written. Name the shape instead.
		return TypedValue{}, moneyValueError(p, raw,
			fmt.Errorf("`amount` holds %s; a money amount is a single value", amountNode.Kind),
			FindingMoneyMalformed)
	}

	switch {
	case !hasCurrency || currencyNode.Kind == KindNull || (currencyNode.Kind == KindScalar && strings.TrimSpace(currencyNode.Text) == ""):
		return TypedValue{}, moneyValueError(p, raw, &MissingCurrencyError{Amount: amountNode.Text}, FindingMoneyNoCurrency)
	case currencyNode.Kind != KindScalar:
		// Same split, same reason: `currency: [SGD]` is a currency written in
		// the wrong shape, not a missing one.
		return TypedValue{}, moneyValueError(p, raw,
			fmt.Errorf("`currency` holds %s; a currency is a single ISO-4217 code, e.g. SGD", currencyNode.Kind),
			FindingMoneyMalformed)
	}
	if err := ValidateCurrency(currencyNode.Text); err != nil {
		return TypedValue{}, moneyValueError(p, raw, err, FindingMoneyBadCurrency)
	}

	if !hasScale {
		// parseMoneyAmount, not ParseDecimal: this branch infers the scale from
		// the amount's own fractional digits, so it is the branch that could
		// mint a scale the wire cannot carry.
		d, err := parseMoneyAmount(amountNode.Text)
		if err != nil {
			return TypedValue{}, moneyValueError(p, raw, fmt.Errorf("`amount` %q: %w", amountNode.Text, err), FindingMoneyMalformed)
		}
		return TypedValue{Type: TypeMoney, Raw: raw, Money: Money{Amount: d, Currency: currencyNode.Text}}, nil
	}

	if scaleNode.Kind != KindScalar || !isIntegerLiteral(scaleNode.Text) {
		return TypedValue{}, moneyValueError(p, raw, fmt.Errorf("`scale` must be a whole number of minor-unit digits, found %q", scaleNode.Text), FindingMoneyMalformed)
	}
	scaleDec, err := ParseDecimal(scaleNode.Text)
	if err != nil || scaleDec.Scale() != 0 {
		return TypedValue{}, moneyValueError(p, raw, fmt.Errorf("`scale` must be a whole number, found %q", scaleNode.Text), FindingMoneyMalformed)
	}
	scale := scaleDec.Unscaled()
	// Bounded by maxMoneyScale, NOT maxDecimalScale. The wire caps `scale` at 12
	// (RecordMoney.yaml), so a value with scale 13..100 validated in Go and then
	// could not be serialised at all — accepted on disk, unrepresentable to a
	// caller. The two bounds are now the same number for that reason.
	if !scale.IsInt64() || scale.Int64() < 0 || scale.Int64() > maxMoneyScale {
		return TypedValue{}, moneyValueError(p, raw, fmt.Errorf("`scale` must be between 0 and %d, found %q", maxMoneyScale, scaleNode.Text), FindingMoneyMalformed)
	}
	if !isIntegerLiteral(amountNode.Text) {
		// Three distinct faults live behind "not an integer literal", and an
		// operator fixes each differently. Collapsing them into the ambiguity
		// message told someone who wrote `1e3` to "write the amount in minor
		// units" — which they had, in the one notation money does not accept.
		switch _, derr := ParseDecimal(amountNode.Text); {
		case derr != nil:
			return TypedValue{}, moneyValueError(p, raw, fmt.Errorf("`amount` %q is not a whole number of minor units", amountNode.Text), FindingMoneyMalformed)
		case strings.ContainsAny(amountNode.Text, "eE"):
			return TypedValue{}, moneyValueError(p, raw,
				fmt.Errorf("`amount` %q uses exponent notation, which a money amount does not accept (write the minor units out in full)", amountNode.Text),
				FindingMoneyMalformed)
		}
		return TypedValue{}, moneyValueError(p, raw,
			fmt.Errorf("`amount` is %q, but a declared `scale` means the amount is an integer count of minor units (ADR-068 O-2) — write {amount: %s, currency: %s} and let the scale be inferred, or write the amount in minor units",
				amountNode.Text, amountNode.Text, currencyNode.Text), FindingMoneyMalformed)
	}
	minor, derr := ParseDecimal(amountNode.Text)
	if derr != nil {
		return TypedValue{}, moneyValueError(p, raw, fmt.Errorf("`amount` %q is not a whole number of minor units", amountNode.Text), FindingMoneyMalformed)
	}
	// The amount is in minor units at `scale`, so the Decimal is that integer
	// carrying that scale — no division, no float, exact by construction.
	value := NewDecimal(minor.Unscaled(), int32(scale.Int64()))
	return TypedValue{Type: TypeMoney, Raw: raw, Money: Money{Amount: value, Currency: currencyNode.Text}}, nil
}

// moneyMappingKeys is the CLOSED set of keys a money mapping may carry. It is
// the same closed set RecordMoney.yaml declares with `additionalProperties:
// false` — kept here as data, so the Go parser and the wire refuse the same
// mappings rather than disagreeing about which files are readable.
var moneyMappingKeys = map[string]struct{}{
	"amount":   {},
	"currency": {},
	"scale":    {},
}

// unknownMoneyKeys lists every key of a money mapping that is not in that
// closed set, in the order the author wrote them.
//
// Keys is the document order the frontmatter parser records and Fields is what
// the money parser actually reads from; production fills both, but a key
// reaching one and not the other is still a key the author wrote, so neither
// route escapes the check.
func unknownMoneyKeys(n Node) []string {
	var unknown []string
	seen := make(map[string]struct{}, len(n.Keys))
	for _, k := range n.Keys {
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		if _, known := moneyMappingKeys[k]; !known {
			unknown = append(unknown, k)
		}
	}
	// Map iteration has no order, so anything found only here is sorted before
	// it joins the list — a rejection that reorders itself between runs is not
	// something an operator can diff.
	var unordered []string
	for k := range n.Fields {
		if _, listed := seen[k]; listed {
			continue
		}
		if _, known := moneyMappingKeys[k]; !known {
			unordered = append(unordered, k)
		}
	}
	sort.Strings(unordered)
	return append(unknown, unordered...)
}

// quoteJoin renders a list of names as `a`, `b`, `c` for a report.
func quoteJoin(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, "`"+n+"`")
	}
	return strings.Join(quoted, ", ")
}

// pluralise adds an "s" for any count that is not one, so a rejection reads as
// a sentence rather than as "1 keys".
func pluralise(word string, n int) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

func renderMoneyMapping(n Node) string {
	parts := make([]string, 0, len(n.Keys))
	for _, k := range n.Keys {
		parts = append(parts, k+": "+n.Fields[k].Text)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func moneyValueError(p *Property, raw string, cause error, code FindingCode) *ValueError {
	return &ValueError{
		Code:     code,
		Reason:   fmt.Sprintf("property %q: %v", p.Name, cause),
		Expected: p.ExpectedShape(),
		Got:      raw,
	}
}
