// Omnipus — ADR-068 D24.2/D24.3 / spec FR-130, FR-140, FR-142, FR-143a: the
// three property namespaces one query resolves names against.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"fmt"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// ONE NAMESPACE, NOT THREE LOOKUP PATHS
//
// A property position — a filter leaf, a sort key, a group_by, a select column,
// a summary target, a formula operand — may now name three different kinds of
// thing:
//
//	status          a property the record type declares
//	file.mtime      one of FR-130's twelve filterable virtual properties
//	formula.age     a computed property the saved view declared (FR-141)
//
// THE POINT OF THIS FILE IS THAT THEY ARE THE SAME KIND OF THING DOWNSTREAM.
// Each resolves to a *records.Property, and from there the comparator, the
// arity rule, the operator table, the sorter, the grouper and the fifteen
// summaries cannot tell them apart — which is FR-142's "goes through the ONE
// comparator like any property value" and FR-143a's "the comparator sees the
// formula's declaration exactly as it sees a schema property's", expressed as
// one resolution function rather than three parallel ones that would eventually
// disagree.
//
// RULING R-A IS UNAFFECTED AND IS AFFECTED BY NOTHING HERE. Nothing in this
// file touches a Selector, a store or a statement. A name resolved here becomes
// a declaration the Go comparator reads; the store is still told only a record
// type, a kind and a path prefix (query.selector).
//
// THE COMPOSITE SCHEMA carries every resolvable name, so records.Filter.Prepare
// — which owns FR-024's refusals, the operator table and R-13's arity rule —
// validates a `file.*` or `formula.*` leaf through exactly the code that
// validates a declared one. Its PropertyOrder deliberately holds ONLY the
// declared properties: PropertyOrder is what a default `select` renders and
// what FR-024's "declared: ..." list quotes, and neither should grow twelve
// file columns because a view happened to sort by one.
// ---------------------------------------------------------------------------

// FormulaNamespace is the reserved prefix a query addresses a view's computed
// properties through (FR-140: "a query reaches a formula only as
// `formula.<name>`").
const FormulaNamespace = "formula."

// isFormulaNamespace reports whether a name is addressed to the formula
// namespace at all — true for `formula.age` AND for the undefined
// `formula.aeg`. Same distinction records.IsFileNamespace draws, for the same
// reason: a name IN the namespace that does not resolve is "you named a formula
// this view does not define, here are the ones it does", while a name outside
// it is the schema's business.
func isFormulaNamespace(name string) bool {
	return strings.HasPrefix(name, FormulaNamespace)
}

// namespace is the set of property names ONE query may resolve, and the single
// refusal ladder for a name it cannot.
type namespace struct {
	// schema is the record type the query named. NIL IS LEGAL and is the
	// untyped multi-type view (FR-018d): a query with no `type` still resolves
	// `file.*` and `formula.*`, because neither is scoped to a record type.
	schema *records.Schema
	// formulas is the saved view's validated formula set, nil when the view
	// declared none or no view was named.
	formulas *records.FormulaSet
	// composite is schema ∪ file.* ∪ formula.*, for records.Filter.Prepare.
	composite *records.Schema
	// formulaProps holds ONE stable *Property per formula. Stable because the
	// comparator reads the declaration off the operand and the memo keys on the
	// name: two pointers for one formula would be two declarations that could
	// drift.
	formulaProps map[string]*records.Property
}

// newNamespace builds the composite for one query.
func newNamespace(schema *records.Schema, formulas *records.FormulaSet) *namespace {
	ns := &namespace{
		schema:       schema,
		formulas:     formulas,
		formulaProps: map[string]*records.Property{},
	}

	composite := &records.Schema{
		SchemaVersion: records.SupportedSchemaVersion,
		Properties:    map[string]*records.Property{},
	}
	if schema != nil {
		composite.Type = schema.Type
		composite.Label = schema.Label
		composite.Identity = schema.Identity
		composite.SourcePath = schema.SourcePath
		composite.Fingerprint = schema.Fingerprint
		composite.PropertyOrder = append([]string(nil), schema.PropertyOrder...)
		for name, prop := range schema.Properties {
			composite.Properties[name] = prop
		}
	}

	// The twelve filterable file.* properties. `file.file` is deliberately
	// absent — records.FileProperty refuses it, and a composite that carried it
	// would make a comparison over a display value constructible.
	for _, name := range records.FileFilterablePropertyNames {
		if prop, ok := records.FileProperty(name); ok {
			composite.Properties[name] = prop
		}
	}

	// The view's formulas, each wearing its ONE static declaration (FR-143a).
	for _, name := range formulas.Names() {
		decl, ok := formulas.Get(name)
		if !ok {
			continue
		}
		pt, ok := records.FormulaPropertyType(decl.Type)
		if !ok {
			// A PRESENTATION formula (link/icon/format/asLink). R-16/FR-147:
			// it has no comparator type by rule, so it gets no *Property and
			// therefore cannot reach a comparison. resolve() refuses it by
			// name rather than reporting it as undefined.
			continue
		}
		prop := &records.Property{
			Name:    FormulaNamespace + name,
			Type:    pt,
			Many:    decl.Arity == records.ArityMany,
			Formula: decl.Source,
		}
		ns.formulaProps[name] = prop
		composite.Properties[prop.Name] = prop
	}

	ns.composite = composite
	return ns
}

// formulaNames lists the view's formulas as a caller would write them, for a
// refusal that has to say what WOULD have resolved.
func (ns *namespace) formulaNames() []string {
	names := ns.formulas.Names()
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, FormulaNamespace+n)
	}
	sort.Strings(out)
	return out
}

// resolve maps one name in one position onto its declaration, or refuses.
//
// `position` is the argument the caller wrote it in ("filter", "sort",
// "group_by", "select", "aggregate") and appears in the refusal, because a
// caller with a dozen names in one request cannot act on a message that does
// not say which one.
func (ns *namespace) resolve(position, name string) (*records.Property, *RefusalError) {
	switch {
	case records.IsFileNamespace(name):
		return ns.resolveFile(position, name)
	case isFormulaNamespace(name):
		return ns.resolveFormula(position, name)
	}

	if ns.schema == nil {
		// FR-018d's untyped view reaching for a typed property. The message
		// names BOTH escapes, because "add a type" is only one of them and the
		// other is frequently what the caller wanted.
		p := problem(generated.UnsupportedParameter,
			fmt.Sprintf("%s names the property %q, but no record type was given, so there is nothing to "+
				"resolve it against", position, name),
			"add type=<record type>, or name a file.* property (or a view formula), neither of which needs one")
		p.Property = str(name)
		permitted := append([]string(nil), records.FileFilterablePropertyNames...)
		permitted = append(permitted, ns.formulaNames()...)
		p.Permitted = &permitted
		return nil, refuse(p, nil)
	}

	prop, ok := ns.schema.Property(name)
	if !ok {
		names := ns.schema.PropertyNames()
		p := problem(generated.UnknownProperty,
			fmt.Sprintf("unknown property %q on record type %q; declared: %s",
				name, ns.schema.Type, strings.Join(names, ", ")),
			"call knowledge_describe record_type="+ns.schema.Type+" to see the declared properties")
		p.Property = str(name)
		p.Permitted = &names
		return nil, refuse(p, nil)
	}
	return prop, nil
}

// resolveFile is FR-130's namespace, with FR-024's posture applied inside it.
func (ns *namespace) resolveFile(position, name string) (*records.Property, *RefusalError) {
	permitted := append([]string(nil), records.FileFilterablePropertyNames...)

	if name == records.FileSelfProp {
		// FR-130 puts `file.file` outside the filterable set explicitly. It is
		// refused BY NAME rather than reported as unknown, because the caller
		// did not misspell anything — they named a real thing in a position it
		// does not belong in, and telling them it does not exist would send
		// them looking for a typo.
		p := problem(generated.UnsupportedParameter,
			fmt.Sprintf("%s names %s, which is a display and formula operand only — it is never a "+
				"comparison, sort, group or summary target (FR-130)", position, records.FileSelfProp),
			"use file.path or file.name to identify the note, or file.asLink() inside a formula")
		p.Property = str(name)
		p.Permitted = &permitted
		return nil, refuse(p, nil)
	}

	prop, ok := records.FileProperty(name)
	if !ok {
		p := problem(generated.UnknownProperty,
			fmt.Sprintf("%s names %q, which is not a file property; the file properties are %s",
				position, name, strings.Join(permitted, ", ")),
			"correct the name — the file. namespace is reserved and closed at these twelve")
		p.Property = str(name)
		p.Permitted = &permitted
		return nil, refuse(p, nil)
	}
	return prop, nil
}

// resolveFormula is FR-140's namespace. Every refusal here says where formulas
// come from, because a formula is declared on a saved VIEW and a caller who has
// only ever written queries has no reason to know that.
func (ns *namespace) resolveFormula(position, name string) (*records.Property, *RefusalError) {
	short := strings.TrimPrefix(name, FormulaNamespace)

	if ns.formulas.Len() == 0 {
		p := problem(generated.UnknownProperty,
			fmt.Sprintf("%s names %q, but this query has no formulas: a formula is declared on a saved "+
				"view's `formulas:` map and reached through view=<name>", position, name),
			"add view=<the view that declares this formula>, or declare it with knowledge_configure")
		p.Property = str(name)
		return nil, refuse(p, nil)
	}

	decl, ok := ns.formulas.Get(short)
	if !ok {
		names := ns.formulaNames()
		p := problem(generated.UnknownProperty,
			fmt.Sprintf("%s names %q, which this view does not define; it defines %s",
				position, name, strings.Join(names, ", ")),
			"correct the name, or add the formula to the view with knowledge_configure")
		p.Property = str(name)
		p.Permitted = &names
		return nil, refuse(p, nil)
	}

	prop, ok := ns.formulaProps[short]
	if !ok {
		// The formula exists and produces a PRESENTATION value. R-16/FR-147:
		// "the comparator refuses a comparison over one with the reason named".
		// The refusal is here rather than in the comparator because there is no
		// *Property to hand it — which is the type-system half of the same
		// rule (FormulaResult.PropertyValue returns ok=false for exactly this).
		p := problem(generated.TypeMismatch,
			fmt.Sprintf("%s names %q, which is a presentation value (%s) — link(), icon(), format() and "+
				"file.asLink() render, they do not compare (FR-147)",
				position, name, decl.Type),
			"select it to render it as a column, and compare the property it was built from instead")
		p.Property = str(name)
		return nil, refuse(p, nil)
	}
	return prop, nil
}

// touchedFileProperties is the file.* names this query actually resolved, which
// is what decides which child-table prepasses run at all. A query naming none
// pays for none.
func (q *query) touchedFileProperties() map[string]bool {
	out := map[string]bool{}
	for _, name := range q.touched {
		if records.IsFileNamespace(name) {
			out[name] = true
		}
	}
	// A formula operand never appears in q.touched — the query names
	// `formula.x`, not the `file.mtime` inside it — so the formula's SOURCE is
	// scanned too. Missing this is how `formula.age` over `file.mtime` would
	// evaluate against a FileMeta whose tags were never streamed: absent, with
	// no error anywhere, which is the silent wrong answer this design removes.
	//
	// WHY THE SOURCE TEXT AND NOT THE TREE. records.FormulaNode's children()
	// is unexported — the AST is closed to its own package (formula_ast.go's
	// isFormulaNode marker) — so there is no exported walk to ask. The scan is
	// therefore over the source, WHITESPACE STRIPPED, and the stripping is what
	// makes it sound rather than approximate: the lexer emits `.` as its own
	// token, so `file . backlinks` is a legal spelling of `file.backlinks`
	// whose raw text does not contain the name. With whitespace removed, no
	// spelling the lexer accepts can hide a reference, because an identifier
	// and a dot are the only tokens a dotted path is made of.
	//
	// It over-includes — a text literal `"see file.tags"` runs the tag prepass
	// for nothing — and that is the correct direction to be wrong in: the cost
	// is one child-table scan, and the alternative is an absent value.
	for _, name := range q.namespace().formulas.Names() {
		decl, ok := q.namespace().formulas.Get(name)
		if !ok {
			continue
		}
		src := strings.Join(strings.Fields(decl.Source), "")
		for _, fileProp := range records.FileFilterablePropertyNames {
			if strings.Contains(src, fileProp) {
				out[fileProp] = true
			}
		}
	}
	return out
}

// namespace returns the query's namespace, never nil, so a caller does not have
// to guard a query built before one was attached.
func (q *query) namespace() *namespace {
	if q.ns == nil {
		q.ns = newNamespace(q.schema, nil)
	}
	return q.ns
}

// buildNamespace validates the view's formula sources and attaches the
// namespace to the query.
//
// FR-140 puts the PARSER in the write path and only there — `knowledge_find`
// accepts no text expression anywhere. What runs here is the LOAD-path
// re-validation the same requirement mandates ("the view loader re-validates on
// load; a hand-edited file is re-checked"), over source text that reached the
// query only because a saved view already held it. The distinction is not
// pedantry: a caller cannot get an expression in front of this parser, so a
// parse failure can never be something the model wrote, and it can never become
// an empty result either — it is a refusal naming the formula and the position.
func (q *query) buildNamespace(sources map[string]string) *RefusalError {
	if len(sources) == 0 {
		q.ns = newNamespace(q.schema, nil)
		return nil
	}

	set, errs := records.ValidateFormulaSet(sources, q.schema)
	if len(errs) > 0 {
		// EVERY error, not the first. ValidateFormulaSet reports them all
		// precisely so an author fixing four formulas does not do it one
		// round-trip at a time, and collapsing them here would throw that away.
		reasons := make([]string, 0, len(errs))
		for _, e := range errs {
			reasons = append(reasons, e.Error())
		}
		sort.Strings(reasons)
		p := problem(generated.UnsupportedParameter,
			fmt.Sprintf("the saved view's formulas did not validate: %s", strings.Join(reasons, "; ")),
			"correct the view's `formulas:` map with knowledge_configure — a formula is validated at write "+
				"AND at load, so a hand-edited view file is re-checked here")
		return refuse(p, nil)
	}

	q.ns = newNamespace(q.schema, set)
	return nil
}
