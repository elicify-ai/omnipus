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
	// Label is the human-readable display name (EnumValueDef.label). Absent
	// means render Name. Display data only — it carries no rule and no
	// ordering meaning; Position and EnumPosition remain the sole authorities
	// on order.
	Label string
	// Position is the declared index, zero-based. FR-010: order is DATA, so it
	// travels with the value instead of being encoded into the spelling — the
	// "1-Pending / 7-DoNotContact" prefix hack exists only because a tool sorted
	// lexically and offered no other way to state sequence.
	//
	// It is OUTPUT, never the ordering authority. Only the schema loader and
	// NewProperty stamp it; a Property built with a plain struct literal — which
	// EnumPosition explicitly supports — leaves it zero on every value. Ask
	// Property.EnumPosition for an ordinal. Nothing in this package may read
	// this field to decide an order, and enum_position_authority_test.go fails
	// the build if anything starts to.
	//
	// It stayed a struct field because it is a required field of the wire type
	// (contracts/components/schemas/EnumValueDef.yaml) that a caller serialising
	// a schema must fill in.
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
// set. This is the ordering oracle for FR-010 and §8 R-5 — the SOLE one. Every
// caller that needs an enum ordinal asks here: value.go's parse, filter.go's
// SortByEnumOrder, and compare_oracle.go's R-5 ordering. There is no second way
// to learn it, and the EnumValue.Position field is not one.
//
// It was, briefly, and the two authorities agreed only by accident: this method
// returns the SLICE INDEX, the field is stamped only by the loader and
// NewProperty, and the comparator read the field. Against the struct-literal
// Property the paragraph below blesses, every field was zero, so `todo < done`
// answered FALSE — silently — while SortByEnumOrder on the same property
// ordered the same values correctly.
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
// the schema came back without it. `scale:` went the same way, and its
// `maximum: 12` matching this package's maxMoneyScale made it look verified
// while nothing read it.
//
// parseProperty already refuses `values` on a non-enum for exactly this
// reason — "an author who writes something meaningful must never be told
// nothing when we throw it away" — and value.go closes the same hole for a
// money mapping's {amount, currency, scale}. This section generalises it so
// the next key added to the contract cannot reopen it: every key a declaration
// may mention is listed, with what the parser DOES with it.
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

	// REFUSED, and the refusal is the honest answer rather than the cautious
	// one. PropertyDef.yaml says a property's `scale` is the scale "every
	// RecordMoney value of this property" carries — a CONSTRAINT on values.
	// Nothing in this package enforces it: money scale is per-value, either
	// declared in the {amount, currency, scale} mapping or inferred from the
	// figure's own spelling (value.go's parseMoneyValue), and no value-parse
	// path consults its Property's declaration. Storing the number and
	// enforcing nothing would be this same silent-drop defect wearing a
	// getter: the author would be promised a guarantee that does not exist.
	//
	// It is not implementable here either, honestly. FR-012 — the requirement
	// the contract cites — is about a VALUE carrying amount, currency and
	// scale together; it says nothing about a property-level declaration. And
	// the only wire code that could report a violation, RecordProblem's
	// `money_scale_mismatch`, still describes the retracted "fractional digit
	// count" rule that RecordMoney.yaml's 2026-08-25 correction says cannot be
	// a finding at all. Implementing against contract text that contradicts
	// itself would be inventing a semantic, which is worse than refusing one.
	"scale": {
		kind: declKeyRefused,
		reason: "is not a property-level declaration in this release: nothing enforces it, " +
			"so accepting it would silently promise that every value of this property carries that scale. " +
			"Declare the scale on the value instead — {amount: 34998, currency: SGD, scale: 2} — " +
			"or write \"349.98 SGD\" and let it be inferred from the figure",
	},
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
// `scale` deliberately has NO field here: it is declKeyRefused, and a field
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
	p.Label = strings.TrimSpace(p.Label)
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
			Name:     long.Name,
			Label:    strings.TrimSpace(long.Label),
			Position: position,
			Group:    long.Group,
		}, nil
	}
	return EnumValue{}, fmt.Errorf("enum value at position %d must be a name or a {name, group} mapping", position)
}

func fingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
