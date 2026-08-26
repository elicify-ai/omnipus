// Omnipus — ADR-068 D2/D3/D4: record-type schemas, loaded from the vault.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
// FR-010  an enum declares its values IN ORDER; sorting follows position
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
	// TypeEnum is one of a declared, ordered, closed value set (D4).
	TypeEnum PropertyType = "enum"
	// TypeRelation is a typed edge to another record (D5), stored on disk as a
	// quoted wikilink (D5.1).
	TypeRelation PropertyType = "relation"
	// TypeDate is a day or an instant, comparable.
	TypeDate PropertyType = "date"
	// TypeNumber is a quantity, exact (see decimal.go). `unit` is metadata.
	TypeNumber PropertyType = "number"
	// TypeMoney is amount + ISO-4217 currency + scale as ONE value (FR-012).
	TypeMoney PropertyType = "money"
	// TypePerson is a relation to whatever record type the VAULT uses for
	// people. It does NOT imply a built-in person type — D0 forbids that. With
	// no `to:` declared, only the link shape is validated.
	TypePerson PropertyType = "person"
)

// PropertyTypes is the closed set, in declaration order. FR-004's "exactly
// these" is asserted against this slice.
var PropertyTypes = []PropertyType{
	TypeText, TypeEnum, TypeRelation, TypeDate, TypeNumber, TypeMoney, TypePerson,
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

// EnumValue is one member of an enum's declared, ordered set (D4).
type EnumValue struct {
	// Name is the value exactly as declared. Matching is EXACT-CASE: `Active`
	// is not `active` (DS-1). D4's reason: auto-accepting a near-miss is how
	// one column comes to hold `Won`, `won` and `Closed Won`.
	Name string
	// Position is the declared index. FR-010: sorting follows THIS, not the
	// alphabet, because otherwise operators encode order into strings.
	Position int
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

	// Values is the enum's ordered set. Empty for every other type.
	Values []EnumValue
	// valuePos is a CACHE of Values, keyed by exact name, for O(1) membership
	// and ordering. It is derived state and never the authority: EnumPosition
	// answers from Values when it is nil, so a Property built with a plain
	// struct literal outside this package behaves exactly like a parsed one.
	//
	// It used to be the authority, and that made an externally-built enum
	// property unusable: EnumPosition returned (0, false) for every value, so
	// every legitimately declared value was rejected as impermissible — with a
	// message listing the permitted values, i.e. the very value being rejected.
	valuePos map[string]int

	// To is the target record type for `relation` and (optionally) `person`.
	To string
	// Inverse is the name the derived reverse edge is exposed under (D5). It is
	// NEVER written to any file (FR-032).
	Inverse string

	// Unit is `number` metadata — declared here rather than glued into the
	// property name (D3's `exercise: 60 minutes` failure).
	Unit string

	// RecordType is the type this property belongs to. FR-009: `status` on one
	// record type and `status` on another are UNRELATED declarations, so a
	// Property always knows whose it is and is never looked up vault-wide.
	RecordType string
}

// EnumPosition returns a value's declared position and whether it is in the
// set. This is the ordering oracle for FR-010 and §8 R-5.
//
// The position returned is the index into Values, which is the DECLARED order —
// FR-010's "sorting follows position, not the alphabet". Callers index Values
// with it (value.go's enum parse does), so it must be the slice index and not
// an EnumValue.Position a caller may have filled in by hand.
//
// When the valuePos cache has not been built — a Property assembled outside
// this package — the answer is scanned out of Values instead of being wrong.
// An enum's declared set is a handful of values; a linear scan of it costs
// nothing next to reporting every one of them as impermissible.
func (p *Property) EnumPosition(value string) (int, bool) {
	if p.valuePos != nil {
		i, ok := p.valuePos[value]
		return i, ok
	}
	for i, v := range p.Values {
		if v.Name == value {
			return i, true
		}
	}
	return 0, false
}

// PermittedValues lists an enum's declared values in order — what FR-011
// requires a rejection to name.
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
	case TypeMoney:
		base = `money (amount and ISO-4217 currency together, e.g. "349.98 SGD" or {amount: 34998, currency: SGD, scale: 2})`
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

	var sf schemaFile
	if err := yaml.Unmarshal(data, &sf); err != nil {
		return nil, reject(RejectInvalidYAML, "", "the schema file is not valid YAML: %v", err)
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

// propertyDecl is one property's declaration.
type propertyDecl struct {
	Type     string      `yaml:"type"`
	Many     bool        `yaml:"many"`
	Required bool        `yaml:"required"`
	Values   []yaml.Node `yaml:"values"`
	To       string      `yaml:"to"`
	Inverse  string      `yaml:"inverse"`
	Unit     string      `yaml:"unit"`
}

func parseProperty(recordType, name string, node *yaml.Node) (*Property, error) {
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
		pos := make(map[string]int, len(p.Values))
		for i, v := range p.Values {
			if strings.TrimSpace(v.Name) == "" {
				return fmt.Errorf("enum value at position %d is empty", i)
			}
			if _, dup := pos[v.Name]; dup {
				return fmt.Errorf("enum value %q is declared twice; declared position is the sort order (FR-010), so a repeat has no defined position", v.Name)
			}
			pos[v.Name] = i
		}
		p.valuePos = pos
	case TypeRelation:
		if p.To == "" {
			return fmt.Errorf("a relation must declare its target record type with `to:`; without it the target type cannot be checked (FR-034)")
		}
	}

	if p.Type != TypeEnum && len(p.Values) > 0 {
		return errValuesOnlyOnEnum(p.Type)
	}
	if p.Type != TypeNumber && p.Unit != "" {
		return fmt.Errorf("`unit` is only meaningful on a number, not on a %s", p.Type)
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
// cannot fill it in from outside the package. EnumPosition tolerates that (it
// scans Values instead), but this is the path that gets the O(1) index AND,
// more importantly, the same rejections the schema loader applies: a relation
// with no `to:`, a `unit` on anything but a number, `values` on anything but an
// enum, a duplicate or empty enum value. A caller that skips it gets a working
// property; a caller that uses it also gets told when the declaration is wrong.
//
// decl is taken by value and copied, so the returned Property shares nothing
// with the caller's. EnumValue.Position is normalised to the declared order
// (FR-010: position IS the order values are declared in), matching exactly what
// the loader stamps.
func NewProperty(decl Property) (*Property, error) {
	p := decl
	p.Name = strings.TrimSpace(p.Name)
	p.To = strings.TrimSpace(p.To)
	p.Inverse = strings.TrimSpace(p.Inverse)
	p.Unit = strings.TrimSpace(p.Unit)
	p.RecordType = strings.TrimSpace(p.RecordType)
	p.valuePos = nil // derived state is ours to compute, never the caller's

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
	for i := range p.Values {
		p.Values[i].Position = i
	}

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
		return EnumValue{Name: n.Value, Position: position}, nil
	case yaml.MappingNode:
		var long struct {
			Name  string `yaml:"name"`
			Group string `yaml:"group"`
		}
		if err := n.Decode(&long); err != nil {
			return EnumValue{}, fmt.Errorf("enum value at position %d is not readable: %v", position, err)
		}
		if strings.TrimSpace(long.Name) == "" {
			return EnumValue{}, fmt.Errorf("enum value at position %d declares no `name`", position)
		}
		return EnumValue{Name: long.Name, Position: position, Group: long.Group}, nil
	}
	return EnumValue{}, fmt.Errorf("enum value at position %d must be a name or a {name, group} mapping", position)
}

func fingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
