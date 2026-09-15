// Omnipus — ADR-068 D14 / ADR-083 §4.6: the record-write guards, in the one
// package BOTH write doors can reach.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
	"strings"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// These rules decide WHAT A CALLER MAY CHANGE about a record. Until now they
// lived inside pkg/gateway's record-write handler, which made them reachable
// from exactly one door: the browser's. An agent-facing record write cannot
// import a gateway handler, so the day one is built it reimplements all four
// — and two independently-written copies of a permission rule do not stay
// equal. Drift in a rendering helper produces an ugly screen; drift here
// produces a door that permits what the other refuses, silently.
//
// So they live here, one layer BELOW both doors. The import direction is what
// makes that work and it is one-way: pkg/knowledge imports pkg/records and
// pkg/gateway imports both, while pkg/records imports neither. Putting the
// guards in pkg/knowledge would have left pkg/records unable to reach them
// and pulled the whole note-authoring package into anything that only wanted
// to validate a property; putting them in pkg/gateway is the situation this
// file exists to end.
//
// ---------------------------------------------------------------------------
// WHAT THIS FILE DOES NOT DO
//
// It does not splice, lock, version-check or audit. It answers one question —
// "may this property be written, and if so what does the caller mean" — and
// returns a decision the CALLER turns into its own edit primitives. That
// boundary is deliberate: pkg/knowledge's NoteEdit constructors are one layer
// up (they import this package), so a guard that emitted them would invert
// the dependency and could not live here at all.
//
// THE RULES ARE UNCHANGED BY THE MOVE. Every refusal below returns the same
// message, in the same precedence order, that pkg/gateway's
// buildRecordPropertyEdits returned when it owned them. That is the whole
// contract of the move: a caller cannot tell from behaviour that the code
// relocated, only that a second caller now gets it too.
// ---------------------------------------------------------------------------

// WriteRefusalCode names WHY a property write was refused, in terms of the
// RULE that refused it rather than any one door's transport.
//
// The codes are the audit vocabulary an operator greps, so they are stable
// strings rather than an opaque int: "the write was refused" alone does not
// tell anyone whether to fix a client, fix a schema, or chase a second
// writer. A door maps a code onto its own transport (pkg/gateway maps to an
// HTTP status); it never re-derives the reason.
type WriteRefusalCode string

const (
	// RefusalPropertyUnknown — the record type declares no such property.
	RefusalPropertyUnknown WriteRefusalCode = "unknown_property"
	// RefusalDerivedProperty — FR-046: the property carries a Formula
	// declaration, so its value is computed and never stored.
	RefusalDerivedProperty WriteRefusalCode = "derived_property"
	// RefusalRelationProperty — FR-045: relation and person properties are
	// not writable through a record write.
	RefusalRelationProperty WriteRefusalCode = "relation_property"
	// RefusalListProperty — ADR-083 §4.6: the write would SHRINK a
	// list-valued property, discarding values it already holds.
	RefusalListProperty WriteRefusalCode = "list_property"
	// RefusalIdentityProperty — ADR-068 D1/D7: the entry names one of the
	// record's identity keys.
	RefusalIdentityProperty WriteRefusalCode = "identity_property"
	// RefusalInvalidValue — the entry is malformed, mis-arity, or carries a
	// value the property's declaration does not permit (a closed enum's
	// non-member included).
	RefusalInvalidValue WriteRefusalCode = "invalid_value"
)

// WriteRefusalCodes is every code this package returns.
//
// It exists so a door's code-to-transport mapping can be pinned against a
// NAMED source of truth rather than a transcription of one: a seventh code
// added here fails that door's test instead of silently falling through to
// whatever its switch defaults to. Same discipline as wire.go's enum pins,
// and for the same reason — the failure it prevents is invisible at runtime.
var WriteRefusalCodes = []WriteRefusalCode{
	RefusalPropertyUnknown,
	RefusalDerivedProperty,
	RefusalRelationProperty,
	RefusalListProperty,
	RefusalIdentityProperty,
	RefusalInvalidValue,
}

// WriteRefusal is one refused property write.
//
// Property is carried separately from Message even though Message names it
// too: a door that wants to report the field a form should highlight needs
// the name as data, not as a substring to parse back out of English prose.
//
// IT IS DELIBERATELY NOT AN `error`. It carried an `Error() string` method
// briefly and that was a mistake worth naming, because the cost is invisible:
// the moment this satisfies the error interface, the shortest correct-looking
// thing a caller can write is `return refusal` into some `error` return, and
// at that point the Code and Property — the two fields the whole design
// exists to carry — are flattened into a string nobody can branch on again.
// A refusal is a VERDICT, returned beside a value rather than in the error
// position, and every door is expected to translate it into its own
// vocabulary: pkg/gateway maps Code to an HTTP status and an audit reason, and
// an agent-facing door would map it to a tool result. Neither wants a bare
// string.
type WriteRefusal struct {
	Code     WriteRefusalCode
	Property string
	Message  string
}

// PropertyWriteKind is what a validated entry MEANS on disk.
type PropertyWriteKind int

const (
	// PropertyWriteClear removes the property (D3.2: an empty values array
	// is an explicit clear, not a no-op and not a collapse).
	PropertyWriteClear PropertyWriteKind = iota
	// PropertyWriteScalar sets a single-valued property.
	PropertyWriteScalar
	// PropertyWriteList replaces a list-valued property's whole list.
	PropertyWriteList
)

// PropertyWrite is one validated, permitted property write.
//
// Values holds the resolved SCALAR TEXT each value contributes — the form
// the frontmatter splice needs — never the wire value it came from. Deciding
// that text is part of validation (which field of a RecordValue counts is a
// function of the SCHEMA's declared type, not of what the client filled in),
// so it is settled here and not a second time by every caller.
type PropertyWrite struct {
	Property string
	Kind     PropertyWriteKind
	Values   []string
}

// ---------------------------------------------------------------------------
// THE TWO RULE PREDICATES BOTH DOORS SHARE
//
// CheckRecordPropertyWrites below is not callable from the agent door, and
// that is a fact about SHAPES rather than a failure of will: it takes
// []generated.RecordPropertyValue (a wire type the agent door never
// constructs — knowledge_edit receives plain strings out of a tool-call
// argument map) and it bundles FOUR more rules the agent door must NOT
// inherit. Two of those would be outright regressions there: the
// list-shrink guard (§4.6) would refuse set_property's `list_op: remove`,
// whose entire purpose is to shrink a list by one, and the identity-key
// refusal duplicates a check knowledge_edit already applies with its own
// wording. A third, the arity refusal, deliberately reads differently on
// the agent door (it names the schema FILE to edit, which is actionable for
// an agent and meaningless to a browser form).
//
// So what is shared is what CAN be shared without either door lying: the
// two PREDICATES that decide whether a rule applies at all. The DECISION
// lives here, once. The refusal WORDING stays per-door, because the two
// doors must say different things — a browser is told to use a different
// request type, an agent is told the exact op and arguments to retry with,
// and collapsing those into one string would leave one of them useless.
//
// What drifts, if these are copied rather than shared, is exactly this:
// what COUNTS as derived, and what COUNTS as a relation. Add a second
// formula-bearing field or a third relation-shaped PropertyType and a
// copied check goes quietly out of date on whichever door nobody edited.
// These two functions are the only place either question is answered.
// ---------------------------------------------------------------------------

// IsDerivedProperty reports whether prop's value is COMPUTED rather than
// stored — FR-046's rule, as a predicate rather than a refusal.
//
// A nil prop answers false: "no declaration" is not "a derived
// declaration", and a caller that failed to resolve a property has an
// unknown-property refusal to give, not this one.
func IsDerivedProperty(prop *Property) bool {
	return prop != nil && prop.Formula != ""
}

// IsRelationProperty reports whether prop holds relation edges — FR-045's
// rule, as a predicate rather than a refusal.
//
// TypePerson is included and that is the whole reason this is a function
// rather than an inline `== TypeRelation`: a person property is a relation
// to whatever record type the vault uses for people (schema.go's own
// TypePerson comment), so it carries every one of the list-replacement
// hazards FR-045 exists to prevent. A door that checked only TypeRelation
// would enforce half the rule and report full compliance.
func IsRelationProperty(prop *Property) bool {
	return prop != nil && (prop.Type == TypeRelation || prop.Type == TypePerson)
}

// CheckRecordPropertyWrites validates every entry against sc's declarations
// and returns what each one means, or the FIRST refusal.
//
// current is the record as it stands on disk, or nil on a create. It is read
// for exactly one purpose: the list-shrink guard, which cannot be evaluated
// without knowing what the property already holds.
//
// THE REFUSALS RUN BEFORE arity or value conformance is checked, and every
// one of them reads the property's OWN declaration off the resolved schema —
// never a flag the request claims about itself. The client is not a party
// this layer trusts to have respected VaultFindCell.derived/.relation/.many;
// those exist so a UI knows what to OFFER, not so the server can skip
// checking. An agent has no UI at all, which is precisely why the check
// cannot live in one.
//
// Precedence, and it is load-bearing rather than incidental:
//
//	identity   `type`/`id`/`omni_id` — refused BEFORE the schema is consulted,
//	           because the refusal does not depend on what the schema says. A
//	           schema is free to declare a property literally named `id`, and
//	           writing through it would rename the record while clearing it
//	           would delete the record's identifier — making it unreachable
//	           through every record door AND invisible to the allocator's
//	           collision check, which would then hand the same identifier to a
//	           second record. All behind a 200.
//	unknown    the type declares no such property, with the declared set named.
//	FR-046     a Formula-bearing property is refused before the relation check
//	           and before arity: a derived property has no meaningful "arity to
//	           satisfy" refusal, so this is the clearest reason to report.
//	FR-045     relation and person properties.
//	arity      a scalar property sent more than one value.
//	§4.6       a list-valued property whose list would SHRINK.
//	value      ParseValue, which is where a closed enum's membership is
//	           decided — the same authority every read-time validation uses,
//	           so a value that survives a write is one a read will accept.
//
// ⚠️ THE FR-046 BRANCH IS UNREACHABLE FROM LoadSchemas TODAY, AND MUST NOT BE
// DELETED AS DEAD CODE (ADR-083 review M3/F14). A schema file declaring
// `formula:` on a property is refused at load (schema.go's propertyDeclKeys),
// so no *Schema that came from LoadSchemas can carry one. It is defence in
// depth held in reserve against a *Schema reaching a write door from
// somewhere else — a saved view's namespace synthesis already constructs
// Formula-bearing properties, in this very package. Now that the guard lives
// beside that synthesis rather than one package away, the reader who wonders
// whether the branch is live can see both halves at once.
func CheckRecordPropertyWrites(sc *Schema, props []generated.RecordPropertyValue, current *Record) ([]PropertyWrite, *WriteRefusal) {
	writes := make([]PropertyWrite, 0, len(props))
	seen := make(map[string]bool, len(props))
	for _, item := range props {
		name := strings.TrimSpace(item.Property)
		if name == "" {
			return nil, &WriteRefusal{RefusalInvalidValue, "", "a property entry must name a property"}
		}
		if seen[name] {
			return nil, &WriteRefusal{RefusalInvalidValue, name,
				fmt.Sprintf("property %q is named more than once in the same write", name)}
		}
		seen[name] = true

		if refusal := identityKeyRefusal(sc, name); refusal != nil {
			return nil, refusal
		}

		prop, ok := sc.Property(name)
		if !ok {
			return nil, &WriteRefusal{RefusalPropertyUnknown, name,
				fmt.Sprintf("%s declares no property %q; declared properties are %s",
					sc.Type, name, strings.Join(sc.PropertyNames(), ", "))}
		}
		if IsDerivedProperty(prop) {
			return nil, &WriteRefusal{RefusalDerivedProperty, name,
				fmt.Sprintf("%s.%s is a derived value, computed rather than stored; it cannot be written", sc.Type, name)}
		}
		if IsRelationProperty(prop) {
			return nil, &WriteRefusal{RefusalRelationProperty, name,
				fmt.Sprintf("%s.%s is a %s property; relations and person properties are not writable through this request — "+
					"they are modified through RelationWriteRequest's explicit add/remove/replace verbs (on the agent door, "+
					"knowledge_edit's op %q)",
					sc.Type, name, prop.Type, "relation")}
		}

		if len(item.Values) == 0 {
			writes = append(writes, PropertyWrite{Property: name, Kind: PropertyWriteClear})
			continue
		}

		values := make([]string, 0, len(item.Values))
		for i, rv := range item.Values {
			text, ok := PropertyValueText(rv, prop.Type)
			if !ok {
				return nil, &WriteRefusal{RefusalInvalidValue, name,
					fmt.Sprintf("%s.%s[%d] does not carry a %s value", sc.Type, name, i, prop.Type)}
			}
			values = append(values, text)
		}
		if !prop.Many && len(values) != 1 {
			return nil, &WriteRefusal{RefusalInvalidValue, name,
				fmt.Sprintf("%s.%s holds one value; got %d — send a single value, or declare many: true",
					sc.Type, name, len(values))}
		}
		if refusal := listArityDropRefusal(sc, prop, current, len(values)); refusal != nil {
			return nil, refusal
		}
		for _, v := range values {
			node := Node{Kind: KindScalar, Text: v}
			if _, verr := ParseValue(prop, node); verr != nil {
				msg := fmt.Sprintf("%s.%s holds %q, which is not %s", sc.Type, name, v, verr.Expected)
				if len(verr.Permitted) > 0 {
					msg += "; permitted values are " + strings.Join(verr.Permitted, ", ")
				}
				return nil, &WriteRefusal{RefusalInvalidValue, name, msg}
			}
		}
		kind := PropertyWriteScalar
		if prop.Many {
			kind = PropertyWriteList
		}
		writes = append(writes, PropertyWrite{Property: name, Kind: kind, Values: values})
	}
	if len(writes) == 0 {
		return nil, &WriteRefusal{RefusalInvalidValue, "", "properties must not be empty"}
	}
	return writes, nil
}

// identityKeyRefusal refuses a write naming `type`, `id` or `omni_id`.
//
// A second, independent copy of this guard is enforced by the note-edit
// primitives at the splice, and deliberately so: this one exists to give the
// CALLER a truthful, specific refusal with a distinct audit reason, where the
// splice-level one exists to protect every OTHER caller of those primitives.
// Neither makes the other redundant — a guard only a write door applies
// protects nothing anything else does, and a guard only the splice applies
// surfaces to a client as a generic internal error.
func identityKeyRefusal(sc *Schema, name string) *WriteRefusal {
	switch name {
	case RecordTypeKey, RecordIDKey, RecordIDKeyNamespaced:
		return &WriteRefusal{RefusalIdentityProperty, name,
			fmt.Sprintf("%s.%s carries the record's identity and cannot be written or cleared through this request "+
				"(ADR-068 D1/D7): a record with no type is not a record, and a record with no id can never be "+
				"found again and lets its identifier be minted to a second record", sc.Type, name)}
	}
	return nil
}

// listArityDropRefusal is ADR-083 §4.6's "a list-valued property gets no
// editor", enforced where a client cannot bypass it.
//
// THE DATA LOSS IT PREVENTS, concretely. A `many: true` property holding
// `[alpha, beta]` renders on the wire as the single string "alpha, beta" —
// VaultFindCell.value is one rendered string whatever the arity. An editor
// offered on that cell sends back ONE value, and a list set replaces the
// WHOLE list span: two tags become one tag whose text is "alpha, beta".
// Success, audit `decision: allow`, and the reader never sees that anything
// was lost. Worse for a `many` ENUM, where the joined text matches no
// declared member, so picking any real option from the dropdown drops the
// rest.
//
// THE RULE IS "MUST NOT SHRINK", NOT "MUST NOT TOUCH", and that choice is
// deliberate. §4.2c is explicit that the absence of a list editor is "a scope
// decision, not a platform limit ... a scope decision with a way forward,
// rather than a wall" — so refusing every write to every list property would
// build the wall the ADR had just declined to build, and would break a future
// multi-value control that legitimately sends the whole list. What must never
// happen is a write that silently discards values the record already holds:
//
//	N == 0     allowed. An empty values array is D3.2's explicit CLEAR, not a
//	           silent collapse; the caller plainly asked for it.
//	N >= K     allowed. The caller sent at least as many values as are stored,
//	           which is a deliberate whole-list write, not a joined string.
//	0 < N < K  REFUSED. The only way to reach this is a client treating the
//	           joined rendering as a single value.
//
// K is counted from the record's CONFORMING values, which is the same
// population a re-read would return — a non-conforming element is already
// absent from what the client was shown, so it cannot be what the client
// meant to preserve.
func listArityDropRefusal(sc *Schema, prop *Property, current *Record, sending int) *WriteRefusal {
	if !prop.Many || current == nil || sending == 0 {
		return nil
	}
	held := len(ResolveProperty(*current, prop).Values)
	if sending >= held {
		return nil
	}
	return &WriteRefusal{RefusalListProperty, prop.Name,
		fmt.Sprintf("%s.%s is a list holding %d values and this write sends %d, which would discard the rest. "+
			"A list-valued property has no inline editor (ADR-083 §4.6) precisely because its cell renders as one "+
			"joined string: send every value the list should end up with, or an empty values array to clear it",
			sc.Type, prop.Name, held, sending)}
}

// PropertyValueText extracts the plain scalar text a frontmatter splice needs
// from a wire RecordValue, per the SCHEMA's declared type — not the value's
// own optional `type` field, which the write-request contract explicitly
// makes advisory ("the schema is the authority").
//
// The second return is false when the value carries nothing for the declared
// type, which is a refusal and not an empty string: a caller that sent
// `{"text": "x"}` for an integer property has not sent an empty integer, it
// has sent the wrong kind of thing, and the two must not answer alike.
func PropertyValueText(rv generated.RecordValue, propType PropertyType) (string, bool) {
	switch propType {
	case TypeText:
		if rv.Text != nil {
			return *rv.Text, true
		}
	case TypeEnum:
		if rv.Enum != nil {
			return *rv.Enum, true
		}
	case TypeDate:
		if rv.Date != nil {
			return *rv.Date, true
		}
	case TypeInteger:
		if rv.Integer != nil {
			return *rv.Integer, true
		}
	case TypeDecimal:
		if rv.Decimal != nil {
			return *rv.Decimal, true
		}
	case TypeCheckbox:
		if rv.Checkbox != nil {
			if *rv.Checkbox {
				return "true", true
			}
			return "false", true
		}
	}
	return "", false
}
