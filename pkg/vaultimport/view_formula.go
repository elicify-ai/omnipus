// view_formula.go: Synthesise filter formulas

package vaultimport

import (
	"fmt"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"gopkg.in/yaml.v3"
)

// jsFalsyLiterals are the value spellings Obsidian's own JavaScript truthy
// test rejects. `false` and `0` are here; `"false"` as a QUOTED string is not,
// because a non-empty string is truthy in JavaScript — but this importer
// cannot see a value's quoting (observedValue carries text, and an enum's
// declared values are text by the time they reach a schema), so a declared
// value that READS like one of these is treated as one. That is the safe
// direction: it costs a view that is disabled when it need not have been.
var jsFalsyLiterals = map[string]bool{
	"false": true,
	"0":     true,
	"0.0":   true,
	"":      true,
}

// enumDeclaresAFalsyValue reports whether any of an enum's declared values is
// one Obsidian's truthy test would reject.
//
// THE TYPE ALONE IS NOT ENOUGH FOR AN ENUM, and this is the residual half of
// the finding that moved boolean inference to `checkbox`. `enum` is on the safe
// side of the partition because most enums are controlled vocabularies of
// words, every one of which is truthy. But an enum is whatever the vault put in
// it: `level: 0 / high / low` infers an enum declaring `0`, and `IS NOT NULL`
// then matches the record holding 0 that Obsidian's bare `level` filter
// rejects. So for this one type the partition consults the DECLARED VALUES,
// which is the only place the answer actually lives.
func enumDeclaresAFalsyValue(values []string) bool {
	for _, v := range values {
		if jsFalsyLiterals[records.FoldKey(strings.TrimSpace(v))] {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// FR-105 — WHERE "has a value" IS NOT "is truthy"
//
// Obsidian's bare-property filter (`archived`) is a JavaScript truthy test.
// Our nearest operator is `IS NOT NULL`, which asks a DIFFERENT question: does
// this record have a value at all. The two agree exactly when every value a
// property can hold is truthy, and they part company on the falsy-but-present
// ones — `false` on a checkbox, `0` on a number, `""` on a TEXT property.
// There the `IS NOT NULL` matches a record Obsidian's own filter rejects,
// which is the broadening FR-105 forbids by name.
//
// So the answer is decided PER DECLARED TYPE, as a partition over
// records.PropertyTypes rather than as a list of the dangerous ones. A list
// cannot detect its own incompleteness: add a ninth type and it defaults to
// "safe", the truthy test translates, and the view broadens — the exact
// failure, reintroduced by an omission. TestTruthyPartition_CoversEveryType
// fails by name instead.
//
// TEXT IS ON THE DANGEROUS SIDE, and it moved there with the version-2 writer.
// FR-007a makes `""` ABSENT for every NON-text type — so on those, "has a
// value" and "is truthy" agree about it. For `text` the same requirement says
// the opposite in as many words: *"For text, `""` remains a PRESENT empty
// string"*, which VaultFilterNode's own contract restates for the operator
// (R-3: "an empty string, an empty list and a zero are all VALUES, not
// absence"). So `IS NOT NULL` matches a text property holding `""` and
// Obsidian's bare truthy test does not. Version 1 classified text as safe on
// the strength of the first half of FR-007a alone; that was wrong, and it is
// corrected here rather than carried forward into a format that can finally
// express the distinction.
// ---------------------------------------------------------------------------

// truthyFalsyLiterals maps each declared property type to the present-but-
// FALSY values it can hold. An empty string means the type has none, so a
// bare truthy test translates faithfully.
var truthyFalsyLiterals = map[records.PropertyType]string{
	records.TypeCheckbox: "false",
	records.TypeInteger:  "0",
	records.TypeDecimal:  "0, 0.0",
	records.TypeText:     `the empty string ""`,

	records.TypeEnum:     "",
	records.TypeDate:     "",
	records.TypeRelation: "",
	records.TypePerson:   "",
}

// truthyAdmitsAFalsyValue reports whether a declared property can hold a value
// that is present and falsy — i.e. whether "has a value" is BROADER than
// "is truthy" for it. An unknown type answers TRUE: an unclassified type is
// treated as the dangerous kind, so forgetting to classify one costs a view
// that is disabled when it need not have been, never a view that broadens.
func truthyAdmitsAFalsyValue(p InferredProperty) bool {
	lit, known := truthyFalsyLiterals[p.Type]
	if !known {
		return true
	}
	if lit != "" {
		return true
	}
	if p.Type == records.TypeEnum {
		return enumDeclaresAFalsyValue(p.EnumValues)
	}
	return false
}

// falsyLiteralsFor names the offending values for the refusal message.
func falsyLiteralsFor(p InferredProperty) string {
	if lit := truthyFalsyLiterals[p.Type]; lit != "" {
		return lit
	}
	if p.Type == records.TypeEnum {
		var falsy []string
		for _, v := range p.EnumValues {
			if jsFalsyLiterals[records.FoldKey(strings.TrimSpace(v))] {
				falsy = append(falsy, v)
			}
		}
		if len(falsy) > 0 {
			return "the declared value(s) " + strings.Join(falsy, ", ")
		}
	}
	return "a present but falsy value"
}

// buildV2LeafNode turns one intermediate leaf into a real filter node, or
// refuses it by name.
//
// It is the ONE place a property's declared type decides an operator, and every
// refusal below is a refusal because the available operator would return MORE
// rows than the Obsidian expression, never merely different ones.
func buildV2LeafNode(r leafResolver, l v2Leaf) (*generated.VaultFilterNode, string, bool) {
	if strings.HasPrefix(l.Property, formulaNamespace) {
		return buildFormulaLeafNode(r, l)
	}

	var prop InferredProperty
	if r.typed() {
		p, ok := r.schemas.Lookup(r.recordType, l.Property)
		if !ok {
			return nil, fmt.Sprintf("property %q is not declared in the %q schema (never observed on a %s note)", l.Property, r.recordType, r.recordType), false
		}
		prop = p
	} else if !records.IsFileNamespace(l.Property) {
		// The FILTER position of the same check checkProperty makes for the
		// grouping, sort, select and aggregate positions. It is repeated rather
		// than shared because a filter leaf never goes through checkProperty —
		// buildV2LeafNode is the whole of a leaf's validation — and leaving it
		// out here would let the one position that decides the ROW SET keep
		// writing views the engine refuses.
		if reason, split := r.untypedSplitDomain(l.Property); split {
			return nil, reason, false
		}
	}

	switch l.Shape {
	case shapeFalsy:
		// `!prop`. Obsidian's falsy test catches absent, `false`, `0` and `""`;
		// `IS NULL` catches absent (and, under FR-007a, `""` on every non-text
		// type). Ours is a strict SUBSET, so it can only ever return FEWER
		// rows — the direction FR-105 permits — and it needs no declared type
		// to be safe, which is why an untyped view may carry it.
		//
		// A SUBSET IS ONLY SAFE IN POSITIVE POSITION. Under a `not:` the
		// subset becomes a superset and the same clause returns MORE rows.
		if l.Negated {
			return nil, fmt.Sprintf(
				"`!%s` has no faithful translation inside a `not:`: `IS NULL` is a strict SUBSET of Obsidian's falsy test, which also catches `false`, `0` and `\"\"` — safe at the top level, where a subset only narrows, but a negation inverts it and the clause would return MORE rows than the Obsidian original",
				l.Property), false
		}
		return opNode(l.Property, generated.VaultFilterNodeOpISNULL), "", true

	case shapeTruthy:
		if !r.typed() {
			return nil, "the bare truthy test cannot be translated in an UNTYPED view: `has a value` is broader than `is truthy` for a checkbox, a number or a text property, and with no declared type there is nothing to rule those out", false
		}
		if truthyAdmitsAFalsyValue(prop) {
			return nil, fmt.Sprintf(
				"the bare truthy test has no faithful translation on a %s property — our nearest operator is `IS NOT NULL`, which also matches a record whose %s is present and FALSY (%s), so it would return MORE rows than the Obsidian original",
				prop.Type, l.Property, falsyLiteralsFor(prop)), false
		}
		return opNode(l.Property, generated.VaultFilterNodeOpISNOTNULL), "", true

	case shapeIsSet:
		// `prop != ""`. FR-007a rules this translation on BOTH sides of its own
		// rule, and they need two different operators.
		//
		// NON-TEXT. The empty string IS the absent state, so Obsidian's
		// idiomatic "is set" is `IS NOT NULL`. It is safe under BOTH readings of
		// what Obsidian does with a property that is not there at all. If
		// `undefined != ""` is TRUE (JavaScript's own answer), Obsidian returns
		// the set-plus-absent notes and ours returns only the set ones —
		// narrower. If Obsidian instead reads an absent property as `""`, the
		// two sets are identical. Neither reading makes ours the larger set,
		// which is the only question FR-105 asks.
		//
		// TEXT, AND WHY THIS IS NO LONGER A REFUSAL. `""` stays a PRESENT value
		// on text, so `IS NOT NULL` genuinely does over-match — it admits the
		// record whose value IS the empty string, which the Obsidian filter
		// excludes. That reasoning was always right ABOUT `IS NOT NULL`, and it
		// was the only operator anyone had asked about. `<>` against the EMPTY
		// LITERAL is a different question with a better answer:
		//
		//	property state        `IS NOT NULL`   `<> ""`
		//	absent                false           false   (§8 R-2: an absent
		//	                                              operand is false for
		//	                                              every operator but
		//	                                              `IS NULL`, and this
		//	                                              leaf carries no
		//	                                              Negate, so FR-008's
		//	                                              re-inclusion — which
		//	                                              is a property of the
		//	                                              negative OPERATOR —
		//	                                              never applies)
		//	present, `""`         TRUE            false   ← the whole defect
		//	present, a value      true            true
		//
		// So `<> ""` selects exactly "present and not the empty string". That is
		// Obsidian's `!= ""` outright under the reading where an absent property
		// is not `!= ""`, and a strict SUBSET under the JavaScript reading —
		// the same two-reading proof the non-text branch already stands on, and
		// neither reading makes ours the larger set.
		//
		// THE EMPTY LITERAL IS EXPRESSIBLE, which is the other half of why this
		// works. `VaultFilterNode.value` carries no `minLength`; knowledgefind's
		// buildLeaf sets `LiteralGiven` from `value != nil` rather than from the
		// string being non-empty; and records.Filter.LiteralGiven exists in as
		// many words because "the empty string is a legitimate value for `=`".
		// A view's write-time validation checks enum literals only, so a text
		// `<> ""` passes it.
		//
		// MANY IS COVERED, not excluded by luck: `=`/`<>` are element-wise on a
		// many property (R-9), so `<> ""` there means "has a non-empty element",
		// which is again a subset of what JavaScript's list-to-string coercion
		// would answer.
		if !r.typed() {
			return nil, "an UNTYPED view cannot carry `!= \"\"`: on a text property `<> \"\"` is the faithful operator and on every other type it is `IS NOT NULL` (FR-007a), the two select different rows, and with no declared type there is nothing to choose between them", false
		}
		if prop.Type == records.TypeText {
			if l.Negated {
				return nil, fmt.Sprintf(
					"`%s != \"\"` has no faithful translation on a TEXT property inside a `not:`: outside a negation it is exactly `%s <> \"\"` — present and not the empty string — but that is at best a SUBSET of the Obsidian clause, and a `not:` inverts a subset into a superset, re-admitting every record that never declared %s and returning MORE rows than the Obsidian original",
					l.Property, l.Property, l.Property), false
			}
			return valueNode(l.Property, generated.VaultFilterNodeOpLessThanGreaterThan, ""), "", true
		}
		if l.Negated {
			return nil, fmt.Sprintf(
				"`%s != \"\"` is `IS NOT NULL` on a %s property (FR-007a makes `\"\"` the absent state there), which is at best a SUBSET of the Obsidian clause — safe at the top level, where a subset only narrows, but a `not:` inverts it and the clause would return MORE rows than the Obsidian original",
				l.Property, prop.Type), false
		}
		return opNode(l.Property, generated.VaultFilterNodeOpISNOTNULL), "", true

	case shapeIsEmpty:
		// `prop == ""`, the mirror case, and it splits the same way.
		//
		// TEXT: `= ""` is exactly "present and empty" — absent is false (§8
		// R-2), `""` is true, a value is false. Obsidian's `== ""` is that same
		// set under the JavaScript reading and that set PLUS the absent records
		// under the other, so ours is never the larger one.
		//
		// EVERY OTHER TYPE: still refused, and now for a reason that has been
		// checked rather than assumed. `IS NULL` over-matches — it also matches
		// a record that never declared the property, which the Obsidian
		// comparison does not — and there is no literal-comparison path either:
		// FR-007a makes `""` the ABSENT state on a date, integer, decimal,
		// enum, relation, person or checkbox, so `""` is not a value any of
		// those types can hold and Filter.Validate refuses the literal through
		// the same ParseValue a note's own value goes through. Both operators
		// are exhausted, not just the obvious one.
		if !r.typed() {
			return nil, "an UNTYPED view cannot carry `== \"\"`: on a text property `= \"\"` is the faithful operator, and on every other type the empty string is not a value the type can hold at all (FR-007a), so with no declared type there is nothing to choose between them", false
		}
		if prop.Type == records.TypeText {
			if l.Negated {
				return nil, fmt.Sprintf(
					"`%s == \"\"` has no faithful translation on a TEXT property inside a `not:`: outside a negation it is exactly `%s = \"\"` — present and empty — but a `not:` over it also admits every record that never declared %s, and Obsidian's own `== \"\"` may already count those in, so the negated clause can return MORE rows than the Obsidian original",
					l.Property, l.Property, l.Property), false
			}
			return valueNode(l.Property, generated.VaultFilterNodeOpEqual, ""), "", true
		}
		return nil, fmt.Sprintf(
			"`%s == \"\"` has no faithful translation on a %s property: `IS NULL` also matches a record that never declared %s, which the Obsidian comparison does not, so it would return MORE rows than the original — and no literal-comparison path exists either, because FR-007a makes `\"\"` the ABSENT state on a %s, so it is not a value the type can hold and the engine refuses the literal",
			l.Property, prop.Type, l.Property, prop.Type), false

	case shapeContains:
		if !r.typed() {
			return nil, "`contains` cannot be translated in an UNTYPED view: it is element membership on a many property and substring matching on a text one, and those are two different operators", false
		}
		if prop.Many {
			// R-9: `=` is element-wise on a many property, which is exactly
			// Obsidian's list `.contains`.
			value := l.Value
			if prop.Type == records.TypeEnum {
				canonical, ok := canonicalEnumValue(prop, value)
				if !ok {
					return nil, fmt.Sprintf("value %q is not one of %q's declared enum values (%s)", value, prop.Name, strings.Join(prop.EnumValues, ", ")), false
				}
				value = canonical
			}
			return valueNode(l.Property, generated.VaultFilterNodeOpEqual, value), "", true
		}
		if prop.Type == records.TypeText {
			// Substring. `LIKE` is anchored to the WHOLE value, so the
			// substring is spelled with wildcards, and the literal is ESCAPED
			// — an unescaped `_` in the operand is a single-character wildcard
			// and would match notes the operator never asked for.
			return valueNode(l.Property, generated.VaultFilterNodeOpLIKE, "%"+escapeLikeOperand(l.Value)+"%"), "", true
		}
		return nil, fmt.Sprintf("`contains` is not defined on %q (declared %s, not many-valued and not text)", l.Property, prop.Type), false

	case shapeCompare:
		if r.typed() {
			if prop.Many && isOrderingOp(l.Op) {
				// §8 R-13: the four ordering operators are UNDEFINED against a
				// many property. Emitting one produces a clause the engine
				// refuses per record at query time, which reads as an empty
				// view rather than as a translation this importer could not
				// make.
				return nil, fmt.Sprintf("operator %q is not defined on many-valued property %q (spec §8 R-13 leaves the ordering operators undefined for a list)", string(l.Op), l.Property), false
			}
			if prop.Type == records.TypeEnum {
				canonical, ok := canonicalEnumValue(prop, l.Value)
				if !ok {
					return nil, fmt.Sprintf("value %q is not one of %q's declared enum values (%s)", l.Value, prop.Name, strings.Join(prop.EnumValues, ", ")), false
				}
				return valueNode(l.Property, l.Op, canonical), "", true
			}
			if prop.Type == records.TypeRelation || prop.Type == records.TypePerson {
				// A relation is compared by TARGET (spec §8 R-8) — and the
				// operand the engine takes for that comparison is a WIKILINK,
				// not the target's bare text.
				//
				// THIS BRANCH USED TO STRIP THE BRACKETS, and to write whatever
				// it was given when there were none. Both spellings are refused
				// at serve time by the same function: Filter.Validate runs every
				// literal through records.ParseValue, and ParseValue on a
				// relation calls ParseWikilink, which answers false for anything
				// that does not start `[[`. So `owner == "Daniel Piatkowski"` in
				// a `.base` file — 308 of the founder's notes hold
				// `owner: "[[Daniel Piatkowski]]"`, so the property really is a
				// relation — was written into an ENABLED view that
				// knowledge_find then refuses on first use. A view that imports
				// clean and dies when someone opens it is worse than one that
				// imports with a named loss, so this now asks the engine.
				value, reason, valid := r.relationLiteral(l.Property, l.Value)
				switch {
				case valid:
					return valueNode(l.Property, l.Op, value), "", true
				case reason != "":
					return nil, reason, false
				}
				return nil, fmt.Sprintf("the literal %q cannot be checked against %q's declaration, so it is not written rather than written unchecked", l.Value, l.Property), false
			}
		}
		return valueNode(l.Property, l.Op, l.Value), "", true
	}
	return nil, "this importer has no translation for that expression shape", false
}

// relationLiteral decides what a `relation`/`person` comparison may carry, by
// asking the ENGINE rather than by restating its rule.
//
// records.ParseValue is the one function a relation value goes through, on a
// note (value.go's readRelation) and on a filter literal alike
// (Filter.Validate's "the literals go through ParseValue — the same function a
// record's own value goes through"). So a literal it accepts is one
// knowledge_find will accept, and a literal it refuses is one knowledge_find
// will refuse, WITH THE SAME WORDS — which is what makes this a derived check
// and not a second opinion that can drift.
//
// WHY A BARE NAME IS NOT QUIETLY WRAPPED IN BRACKETS. `[[Daniel Piatkowski]]`
// would parse, and would then compare by RESOLVED TARGET IDENTITY under §8 R-8
// — matching `[[People/Daniel Piatkowski|Danny]]` and every other spelling that
// resolves to the same note. Obsidian's `==` against a string compares against
// the value it holds. Those are not the same set, and the identity reading is
// the LARGER one, which is the direction FR-105 forbids. Nothing in this
// repository settles what Obsidian does with a link-to-string comparison, and a
// translation nobody can check is a guess; a named loss is not.
//
// ok=false with an empty reason means the declaration itself could not be read,
// which the caller reports in its own words.
func (r leafResolver) relationLiteral(property, raw string) (value, reason string, ok bool) {
	schema := r.declaredSchema()
	if schema == nil {
		return "", "", false
	}
	prop, found := schema.Property(property)
	if !found {
		return "", "", false
	}
	_, verr := records.ParseValue(prop, records.Node{Kind: records.KindScalar, Text: raw})
	if verr == nil {
		return raw, "", true
	}
	return "", fmt.Sprintf(
		"the literal %q is not a value a %s property can hold: %s. knowledge_find validates a filter literal through the same records.ParseValue a note's own value goes through, so writing it anyway would produce a view that imports clean and is REFUSED the first time anyone opens it",
		raw, prop.Type, verr.Reason), false
}

func isOrderingOp(op generated.VaultFilterNodeOp) bool {
	switch op {
	case generated.VaultFilterNodeOpLessThan, generated.VaultFilterNodeOpLessThanEqual, generated.VaultFilterNodeOpGreaterThan, generated.VaultFilterNodeOpGreaterThanEqual:
		return true
	}
	return false
}

func canonicalEnumValue(prop InferredProperty, raw string) (string, bool) {
	for _, v := range prop.EnumValues {
		if records.FoldKey(v) == records.FoldKey(raw) {
			return v, true
		}
	}
	return "", false
}

func opNode(property string, op generated.VaultFilterNodeOp) *generated.VaultFilterNode {
	p, o := property, op
	return &generated.VaultFilterNode{Property: &p, Op: &o}
}

func valueNode(property string, op generated.VaultFilterNodeOp, value string) *generated.VaultFilterNode {
	n := opNode(property, op)
	v := value
	n.Value = &v
	return n
}

// escapeLikeOperand makes a literal safe as a LIKE operand. It mirrors
// pkg/records/filemeta.go's escapeLikeLiteral, which is unexported and is the
// canonical statement of the rule; the order matters (`\` first, or the two
// wildcard escapes would themselves be escaped).
func escapeLikeOperand(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}

// ---------------------------------------------------------------------------
// COMPUTED PROPERTIES — the base's `formulas:` block, carried
// ---------------------------------------------------------------------------

// buildFormulaLeafNode builds a filter leaf comparing against a COMPUTED
// property, or refuses it by name.
//
// The declared type comes from the translated formula rather than from a
// schema, which is the whole reason this is a separate function: `formula.*` is
// not scoped to a record type, so SchemaIndex.Lookup cannot answer for it. It
// is the same declaration the query path builds — namespace.go gives each
// formula ONE stable *Property wearing its inferred type — so a literal checked
// here is a literal that will compare there.
//
// THE LITERAL IS CHECKED, AND THAT IS NOT BELT-AND-BRACES. An unparseable
// literal is not refused at load: ValidateViewAgainstSchemas checks the NAME
// resolves, not the value's shape. It would surface only at query time, as a
// non-conforming comparison that is FALSE for every record — a view that
// returns nothing and looks exactly like a view whose filter matched nothing.
// Refusing it here turns a silent empty answer into a named loss.
func buildFormulaLeafNode(r leafResolver, l v2Leaf) (*generated.VaultFilterNode, string, bool) {
	name := strings.TrimPrefix(l.Property, formulaNamespace)
	decl, carried := r.formulas.Declared(name)
	if !carried {
		return nil, r.formulas.RefusalFor(name), false
	}
	if l.Shape != shapeCompare {
		return nil, fmt.Sprintf("only a direct comparison against %q is translated; this importer does not read the other Obsidian filter idioms on a computed property", l.Property), false
	}
	if decl.Arity == records.ArityMany && isOrderingOp(l.Op) {
		// §8 R-13 again, one namespace over: the ordering operators are
		// undefined against a list, and emitting one produces a clause the
		// engine refuses per record — an empty view rather than a named loss.
		return nil, fmt.Sprintf("operator %q is not defined on %q, whose result is a LIST (spec §8 R-13 leaves the ordering operators undefined for a list)", string(l.Op), l.Property), false
	}
	if reason, ok := formulaLiteralFits(decl, l.Value); !ok {
		return nil, reason, false
	}
	return valueNode(l.Property, l.Op, l.Value), "", true
}

// formulaLiteralFits reports whether a filter literal can be read as the
// formula's declared result type.
//
// A TYPE THIS FUNCTION CANNOT CHECK IS REFUSED, not waved through. `date` and
// `link` results are legal formula types that the founder's vault never
// compares against, and accepting a literal for one without checking it would
// be exactly the silently-empty view the caller's doc comment describes. A
// refusal here costs a named loss on a shape that does not occur; accepting
// blind would cost a wrong answer on a shape that might.
func formulaLiteralFits(decl records.FormulaDecl, literal string) (string, bool) {
	switch decl.Type {
	case records.FormulaNumber:
		if _, err := records.ParseDecimal(strings.TrimSpace(literal)); err != nil {
			return fmt.Sprintf("%q is not a number, and formula %q produces one — the comparison would be non-conforming for every record, which reads as an empty view rather than as a translation this importer could not make", literal, decl.Name), false
		}
		return "", true
	case records.FormulaBoolean:
		switch strings.ToLower(strings.TrimSpace(literal)) {
		case "true", "false":
			return "", true
		}
		return fmt.Sprintf("%q is not `true` or `false`, and formula %q produces a truth value", literal, decl.Name), false
	case records.FormulaText:
		return "", true
	}
	return fmt.Sprintf("this importer does not compare against a %s-valued formula (%q); the comparison is recorded here rather than written as a clause that could not match", decl.Type, decl.Name), false
}

// rewrite returns a COPY of the tree with every decided expression node
// replaced.
//
// It copies rather than mutating because a base's OUTER filter tree is shared
// by every view in the file: translateOneView is called once per view against
// the same `outer`, and a rewrite applied in place would leak one view's
// authored formula into the next view's tree, under a name that view never
// declared.
func (a authoredFormulas) rewrite(n *rawNode) *rawNode {
	if n == nil {
		return nil
	}
	if repl, decided := a.replace[n]; decided {
		return repl
	}
	if len(n.Kids) == 0 && len(n.Branches) == 0 {
		return n
	}
	out := *n
	if len(n.Kids) > 0 {
		out.Kids = make([]*rawNode, len(n.Kids))
		for i, k := range n.Kids {
			out.Kids[i] = a.rewrite(k)
		}
	}
	if len(n.Branches) > 0 {
		out.Branches = make([]typedBranch, len(n.Branches))
		for i, b := range n.Branches {
			out.Branches[i] = typedBranch{RecordType: b.RecordType, Remainder: a.rewrite(b.Remainder)}
		}
	}
	return &out
}

// collectExpressionNodes finds every undecided expression node, in tree order.
//
// The order is what makes the authored names deterministic — the same `.base`
// file imports to the same `imported__filter_N` on every run, so re-importing a
// vault produces a diff of the clauses that changed and nothing else.
func collectExpressionNodes(trees ...*rawNode) []*rawNode {
	var out []*rawNode
	var walk func(*rawNode)
	walk = func(n *rawNode) {
		if n == nil {
			return
		}
		if n.Kind == rawKindExpression {
			out = append(out, n)
			return
		}
		for _, k := range n.Kids {
			walk(k)
		}
		for _, b := range n.Branches {
			walk(b.Remainder)
		}
	}
	for _, t := range trees {
		walk(t)
	}
	return out
}

// ---------------------------------------------------------------------------
// FR-140 — THE IMPORTER AUTHORS A FORMULA, AND THE CONTAINMENT IS THE FEATURE
//
// `date(close_date).year == today().year` is an ordinary "closing this year"
// filter and it is not a filter LEAF: a leaf holds a property, an operator and
// a literal, and this clause is an expression on BOTH sides. The one place an
// expression may live in a view is a `formula.<name>` the view declares
// (FR-140), so carrying the clause means writing a formula into the operator's
// file that the operator did not write.
//
// THAT WAS RULED AGAINST ONCE, AND THE RULING WAS RIGHT AT THE TIME: an
// importer inventing a definition in somebody's file is a new surface, and a
// new surface needs its own decision rather than arriving as a side effect of
// closing one view. The decision has since been taken — it is ALLOWED, under
// containment — and the containment is enumerated here so that a later reader
// can check each condition rather than take this comment's word for it.
//
//	1. THE GRAMMAR READS IT AND IT TYPES AS A TRUTH VALUE. Nothing here parses
//	   anything: records.ParseFormula and records.ValidateFormulaSet decide,
//	   which are the same functions the view LOADER runs on the file afterwards
//	   (view.go::validateViewFormulas). An expression that types as a number,
//	   a date, a text value or a list is refused by name — a filter leaf over
//	   an authored formula is `= true` and nothing else, because any other
//	   literal would be this importer choosing a threshold the operator never
//	   wrote.
//	2. THE NAME CANNOT COLLIDE. It is taken from a reserved namespace
//	   (`imported__filter_`) and is additionally CHECKED against every name the
//	   base declares — carried, refused and unreadable alike — so a collision
//	   is a refusal, never a silent shadowing. FormulaTranslation.Declared then
//	   consults the operator's own set FIRST, so even a wrong answer here
//	   cannot make an authored formula win over one he wrote.
//	3. IT IS REPORTED. Every authored formula produces a note naming the
//	   clause it stands for, carried on the view's outcome and written into the
//	   produced file's header comment. See ViewOutcome.AuthoredFormulas.
//	4. IT FITS FR-146's PER-VIEW BUDGET, OR NOTHING IS WRITTEN. The budget is
//	   16 formulas, 64 nodes each, 256 nodes in total, and it is applied to
//	   exactly the map this view will emit — the closure of the formulas it
//	   already references, plus the authored ones. Over budget, EVERY authored
//	   formula for the view is refused and each clause becomes a named loss:
//	   a refusal, never a truncation, because a view carrying half the clauses
//	   the operator wrote returns more rows than the one he wrote.
//	5. IT IS A POSITIVE CONJUNCT. translate.go's containsLost treats an
//	   undecided expression as lost, so an `or:` or a `not:` holding one is
//	   lost whole and an authored formula can never sit under a negation.
//
// WHAT THE CLOCK DOES, because it is the failure with no symptom. The formula
// is stored as SOURCE (FR-141) and evaluated per query against Deps.Now, so
// `today()` means today when the VIEW RUNS. If any part of this pipeline
// resolved it to a date at import time the view would keep answering a
// question about the afternoon it was imported, correctly-looking and wrong.
// That is asserted with two clocks and a DIFFERENCE, not described — see
// pkg/vaultimport/authored_formula_clock_test.go.
//
// WHAT IS NOT CLAIMED. Our `date()` reads a note's date as UTC and Obsidian
// reads it in the operator's local zone, so a close date on the first or last
// day of a month can fall on different sides of `.month` in the two systems.
// That divergence is not created here — it is a property of every date formula
// this importer already carries (`is_overdue`, `days_until_due`) — but it is
// the reason this path is confined to a clause the operator can read back
// verbatim in the produced file and check for himself.
// ---------------------------------------------------------------------------

// authoredFormulaPrefix is the reserved namespace for a formula this importer
// wrote. Condition 2 above: it is a namespace AND a checked collision, not one
// or the other.
const authoredFormulaPrefix = "imported__filter_"

// authoredFormulas is one view's decisions about its expression clauses: what
// each rawKindExpression node becomes, and what the operator is told.
type authoredFormulas struct {
	// replace maps each expression node to the node that stands in its place —
	// a real formula leaf, or a loss carrying the refusal.
	replace map[*rawNode]*rawNode
	// Notes is the reporting payload, in the order the formulas were authored.
	Notes []string
}

// synthesiseFilterFormulas decides every expression clause in one view, and
// extends the resolver's formula set with the ones it authored.
func synthesiseFilterFormulas(res *leafResolver, pb *ParsedBase, baseRelPath string, vraw map[string]any, trees ...*rawNode) authoredFormulas {
	nodes := collectExpressionNodes(trees...)
	if len(nodes) == 0 {
		return authoredFormulas{}
	}
	out := authoredFormulas{replace: make(map[*rawNode]*rawNode, len(nodes))}
	refuse := func(n *rawNode, why string) {
		out.replace[n] = lostNodeWithReason(n.Verbatim, expressionNotCarriedAsFormula(why))
	}

	if res.schema == nil {
		// An UNTYPED view. SchemaFormulaEnv refuses every property operand
		// when there is no schema, so the expression would be refused at
		// validation anyway — refusing it here says WHY in terms the operator
		// can act on (give the view a record type) instead of quoting a
		// parser about a property it could not look up.
		for _, n := range nodes {
			refuse(n, "this view declares no record type, so a formula naming one of its properties has nothing to type that property against — every property operand is refused, and an authored formula would be refused with it")
		}
		return out
	}

	taken := takenFormulaNames(pb, res.formulas)
	accepted := map[string]string{}
	notes := map[string]string{}
	byName := map[string]*rawNode{}
	var order []string

	for _, n := range nodes {
		expr := n.Verbatim
		name, free := authoredFormulaName(taken)
		if !free {
			refuse(n, fmt.Sprintf("no free name is left in this importer's reserved `%s` namespace — the base already occupies every one this importer probes, and shadowing a formula the operator wrote is not an option it has", authoredFormulaPrefix))
			continue
		}
		set, errs := records.ValidateFormulaSet(map[string]string{name: expr}, res.schema)
		if len(errs) > 0 {
			refuse(n, fmt.Sprintf("the grammar reads it, but validating it as a formula of record type %q is refused: %s", res.recordType, errs[0].Error()))
			continue
		}
		decl, ok := set.Get(name)
		if !ok {
			refuse(n, "it validated as a formula and then could not be read back, which is this importer's own defect and not a property of the clause")
			continue
		}
		if decl.Arity == records.ArityMany {
			refuse(n, "it produces a LIST, and the leaf that would compare an authored formula compares one truth value — a list has no single truth value to compare")
			continue
		}
		if decl.Type != records.FormulaBoolean {
			refuse(n, fmt.Sprintf("it types as %s, not as a truth value. The leaf that carries an authored formula is `= true` and nothing else: any other literal would be this importer choosing a threshold the operator never wrote", decl.Type))
			continue
		}
		taken[name] = true
		accepted[name] = expr
		byName[name] = n
		order = append(order, name)
		notes[name] = fmt.Sprintf(
			"formula %q was AUTHORED BY THE IMPORTER — you did not write it. It stands for the `.base` filter clause `%s`, which is an EXPRESSION on both sides; a view's filter leaf holds a property, an operator and a literal, so the clause is carried as a computed property this view declares and the leaf compares it to `true`. The source written into `formulas:` below is your clause verbatim, and `today()` in it is still a CALL, evaluated when the view runs. The name is in this importer's reserved `%s` namespace and was checked against every formula %s declares, so it shadows none of yours",
			name, expr, authoredFormulaPrefix, baseFileLabel(baseRelPath))
	}

	if len(accepted) == 0 {
		return out
	}

	// FR-146's budget, applied to EXACTLY the map this view will emit.
	budget := map[string]string{}
	for _, n := range res.formulas.Closure(collectFormulaRefs(vraw, trees...)) {
		budget[n] = res.formulas.Sources[n]
	}
	for name, src := range accepted {
		budget[name] = src
	}
	set, errs := records.ValidateFormulaSet(budget, res.schema)
	if len(errs) > 0 {
		// REFUSED, NEVER TRUNCATED. Writing the authored formulas that happen
		// to fit and dropping the rest would leave a view whose filter is
		// missing a conjunct — strictly MORE rows than the Obsidian original,
		// which is the one direction FR-105 forbids. So the whole authored set
		// goes and every clause becomes a named loss, which disables the view.
		why := fmt.Sprintf("carrying it would put this view over FR-146's per-view formula budget (at most 16 formulas, 64 nodes each, 256 nodes in total, because every formula is evaluated once per candidate): %s. The budget is a REFUSAL and never a truncation — writing the clauses that fit and dropping the rest would leave a filter missing a conjunct, which returns MORE rows than the original", errs[0].Error())
		for _, name := range order {
			refuse(byName[name], why)
		}
		return authoredFormulas{replace: out.replace}
	}

	res.formulas = res.formulas.WithAuthored(accepted, notes, set)
	for _, name := range order {
		n := byName[name]
		out.replace[n] = &rawNode{
			Kind:     rawKindLeaf,
			Verbatim: n.Verbatim,
			Leaf: v2Leaf{
				Property: formulaNamespace + name,
				Shape:    shapeCompare,
				Op:       generated.VaultFilterNodeOpEqual,
				Value:    "true",
				Source:   n.Verbatim,
			},
		}
		out.Notes = append(out.Notes, notes[name])
	}
	return out
}

// takenFormulaNames is every formula name this base has spoken for — carried,
// refused, and declared-but-unreadable alike.
//
// ALL THREE COUNT. A formula this importer could not carry still occupies its
// name in the operator's file, and authoring one that collides with it would
// make the produced view refer to a definition the operator would reasonably
// read as his own.
func takenFormulaNames(pb *ParsedBase, ft FormulaTranslation) map[string]bool {
	taken := map[string]bool{}
	if pb != nil {
		for _, n := range pb.FormulaNames {
			taken[n] = true
		}
	}
	for n := range ft.Sources {
		taken[n] = true
	}
	for n := range ft.Refused {
		taken[n] = true
	}
	return taken
}

// maxAuthoredFormulaNameProbes bounds the search for a free name. It is far
// above FR-146's 16-formula cap on purpose: the search only ever runs out when
// a base has genuinely occupied the namespace, and then the refusal names it.
const maxAuthoredFormulaNameProbes = 64

// authoredFormulaName returns the first free name in the reserved namespace.
func authoredFormulaName(taken map[string]bool) (string, bool) {
	for i := 1; i <= maxAuthoredFormulaNameProbes; i++ {
		name := fmt.Sprintf("%s%d", authoredFormulaPrefix, i)
		if !taken[name] {
			return name, true
		}
	}
	return "", false
}

// baseFileLabel names the `.base` file in a sentence the operator reads.
func baseFileLabel(baseRelPath string) string {
	if baseRelPath == "" {
		return "this base file"
	}
	return "`" + baseRelPath + "`"
}

// authoredFormulaHeader renders the authored-formula notes as the produced
// file's opening comment.
//
// THE FILE IS THE SURFACE THAT SURVIVES. The same argument schema_write.go
// makes for provisioning and formulaRewriteHeader makes for a rewritten source
// applies with more force here, because this is not a changed definition but a
// NEW one: an operator opening a view file he owns must be able to see, without
// diffing anything, that a definition in it is not his.
func authoredFormulaHeader(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# GENERATED BY `omnipus records import`.\n")
	b.WriteString("#\n")
	b.WriteString("# THIS VIEW DECLARES A FORMULA THE IMPORTER WROTE, WHICH YOU DID NOT.\n")
	b.WriteString("# Your `.base` file filtered on a whole expression. A view's filter leaf holds\n")
	b.WriteString("# a property, an operator and a literal, so the expression is carried as a\n")
	b.WriteString("# computed property this view declares, and the leaf compares it to `true`.\n")
	b.WriteString("# Nothing was approximated: the source below is your clause verbatim, read by\n")
	b.WriteString("# the same grammar that evaluates it, and refused rather than reshaped when it\n")
	b.WriteString("# did not fit.\n")
	b.WriteString("#\n")
	for _, n := range notes {
		for _, wrapped := range wrapComment(n, 76) {
			b.WriteString("# ")
			b.WriteString(wrapped)
			b.WriteString("\n")
		}
	}
	b.WriteString("#\n")
	return b.String()
}

// collectFormulaRefs gathers every `formula.<name>` this view names, in any
// position, so the view declares exactly the formulas it uses (FR-146's budget
// — see FormulaTranslation.Closure).
//
// It reads the RESOLVED filter trees rather than the raw YAML, so a reference
// inside an `or:` group that was itself lost does not drag its formula into the
// declaration. Everything else is read from the raw view, because a column or a
// summary is a plain string either way.
func collectFormulaRefs(vraw map[string]any, trees ...*rawNode) []string {
	seen := map[string]bool{}
	add := func(name string) {
		if strings.HasPrefix(name, formulaNamespace) {
			seen[strings.TrimPrefix(name, formulaNamespace)] = true
		}
	}
	var walk func(*rawNode)
	walk = func(n *rawNode) {
		if n == nil {
			return
		}
		if n.Kind == rawKindLeaf {
			add(n.Leaf.Property)
		}
		for _, k := range n.Kids {
			walk(k)
		}
	}
	for _, t := range trees {
		walk(t)
	}
	if gb, ok := vraw["groupBy"].(map[string]any); ok {
		add(stringOf(gb["property"]))
	}
	if ord, ok := vraw["order"].([]any); ok {
		for _, o := range ord {
			add(strings.TrimSpace(stringOf(o)))
		}
	}
	if srt, ok := vraw["sort"].([]any); ok {
		for _, e := range srt {
			if sm, isMap := e.(map[string]any); isMap {
				add(stringOf(sm["property"]))
			}
		}
	}
	if summ, ok := vraw["summaries"].(map[string]any); ok {
		for k := range summ {
			add(k)
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// formulasYAML renders the view's `formulas:` map — name to SOURCE TEXT
// (FR-141), which is what makes it diffable against the `.base` file it came
// from.
func formulasYAML(ft FormulaTranslation, names []string) *yaml.Node {
	pairs := make([]ordPair, 0, len(names))
	for _, n := range names {
		pairs = append(pairs, ordPair{Key: n, Value: ft.Sources[n]})
	}
	return orderedMap(pairs...)
}

// formulaRewriteNotes renders, for the view's own outcome and header comment,
// what changed about each carried formula's source.
func formulaRewriteNotes(ft FormulaTranslation, names []string) []string {
	var out []string
	for _, n := range names {
		note, changed := ft.Rewritten[n]
		if !changed {
			continue
		}
		out = append(out, fmt.Sprintf("formula %q was rewritten: %s", n, note))
	}
	return out
}

// formulaRewriteHeader renders the rewrite notes as the produced file's header
// comment.
//
// THE FILE IS WHERE THE CORRECTION IS MADE, which is the same argument
// schema_write.go makes for provisioning: a console report scrolls away, and an
// operator comparing this view against the `.base` file it came from is holding
// the file, not the report.
func formulaRewriteHeader(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# GENERATED BY `omnipus records import`.\n")
	b.WriteString("#\n")
	b.WriteString("# THE FORMULA SOURCES BELOW ARE NOT CHARACTER-FOR-CHARACTER THE `.base` FILE'S.\n")
	b.WriteString("# Obsidian's formulas are JavaScript expressions; these are typed expressions\n")
	b.WriteString("# over declared properties, where absence propagates and `if` takes a boolean.\n")
	b.WriteString("# Each rewrite below returns the same value on every record as the original,\n")
	b.WriteString("# or no value where the original showed nothing — never a value where the\n")
	b.WriteString("# original had none.\n")
	b.WriteString("#\n")
	for _, n := range notes {
		for _, wrapped := range wrapComment(n, 76) {
			b.WriteString("# ")
			b.WriteString(wrapped)
			b.WriteString("\n")
		}
	}
	b.WriteString("#\n")
	return b.String()
}
