// Omnipus — ADR-068 FR-011/FR-042: refusing a knowledge_edit write that does not
// conform to the note's own declared record schema, rather than writing it
// and letting check_integrity discover the damage later.
//
// # Why this validation happens HERE and not in pkg/records
//
// pkg/records/validate.go answers "does this ALREADY-WRITTEN note conform to
// its schema" — it reads a Record's parsed Frontmatter and reports findings.
// This file answers a different, earlier question: "does the value an agent
// is ABOUT TO WRITE conform", using the exact same authority (Property,
// ParseValue, ResolveEnum) so the two can never disagree about what is
// valid. It does not duplicate validate.go's logic — it builds a synthetic
// records.Node from the incoming write value and hands it to the same
// records.ParseValue every read-time validation uses.
//
// # Ordinary notes are unconstrained (FR-005)
//
// A note with no declared `type:`, or a declared type with no matching
// schema file, is not a record. Every property name and every value is
// accepted without comment — there is nothing to violate, because nothing
// was ever declared. This mirrors ResolveProperty's own "absent state" and
// Record.IsRecord's boolean-with-no-error philosophy.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"errors"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// Sentinel errors for a schema-refused write, so a caller can branch on the
// CLASS without parsing message text — mirroring author.go's own sentinel
// pattern for its refusals.
var (
	// ErrUnknownProperty means the record's schema declares no property by
	// that name.
	ErrUnknownProperty = errors.New("knowledge: record schema declares no such property")
	// ErrPropertyArity means the value's shape (scalar vs list) disagrees
	// with the property's declared Many.
	ErrPropertyArity = errors.New("knowledge: value arity does not match the declared property")
	// ErrPropertyValue means one element failed type, enum or shape
	// validation (records.ParseValue).
	ErrPropertyValue = errors.New("knowledge: value does not conform to the declared property")
	// ErrDerivedProperty is FR-046 on the agent door: the property carries a
	// formula, so its value is computed and never stored. No caller — web or
	// agent — may write one.
	ErrDerivedProperty = errors.New("knowledge: property is derived and cannot be written")
	// ErrRelationProperty is FR-045 on the agent door: a relation or person
	// property is written through knowledge_edit's op "relation" and its
	// three explicit verbs, never by sending a whole value.
	ErrRelationProperty = errors.New("knowledge: relation properties are written through op \"relation\"")
	// ErrRequiredProperty is the removal half of Property.Required: a
	// required property cannot be removed (set_property value:null), because
	// every record of the type must carry a value and a write door must not
	// create the check_integrity finding it exists to prevent.
	ErrRequiredProperty = errors.New("knowledge: required property cannot be removed")
	// ErrUndeclaredRecordType is C7's guard on the discriminator: a `type`
	// write (set_property, or create's frontmatter argument) must name a
	// record type this collection's schemas actually declare. A typo or an
	// undeclared name would otherwise produce a note that claims to be a
	// record of a type nobody defined — ungoverned, invisible to the record
	// doors, and (on a retype) still carrying its old-type identifier.
	ErrUndeclaredRecordType = errors.New("knowledge: record type is not declared in this knowledge base")
	// ErrReservedProperty is UAT 2026-09-13 D-51: a write to the record's
	// identity (`id` / `omni_id`), to a `file.*` virtual property or to a
	// `formula.*` derived column. All three used to fall through to the
	// generic "record schema declares no such property" refusal, which for
	// `id` is actively misleading — `id:` IS in the frontmatter and IS the
	// identity. The protection held; only the classification was wrong.
	ErrReservedProperty = errors.New("knowledge: property is reserved and cannot be written")
)

// knowledgeEditRefuseReservedProperty is D-51's classification, applied to
// EVERY property write on the agent door whether or not a schema governs the
// note — an ordinary note's `id:` is no more writable than a record's, and
// `file.mtime` is virtual on every note.
func knowledgeEditRefuseReservedProperty(property string) error {
	switch {
	case property == records.RecordIDKey || property == records.RecordIDKeyNamespaced:
		return fmt.Errorf("%w: %q is the record's identity — minted by the knowledge base when the "+
			"record is created (or when `type` is first set) and never written by a caller; it is "+
			"what makes the record findable and unique within its type. Drop it from this write",
			ErrReservedProperty, property)
	case strings.HasPrefix(property, "file."):
		return fmt.Errorf("%w: %q is a virtual property — computed from the file itself (its path, "+
			"modification time, size, backlinks, ...) every time it is read, and never stored in the "+
			"note. It can be filtered and sorted on in knowledge_find, not written",
			ErrReservedProperty, property)
	case strings.HasPrefix(property, "formula."):
		return fmt.Errorf("%w: %q is a derived value — computed by a saved view's formula from the "+
			"note's other properties, never stored in the note. Change the properties it derives "+
			"from instead; knowledge_find reports the computed value",
			ErrReservedProperty, property)
	}
	return nil
}

// knowledgeEditRelationPosture says whether FR-045 applies to ONE validation
// call. It is a named type rather than a bool because the two answers are not
// "on" and "off" — they are two different, separately-argued situations, and
// a reader at a call site needs to see which one it picked without counting
// `true`s.
//
// # Why this is per-call and not global
//
// FR-045's rationale, quoted from ADR-083 §1540, is that relations "are
// modified through RelationWriteRequest's three explicit verbs, because a
// read-then-write round trip silently replaces a relation list". That names
// a specific hazard — an existing list of edges, some put there by another
// writer, replaced wholesale by a caller who only meant to change one. The
// refusal belongs exactly where that hazard is reachable, and nowhere else:
// applied more widely it stops being a data-integrity rule and starts being
// a capability the agent door simply lacks.
//
// Two write paths CANNOT reach the hazard, and both are deliberately allowed.
// (There were THREE until op "link"'s `relation` mode was removed — it was
// add-only, so it could not reach the hazard either, but an add-only half of
// a verb set is not a way in, and a relation property now has exactly one.)
//
//   - CREATE. knowledge_edit's op "create" refuses outright when the file
//     already exists (author.go's ErrNoteExists), so there is no prior list
//     of edges in existence to replace and no other writer to lose. Refusing
//     here would also break every note created from a TEMPLATE that mentions
//     a relation property, because create validates the whole ASSEMBLED
//     frontmatter — template bytes included — not just the caller's own
//     `frontmatter` argument. That is a large capability loss bought for no
//     integrity gain.
//   - The EXPLICIT VERBS themselves. op "relation" IS the add/remove/replace
//     verb set FR-045 points callers towards, so it cannot also be subject
//     to it.
//
// The path that CAN reach the hazard is set_property — in both its modes —
// and that is where the refusal fires.
type knowledgeEditRelationPosture int

const (
	// knowledgeEditRelationRefused is FR-045 in force: a relation or person
	// property is refused, with the op to use instead named in the message.
	//
	// It is the ZERO VALUE on purpose. A validation call added later that
	// forgets to state its posture gets the safe answer — a refusal an agent
	// can read its way out of — rather than silently opting itself out of the
	// rule.
	knowledgeEditRelationRefused knowledgeEditRelationPosture = iota
	// knowledgeEditRelationAllowed is a caller that is ITSELF one of FR-045's
	// sanctioned paths: a create, or op "relation"'s explicit verbs. See the
	// type comment for why each is out of the rule's reach.
	knowledgeEditRelationAllowed
)

// knowledgeEditGovernanceReason distinguishes WHY a write's record type did
// not resolve to a schema (G3, design doc §1.3/§3 class 5) — three distinct
// misses that used to collapse into one indistinguishable "nothing was
// checked" outcome: unparsable frontmatter, an absent/empty/list-valued
// `type:`, and a declared type with no matching schema. A fourth case (G4)
// is a declared type whose OWN schema FILE exists but was REJECTED at load
// time (malformed YAML, missing schema_version, a duplicate declaration,
// ...) — records.LoadSchemas simply omits a rejected type from the returned
// SchemaSet, so without tracking this separately it is indistinguishable
// from "nobody ever declared this type", which is exactly the silent
// degrade G4 exists to stop.
type knowledgeEditGovernanceReason int

const (
	// knowledgeEditGoverned means a schema resolved for this write's record
	// type and validation ran against it. The zero value, so a
	// knowledgeEditGovernance left unset (an op that never touches a
	// property, e.g. append_section/replace_body) also reads as "nothing to
	// report" via Note().
	knowledgeEditGoverned knowledgeEditGovernanceReason = iota
	// knowledgeEditUnparsable means src's frontmatter could not be parsed at
	// all.
	knowledgeEditUnparsable
	// knowledgeEditNoType means src declares no `type:` — absent, empty, or
	// (per Record.TypeName) list-valued.
	knowledgeEditNoType
	// knowledgeEditUnknownType means src declares a type, but no schema file
	// in this vault declares that type — and no rejected schema file claims
	// it either (see knowledgeEditRejectedSchema).
	knowledgeEditUnknownType
	// knowledgeEditRejectedSchema means src declares a type whose OWN schema
	// file was found and parsed as a candidate, but rejected at load time
	// (G4) — RejectionReason on knowledgeEditGovernance names why.
	knowledgeEditRejectedSchema
)

// knowledgeEditGovernance is what a write's schema resolution decided —
// carried out of the validation call so the tool-level result can tell the
// caller "validated and fine" from "nothing was checked, because ..." (G3).
// The zero value means "nothing to report": either a schema governed the
// write (ordinary success — the caller already knows from the absence of an
// error) or this op never populates one at all.
type knowledgeEditGovernance struct {
	Reason knowledgeEditGovernanceReason
	// TypeName is the declared type, when one was declared at all —
	// populated for knowledgeEditUnknownType and knowledgeEditRejectedSchema
	// (and, redundantly but harmlessly, knowledgeEditGoverned).
	TypeName string
	// RejectionReason is the records.SchemaRejection.Reason text for
	// TypeName's schema file — populated only for knowledgeEditRejectedSchema.
	RejectionReason string
}

// Note renders the governance outcome as an appendable result line, or ""
// when there is nothing to say: a schema governed the write, or this op
// never resolves one. FR-072: compact text, one line, no JSON.
func (g knowledgeEditGovernance) Note() string {
	switch g.Reason {
	case knowledgeEditUnparsable:
		return "NOTE: no schema governed this write — the note's frontmatter could not be parsed, so nothing was checked"
	case knowledgeEditNoType:
		return "NOTE: no schema governed this write — the note declares no record type, so nothing was checked"
	case knowledgeEditUnknownType:
		return fmt.Sprintf("NOTE: no schema governed this write — %q has no schema in this knowledge base, so nothing was checked", g.TypeName)
	case knowledgeEditRejectedSchema:
		return fmt.Sprintf("NOTE: no schema governed this write — %s's schema file failed to load (%s), so nothing was checked; fix it via knowledge_configure", g.TypeName, g.RejectionReason)
	default:
		return ""
	}
}

// knowledgeEditRejectedSchemaDetail reports whether report rejected a schema
// file that declared typeName, and if so, the reason text (G4). A nil report
// (a caller that has none to hand, or hasn't been updated) always answers
// false — the caller then falls back to knowledgeEditUnknownType, which was
// this whole call site's behaviour before G4.
func knowledgeEditRejectedSchemaDetail(report *records.SchemaLoadReport, typeName string) (reason string, rejected bool) {
	if report == nil {
		return "", false
	}
	for _, rej := range report.Rejections {
		if rej.Type == typeName {
			return rej.Reason, true
		}
	}
	return "", false
}

// knowledgeEditResolveSchema resolves src's own declared record type against
// set, when it has one. reason is knowledgeEditGoverned exactly when there is
// something to validate against; every other reason is a distinct miss
// (G3/G4) that both callers below (schema validation and the link
// operation's arity lookup) still treat identically for THEIR OWN decision
// (FR-005's "ordinary notes are unconstrained") — but the reason itself now
// survives to the tool-result layer instead of being thrown away.
//
// It is deliberately not an error return, even though a records.ParseFrontmatter
// failure is a real parse error: the shared reason is documented once here
// rather than twice at the two call sites.
//
// An unparsable frontmatter block specifically is not THIS function's
// failure to report. Every splice this package's callers run immediately
// after a successful resolution here (SetPropertyList, SetPropertyScalarChecked,
// AddListValue, RemoveListValue) calls parseListSpan -> fmParse on the very
// same src, independently and unconditionally, and refuses with
// ErrFrontmatterUnterminated when it cannot parse. A caller of this
// function never ends up believing an unparsable note was accepted: the very
// next call in the same NoteEdit closure re-parses it and refuses. Two
// parses of the same bytes for two different questions ("does a schema
// apply" vs. "where do I splice") is the cost of keeping those two
// questions separate rather than threading a parsed block through both.
//
// The `ferr != nil` branch below is, under records.ParseFrontmatter's
// CURRENT implementation, provably unreachable as a distinguishing case: read
// against its source, every one of its error returns hands back a
// Frontmatter with an empty Values map, which makes rec.TypeName() return ""
// unconditionally — the exact same answer the `typeName == ""` check two
// lines down already gives. A mutation that deletes this branch cannot be
// killed by any test built on the real parser, and one was tried. The branch
// stays anyway: "every current error path happens to also leave Values
// empty" is an implementation detail of another package, not a promise in
// ParseFrontmatter's documented contract, and collapsing this check into
// that assumption would make vault_edit_schema.go's correctness depend on
// something it has no way to notice changing.
func knowledgeEditResolveSchema(set *records.SchemaSet, report *records.SchemaLoadReport, src []byte) (schema *records.Schema, typeName string, reason knowledgeEditGovernanceReason, rejectionDetail string) {
	fm, ferr := records.ParseFrontmatter(src)
	if ferr != nil {
		return nil, "", knowledgeEditUnparsable, ""
	}
	rec := records.Record{Frontmatter: fm}
	typeName = rec.TypeName()
	if typeName == "" {
		return nil, "", knowledgeEditNoType, ""
	}
	schema, ok := set.Get(typeName)
	if ok {
		return schema, typeName, knowledgeEditGoverned, ""
	}
	// G4: a type that resolves to no LIVE schema might still be one whose
	// schema file the loader saw and rejected — distinct from a type nobody
	// ever declared at all (knowledgeEditUnknownType).
	if detail, rejected := knowledgeEditRejectedSchemaDetail(report, typeName); rejected {
		return nil, typeName, knowledgeEditRejectedSchema, detail
	}
	return nil, typeName, knowledgeEditUnknownType, ""
}

// knowledgeEditValidatePropertyAgainstSchema is the CORE arity/value check,
// shared by every write path that has already resolved a governing schema —
// knowledgeEditValidateValue (one property, from a tool argument) and
// knowledgeEditValidateAssembledFrontmatter (G1: every property present in
// a freshly-assembled note, including ones that arrived via raw body/template
// bytes rather than the `frontmatter` argument). Keeping the check in exactly
// one place is what "the same sentinel errors and message quality — do not
// invent a second error vocabulary" (G1's brief) means in code: there is
// only one vocabulary because there is only one function that speaks it.
// The two refusals below run BEFORE arity and before value conformance, in
// the same precedence CheckRecordPropertyWrites uses on the web door
// (FR-046 then FR-045), and for the same reason its doc comment gives: a
// derived property has no meaningful "arity to satisfy", so reporting an
// arity mismatch for one would send a caller off to fix the wrong thing.
// Both read the property's OWN declaration off the resolved schema, never a
// claim the caller made about itself.
//
// It returns the values in their CANONICAL spelling (UAT 2026-09-13 D-04):
// an enum member written in the wrong case (`Done` against a declared
// `done`) is accepted by ResolveEnum's case-insensitive match, and used to
// be written to disk verbatim — so `knowledge_read`, which renders the
// DECLARED spelling, immediately disagreed with the file it had just
// written, and every other reader (Obsidian, git, grep) saw the caller's
// spelling. The write now lands the declared spelling; a checkbox lands as
// the bare `true`/`false` it resolved to. Every other type is returned
// exactly as sent.
func knowledgeEditValidatePropertyAgainstSchema(schema *records.Schema, typeName, property string, values []string, isList bool, posture knowledgeEditRelationPosture) ([]string, error) {
	if rerr := knowledgeEditRefuseReservedProperty(property); rerr != nil {
		return nil, rerr
	}
	prop, ok := schema.Property(property)
	if !ok {
		return nil, fmt.Errorf("%w: %s declares no property %q; declared properties are %s",
			ErrUnknownProperty, typeName, property, strings.Join(schema.PropertyNames(), ", "))
	}
	// FR-046, shared with the web door through records.IsDerivedProperty.
	//
	// ⚠️ AS ON THE WEB DOOR, THIS IS UNREACHABLE FROM LoadSchemas TODAY AND
	// MUST NOT BE DELETED AS DEAD CODE. A schema file declaring `formula:`
	// is refused at load (schema.go's propertyDeclKeys), so no *Schema that
	// this package loads can carry one. It is defence in depth held against
	// a *Schema reaching here from somewhere else — records' own saved-view
	// namespace synthesis already constructs Formula-bearing properties.
	// record_write_guard.go's CheckRecordPropertyWrites carries the same
	// branch for the same reason; the two are now one predicate.
	if records.IsDerivedProperty(prop) {
		return nil, fmt.Errorf("%w: %s.%s is a derived value, computed from other properties rather than "+
			"stored — no caller can write one, because a stored value goes stale the moment anything it "+
			"derives from changes. Drop %q from this write; knowledge_read reports the computed value",
			ErrDerivedProperty, typeName, property, property)
	}
	// FR-045, shared with the web door through records.IsRelationProperty.
	// See knowledgeEditRelationPosture for which callers pass which posture
	// and the argument for each.
	if posture == knowledgeEditRelationRefused && records.IsRelationProperty(prop) {
		return nil, fmt.Errorf("%w: %s.%s is a %s property, and set_property cannot write one — sending a value "+
			"replaces the whole list, silently discarding edges another writer added (ADR-068 FR-045). "+
			"Use op \"relation\" instead: same collection, path and expect_version as this call, plus "+
			"property: %q, relation_op: \"add\", \"remove\" or \"replace\", and targets: a list of note "+
			"names or paths. \"add\" and \"remove\" leave the rest of the list untouched; \"replace\" "+
			"discards it on purpose",
			ErrRelationProperty, typeName, property, prop.Type, property)
	}
	if isList != prop.Many {
		schemaPath := records.VaultMarkerDirName + "/" + records.RecordsDirName + "/" + typeName + ".yaml"
		if isList {
			return nil, fmt.Errorf("%w: %s.%s holds one value; got a list of %d — send a single value, "+
				"or declare many: true in %s",
				ErrPropertyArity, typeName, property, len(values), schemaPath)
		}
		return nil, fmt.Errorf("%w: %s.%s is declared as a list (many: true); got a single value — send a list",
			ErrPropertyArity, typeName, property)
	}
	canonical := make([]string, len(values))
	for i, v := range values {
		node := records.Node{Kind: records.KindScalar, Text: v}
		typed, verr := records.ParseValue(prop, node)
		if verr != nil {
			label := property
			if isList {
				label = fmt.Sprintf("%s[%d]", property, i)
			}
			msg := fmt.Sprintf("%s.%s holds %q, which is not %s", typeName, label, v, verr.Expected)
			if len(verr.Permitted) > 0 {
				msg += "; permitted values are " + strings.Join(verr.Permitted, ", ")
			}
			return nil, fmt.Errorf("%w: %s", ErrPropertyValue, msg)
		}
		canonical[i] = knowledgeEditCanonicalText(prop, v, typed)
	}
	return canonical, nil
}

// knowledgeEditCanonicalText is the spelling a validated value is WRITTEN
// in (D-04): the declared enum member for an enum, `true`/`false` for a
// checkbox, the caller's own text for everything else.
func knowledgeEditCanonicalText(prop *records.Property, sent string, typed records.TypedValue) string {
	switch prop.Type {
	case records.TypeEnum:
		if typed.Enum.Name != "" {
			return typed.Enum.Name
		}
	case records.TypeCheckbox:
		if typed.Bool {
			return "true"
		}
		return "false"
	}
	return sent
}

// knowledgeEditWritesPlain reports whether a validated scalar for prop may be
// written BARE rather than double-quoted (UAT 2026-09-13 D-49): a declared
// integer, decimal or checkbox whose text is exactly the number or boolean
// the schema declares. author.go's SetProperty quotes anything that LOOKS
// numeric or boolean, because on an ungoverned note it cannot know whether
// "480000" is a number or the text "480000" — here the schema has just said
// which, so `budget: 480000` is written the way every other YAML reader
// expects a number to look.
func knowledgeEditWritesPlain(prop *records.Property, value string) bool {
	if prop == nil {
		return false
	}
	switch prop.Type {
	case records.TypeInteger, records.TypeDecimal:
		return authorLooksNumeric(value)
	case records.TypeCheckbox:
		return value == "true" || value == "false"
	}
	return false
}

// knowledgeEditValidateValue validates an INCOMING write (values, isList) against
// property's declaration in src's own record type, if src declares one that
// resolves in set. It returns nil when there is nothing to violate — no
// declared type, an undeclared type, or a rejected schema file
// (knowledgeEditResolveSchema) — or (by construction of the caller) a
// property the schema does not mention gets its own named refusal below.
//
// values holds exactly one element for a scalar write, N for a list write —
// the SHAPE THE CALLER SENT, which is what an arity mismatch is measured
// against (FR-006/FR-042: "the interesting fact is the shape").
//
// gov, when non-nil, is filled in with WHY nothing was checked whenever
// nothing was (G3) — the tool-result layer reads it back to say so instead
// of returning nil indistinguishably from "validated and fine". Passing nil
// is for callers (e.g. execCreate's per-pair splice loop) that compute their
// own governance note separately, over the fully assembled note, rather than
// per property.
// posture is FR-045's applicability for THIS caller — see
// knowledgeEditRelationPosture. It is threaded rather than decided here
// because this function cannot tell a set_property from an op "relation":
// both arrive as "one property, some values, against this note's schema".
//
// It returns the values to WRITE — canonicalised (D-04) when a schema
// governed them, exactly as sent otherwise — and the declared property (nil
// on an ungoverned note), so the caller can decide the bare-vs-quoted
// question (D-49) without a second schema lookup.
func knowledgeEditValidateValue(set *records.SchemaSet, report *records.SchemaLoadReport, src []byte, property string, values []string, isList bool, posture knowledgeEditRelationPosture, gov *knowledgeEditGovernance) ([]string, *records.Property, error) {
	// D-51 applies whether or not a schema governs the note: an ordinary
	// note's `id:` is no more a caller's to write than a record's.
	if rerr := knowledgeEditRefuseReservedProperty(property); rerr != nil {
		return nil, nil, rerr
	}
	// `type` is the record DISCRIMINATOR, not a declared property — no
	// schema declares it as one of its own properties (the assembled-
	// frontmatter check on create exempts it for the same reason), so it is
	// written as sent: a scalar naming the type the note is (becoming).
	//
	// C7 (Claude review round 3): "as sent" used to mean "unchecked", which
	// let a record be retyped to a typo (`dael`) keeping its old-type
	// identifier — pre-D-28 the generic unknown-property refusal caught this
	// by accident, and the short-circuit lost that. The name is now checked
	// against the collection's OWN declarations: writing `type` is always the
	// claim "this note IS a <name> record", and that claim is only verifiable
	// against a schema that loaded. A miss is refused naming the declared
	// types (or the load failure, when the name matches a schema file the
	// loader rejected — the G4 distinction, so a broken schema does not read
	// like a typo); a hit falls through to the caller's own identity rule
	// (knowledgeEditRefuseTypeChange, 2faacf492) exactly as before.
	if property == records.RecordTypeKey {
		if isList {
			return nil, nil, fmt.Errorf("%w: 'type' holds one record type name, not a list", ErrPropertyArity)
		}
		want := strings.TrimSpace(values[0])
		if _, ok := set.Get(want); !ok {
			if reason, rejected := knowledgeEditRejectedSchemaDetail(report, want); rejected {
				return nil, nil, fmt.Errorf("%w: %s's schema file failed to load (%s), so a note cannot be made a %s record until it is fixed — fix the schema with knowledge_configure, then retry",
					ErrUndeclaredRecordType, want, reason, want)
			}
			if types := set.Types(); len(types) > 0 {
				return nil, nil, fmt.Errorf("%w: %q is not one of them; this knowledge base declares %s. 'type' must name a declared record type — create the type first with knowledge_configure (op create_record_type)",
					ErrUndeclaredRecordType, want, strings.Join(types, ", "))
			}
			return nil, nil, fmt.Errorf("%w: no record types are declared in this knowledge base yet, so there is nothing to make a record of — create the type first with knowledge_configure (op create_record_type)",
				ErrUndeclaredRecordType)
		}
		if gov != nil {
			*gov = knowledgeEditGovernance{Reason: knowledgeEditGoverned, TypeName: want}
		}
		return values, nil, nil
	}
	schema, typeName, reason, detail := knowledgeEditResolveSchema(set, report, src)
	if reason != knowledgeEditGoverned {
		if gov != nil {
			*gov = knowledgeEditGovernance{Reason: reason, TypeName: typeName, RejectionReason: detail}
		}
		return values, nil, nil
	}
	if gov != nil {
		*gov = knowledgeEditGovernance{Reason: knowledgeEditGoverned, TypeName: typeName}
	}
	canonical, err := knowledgeEditValidatePropertyAgainstSchema(schema, typeName, property, values, isList, posture)
	if err != nil {
		return nil, nil, err
	}
	prop, _ := schema.Property(property)
	return canonical, prop, nil
}

// knowledgeEditSetPropertyEdit composes schema validation with the low-level
// splice: a NoteEdit that refuses (leaving src untouched) when the value
// does not conform, and otherwise delegates to the scalar or list splice.
//
// posture is threaded rather than fixed because this constructor has TWO
// callers that sit on opposite sides of FR-045 — which is not obvious from
// its name, and cost a real regression to discover:
//
//   - execSetProperty passes knowledgeEditRelationRefused. That is the exact
//     read-then-write shape the rule exists to stop.
//   - execCreate's per-pair splice loop passes knowledgeEditRelationAllowed.
//     It reuses this constructor purely to get the same validate-then-splice
//     composition for each `frontmatter` pair of a note being CREATED, and a
//     create cannot reach FR-045's hazard at all (see
//     knowledgeEditRelationPosture). Hardcoding the refusal here made
//     `op: create` with a relation in its frontmatter fail outright — a
//     capability loss, not an enforcement win.
func knowledgeEditSetPropertyEdit(set *records.SchemaSet, report *records.SchemaLoadReport, property string, values []string, isList bool, posture knowledgeEditRelationPosture, gov *knowledgeEditGovernance) NoteEdit {
	return func(src []byte) ([]byte, error) {
		canonical, prop, err := knowledgeEditValidateValue(set, report, src, property, values, isList, posture, gov)
		if err != nil {
			return nil, err
		}
		if isList {
			return SetPropertyList(property, canonical)(src)
		}
		return setPropertyScalarCheckedWith(property, canonical[0], knowledgeEditWritesPlain(prop, canonical[0]))(src)
	}
}

// knowledgeEditListOpEdit composes schema validation with AddListValue /
// RemoveListValue for set_property's list_op mode.
//
// FR-045 is IN FORCE here (knowledgeEditRelationRefused) even though list_op
// is itself additive and non-destructive, which is worth the sentence it
// costs to justify. The reason is not that `list_op: add` is dangerous — it
// is not. It is that list_op operates on RAW STRINGS with no relation
// semantics at all: it will not wrap a bare target as a wikilink, will not
// enforce a declared relation's cardinality, and cannot tell a relation
// property from a tag list. Leaving it as a second, quieter way to write
// relations would mean the two paths diverge on every relation-specific
// rule op "relation" enforces, and an agent that found this one first would
// never discover the other. One way in, and the refusal names it.
func knowledgeEditListOpEdit(set *records.SchemaSet, report *records.SchemaLoadReport, property, value string, add bool, gov *knowledgeEditGovernance) NoteEdit {
	return func(src []byte) ([]byte, error) {
		canonical, _, err := knowledgeEditValidateValue(set, report, src, property, []string{value}, true, knowledgeEditRelationRefused, gov)
		if err != nil {
			return nil, err
		}
		if add {
			return AddListValue(property, canonical[0])(src)
		}
		return RemoveListValue(property, canonical[0])(src)
	}
}

// knowledgeEditRemovePropertyEdit composes schema validation with
// RemoveProperty for set_property's value:null mode (Claude review round 3,
// finding C6).
//
// The write path (knowledgeEditSetPropertyEdit) and the list path
// (knowledgeEditListOpEdit) each validate against the note's own resolved
// schema before splicing; the removal path used to be the one mode of
// set_property that consulted nothing but the reserved-key list, so
// `value:null` could wipe a whole relation list — the exact read-then-write
// hazard FR-045 exists to stop — and delete a required property's value,
// manufacturing the very missing_required_property finding the write door
// is supposed to prevent. This constructor routes removals through the same
// knowledgeEditResolveSchema authority, with exactly two refusals, in the
// write path's own precedence order:
//
//   - FR-045, FIRST as everywhere: a relation or person property is not
//     removable here. Removing IS the whole-list discard the write refusal
//     names, with none of the intent `relation_op: "replace"` records, so
//     the caller is directed to op "relation" — whose "remove" verb takes
//     one edge at a time and whose "replace" with an empty targets list is
//     the deliberate, on-purpose way to clear the property.
//   - Required: a required property's absence is a validation failure, and
//     a write door does not get to create one behind a 200.
//
// Everything else is REMOVED exactly as before, and that set is deliberate
// rather than residual: a property the schema does not declare (D-93's
// stale pre-rename key — removing it makes the record MORE conformant, and
// TestUAT_D93 pins the behavior), every property of an ordinary note
// (FR-005 — no declaration, nothing to violate), and every optional
// declared property. An undeclared type, a rejected schema file and an
// unparsable frontmatter block are all misses knowledgeEditResolveSchema
// reports through gov, and each leaves the removal allowed, exactly as the
// same misses leave a write allowed.
func knowledgeEditRemovePropertyEdit(set *records.SchemaSet, report *records.SchemaLoadReport, property string, gov *knowledgeEditGovernance) NoteEdit {
	return func(src []byte) ([]byte, error) {
		schema, typeName, reason, detail := knowledgeEditResolveSchema(set, report, src)
		if reason != knowledgeEditGoverned {
			if gov != nil {
				*gov = knowledgeEditGovernance{Reason: reason, TypeName: typeName, RejectionReason: detail}
			}
			return RemoveProperty(property)(src)
		}
		if gov != nil {
			*gov = knowledgeEditGovernance{Reason: knowledgeEditGoverned, TypeName: typeName}
		}
		if prop, ok := schema.Property(property); ok {
			// FR-045 before Required, matching the precedence
			// knowledgeEditValidatePropertyAgainstSchema applies to writes
			// (FR-046 then FR-045 then shape): a relation property has no
			// "required-ness to satisfy" this door may weigh, because it has
			// no business being removed through this door at all.
			if records.IsRelationProperty(prop) {
				return nil, fmt.Errorf("%w: %s.%s is a %s property, and set_property cannot remove one — removing it discards the whole list of edges, "+
					"including any another writer added (ADR-068 FR-045). Use op \"relation\" instead: same collection, path and expect_version as this call, "+
					"plus property: %q, relation_op: \"remove\" and targets naming the one edge to take out (repeat per edge; the rest of the list is left "+
					"untouched), or relation_op: \"replace\" with an empty targets list when clearing the property on purpose",
					ErrRelationProperty, typeName, property, prop.Type, property)
			}
			if prop.Required {
				return nil, fmt.Errorf("%w: %s.%s is required — every %s record must carry a value, so removing it would leave this one carrying a "+
					"missing-value finding. Set a different value instead, or declare the property optional in %s/%s/%s.yaml",
					ErrRequiredProperty, typeName, property, typeName,
					records.VaultMarkerDirName, records.RecordsDirName, typeName)
			}
		}
		return RemoveProperty(property)(src)
	}
}

// ---------------------------------------------------------------------------
// G1 — validating the WHOLE assembled frontmatter of a note being created,
// not just the `frontmatter` argument map.
// ---------------------------------------------------------------------------

// knowledgeEditValidateAssembledFrontmatter validates EVERY property present
// in a freshly-assembled note's frontmatter — not only the ones that arrived
// through create's `frontmatter` argument, but also anything already baked
// into the raw `body` or an expanded `template` before this runs — against
// the note's own declared record type, through the exact same
// knowledgeEditValidatePropertyAgainstSchema authority set_property and link
// use (G1's brief: "the same sentinel errors and message quality — do not
// invent a second error vocabulary").
//
// Governance is resolved ONCE from content's OWN `type:` (already spliced in
// by the time this runs — from the raw body, the template, or a prior
// `frontmatter.type` pair in the same create call; see execCreate's
// pairs-sorted-type-first ordering), so every property this pass checks is
// measured against that SAME schema. There is no separate "type first" step
// to repeat here: by construction, whichever property named `type` is
// already reflected in content by the time the WHOLE document is re-parsed.
//
// RecordTypeKey/RecordIDKey/RecordIDKeyNamespaced are reserved
// discriminator/identity keys, never ordinary declared properties — this
// mirrors records/validate.go's own exclusion list for the identical reason:
// a schema does not (and must not have to) declare `type` as one of its own
// properties for `type: <itself>` to be legal.
//
// An explicit null (`status:` with nothing after it) is FR-007 absence, not
// a value — skipped, exactly as ParseValue's callers elsewhere never see a
// null node either.
//
// It returns the content to WRITE: byte-identical to the input except where
// a governed scalar or list value was accepted in a non-canonical spelling
// (D-04, e.g. a template's `status: Done` against a declared `done`), which
// is re-spliced in the declared spelling through the same SetProperty
// primitives every other write uses.
func knowledgeEditValidateAssembledFrontmatter(set *records.SchemaSet, report *records.SchemaLoadReport, content []byte) ([]byte, knowledgeEditGovernance, error) {
	schema, typeName, reason, detail := knowledgeEditResolveSchema(set, report, content)
	if reason != knowledgeEditGoverned {
		return content, knowledgeEditGovernance{Reason: reason, TypeName: typeName, RejectionReason: detail}, nil
	}

	// A second parse of the same bytes for a different question ("every
	// property present" rather than "what type does this declare") — the
	// same trade-off knowledgeEditResolveSchema's own doc comment already
	// makes for its callers, kept here for the same reason: it keeps the two
	// questions independent rather than threading one parse's internals
	// through both.
	fm, ferr := records.ParseFrontmatter(content)
	if ferr != nil {
		// Not this function's failure to report, by the same reasoning
		// knowledgeEditResolveSchema's own doc comment gives: an unparsable
		// frontmatter block is reported through the governance reason
		// (knowledgeEditUnparsable), not as an error — the caller's own
		// CreateNote call still writes the note as ordinary content when
		// nothing else refuses it, which is correct: an unparsable
		// frontmatter block is not this layer's problem to solve.
		return content, knowledgeEditGovernance{Reason: knowledgeEditUnparsable}, nil //nolint:nilerr // reported via the governance reason, not an error
	}

	out := content
	for _, key := range fm.Keys {
		if key == records.RecordTypeKey || key == records.RecordIDKey || key == records.RecordIDKeyNamespaced {
			continue
		}
		node := fm.Values[key]
		if node.Kind == records.KindNull {
			continue
		}
		var values []string
		isList := node.Kind == records.KindSequence
		switch node.Kind {
		case records.KindScalar:
			values = []string{node.Text}
		case records.KindSequence:
			for i, item := range node.Items {
				if item.Kind != records.KindScalar {
					return nil, knowledgeEditGovernance{}, fmt.Errorf(
						"frontmatter.%s: %w: element %d is %s, not a single value",
						key, ErrPropertyValue, i, item.Kind)
				}
				values = append(values, item.Text)
			}
		default: // records.KindMapping — no property type accepts one
			return nil, knowledgeEditGovernance{}, fmt.Errorf(
				"frontmatter.%s: %w: is a mapping, not a single value or a list",
				key, ErrPropertyValue)
		}
		// FR-045 does NOT apply (knowledgeEditRelationAllowed). This pass
		// runs only on a CREATE, which author.go refuses outright when the
		// file already exists (ErrNoteExists) — so there is no prior list of
		// edges to replace and no other writer's additions to lose, which is
		// the entire hazard FR-045 names. Enforcing it here would instead
		// break every note created from a TEMPLATE that mentions a relation
		// property, because this pass validates the whole ASSEMBLED
		// frontmatter — template and raw-body bytes included, not just the
		// caller's own `frontmatter` argument. FR-046 above still applies:
		// a derived value is wrong to store whether or not the note is new.
		canonical, err := knowledgeEditValidatePropertyAgainstSchema(schema, typeName, key, values, isList, knowledgeEditRelationAllowed)
		if err != nil {
			return nil, knowledgeEditGovernance{}, fmt.Errorf("frontmatter.%s: %w", key, err)
		}
		if !stringSlicesEqual(canonical, values) {
			var splice NoteEdit
			if isList {
				splice = SetPropertyList(key, canonical)
			} else {
				prop, _ := schema.Property(key)
				splice = setPropertyScalarCheckedWith(key, canonical[0], knowledgeEditWritesPlain(prop, canonical[0]))
			}
			next, serr := splice(out)
			if serr != nil {
				return nil, knowledgeEditGovernance{}, fmt.Errorf("frontmatter.%s: %w", key, serr)
			}
			out = next
		}
	}
	return out, knowledgeEditGovernance{Reason: knowledgeEditGoverned, TypeName: typeName}, nil
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
