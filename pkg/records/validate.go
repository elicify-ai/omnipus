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
//	FR-012  a money value with no currency is rejected.
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
	// FindingMoneyNoCurrency — FR-012.
	FindingMoneyNoCurrency FindingCode = "money_missing_currency"
	// FindingMoneyBadCurrency — a code that is not ISO-4217.
	FindingMoneyBadCurrency FindingCode = "money_unknown_currency"
	// FindingMoneyMalformed — a money value that is neither of the accepted forms.
	FindingMoneyMalformed FindingCode = "money_malformed"
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
	ReportDuplicateListValues bool
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
	Values []TypedValue
	// Findings holds what went wrong, if anything.
	Findings []Finding
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
		seen := map[string]int{}
		for i, v := range pv.Values {
			key := v.String()
			if first, dup := seen[key]; dup {
				findings = append(findings, Finding{
					RecordPath:   rec.Path,
					RecordType:   report.Type,
					RecordID:     report.ID,
					Property:     prop.Name,
					ElementIndex: i,
					Code:         FindingDuplicateListValue,
					Severity:     SeverityWarning,
					Reason:       fmt.Sprintf("property %q holds %q at positions %d and %d", prop.Name, key, first, i),
					Expected:     prop.ExpectedShape(),
					Got:          key,
				})
				continue
			}
			seen[key] = i
		}
	}
	return findings
}

// Validate validates many records against a schema set, one report per record.
func Validate(set *SchemaSet, recs []Record, opts ValidateOptions) *ValidationReport {
	out := &ValidationReport{Records: make([]RecordReport, 0, len(recs))}
	for _, r := range recs {
		out.Records = append(out.Records, ValidateRecord(set, r, opts))
	}
	return out
}
