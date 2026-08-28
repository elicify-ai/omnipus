// Omnipus — ADR-068: per-record validation that names the fault and the shape.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// A report that says only "invalid" is a failure of this whole layer. Every
// finding here carries: which record, which property, what was found, what was
// expected, and — where a closed set exists — what would have been permitted.
//
// The requirements this implements, and the failure each one closes:
//
//	FR-006  ARITY. "A scalar property never silently becomes a list." ADR-068
//	        D3.1 records this as the single most-reported failure in the
//	        research corpus: the editor converts a scalar to a list the instant
//	        a second value is added, every query written against it returns
//	        nothing, and NOTHING REPORTS AN ERROR. So arity is checked before
//	        the value is even looked at, in both directions.
//	FR-007  ABSENT is a distinct state from every value. Not empty string, not
//	        zero, not false, not an empty list.
//	FR-011  an enum value outside the declared set is rejected, listing the
//	        permitted values.
//	FR-005  a note whose type matches no schema is an ORDINARY NOTE. It is
//	        reported as unrecognised, with zero findings, and that is success.
// ---------------------------------------------------------------------------

// FindingCode classifies a validation fault. Callers branch on the code; humans
// read the reason.
type FindingCode string

const (
	// FindingFrontmatterUnreadable — the YAML did not parse.
	FindingFrontmatterUnreadable FindingCode = "frontmatter_unreadable"
	// FindingDuplicateKey — the frontmatter declares one property twice.
	FindingDuplicateKey FindingCode = "duplicate_property_key"
	// FindingMissingRequired — a required property is absent (FR-007's absent,
	// which includes an explicit null).
	FindingMissingRequired FindingCode = "missing_required_property"
	// FindingArity — FR-006. The property is declared scalar and holds a list,
	// or declared many and holds a single value.
	FindingArity FindingCode = "arity_violation"
	// FindingWrongShape — the value is neither a scalar nor an accepted
	// structure for its declared type (e.g. a mapping in a text property).
	FindingWrongShape FindingCode = "wrong_shape"
	// FindingEnumNotPermitted — FR-011.
	FindingEnumNotPermitted FindingCode = "enum_value_not_permitted"
	// FindingNotAWikilink — a relation or person that is not a wikilink (D5.1).
	FindingNotAWikilink FindingCode = "not_a_wikilink"
	// FindingNotADate — an unparseable date.
	FindingNotADate FindingCode = "not_a_date"
	// FindingNotANumber — a non-numeric value in a numeric property. DS-1's
	// `PLACEHOLDER — unknown` lands here.
	FindingNotANumber FindingCode = "not_a_number"
	// FindingIntegerNotWhole — a fractional value in an `integer` property.
	// It is a SEPARATE code from FindingNotANumber because `3.5` parses
	// perfectly well as a number: the fault is the declared type, and the
	// remedy is to declare the property `decimal`, not to fix the digits.
	FindingIntegerNotWhole FindingCode = "integer_not_whole"
	// FindingIntegerOutOfRange — a whole number outside int64 in an `integer`
	// property (FR-013). D3's "a large identifier silently truncated": it is
	// refused naming the bound, never saturated and never widened to a float.
	FindingIntegerOutOfRange FindingCode = "integer_out_of_range"
	// FindingUndeclaredProperty — a frontmatter key with no declaration. OFF by
	// default; see ValidateOptions.
	FindingUndeclaredProperty FindingCode = "undeclared_property"
	// FindingUnsupportedType — defensive; a schema that loaded cannot reach it.
	FindingUnsupportedType FindingCode = "unsupported_property_type"
	// FindingDuplicateListValue — the same value twice in one list property.
	FindingDuplicateListValue FindingCode = "duplicate_list_value"
)

// Severity separates "this record is wrong" from "you may want to know".
type Severity string

const (
	// SeverityError means the record does not conform to its schema.
	SeverityError Severity = "error"
	// SeverityWarning means the record conforms but something is worth saying.
	SeverityWarning Severity = "warning"
)

// Finding is one fault in one record.
type Finding struct {
	// RecordPath is the note. Always set — FR-026's "names the offending
	// records" is not satisfiable without it.
	RecordPath string
	// RecordType is the declared type, where the note declared one.
	RecordType string
	// RecordID is D7's identifier, where the note carries one.
	RecordID string

	// Property is the property at fault, "" for a whole-record finding.
	Property string
	// ElementIndex is the 0-based position within a list property, or -1 when
	// the finding is about the property as a whole. A list where element 3 is
	// wrong must say THREE, not "one of them".
	ElementIndex int

	Code     FindingCode
	Severity Severity

	// Reason is the human sentence: what is wrong.
	Reason string
	// Expected is the shape that would have been accepted. Non-empty for every
	// value-level finding — this is the field that stops a report saying only
	// "invalid".
	Expected string
	// Got is what the file actually held.
	Got string
	// Permitted lists a closed set where one applies (FR-011).
	Permitted []string
	// Line is the 1-based source line, where known.
	Line int
}

// String renders a finding as one reviewable line.
func (f Finding) String() string {
	var b strings.Builder
	b.WriteString(f.RecordPath)
	if f.Property != "" {
		b.WriteString(": ")
		b.WriteString(f.Property)
		if f.ElementIndex >= 0 {
			fmt.Fprintf(&b, "[%d]", f.ElementIndex)
		}
	}
	b.WriteString(" — ")
	b.WriteString(f.Reason)
	if f.Expected != "" {
		b.WriteString("; expected ")
		b.WriteString(f.Expected)
	}
	if len(f.Permitted) > 0 {
		b.WriteString("; permitted values are ")
		b.WriteString(strings.Join(f.Permitted, ", "))
	}
	return b.String()
}

// RecordReport is the verdict on ONE record. Validation is reported per record
// because that is the unit an operator fixes.
type RecordReport struct {
	Path string
	Type string
	ID   string

	// Recognised is false when the note declares no type, or a type no schema
	// declares. FR-005: that is an ordinary note and NOT a failure — Findings
	// is empty and Valid() is true.
	Recognised bool

	Findings []Finding
}

// Valid reports whether the record conforms. Warnings do not make it invalid.
func (r RecordReport) Valid() bool {
	for _, f := range r.Findings {
		if f.Severity == SeverityError {
			return false
		}
	}
	return true
}

// Errors returns only the error-severity findings.
func (r RecordReport) Errors() []Finding {
	out := make([]Finding, 0, len(r.Findings))
	for _, f := range r.Findings {
		if f.Severity == SeverityError {
			out = append(out, f)
		}
	}
	return out
}

// ValidationReport is the verdict across a set of records.
type ValidationReport struct {
	// Records holds one entry per record examined, in the order given.
	Records []RecordReport
}

// Valid reports whether every record conformed.
func (r *ValidationReport) Valid() bool {
	for _, rec := range r.Records {
		if !rec.Valid() {
			return false
		}
	}
	return true
}

// Findings flattens every finding across every record.
func (r *ValidationReport) Findings() []Finding {
	n := 0
	for _, rec := range r.Records {
		n += len(rec.Findings)
	}
	out := make([]Finding, 0, n)
	for _, rec := range r.Records {
		out = append(out, rec.Findings...)
	}
	return out
}

// InvalidRecords lists the paths that failed, sorted. This is the list a query
// names when it excludes records from an answer (FR-026).
func (r *ValidationReport) InvalidRecords() []string {
	seen := map[string]struct{}{}
	for _, rec := range r.Records {
		if !rec.Valid() {
			seen[rec.Path] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ValidateOptions tunes what is reported. The defaults are deliberately quiet
// about things that are not faults.
type ValidateOptions struct {
	// ReportUndeclaredProperties adds a WARNING for each frontmatter key with
	// no declaration in the schema.
	//
	// Off by default, and the reason is ADR-068 D8: "the operator's fields are
	// not renamed by us". A real vault's notes carry `tags`, `aliases`,
	// `cssclasses` and a decade of the operator's own conventions. Reporting
	// every one of them as a finding would bury the faults that matter under
	// noise the operator did not ask us to have an opinion about.
	ReportUndeclaredProperties bool

	// ReportDuplicateListValues adds a WARNING when a list property holds the
	// same value twice. Off by default: a repeat is legal, occasionally
	// intentional, and never ambiguous.
	//
	// "The same value" means what spec §8 means by it — see
	// duplicateListFindings. It is NOT "the same text".
	ReportDuplicateListValues bool

	// ResolveRelation supplies the record identity behind a wikilink, for the
	// duplicate check's §8 R-8 comparison of `relation` and `person` values.
	//
	// It is the comparator's own seam (compare_oracle.go's RelationResolver),
	// handed through rather than reimplemented, so a caller that HAS a vault
	// index gets real identity: two different note names the index resolved to
	// one record are one value, and a list naming it twice is a repeat.
	//
	// nil is the ordinary case — validation is a per-record operation and most
	// callers have no index. It then falls back to linkTargetIsIdentity, which
	// is the most any single record can know on its own.
	ResolveRelation RelationResolver
}

// ValidateRecord validates ONE record against a schema set.
//
// FR-005 in the first four lines: no declared type, or a type the vault does
// not declare, means an ordinary note — Recognised=false, zero findings, and
// no error anywhere.
func ValidateRecord(set *SchemaSet, rec Record, opts ValidateOptions) RecordReport {
	report := RecordReport{Path: rec.Path, Type: rec.TypeName(), ID: rec.ID()}

	if rec.ParseError != "" {
		// A note we cannot read is named, not dropped. It may or may not be a
		// record — we cannot tell — and pretending it is not one would remove
		// it from every answer without saying so.
		report.Findings = append(report.Findings, Finding{
			RecordPath:   rec.Path,
			ElementIndex: -1,
			Code:         FindingFrontmatterUnreadable,
			Severity:     SeverityError,
			Reason:       rec.ParseError,
			Expected:     "a YAML frontmatter block delimited by ---",
		})
		return report
	}

	schema, ok := set.Get(report.Type)
	if !ok {
		return report // FR-005 — an ordinary note.
	}
	report.Recognised = true

	for _, problem := range rec.Frontmatter.Problems {
		report.Findings = append(report.Findings, Finding{
			RecordPath:   rec.Path,
			RecordType:   report.Type,
			RecordID:     report.ID,
			ElementIndex: -1,
			Code:         FindingDuplicateKey,
			Severity:     SeverityError,
			Reason:       problem,
			Expected:     "each property declared at most once",
		})
	}

	// Walk the SCHEMA, not the file, so a required property that is missing
	// entirely is still reached. FR-007's absence is only visible from this
	// direction.
	for _, name := range schema.PropertyOrder {
		prop := schema.Properties[name]
		report.Findings = append(report.Findings, validateProperty(rec, report, prop, opts)...)
	}

	if opts.ReportUndeclaredProperties {
		for _, key := range rec.Frontmatter.Keys {
			if key == RecordTypeKey || key == RecordIDKey || key == RecordIDKeyNamespaced {
				continue
			}
			if _, declared := schema.Property(key); declared {
				continue
			}
			n := rec.Frontmatter.Values[key]
			report.Findings = append(report.Findings, Finding{
				RecordPath:   rec.Path,
				RecordType:   report.Type,
				RecordID:     report.ID,
				Property:     key,
				ElementIndex: -1,
				Code:         FindingUndeclaredProperty,
				Severity:     SeverityWarning,
				Reason:       fmt.Sprintf("property %q is not declared in the %q schema; it is left exactly as it is and is not queryable as a typed property", key, schema.Type),
				Expected:     "one of: " + strings.Join(schema.PropertyNames(), ", "),
				Line:         n.Line,
			})
		}
	}

	return report
}

// PropertyState is what a record says about one declared property. It exists so
// FR-007's three states are named rather than inferred from a nil check.
type PropertyState int

const (
	// StateAbsent — the key is missing entirely, or present with an explicit
	// null. FR-007: this is a DISTINCT state from every value of the property.
	// An explicit `status:` with nothing after it is not "the empty status", it
	// is no status.
	StateAbsent PropertyState = iota
	// StatePresent — the property holds one or more values.
	StatePresent
	// StateNonConforming — the property holds something, but it does not
	// conform to its declaration. §8 R-4: this compares false for every
	// operator AND is reported. It is not absence, and treating it as absence
	// is how a corrupt value disappears from an answer.
	StateNonConforming
)

func (s PropertyState) String() string {
	switch s {
	case StateAbsent:
		return "absent"
	case StatePresent:
		return "present"
	case StateNonConforming:
		return "non-conforming"
	}
	return "unknown"
}

// PropertyValue is the resolved state of one declared property on one record.
type PropertyValue struct {
	Property *Property
	State    PropertyState
	// Values holds the conforming values. A scalar property has at most one;
	// a `many` property has zero or more, in document order. An EMPTY LIST is
	// StatePresent with zero values — R-3: an empty list is a value, not absence.
	//
	// It is a FILTERED slice: a non-conforming element is reported and skipped,
	// so an index into Values is NOT an index into the file. Anything that
	// reports a position must go through SourcePosition.
	Values []TypedValue
	// SourceIndex[i] is the 0-based position of Values[i] among the property's
	// SOURCE elements — the position the operator sees in their own file.
	//
	// It exists because Values is filtered. With `tags: [{a: b}, dup, dup]` the
	// mapping is dropped as non-conforming, so the two duplicates sit at Values
	// indexes 0 and 1 while the file has them at lines 2 and 3. A finding that
	// reported the Values index would send the operator to the wrong line of
	// their own note — and every position after ANY dropped element is wrong,
	// not just the first. Read it via SourcePosition, never directly.
	//
	// len(SourceIndex) == len(Values) for every PropertyValue that came out of
	// ResolveProperty. It is empty on a hand-built one, which SourcePosition
	// handles.
	SourceIndex []int
	// Findings holds what went wrong, if anything.
	Findings []Finding
}

// SourcePosition maps an index into Values back to the element position in the
// source file — what a finding must name.
//
// It falls back to i for a PropertyValue that was assembled by hand rather than
// resolved from a record (filter.go synthesises literal operands this way).
// Those carry exactly one unfiltered value, so i is already the source position.
func (pv PropertyValue) SourcePosition(i int) int {
	if i >= 0 && i < len(pv.SourceIndex) {
		return pv.SourceIndex[i]
	}
	return i
}

// ResolveProperty reads one declared property off a record: its state, its
// typed values, and any findings. Validation and filtering both go through it,
// so the two can never disagree about what a record says.
func ResolveProperty(rec Record, prop *Property) PropertyValue {
	pv := PropertyValue{Property: prop, State: StateAbsent}

	base := Finding{
		RecordPath:   rec.Path,
		RecordType:   rec.TypeName(),
		RecordID:     rec.ID(),
		Property:     prop.Name,
		ElementIndex: -1,
		Severity:     SeverityError,
	}

	n, present := rec.Frontmatter.Get(prop.Name)
	if !present || n.Kind == KindNull {
		// FR-007 — absent. Missing key and explicit null are the same state:
		// a key with no value is not a value.
		return pv
	}
	base.Line = n.Line

	// FR-006 — ARITY FIRST, before the value is looked at.
	//
	// This ordering is deliberate. If a scalar `segment` has become
	// `[vendor, customer]`, the interesting fact is the SHAPE, not that
	// "[vendor, customer]" is not a permitted enum value. Reporting the latter
	// sends the operator to fix the wrong thing.
	isList := n.Kind == KindSequence
	if isList != prop.Many {
		f := base
		f.Code = FindingArity
		f.Expected = prop.ExpectedShape()
		if isList {
			f.Reason = fmt.Sprintf("property %q is declared as a single value but holds a list of %d", prop.Name, len(n.Items))
			f.Got = fmt.Sprintf("a list of %d values", len(n.Items))
		} else {
			f.Reason = fmt.Sprintf("property %q is declared as a list (many: true) but holds a single value", prop.Name)
			f.Got = "a single value"
		}
		pv.State = StateNonConforming
		pv.Findings = append(pv.Findings, f)
		return pv
	}

	elements := []Node{n}
	if isList {
		elements = n.Items
	}

	pv.State = StatePresent
	for i, el := range elements {
		if el.Kind == KindNull {
			f := base
			if isList {
				f.ElementIndex = i
			}
			f.Line = el.Line
			f.Code = FindingWrongShape
			f.Reason = fmt.Sprintf("property %q holds an empty entry; a list entry with no value is not a value", prop.Name)
			f.Expected = prop.ExpectedShape()
			f.Got = "empty"
			pv.Findings = append(pv.Findings, f)
			pv.State = StateNonConforming
			continue
		}
		tv, verr := ParseValue(prop, el)
		if verr != nil {
			f := base
			if isList {
				f.ElementIndex = i
			}
			f.Line = el.Line
			f.Code = verr.Code
			f.Reason = verr.Reason
			f.Expected = verr.Expected
			f.Got = verr.Got
			f.Permitted = verr.Permitted
			pv.Findings = append(pv.Findings, f)
			pv.State = StateNonConforming
			continue
		}
		pv.Values = append(pv.Values, tv)
		// i is the SOURCE element index — `elements` is the file's own list,
		// unfiltered. Recording it here is what keeps a later position report
		// honest once a non-conforming element above has been skipped.
		pv.SourceIndex = append(pv.SourceIndex, i)
	}
	return pv
}

func validateProperty(rec Record, report RecordReport, prop *Property, opts ValidateOptions) []Finding {
	pv := ResolveProperty(rec, prop)

	if pv.State == StateAbsent {
		if !prop.Required {
			return nil
		}
		n, present := rec.Frontmatter.Get(prop.Name)
		line := 0
		reason := fmt.Sprintf("required property %q is absent", prop.Name)
		if present {
			line = n.Line
			reason = fmt.Sprintf("required property %q is declared but has no value; an empty key is absence, not a value", prop.Name)
		}
		return []Finding{{
			RecordPath:   rec.Path,
			RecordType:   report.Type,
			RecordID:     report.ID,
			Property:     prop.Name,
			ElementIndex: -1,
			Code:         FindingMissingRequired,
			Severity:     SeverityError,
			Reason:       reason,
			Expected:     prop.ExpectedShape(),
			Got:          "absent",
			Permitted:    prop.PermittedValues(),
			Line:         line,
		}}
	}

	findings := pv.Findings
	if opts.ReportDuplicateListValues && prop.Many && len(pv.Values) > 1 {
		findings = append(findings, duplicateListFindings(rec, report, prop, pv, opts)...)
	}
	return findings
}

// linkTargetIsIdentity is the relation identity available to a record on its
// own, with no vault index: the link's own target.
//
// value.go states the split this rests on, and it is not restated here — it is
// USED here. Wikilink.Target is "the join key"; Wikilink.Display "is NEVER
// identity"; ParseWikilink puts neither the `|alias` nor the `#heading` into
// Target. So `[[Acme]]`, `[[Acme|Acme Corp]]` and `[[Acme#Billing]]` are one
// target, which is exactly R-8's answer for them.
//
// What it CANNOT know is aliasing — that `[[Acme]]` and `[[Acme Corporation Pte
// Ltd]]` are one note. Only an index knows that, and a caller holding one passes
// it as ValidateOptions.ResolveRelation instead of this.
func linkTargetIsIdentity(l Wikilink) (string, bool) {
	if l.Target == "" {
		return "", false
	}
	return l.Target, true
}

// duplicateListFindings reports a `many` property that holds one value twice.
//
// ---------------------------------------------------------------------------
// IT ASKS THE COMPARATOR. THERE IS NO SECOND ANSWER TO "ARE THESE THE SAME".
//
// This function used to key a map on `TypedValue.String()`. That method is the
// REPORT RENDERER — it exists so a finding can quote a value — and using it as
// an identity made this the package's second equality implementation, the exact
// arrangement filter.go's header forbids at length and for the same reason: the
// verified comparator sits off the path while an unverified one does the work.
//
// It did not merely risk drifting; it was already wrong for five of the seven
// declared types, and silently:
//
//	R-8  `[[Acme]]` and `[[Acme|Acme Corp]]` are ONE record listed twice — a
//	     display alias is presentation. Under the string key: two values, no
//	     warning. Same for `person`.
//	R-7  `2026-01-01` and `2026-01-01T00:00:00Z` are one instant. Two values.
//	     `1.0` and `1.00` are one number. Two values.
//	R-6  `10.0 USD` and `USD 10.00` are one amount. Two values.
//
// Every one of those is precisely what this check exists to report, and it
// reported none of them.
//
// WHY elementsEqual AND NOT Compare. Compare is an ORDERING view, and §8 gives
// `text` no ordering and no scalar equality, so routing through it would stop
// reporting repeats in a `many text` list — a regression on the one type the
// string key did handle. elementsEqual is R-9's whole-element equality, defined
// for every declared type; membership in a list and repetition in a list are
// the same question asked twice.
//
// A REFUSED COMPARISON IS NOT A DUPLICATE, AND NOT A FINDING. The oracle
// returns a problem rather than a boolean when it cannot decide — a value that
// does not conform to its declared type (R-4), a link no resolver could place
// (R-8). Both are legal data. This check reports what it KNOWS repeats; it does not guess, and
// it does not convert the oracle's problem into a conformance fault, because
// neither of those is a fault of THIS record. That disposition is pinned by
// test, not left to be rediscovered.
//
// COST, AND IT IS A REAL ONE. The map was O(n) on a hash of the rendering.
// Comparator equality has no hash — R-8 identity is a property of a resolver,
// not of any one value in isolation — so this compares each element against the
// DISTINCT values seen so far: O(n x k), worst case O(n^2) when every element
// differs. Measured (BenchmarkDuplicateListFindings, figures and machine in its
// comment): a 1,000-element all-distinct `many relation` list costs ~23ms to
// scan, against well under a millisecond for the map that got it wrong; a
// 100-element one costs ~0.3ms. Ten times the list is about seventy-five times
// the scan, so the corner is real and it arrives somewhere in the high
// hundreds.
//
// Three things make that affordable, and they should be checked before anyone
// relaxes them. The check is opt-in and OFF by default, so a caller pays
// nothing unless they asked the question. It runs when a vault is audited, not
// per query and not per keystroke. And a list of a thousand values in ONE
// property of ONE note is already an unusual note. If a caller ever does need
// this at scale, the answer is to bound the work explicitly (a size cap that
// REPORTS itself, not one that silently stops looking) — not to reintroduce a
// hash of the rendering, which is where this started.
// ---------------------------------------------------------------------------
func duplicateListFindings(rec Record, report RecordReport, prop *Property, pv PropertyValue, opts ValidateOptions) []Finding {
	resolve := opts.ResolveRelation
	if resolve == nil {
		resolve = linkTargetIsIdentity
	}
	c := Comparator{ResolveRelation: resolve}

	// One entry per DISTINCT value, holding the first element that carried it.
	// Comparing against representatives rather than against every earlier
	// element is what keeps a list of repeats linear; it rests on the oracle's
	// equality being transitive, which it is for every type it decides (each is
	// equality of a resolved id, an instant, an exact decimal or an exact name).
	type occurrence struct {
		value TypedValue
		pos   int
	}
	firsts := make([]occurrence, 0, len(pv.Values))

	var out []Finding
	for i, v := range pv.Values {
		// SOURCE positions throughout. pv.Values is filtered — an element that
		// failed to parse was reported and dropped — so its index stops
		// matching the file the moment anything above it is dropped, and a
		// warning that names the wrong element is worse than no warning.
		pos := pv.SourcePosition(i)

		first, repeat := -1, false
		for j := range firsts {
			// OpEqual is R-9's element-wise equality — against a `many`
			// property, `=` matches an element exactly. It is carried only so
			// a problem the oracle reports names the rule it came from. Both
			// operands are this same property, so R-5's shared-value-set
			// precondition holds by construction.
			equal, problems := c.compareElements(OpEqual, firsts[j].value, v, pv, pv)
			if len(problems) > 0 {
				continue // undecidable, therefore not known to repeat
			}
			if equal {
				first, repeat = j, true
				break
			}
		}
		if !repeat {
			firsts = append(firsts, occurrence{value: v, pos: pos})
			continue
		}
		out = append(out, Finding{
			RecordPath:   rec.Path,
			RecordType:   report.Type,
			RecordID:     report.ID,
			Property:     prop.Name,
			ElementIndex: pos,
			Code:         FindingDuplicateListValue,
			Severity:     SeverityWarning,
			Reason:       duplicateReason(prop, firsts[first].value, firsts[first].pos, v, pos),
			Expected:     prop.ExpectedShape(),
			Got:          v.String(),
		})
	}
	return out
}

// duplicateReason writes the warning.
//
// It has two forms because identity equality admits a case the string key could
// not produce: the two elements are ONE value and the file spells them
// differently. Quoting one spelling and reporting it "at positions 0 and 1"
// would send the operator to a line that does not contain the text they were
// just shown — so when the spellings differ, both are named.
//
// The `a == b` below is NOT an equality test and must not be mistaken for one —
// by the time this is called the comparator has ALREADY ruled these two the same
// value. It asks a rendering question: do they LOOK the same to a reader? That
// is a legitimate use of String(), which is what String() is for.
func duplicateReason(prop *Property, first TypedValue, firstPos int, repeat TypedValue, repeatPos int) string {
	a, b := first.String(), repeat.String()
	if a == b {
		return fmt.Sprintf("property %q holds %q at positions %d and %d", prop.Name, b, firstPos, repeatPos)
	}
	return fmt.Sprintf("property %q holds one value at positions %d and %d, written two ways: %q and %q — they are the same %s and only the spelling differs",
		prop.Name, firstPos, repeatPos, a, b, prop.Type)
}

// Validate validates many records against a schema set, one report per record.
func Validate(set *SchemaSet, recs []Record, opts ValidateOptions) *ValidationReport {
	out := &ValidationReport{Records: make([]RecordReport, 0, len(recs))}
	for _, r := range recs {
		out.Records = append(out.Records, ValidateRecord(set, r, opts))
	}
	return out
}
