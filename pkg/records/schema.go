// Omnipus — ADR-068 D2/D3/D4: record-type schemas, loaded from the vault.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// FR-001  schemas load from <vault>/.omnipus-vault/records/<type>.yaml
// FR-002  a schema without schema_version is REJECTED
// FR-003  two files declaring the same record type are BOTH rejected, both
//         paths named
// FR-004  exactly seven property types exist
// FR-006  every property declares arity
// FR-009  property types are scoped to their record type
// FR-010  an enum declares a closed SET of values; sorting is LEXICAL over the
//         folded value (D4 as revised in ADR-068 revision 8). An author who
//         wants a domain order prefixes the values: `1-lead`, `2-qualified`.
//         There is no declared-position ordinal — see EnumValue.
//
// A loading note that matters: one bad schema file does NOT blind the vault.
// LoadSchemas returns the schemas that parsed AND a report naming the ones that
// did not, because a vault with six good schemas and one typo should keep
// answering six types of question while telling the operator about the seventh.
// US-1.5's "no records of that type are validated against it" is satisfied by
// the rejected type simply not being in the set — its notes fall through to
// FR-005 and are ordinary notes.
// ---------------------------------------------------------------------------

const (
	// VaultMarkerDirName is the Omnipus directory at a vault's root. It is the
	// same directory pkg/knowledge writes its marker into (knowledge.MarkerDirName);
	// it is restated here rather than imported because this package deliberately
	// has no storage dependency (see doc.go). TestSchema_LoadPathMatchesFR001
	// pins the literal string FR-001 names.
	VaultMarkerDirName = ".omnipus-vault"

	// RecordsDirName is the subdirectory holding record-type schemas.
	RecordsDirName = "records"
)

// SchemaDir returns <vault>/.omnipus-vault/records — the FR-001 location.
func SchemaDir(vaultRoot string) string {
	return filepath.Join(vaultRoot, VaultMarkerDirName, RecordsDirName)
}

// ---------------------------------------------------------------------------
// Property types — FR-004, closed
// ---------------------------------------------------------------------------

// PropertyType is one of the seven types ADR-068 D3 declares. The set is
// CLOSED: adding an eighth is an ADR change, not an implementation detail.
type PropertyType string

const (
	// TypeText is prose. Never validated for shape, never compared for ordering.
	TypeText PropertyType = "text"
	// TypeEnum is one of a declared, closed value SET (D4). The set is closed;
	// it is NOT ordered — ordering is lexical over the folded value (R-5).
	TypeEnum PropertyType = "enum"
	// TypeRelation is a typed edge to another record (D5), stored on disk as a
	// quoted wikilink (D5.1).
	TypeRelation PropertyType = "relation"
	// TypeDate is a day or an instant, comparable.
	TypeDate PropertyType = "date"
	// TypeInteger is a signed 64-bit whole number, bound-checked and REFUSED
	// outside [-9223372036854775808, 9223372036854775807] (FR-013). It closes a
	// count silently widened to a float and a large identifier silently
	// truncated.
	TypeInteger PropertyType = "integer"
	// TypeDecimal is an exact, arbitrary-precision number (see decimal.go),
	// bounded at maxDecimalScale fractional places. `unit` is metadata,
	// declared here rather than glued into the property name. A value beyond
	// the bound is refused naming the bound, NEVER rounded (FR-013).
	TypeDecimal PropertyType = "decimal"
	// TypePerson is a relation to whatever record type the VAULT uses for
	// people. It does NOT imply a built-in person type — D0 forbids that. With
	// no `to:` declared, only the link shape is validated.
	TypePerson PropertyType = "person"
)

// PropertyTypes is the closed set, in declaration order. FR-004's "exactly
// these" is asserted against this slice.
//
// THE COUNT IS SEVEN, and the arithmetic is worth stating because it is easy
// to get wrong in the other direction (ADR-068 D3, revision 7): `money` was
// DELETED and `number` was SPLIT into `integer` and `decimal`, so
// −1 −1 +2 = still seven. The membership changed; the count did not.
var PropertyTypes = []PropertyType{
	TypeText, TypeEnum, TypeRelation, TypeDate, TypeInteger, TypeDecimal, TypePerson,
}

// isNumericType reports whether a declared type holds a number. §8 R-1 treats
// `integer` and `decimal` as ONE declared type for comparison purposes — an
// author chooses the STORAGE and its bounds, not a distinct comparison domain —
// so `3 = 3.0` is true and an integer compares with a decimal numerically.
func isNumericType(t PropertyType) bool {
	return t == TypeInteger || t == TypeDecimal
}

func isKnownPropertyType(t PropertyType) bool {
	for _, k := range PropertyTypes {
		if k == t {
			return true
		}
	}
	return false
}

func propertyTypeNames() []string {
	out := make([]string, 0, len(PropertyTypes))
	for _, t := range PropertyTypes {
		out = append(out, string(t))
	}
	return out
}

// ---------------------------------------------------------------------------
// Schema model
// ---------------------------------------------------------------------------

// EnumValue is one member of an enum's declared, closed set (D4).
//
// THERE IS NO Position FIELD, and its absence is the decision rather than an
// omission. ADR-068 D4's second clause was REVERSED by operator ruling: *"the
// enum ordering is following SQLite standard; if we need different ordering we
// need to prefix the content."* Enums therefore order LEXICALLY over the
// folded value (R-5), and an author who wants a domain order writes it into the
// values — `1-lead`, `2-qualified`, `3-proposal`, `4-won`.
//
// What that deleted: a derived ordinal column, the schema bookkeeping that kept
// it in step with the file, the NULLS-LAST requirement that came with it, and
// an unwritten rebuild obligation — an enum REORDER changed the derived ordinal
// for every record of the type and nothing said the index had to be rebuilt.
// The trade is visibility: a prefix sits in the operator's own file and does
// exactly what it looks like it does, where an ordinal was a second source of
// truth for the order, invisible in the vault, capable of changing every
// existing report while the cascade block reported "0 records lost validity".
type EnumValue struct {
	// Name is the value exactly as declared, and it is what a report renders
	// (FR-011c). Matching a WRITTEN value against it is case-INSENSITIVE in
	// full Unicode (FR-011a, FoldKey) — `Won` in a note resolves TO a declared
	// `won`, collapsing two spellings into one value. That is not the thing D4
	// forbids: D4 forbids auto-creating a SECOND de-facto value, which is what
	// Notion's multi-select does on any typo.
	Name string
	// Label is the human-readable display name (EnumValueDef.label). Absent
	// means render Name. Display data only — it carries no rule and takes no
	// part in matching or ordering.
	Label string
	// Group is D4's optional lifecycle bucket (open / done / cancelled) so
	// "is this finished?" is answerable across types with different vocabularies.
	Group string
}

// Property is one declared property of one record type.
type Property struct {
	Name string
	Type PropertyType

	// Many is FR-006's declared arity. A scalar property never silently
	// becomes a list, and a list property is never silently a scalar; both
	// directions are reported with the expected shape named.
	Many bool

	// Required means a record must carry a value. Absent (or explicitly null)
	// then fails validation.
	Required bool

	// Label is the human-readable display name (PropertyDef.label). Absent
	// means render Name, so no consumer has to invent its own fallback.
	//
	// Display data: there is no type it is invalid on and no bound the
	// contract states, so it has no rule in finalize. It is a FIELD, rather
	// than a key the parser skips, because the contract has always defined it
	// and the parser used to drop it in silence — `label: Status` produced no
	// label, no rejection and no warning, which is the exact fault the
	// `values`-on-a-non-enum check in parseProperty was written to end.
	Label string

	// Values is the enum's closed set, in declaration order. Declaration order
	// is preserved for REPORTING only — it is what PermittedValues lists in a
	// rejection, so the operator sees their own file back. It is NOT a sort
	// order; R-5 sorts lexically over the folded value.
	//
	// Empty for every other type.
	Values []EnumValue
	// foldIndex is a CACHE of Values, keyed by the FOLDED name, for O(1)
	// case-insensitive membership (FR-011a). It is derived state and never the
	// authority: ResolveEnum scans Values when it is nil, so a Property built
	// with a plain struct literal outside this package behaves exactly like a
	// parsed one.
	//
	// The cache used to be the authority, and that made an externally-built
	// enum property unusable: every legitimately declared value was rejected as
	// impermissible, with a message listing the permitted values — i.e. listing
	// the very value being rejected.
	foldIndex map[string]int

	// To is the target record type for `relation` and (optionally) `person`.
	To string
	// Inverse is the name the derived reverse edge is exposed under (D5). It is
	// NEVER written to any file (FR-032).
	Inverse string

	// Unit is numeric metadata — valid on `integer` and `decimal`, declared
	// here rather than glued into the property name (D3's `exercise: 60
	// minutes` failure).
	Unit string

	// RecordType is the type this property belongs to. FR-009: `status` on one
	// record type and `status` on another are UNRELATED declarations, so a
	// Property always knows whose it is and is never looked up vault-wide.
	RecordType string
}

// ResolveEnum resolves a WRITTEN value to the declared EnumValue it names, and
// reports whether it named one at all. It is the SOLE membership oracle for an
// enum: value.go's parse and the comparator both ask here, and there is no
// second way to learn the answer.
//
// Matching is CASE-INSENSITIVE in FULL UNICODE (FR-011a, R-5), performed by
// FoldKey — never strings.ToLower and never strings.EqualFold, both of which get
// German ß wrong and disagree with each other on Greek (see fold.go). A note
// that writes `Won` against a declared `won` resolves to `won`; the file keeps
// its own spelling and the report renders the DECLARED name (FR-011c), so two
// spellings collapse into ONE value rather than creating a second.
//
// It returns the DECLARED value, not the written one, which is what makes
// grouping agree with equality: three notes spelling one state three ways group
// once, under the schema's spelling.
//
// When the foldIndex cache has not been built — a Property assembled outside
// this package with a plain struct literal — the answer is scanned out of
// Values instead of being wrong. An enum's declared set is a handful of values;
// a linear scan costs nothing next to reporting every one of them as
// impermissible.
func (p *Property) ResolveEnum(value string) (EnumValue, bool) {
	key := FoldKey(value)
	if p.foldIndex != nil {
		i, ok := p.foldIndex[key]
		if !ok {
			return EnumValue{}, false
		}
		return p.Values[i], true
	}
	for _, v := range p.Values {
		if FoldKey(v.Name) == key {
			return v, true
		}
	}
	return EnumValue{}, false
}

// PermittedValues lists an enum's declared values in declaration order — what
// FR-011 requires a rejection to name. Declaration order is the operator's own
// file order, so a rejection reads back the way they wrote it; it is NOT the
// sort order (R-5 sorts lexically over the folded value).
func (p *Property) PermittedValues() []string {
	out := make([]string, 0, len(p.Values))
	for _, v := range p.Values {
		out = append(out, v.Name)
	}
	return out
}

// ExpectedShape renders the arity+type a value must have, in the words a
// report shows an operator. FR-006 and FR-042 both require the expected shape
// to be NAMED, not merely implied by the word "invalid".
func (p *Property) ExpectedShape() string {
	base := string(p.Type)
	switch p.Type {
	case TypeEnum:
		base = "enum(" + strings.Join(p.PermittedValues(), ", ") + ")"
	case TypeRelation, TypePerson:
		if p.To != "" {
			base = string(p.Type) + " to " + p.To + ` (a quoted wikilink, e.g. "[[Target]]")`
		} else {
			base = string(p.Type) + ` (a quoted wikilink, e.g. "[[Target]]")`
		}
	case TypeInteger:
		base = fmt.Sprintf("integer (a whole number between %d and %d)", math.MinInt64, math.MaxInt64)
	case TypeDecimal:
		base = fmt.Sprintf("decimal (an exact number, at most %d decimal places)", maxDecimalScale)
	case TypeDate:
		base = "date (YYYY-MM-DD, or an RFC-3339 instant)"
	}
	if p.Many {
		return "a list of " + base
	}
	return "a single " + base
}

// Identity is D7's per-type identifier configuration.
type Identity struct {
	// Prefix yields identifiers like CO-0142. The allocator itself is D7.1 and
	// lives outside this package; the schema only declares the prefix.
	Prefix string
}

// Schema is one record type's declaration.
type Schema struct {
	// SchemaVersion is FR-002's mandatory field. Obsidian's `.base` format
	// broke in five consecutive releases across eight weeks, two of them
	// unannounced; machine-generated schemas make that worse, not better.
	SchemaVersion int
	// Type is the record type name — the value a note's `type:` must hold.
	Type  string
	Label string

	Identity Identity

	// Properties is keyed by property name, scoped to THIS record type (FR-009).
	Properties map[string]*Property
	// PropertyOrder preserves declaration order for stable reporting.
	PropertyOrder []string

	// SourcePath is the file this came from — named in every FR-003 conflict
	// and in any report about the schema itself.
	SourcePath string
	// Fingerprint is a content hash of the source file. FR-015 uses it to
	// detect that a schema changed, because schemas live under a directory the
	// note scanner does not walk and therefore have no manifest entry.
	Fingerprint string
}

// PropertyNames lists declared property names in declaration order — what
// FR-024's "valid names listed" rejection shows.
func (s *Schema) PropertyNames() []string {
	out := make([]string, len(s.PropertyOrder))
	copy(out, s.PropertyOrder)
	return out
}

// Property looks up a property BY THIS RECORD TYPE. FR-009 in one method: the
// lookup is never vault-wide, so `status` on one type cannot answer for
// `status` on another.
func (s *Schema) Property(name string) (*Property, bool) {
	p, ok := s.Properties[name]
	return p, ok
}

// ---------------------------------------------------------------------------
// SchemaSet
// ---------------------------------------------------------------------------

// SchemaSet is every record type a vault declares.
type SchemaSet struct {
	byType map[string]*Schema
	order  []string
}

// NewSchemaSet builds an empty set. Note what it does NOT do: it seeds nothing.
// ADR-068 D0 — the product ships no record types of its own, not even as
// overridable defaults.
func NewSchemaSet() *SchemaSet {
	return &SchemaSet{byType: map[string]*Schema{}}
}

// Get returns the schema for a record type.
//
// A miss is FR-005: the note is an ordinary note, not an error.
func (s *SchemaSet) Get(recordType string) (*Schema, bool) {
	if s == nil {
		return nil, false
	}
	sc, ok := s.byType[recordType]
	return sc, ok
}

// Types lists declared record types, sorted for stable output.
func (s *SchemaSet) Types() []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s.order))
	copy(out, s.order)
	sort.Strings(out)
	return out
}

// Len reports how many record types loaded.
func (s *SchemaSet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.byType)
}

func (s *SchemaSet) add(sc *Schema) {
	s.byType[sc.Type] = sc
	s.order = append(s.order, sc.Type)
}

// ---------------------------------------------------------------------------
// Rejections
// ---------------------------------------------------------------------------

// SchemaRejectionCode names WHY a schema file was refused, so a caller can
// react differently to "you forgot a field" and "two files disagree".
type SchemaRejectionCode string

const (
	RejectUnreadable         SchemaRejectionCode = "schema_unreadable"
	RejectInvalidYAML        SchemaRejectionCode = "schema_invalid_yaml"
	RejectMissingVersion     SchemaRejectionCode = "schema_missing_version"
	RejectUnsupportedVersion SchemaRejectionCode = "schema_unsupported_version"
	RejectMissingType        SchemaRejectionCode = "schema_missing_type"
	RejectDuplicateType      SchemaRejectionCode = "schema_duplicate_type"
	RejectNoProperties       SchemaRejectionCode = "schema_no_properties"
	RejectBadProperty        SchemaRejectionCode = "schema_bad_property"
	// RejectUnknownKey is a key the schema file is not entitled to mention —
	// at the top level or inside `identity`. A property-level unknown key is
	// reported as RejectBadProperty instead, because the property it belongs
	// to is the thing an operator has to go and fix.
	RejectUnknownKey SchemaRejectionCode = "schema_unknown_key"
)

// SchemaRejection is one refused schema file.
type SchemaRejection struct {
	// Paths names every file involved. For FR-003's duplicate-type conflict it
	// holds BOTH paths — the requirement is explicit that both are named, and
	// a rejection that named only the second would leave an operator hunting.
	Paths []string
	// Type is the declared record type where one was readable.
	Type   string
	Code   SchemaRejectionCode
	Reason string
}

// String renders a rejection as one reviewable line.
//
// Deliberately NOT named Error(): a SchemaRejection is a REPORT ENTRY, not an
// error value. Giving it an Error() method would let it be returned as an
// `error` and lose its Code and Paths on the way — and the whole point of the
// type is that a caller can act on which file and which fault.
func (r SchemaRejection) String() string {
	return fmt.Sprintf("%s: %s (%s)", r.Code, r.Reason, strings.Join(r.Paths, ", "))
}

// SchemaLoadReport is everything the loader could not accept.
type SchemaLoadReport struct {
	Rejections []SchemaRejection
	// ScannedFiles is every candidate file the loader looked at, sorted.
	ScannedFiles []string
}

// OK reports whether every candidate schema file loaded.
func (r *SchemaLoadReport) OK() bool { return r == nil || len(r.Rejections) == 0 }

// RejectedTypes lists the record types that failed to load, sorted.
func (r *SchemaLoadReport) RejectedTypes() []string {
	if r == nil {
		return nil
	}
	seen := map[string]struct{}{}
	for _, rej := range r.Rejections {
		if rej.Type != "" {
			seen[rej.Type] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------

// LoadSchemas reads every record-type schema in a vault (FR-001).
//
// A vault with no schema directory is NOT an error: it is a vault with no
// record types, every note in it is an ordinary note (FR-005), and that is a
// completely normal state — ADR-068 §9's first holdout scenario is exactly
// this. It returns an empty set and an empty report.
func LoadSchemas(vaultRoot string) (*SchemaSet, *SchemaLoadReport, error) {
	dir := SchemaDir(vaultRoot)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return NewSchemaSet(), &SchemaLoadReport{}, nil
		}
		return nil, nil, fmt.Errorf("reading schema directory %s: %w", dir, err)
	}

	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Skip dotfiles: D7.1's `.seq` allocator state lives in this same
		// directory and is not a schema.
		if strings.HasPrefix(name, ".") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		paths = append(paths, filepath.Join(dir, name))
	}
	sort.Strings(paths) // deterministic order, so reports are reproducible

	return loadSchemaPaths(paths)
}

// loadSchemaPaths does the two-pass load. The two passes exist for FR-003: a
// duplicate type cannot be detected until every file has declared its type, and
// the requirement is that BOTH files are rejected — so nothing may be committed
// to the set until the conflict check has run.
func loadSchemaPaths(paths []string) (*SchemaSet, *SchemaLoadReport, error) {
	report := &SchemaLoadReport{ScannedFiles: append([]string(nil), paths...)}
	parsed := make([]*Schema, 0, len(paths))

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			report.Rejections = append(report.Rejections, SchemaRejection{
				Paths:  []string{p},
				Code:   RejectUnreadable,
				Reason: fmt.Sprintf("could not read the schema file: %v", err),
			})
			continue
		}
		sc, rej := ParseSchema(p, data)
		if rej != nil {
			report.Rejections = append(report.Rejections, *rej)
			continue
		}
		parsed = append(parsed, sc)
	}

	// FR-003 — group by declared type and reject every member of any group
	// larger than one, naming all their paths.
	byType := map[string][]*Schema{}
	typeOrder := []string{}
	for _, sc := range parsed {
		if _, seen := byType[sc.Type]; !seen {
			typeOrder = append(typeOrder, sc.Type)
		}
		byType[sc.Type] = append(byType[sc.Type], sc)
	}

	set := NewSchemaSet()
	for _, t := range typeOrder {
		group := byType[t]
		if len(group) > 1 {
			allPaths := make([]string, 0, len(group))
			for _, sc := range group {
				allPaths = append(allPaths, sc.SourcePath)
			}
			sort.Strings(allPaths)
			report.Rejections = append(report.Rejections, SchemaRejection{
				Paths: allPaths,
				Type:  t,
				Code:  RejectDuplicateType,
				Reason: fmt.Sprintf(
					"record type %q is declared in %d schema files (%s); all of them are rejected because there is no basis for preferring one, and no records of type %q will be validated until exactly one declaration remains",
					t, len(group), strings.Join(allPaths, " and "), t),
			})
			continue
		}
		set.add(group[0])
	}

	sort.Slice(report.Rejections, func(i, j int) bool {
		if report.Rejections[i].Paths[0] != report.Rejections[j].Paths[0] {
			return report.Rejections[i].Paths[0] < report.Rejections[j].Paths[0]
		}
		return report.Rejections[i].Code < report.Rejections[j].Code
	})
	return set, report, nil
}

// schemaFile is the on-disk shape. It is decoded through yaml.Node for the
// property map so a property's declaration order survives (FR-010 needs enum
// order; stable reporting needs property order).
type schemaFile struct {
	SchemaVersion *int      `yaml:"schema_version"`
	Type          string    `yaml:"type"`
	Label         string    `yaml:"label"`
	Identity      *identity `yaml:"identity"`
	Properties    yaml.Node `yaml:"properties"`
}

type identity struct {
	Prefix string `yaml:"prefix"`
}

// SupportedSchemaVersion is the only schema_version this release understands.
// FR-002 makes the field mandatory; this makes an unknown value LOUD rather
// than letting a future format be misread as the current one.
const SupportedSchemaVersion = 1

// ParseSchema parses one schema file's bytes. It returns either a schema or a
// rejection, never both, and never a bare error — every refusal carries a code
// and a path so a report can be assembled from it.
func ParseSchema(path string, data []byte) (*Schema, *SchemaRejection) {
	reject := func(code SchemaRejectionCode, declaredType, format string, args ...any) *SchemaRejection {
		return &SchemaRejection{
			Paths:  []string{path},
			Type:   declaredType,
			Code:   code,
			Reason: fmt.Sprintf(format, args...),
		}
	}

	// Decoded through a yaml.Node rather than straight into schemaFile so the
	// file's OWN key list survives the decode — see checkDeclaredKeys. An
	// empty file yields a zero-Kind node and no error, exactly as unmarshalling
	// into the struct did, so it still falls through to the FR-002 rejection
	// below rather than being reported as broken YAML.
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, reject(RejectInvalidYAML, "", "the schema file is not valid YAML: %v", err)
	}
	var sf schemaFile
	if root.Kind != 0 {
		if err := root.Decode(&sf); err != nil {
			return nil, reject(RejectInvalidYAML, "", "the schema file is not valid YAML: %v", err)
		}
	}

	// FR-002 — the version is mandatory, and "0" is not a substitute for
	// "absent", which is why SchemaVersion is a *int.
	if sf.SchemaVersion == nil {
		return nil, reject(RejectMissingVersion, sf.Type,
			"schema_version is missing; it is mandatory from the first release (ADR-068 D2), so this schema is rejected and no records of type %q are validated against it", sf.Type)
	}
	if *sf.SchemaVersion != SupportedSchemaVersion {
		return nil, reject(RejectUnsupportedVersion, sf.Type,
			"schema_version is %d; this release understands version %d only", *sf.SchemaVersion, SupportedSchemaVersion)
	}

	if strings.TrimSpace(sf.Type) == "" {
		return nil, reject(RejectMissingType, "", "the schema declares no `type`, so there is no record type for it to describe")
	}
	recordType := strings.TrimSpace(sf.Type)

	// After the version checks, deliberately: a key this release does not know
	// inside a schema_version it does not know is an unsupported VERSION, and
	// saying "unknown key `x`" about a version 2 file would send the operator
	// to fix the wrong thing.
	if err := checkDeclaredKeys("a schema file", &root, schemaFileKeys); err != nil {
		return nil, reject(RejectUnknownKey, recordType, "%v", err)
	}
	if idNode := mappingValue(&root, "identity"); idNode != nil {
		if err := checkDeclaredKeys("an `identity` block", idNode, identityDeclKeys); err != nil {
			return nil, reject(RejectUnknownKey, recordType, "%v", err)
		}
	}

	if sf.Properties.Kind == 0 || len(sf.Properties.Content) == 0 {
		return nil, reject(RejectNoProperties, recordType, "the schema declares no properties")
	}
	if sf.Properties.Kind != yaml.MappingNode {
		return nil, reject(RejectNoProperties, recordType, "`properties` must be a mapping of property name to declaration, found %s", yamlKindName(sf.Properties.Kind))
	}

	sc := &Schema{
		SchemaVersion: *sf.SchemaVersion,
		Type:          recordType,
		Label:         sf.Label,
		Properties:    map[string]*Property{},
		SourcePath:    path,
		Fingerprint:   fingerprint(data),
	}
	if sf.Identity != nil {
		sc.Identity = Identity{Prefix: sf.Identity.Prefix}
	}

	content := sf.Properties.Content
	for i := 0; i+1 < len(content); i += 2 {
		name := content[i].Value
		if _, dup := sc.Properties[name]; dup {
			return nil, reject(RejectBadProperty, recordType, "property %q is declared twice", name)
		}
		prop, err := parseProperty(recordType, name, content[i+1])
		if err != nil {
			return nil, reject(RejectBadProperty, recordType, "property %q: %v", name, err)
		}
		sc.Properties[name] = prop
		sc.PropertyOrder = append(sc.PropertyOrder, name)
	}
	if len(sc.PropertyOrder) == 0 {
		return nil, reject(RejectNoProperties, recordType, "the schema declares no properties")
	}
	return sc, nil
}

// ---------------------------------------------------------------------------
// Declared keys — PARSED or REFUSED, never a silent third state
//
// yaml.v3 drops a key that has no field to decode into, without an error and
// without a warning. That is how `label:` on a property declaration went
// nowhere for the whole life of this parser: the contract has always defined
// it (contracts/components/schemas/PropertyDef.yaml), the author wrote it, and
// the schema came back without it. A `scale:` key went the same way for the
// whole life of the type that used it, and its wire `maximum: 12` matching a Go
// constant of the same value made it look VERIFIED while nothing read it — a
// number agreeing with a number is not an enforcement. (Both that key and its
// type are gone; the lesson is why this section exists.)
//
// parseProperty already refuses `values` on a non-enum for exactly this
// reason — "an author who writes something meaningful must never be told
// nothing when we throw it away". This section generalises it so the next key
// added to the contract cannot reopen it: every key a declaration may mention
// is listed, with what the parser DOES with it.
//
//	declKeyParsed   read, and it changes the resulting Property/Schema
//	declKeyRefused  rejected, with its own reason naming the key
//
// A key in neither state is UNKNOWN and is rejected by name. Adding a key to
// the contract without adding it here fails
// TestSchema_EveryContractPropertyKeyIsHandled, which reads the contract file
// itself rather than a transcription of it.
// ---------------------------------------------------------------------------

type declKeyKind int

const (
	declKeyParsed declKeyKind = iota
	declKeyRefused
)

// declKey is one entitled key and its fate.
type declKey struct {
	kind declKeyKind
	// reason is the refusal, in the operator's words. Non-empty EXACTLY when
	// kind is declKeyRefused; TestSchema_RefusedKeysCarryAReason holds that.
	reason string
}

// propertyDeclKeys is the closed key set of one property declaration.
//
// `name` is absent on purpose: in a schema FILE the property name is the map
// key the declaration hangs off, not a key inside it. It is a field of the
// PropertyDef WIRE type, which is this same declaration flattened.
var propertyDeclKeys = map[string]declKey{
	"type":     {kind: declKeyParsed},
	"many":     {kind: declKeyParsed},
	"required": {kind: declKeyParsed},
	"label":    {kind: declKeyParsed},
	"values":   {kind: declKeyParsed},
	"to":       {kind: declKeyParsed},
	"inverse":  {kind: declKeyParsed},
	"unit":     {kind: declKeyParsed},

	// `scale` USED to live here as a declKeyRefused entry. It is now simply
	// UNKNOWN, and that is the correct answer rather than a loosening: it was
	// a property-level declaration for a type ADR-068 D3 deleted. A key naming
	// a type that no longer exists cannot be "refused with a reason" — there is
	// no remedy to point the author at. It falls through to the unknown-key
	// rejection, which names the key and lists what a property declaration DOES
	// carry.
	//
	// A `decimal` deliberately does NOT gain a property-level scale in its
	// place. The bound is maxDecimalScale and it is enforced per VALUE at
	// parse time; a second, per-property bound would be a constraint nothing
	// reads, which is exactly the silent-drop defect this whole section
	// exists to prevent.
}

// schemaFileKeys is the closed key set of a schema file's top level.
//
// It is NOT RecordType.yaml's key list, and the divergence is deliberate
// rather than drift: the wire type flattens `identity: {prefix: WI}` into
// `identity_prefix` and adds `source_path`, which is the loader's own answer
// about where the file was, never something a file declares about itself.
var schemaFileKeys = map[string]declKey{
	"schema_version": {kind: declKeyParsed},
	"type":           {kind: declKeyParsed},
	"label":          {kind: declKeyParsed},
	"identity":       {kind: declKeyParsed},
	"properties":     {kind: declKeyParsed},
}

// identityDeclKeys is the closed key set of the `identity` block (D7).
var identityDeclKeys = map[string]declKey{
	"prefix": {kind: declKeyParsed},
}

// enumValueDeclKeys is the closed key set of an enum value's LONG form.
//
// The short form (`values: [draft, shipped]`) is a scalar and has no keys.
// These are EnumValueDef.yaml's keys with two differences that are the file
// format's, not omissions: the file spells the token `name` where the wire
// spells it `value`, and `position` is never written by hand — FR-010 makes it
// the declared index, which the loader stamps.
var enumValueDeclKeys = map[string]declKey{
	"name":  {kind: declKeyParsed},
	"label": {kind: declKeyParsed},
	"group": {kind: declKeyParsed},
}

// checkDeclaredKeys rejects any key `node` is not entitled to mention.
//
// An unknown key is named, and so is the permitted set — a rejection that does
// not say what IS allowed makes the operator go and read our source. A refused
// key gets its OWN reason instead of "unknown": the operator wrote a key we
// publish, and calling it unknown would be a second lie on top of the first.
//
// A node that is not a mapping is left alone; the caller's own decode reports
// that shape in its own words.
func checkDeclaredKeys(what string, node *yaml.Node, entitled map[string]declKey) error {
	for _, k := range declaredKeys(node) {
		e, ok := entitled[k]
		if !ok {
			return fmt.Errorf("unknown key %q in %s; it carries only %s (an unrecognised key would otherwise be dropped in silence, changing what the declaration means)",
				k, what, strings.Join(entitledKeyNames(entitled), ", "))
		}
		if e.kind == declKeyRefused {
			return fmt.Errorf("`%s` %s", k, e.reason)
		}
	}
	return nil
}

// entitledKeyNames lists a key set in a stable order, for the message above.
func entitledKeyNames(entitled map[string]declKey) []string {
	out := make([]string, 0, len(entitled))
	for k := range entitled {
		out = append(out, "`"+k+"`")
	}
	sort.Strings(out)
	return out
}

// mergeKey is YAML's merge key. It is not a key OF the declaration — it names
// mappings whose keys are merged into it — so the merged keys are what get
// checked. yaml.v3 honours `<<` when it decodes, so a schema that shares a
// block of defaults across properties works today; reporting `<<` itself as
// unknown would refuse that schema for using a feature it already had.
const mergeKey = "<<"

// declaredKeys returns every key a mapping node effectively declares, in
// declaration order, with `<<` merges resolved into the keys they contribute.
//
// It follows an alias and unwraps a document so an anchored declaration
// (`status: *shared`) is checked like any other. Without that, one `*anchor`
// would walk every rule in this file straight past — the check would read a
// node with no Content and find nothing to object to.
func declaredKeys(node *yaml.Node) []string {
	return appendDeclaredKeys(nil, node, 0)
}

// maxDeclaredKeyDepth bounds merge/alias following. yaml.v3 already refuses a
// recursive anchor while parsing, so this is insurance for hand-built nodes
// rather than a limit any real schema can reach.
const maxDeclaredKeyDepth = 8

func appendDeclaredKeys(out []string, node *yaml.Node, depth int) []string {
	node = resolveNode(node)
	if node == nil || depth > maxDeclaredKeyDepth {
		return out
	}
	if node.Kind == yaml.SequenceNode {
		// `<<: [*a, *b]` — a merge from several sources.
		for _, item := range node.Content {
			out = appendDeclaredKeys(out, item, depth+1)
		}
		return out
	}
	if node.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if k := node.Content[i].Value; k != mergeKey {
			out = append(out, k)
			continue
		}
		out = appendDeclaredKeys(out, node.Content[i+1], depth+1)
	}
	return out
}

// mappingValue returns the value node a mapping holds under key, or nil.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	node = resolveNode(node)
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// resolveNode unwraps documents and follows aliases to the node that actually
// holds the content. The loop is bounded by yaml.v3's own alias-depth limit —
// it refuses a recursive anchor at parse time, so this cannot spin.
func resolveNode(node *yaml.Node) *yaml.Node {
	for node != nil {
		switch {
		case node.Kind == yaml.AliasNode:
			node = node.Alias
		case node.Kind == yaml.DocumentNode && len(node.Content) > 0:
			node = node.Content[0]
		default:
			return node
		}
	}
	return nil
}

// propertyDecl is one property's declaration.
//
// Its fields are the declKeyParsed half of propertyDeclKeys and nothing else.
// A key that is not declKeyParsed deliberately has NO field here: a field
// would make it decodable and therefore droppable again.
type propertyDecl struct {
	Type     string      `yaml:"type"`
	Many     bool        `yaml:"many"`
	Required bool        `yaml:"required"`
	Label    string      `yaml:"label"`
	Values   []yaml.Node `yaml:"values"`
	To       string      `yaml:"to"`
	Inverse  string      `yaml:"inverse"`
	Unit     string      `yaml:"unit"`
}

func parseProperty(recordType, name string, node *yaml.Node) (*Property, error) {
	// Before the decode, not after: yaml.v3 discards a key it has no field for
	// without a word, so the decoded struct can no longer tell us what the
	// author actually wrote. The key list is the only place that knowledge
	// still exists.
	if err := checkDeclaredKeys("a property declaration", node, propertyDeclKeys); err != nil {
		return nil, err
	}

	var decl propertyDecl
	if err := node.Decode(&decl); err != nil {
		return nil, fmt.Errorf("declaration is not readable: %v", err)
	}

	pt := PropertyType(strings.TrimSpace(decl.Type))
	if pt == "" {
		return nil, fmt.Errorf("no `type` declared; one of %s is required", strings.Join(propertyTypeNames(), ", "))
	}
	if !isKnownPropertyType(pt) {
		// FR-004's closed set, enforced. The permitted names are listed,
		// because a rejection that does not say what IS allowed makes the
		// operator go and read our source.
		return nil, fmt.Errorf("type %q is not a supported property type; the supported types are %s", pt, strings.Join(propertyTypeNames(), ", "))
	}

	p := &Property{
		Name:       name,
		Type:       pt,
		Many:       decl.Many, // FR-006: arity is declared, and `many` absent means scalar
		Required:   decl.Required,
		Label:      strings.TrimSpace(decl.Label),
		To:         strings.TrimSpace(decl.To),
		Inverse:    strings.TrimSpace(decl.Inverse),
		Unit:       strings.TrimSpace(decl.Unit),
		RecordType: recordType,
	}

	// `values` is enum-only. This check sits OUTSIDE the type switch below on
	// purpose: it used to be the switch's `default:` arm, which meant any type
	// with its own `case` skipped it entirely. `relation` had one, so
	// `{type: relation, to: person, values: [a, b]}` was ACCEPTED and the
	// values silently discarded — while the identical `person` declaration,
	// which had no case, was correctly refused. An author who writes something
	// meaningful must never be told nothing when we throw it away.
	if pt != TypeEnum && len(decl.Values) > 0 {
		return nil, errValuesOnlyOnEnum(pt)
	}
	if pt == TypeEnum {
		for i, vn := range decl.Values {
			ev, err := parseEnumValue(vn, i)
			if err != nil {
				return nil, err
			}
			p.Values = append(p.Values, ev)
		}
	}

	// Every remaining cross-field rule — and the derived valuePos index — comes
	// from finalize, which NewProperty runs too. The two construction paths
	// therefore cannot drift apart on what a valid declaration is.
	if err := p.finalize(); err != nil {
		return nil, err
	}
	return p, nil
}

func errValuesOnlyOnEnum(pt PropertyType) error {
	return fmt.Errorf("`values` is only meaningful on an enum, not on a %s", pt)
}

// finalize applies every cross-field rule a property declaration must satisfy
// and populates the derived valuePos index. It is the single definition of "a
// well-formed Property", shared by the schema loader and NewProperty.
//
// It is written against the Property's own exported fields rather than against
// a YAML declaration, so a hand-built property is held to exactly the same
// rules as a parsed one.
func (p *Property) finalize() error {
	switch p.Type {
	case TypeEnum:
		if len(p.Values) == 0 {
			return fmt.Errorf("an enum must declare its `values`; an enum with no values can never be satisfied")
		}
		// The index is keyed by the FOLDED name, because that is how a written
		// value resolves (FR-011a) — and it is therefore also where a
		// duplicate has to be detected. Two values that fold to the same key
		// ARE one value under R-5: declaring both `Won` and `won` would give
		// ResolveEnum two right answers and let the map hand back whichever
		// was indexed last, silently. It is refused, naming both spellings,
		// because the author has to choose which one their reports render.
		idx := make(map[string]int, len(p.Values))
		for i, v := range p.Values {
			if strings.TrimSpace(v.Name) == "" {
				return fmt.Errorf("enum value at position %d is empty", i)
			}
			key := FoldKey(v.Name)
			if j, dup := idx[key]; dup {
				if p.Values[j].Name == v.Name {
					return fmt.Errorf("enum value %q is declared twice", v.Name)
				}
				return fmt.Errorf("enum values %q and %q differ only by case, and matching is case-insensitive (FR-011a), so they are one value; declare one of them",
					p.Values[j].Name, v.Name)
			}
			idx[key] = i
		}
		p.foldIndex = idx
	case TypeRelation:
		if p.To == "" {
			return fmt.Errorf("a relation must declare its target record type with `to:`; without it the target type cannot be checked (FR-034)")
		}
	}

	if p.Type != TypeEnum && len(p.Values) > 0 {
		return errValuesOnlyOnEnum(p.Type)
	}
	if !isNumericType(p.Type) && p.Unit != "" {
		return fmt.Errorf("`unit` is only meaningful on an integer or a decimal, not on a %s", p.Type)
	}
	if p.Type != TypeRelation && p.Type != TypePerson && (p.To != "" || p.Inverse != "") {
		return fmt.Errorf("`to`/`inverse` are only meaningful on a relation or person, not on a %s", p.Type)
	}
	return nil
}

// NewProperty builds a Property from a hand-written declaration — the path a
// consumer of this package takes when the schema does not come from a file
// (a test fixture, a generated schema, an in-memory record type).
//
// It exists because a Property carries derived state, and a struct literal
// cannot fill it in from outside the package. ResolveEnum tolerates that (it
// scans Values instead), but this is the path that gets the O(1) folded index
// AND, more importantly, the same rejections the schema loader applies: a
// relation with no `to:`, a `unit` on anything but a number, `values` on
// anything but an enum, an empty enum value, and two enum values that differ
// only by case. A caller that skips it gets a working property; a caller that
// uses it also gets told when the declaration is wrong.
//
// decl is taken by value and copied, so the returned Property shares nothing
// with the caller's.
func NewProperty(decl Property) (*Property, error) {
	p := decl
	p.Name = strings.TrimSpace(p.Name)
	p.Label = strings.TrimSpace(p.Label)
	p.To = strings.TrimSpace(p.To)
	p.Inverse = strings.TrimSpace(p.Inverse)
	p.Unit = strings.TrimSpace(p.Unit)
	p.RecordType = strings.TrimSpace(p.RecordType)
	p.foldIndex = nil // derived state is ours to compute, never the caller's

	if p.Name == "" {
		return nil, fmt.Errorf("a property must declare a name")
	}
	if p.Type == "" {
		return nil, fmt.Errorf("property %q declares no type; one of %s is required", p.Name, strings.Join(propertyTypeNames(), ", "))
	}
	if !isKnownPropertyType(p.Type) {
		return nil, fmt.Errorf("property %q: type %q is not a supported property type; the supported types are %s", p.Name, p.Type, strings.Join(propertyTypeNames(), ", "))
	}

	p.Values = append([]EnumValue(nil), decl.Values...)

	if err := p.finalize(); err != nil {
		return nil, fmt.Errorf("property %q: %w", p.Name, err)
	}
	return &p, nil
}

// parseEnumValue accepts either the short form (`values: [prospect, active]`)
// or the long form (`values: [{name: prospect, group: open}]`) so D4's optional
// lifecycle group is expressible without forcing every schema to use it.
func parseEnumValue(n yaml.Node, position int) (EnumValue, error) {
	switch n.Kind {
	case yaml.ScalarNode:
		if strings.TrimSpace(n.Value) == "" {
			return EnumValue{}, fmt.Errorf("enum value at position %d is empty", position)
		}
		return EnumValue{Name: n.Value}, nil
	case yaml.MappingNode:
		if err := checkDeclaredKeys("an enum value", &n, enumValueDeclKeys); err != nil {
			return EnumValue{}, fmt.Errorf("enum value at position %d: %v", position, err)
		}
		var long struct {
			Name  string `yaml:"name"`
			Label string `yaml:"label"`
			Group string `yaml:"group"`
		}
		if err := n.Decode(&long); err != nil {
			return EnumValue{}, fmt.Errorf("enum value at position %d is not readable: %v", position, err)
		}
		if strings.TrimSpace(long.Name) == "" {
			return EnumValue{}, fmt.Errorf("enum value at position %d declares no `name`", position)
		}
		return EnumValue{
			Name:  long.Name,
			Label: strings.TrimSpace(long.Label),
			Group: long.Group,
		}, nil
	}
	return EnumValue{}, fmt.Errorf("enum value at position %d must be a name or a {name, group} mapping", position)
}

func fingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
