// Omnipus — ADR-068 D24.3 / spec FR-143a, rule R-18/FR-217: one static type per formula.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
	"sort"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS — the failure it removes is SILENT
//
// FR-143a / R-18. Without static typing, a formula returning `decimal` on some
// records and `text` on others compares FALSE under R-1's different-domains
// rule with NO problem reported. That is the worst shape a defect can take
// here: a wrong answer wearing a type system. The comparator is working exactly
// as specified, the filter is well-formed, the result set is simply missing
// rows, and nothing anywhere says so.
//
// So every formula gets ONE static type and ONE arity, inferred from the
// expression's STRUCTURE and validated BEFORE storage. The comparator then
// receives a formula's declaration exactly as it receives a schema property's,
// and R-1 has one domain to reason about instead of a per-record surprise.
//
// The rule that does most of the work is `if()`: its branches must AGREE, or
// one be absent. `if(c, 1, "x")` is refused at validation naming BOTH branch
// types — a two-argument `if(c, 1)` is fine, because the missing branch is
// absence, and absence has a defined disposition under R-2/R-14.
// ---------------------------------------------------------------------------

// FormulaType is a formula's ONE static result type.
//
// It is a distinct enum from PropertyType, and that is deliberate rather than
// duplication. PropertyType is what a SCHEMA declares about a note's
// frontmatter; FormulaType is what an EXPRESSION produces, and the two sets
// genuinely differ at both ends: `enum` and `integer`/`decimal` collapse here
// (R-1 already makes integer and decimal one comparison domain), while
// `presentation` exists only here and has no frontmatter form at all.
//
// FormulaPropertyType maps this enum onto the comparator's, which is where the
// two meet.
type FormulaType string

const (
	// FormulaNumber is the number domain — R-1 makes `integer` and `decimal`
	// one declared type for comparison, so one constant covers both.
	FormulaNumber FormulaType = "number"
	// FormulaText is text, including a resolved `enum` value.
	FormulaText FormulaType = "text"
	// FormulaDate is a day or an instant (R-7: one type, both spellings).
	FormulaDate FormulaType = "date"
	// FormulaBoolean is a truth value — a `checkbox` property (FR-004c) or a
	// comparison's answer.
	FormulaBoolean FormulaType = "boolean"
	// FormulaLink is a `relation` or `person` value, compared by resolved
	// identity under R-8.
	FormulaLink FormulaType = "link"
	// FormulaPresentation is a DISPLAY value from `link()`, `icon()`,
	// `format()` or `file.asLink()`. R-16/FR-215: a comparison over one is
	// refused with the reason named. Having it in the type system is what makes
	// that refusal STATIC — the author is told at write time, not per record.
	FormulaPresentation FormulaType = "presentation"
	// FormulaDuration is the SPAN between two dates — what `dateA - dateB`
	// produces, per the pinned snapshot's "When subtracting two dates, the
	// result is a Duration type (not a number)".
	//
	// It is deliberately a DEAD END in the type system, and that is the design
	// rather than an omission. A duration has no PropertyType (see
	// FormulaPropertyType), so it cannot be compared, cannot be stored and
	// cannot be a formula's declared result; the only thing it can do is have
	// `.days`/`.hours`/`.minutes`/`.seconds`/`.milliseconds` read from it. That
	// mirrors the snapshot exactly — "Duration does NOT support .round(),
	// .floor(), .ceil() directly; access a numeric field first" — and it means
	// every other use is a NAMED refusal at write time rather than a value
	// nobody can render.
	FormulaDuration FormulaType = "duration"
	// FormulaAbsent is the type of a branch that is not there. It arises from
	// exactly one construct — a two-argument `if()` — and it is what FR-143a
	// means by "or one branch be absent": it agrees with everything, and the
	// other branch decides the formula's type.
	FormulaAbsent FormulaType = "absent"
)

// isTypeNames is the closed argument set of `isType`.
//
// The snapshot's signature is `any.isType(type): boolean` and does not
// enumerate the type names, so this set is the three the founder's vault and
// this package can BOTH answer honestly: `number` and `string` are the two
// domains a scalar property can be in, and `list` is arity. A name outside the
// set is refused BY NAME listing the three — the alternative, answering `false`
// to an unknown type name, is a guard that silently never fires.
var isTypeNames = map[string]bool{"number": true, "string": true, "list": true}

// FormulaArity mirrors a property's arity: a formula produces one value or a
// list of them. FR-143a: "Arity is single unless built by `list()` or a `many`
// operand."
type FormulaArity string

const (
	// ArityOne is a single value.
	ArityOne FormulaArity = "single"
	// ArityMany is a list.
	ArityMany FormulaArity = "many"
)

// FormulaDecl is a formula's declaration — everything the comparator and the
// evaluator need to know about it without looking at a single record.
type FormulaDecl struct {
	// Name is the formula's name in the view's `formulas:` map.
	Name string
	// Source is the expression exactly as written (FR-141: views store SOURCE).
	Source string
	// Type is the ONE static result type (FR-143a).
	Type FormulaType
	// Arity is single or many.
	Arity FormulaArity
	// Scale is the declared decimal scale a number result crosses the boundary
	// at (FR-144). It is FormulaDefaultScale unless `toFixed`/`round` at the
	// root of the expression said otherwise.
	Scale int32
	// Root is the parsed tree. It is not written to disk — FR-141 stores source
	// — but it is what evaluation walks.
	Root FormulaNode
	// Nodes and Depth are FR-146's measured quantities, retained so a report can
	// state a view's budget usage without re-walking every tree.
	Nodes int
	Depth int
	// Refs are the other formulas this one names, sorted. FR-148's cycle walk
	// runs over these.
	Refs []string
}

// FormulaDefaultScale is FR-144's documented default: a number result that no
// `toFixed`/`round` gave a scale to crosses into display or comparison at scale
// 10, round-half-even, and is labelled as rounded when the exact rational did
// not fit.
const FormulaDefaultScale int32 = 10

// FormulaPropertyType maps a formula's static type onto the comparator's
// declared-type vocabulary — the seam FR-143a describes as "the comparator sees
// the formula's declaration exactly as it sees a schema property's".
//
// ok=false for FormulaPresentation and FormulaAbsent, and those two falses mean
// different things:
//
//   - PRESENTATION has no comparator type BY RULE (R-16/FR-215) — a display
//     value does not compare, and giving it one would be the bug.
//   - ABSENT has no comparator type because absence is a STATE, not a type; the
//     comparator already expresses it as PropertyValue.State (R-3).
//   - DURATION has no comparator type because it has no wire type either: there
//     is no `duration` PropertyType, so there is nothing for the comparator to
//     compare against and nothing for a view to store. It is read through
//     `.days` and its siblings, and every other use is refused at write time.
//
// FR-004c's `TypeCheckbox` has landed in schema.go, so the boolean case names
// the constant. It was a string literal while the constant was still in flight;
// formula_type_test.go asserts the two agree, which is what made the swap a
// non-event rather than a thing to remember.
func FormulaPropertyType(t FormulaType) (PropertyType, bool) {
	switch t {
	case FormulaNumber:
		return TypeDecimal, true
	case FormulaText:
		return TypeText, true
	case FormulaDate:
		return TypeDate, true
	case FormulaLink:
		return TypeRelation, true
	case FormulaBoolean:
		return TypeCheckbox, true
	}
	return "", false
}

// formulaTypeOfProperty maps a declared property type onto the formula type
// system. It is the other direction of the same seam.
func formulaTypeOfProperty(t PropertyType) (FormulaType, bool) {
	switch t {
	case TypeText, TypeEnum:
		return FormulaText, true
	case TypeRelation, TypePerson:
		return FormulaLink, true
	case TypeDate:
		return FormulaDate, true
	case TypeInteger, TypeDecimal:
		return FormulaNumber, true
	case TypeCheckbox:
		return FormulaBoolean, true
	}
	return "", false
}

// FormulaEnv is what a formula is typed AGAINST: the declarations of the
// properties and sibling formulas it may name.
//
// It is an interface rather than a *Schema because the caller that knows the
// answer differs by position. `knowledge_configure` types against the schema of
// the view's record type; the view LOADER types against whatever
// ValidateViewAgainstSchemas resolved; and an untyped view (FR-018d) has no
// schema at all and must refuse a property operand rather than guess one.
type FormulaEnv interface {
	// LookupProperty resolves a declared property to its static type and
	// arity. ok=false means the environment cannot type it, and the caller
	// REFUSES — it never assumes a type, because assuming is how a mixed-type
	// formula gets stored.
	LookupProperty(name string) (FormulaType, FormulaArity, bool)
	// LookupFormula resolves `formula.<name>` to a declaration already
	// inferred. ok=false is a reference to a formula the view does not define.
	LookupFormula(name string) (FormulaDecl, bool)
}

// SchemaFormulaEnv types formulas against one record type's schema plus the
// declarations of the sibling formulas inferred so far.
//
// A nil Schema is the TYPELESS-VIEW case (FR-018d) and is handled honestly:
// LookupProperty returns ok=false for every name, so a formula naming a
// property is REFUSED with a message that says the view declares no type. A
// typeless view can still carry formulas over `file.*` metadata and literals,
// which is exactly the set whose types do not depend on a schema.
type SchemaFormulaEnv struct {
	Schema   *Schema
	Formulas map[string]FormulaDecl
}

// LookupProperty implements FormulaEnv.
func (e SchemaFormulaEnv) LookupProperty(name string) (FormulaType, FormulaArity, bool) {
	if e.Schema == nil {
		return "", "", false
	}
	prop, ok := e.Schema.Property(name)
	if !ok {
		return "", "", false
	}
	ft, ok := formulaTypeOfProperty(prop.Type)
	if !ok {
		return "", "", false
	}
	arity := ArityOne
	if prop.Many {
		arity = ArityMany
	}
	return ft, arity, true
}

// LookupFormula implements FormulaEnv.
func (e SchemaFormulaEnv) LookupFormula(name string) (FormulaDecl, bool) {
	d, ok := e.Formulas[name]
	return d, ok
}

// inferred is one subexpression's static shape.
type inferred struct {
	typ   FormulaType
	arity FormulaArity
	// scale is the declared scale a number carries. It is only meaningful for
	// FormulaNumber and is FormulaDefaultScale unless toFixed/round set it.
	scale int32
}

// InferFormulaType walks a parsed tree and returns its ONE static type and
// arity, or the refusal that says why it has none (FR-143a).
func InferFormulaType(root FormulaNode, env FormulaEnv) (FormulaType, FormulaArity, int32, *FormulaError) {
	got, err := inferNode(root, env)
	if err != nil {
		return "", "", 0, err
	}
	return got.typ, got.arity, got.scale, nil
}

func inferNode(n FormulaNode, env FormulaEnv) (inferred, *FormulaError) {
	switch node := n.(type) {
	case *NumberLit:
		if _, err := ParseDecimal(node.Text); err != nil {
			return inferred{}, newFormulaError(FormulaErrSyntax, node.Offset, "a number",
				"`%s` is not a number this package can represent exactly: %v", node.Text, err)
		}
		return inferred{typ: FormulaNumber, arity: ArityOne, scale: FormulaDefaultScale}, nil

	case *TextLit:
		return inferred{typ: FormulaText, arity: ArityOne}, nil

	case *BoolLit:
		return inferred{typ: FormulaBoolean, arity: ArityOne}, nil

	case *Ref:
		return inferRef(node, env)

	case *UnaryOp:
		return inferUnary(node, env)

	case *BinaryOp:
		return inferBinary(node, env)

	case *FieldAccess:
		return inferFieldAccess(node, env)

	case *Call:
		return inferCall(node, env)
	}
	return inferred{}, newFormulaError(FormulaErrSyntax, 0, "", "the expression contains a node the type checker does not know")
}

func inferRef(node *Ref, env FormulaEnv) (inferred, *FormulaError) {
	switch node.Kind {
	case RefFile:
		typ, ok := fileVirtualProperties[node.Name]
		if !ok {
			return inferred{}, newFormulaError(FormulaErrUnknownReference, node.Offset,
				fileVirtualPropertyList(), "`%s` is not one of the file properties", node.Name)
		}
		arity := ArityOne
		if fileManyProperties[node.Name] {
			arity = ArityMany
		}
		scale := int32(0)
		if typ == FormulaNumber {
			scale = 0 // file.size is a byte count: a whole number.
		}
		return inferred{typ: typ, arity: arity, scale: scale}, nil

	case RefFormula:
		decl, ok := env.LookupFormula(node.Name)
		if !ok {
			return inferred{}, newFormulaError(FormulaErrUnknownReference, node.Offset,
				"a formula this view defines",
				"`formula.%s` names a formula the view does not define", node.Name)
		}
		return inferred{typ: decl.Type, arity: decl.Arity, scale: decl.Scale}, nil
	}

	typ, arity, ok := env.LookupProperty(node.Name)
	if !ok {
		return inferred{}, newFormulaError(FormulaErrUnknownReference, node.Offset,
			"a property the view's record type declares",
			"`%s` is not a property this view can type — a formula operand must have a DECLARED type, or the formula would compare FALSE on some records with nothing reported", node.Name)
	}
	scale := int32(0)
	if typ == FormulaNumber {
		scale = FormulaDefaultScale
	}
	return inferred{typ: typ, arity: arity, scale: scale}, nil
}

// inferFieldAccess types a parenless accessor against accessorFields' table.
//
// The receiver check is the point. `.days` reads a DURATION; `due.days`, where
// `due` is a date, is refused naming what the receiver actually is — because
// the alternative is an accessor that reads a field the receiver does not have
// and produces a number anyway, which is a wrong answer with no error.
func inferFieldAccess(node *FieldAccess, env FormulaEnv) (inferred, *FormulaError) {
	recv, err := inferNode(node.Receiver, env)
	if err != nil {
		return inferred{}, err
	}
	rule, ok := accessorFields[node.Field]
	if !ok {
		return inferred{}, newFormulaError(FormulaErrUnknownReference, node.Offset, accessorFieldList(),
			"`.%s` is not a field the formula grammar defines", node.Field)
	}
	if rule.requiresMany {
		if recv.arity != ArityMany {
			return inferred{}, newFormulaError(FormulaErrType, node.Offset, rule.receiverPhrase,
				"`.%s` counts the elements of a LIST, but its receiver is a single %s", node.Field, recv.typ)
		}
		return inferred{typ: rule.result, arity: ArityOne, scale: rule.scale}, nil
	}
	if recv.typ != rule.receiver && recv.typ != FormulaAbsent {
		return inferred{}, newFormulaError(FormulaErrType, node.Offset, rule.receiverPhrase,
			"`.%s` is defined on %s, but its receiver is %s", node.Field, rule.receiverPhrase, recv.typ)
	}
	return inferred{typ: rule.result, arity: ArityOne, scale: rule.scale}, nil
}

func inferUnary(node *UnaryOp, env FormulaEnv) (inferred, *FormulaError) {
	operand, err := inferNode(node.Operand, env)
	if err != nil {
		return inferred{}, err
	}
	switch node.Op {
	case "!":
		if operand.typ != FormulaBoolean && operand.typ != FormulaAbsent {
			return inferred{}, newFormulaError(FormulaErrType, node.Offset, "a truth value",
				"`!` is defined over a truth value, but its operand is %s", operand.typ)
		}
		return inferred{typ: FormulaBoolean, arity: ArityOne}, nil
	case "-":
		if operand.typ != FormulaNumber && operand.typ != FormulaAbsent {
			return inferred{}, newFormulaError(FormulaErrType, node.Offset, "a number",
				"unary `-` is defined over a number, but its operand is %s", operand.typ)
		}
		return inferred{typ: FormulaNumber, arity: ArityOne, scale: operand.scale}, nil
	}
	return inferred{}, newFormulaError(FormulaErrSyntax, node.Offset, "", "unknown unary operator %q", node.Op)
}

func inferBinary(node *BinaryOp, env FormulaEnv) (inferred, *FormulaError) {
	left, err := inferNode(node.Left, env)
	if err != nil {
		return inferred{}, err
	}
	right, err := inferNode(node.Right, env)
	if err != nil {
		return inferred{}, err
	}

	switch node.Op {
	case "&&", "||":
		// Ordered, not a map range: a refusal that names "left" or "right"
		// depending on Go's map iteration order is a message that changes
		// between runs, and a test asserting it would be flaky for a reason
		// nobody would look for.
		for _, side := range []struct {
			name string
			got  inferred
		}{{"left", left}, {"right", right}} {
			if side.got.typ != FormulaBoolean && side.got.typ != FormulaAbsent {
				return inferred{}, newFormulaError(FormulaErrType, node.Offset, "a truth value",
					"`%s` is defined over truth values, but its %s operand is %s", node.Op, side.name, side.got.typ)
			}
		}
		return inferred{typ: FormulaBoolean, arity: ArityOne}, nil

	case "+", "-", "*", "/", "%":
		// The pinned snapshot: "When subtracting two dates, the result is a
		// Duration type (not a number)." This is the ONLY producer of a
		// duration, which is what keeps the type a closed dead end rather than
		// something that can leak into arbitrary arithmetic.
		if node.Op == "-" && left.typ == FormulaDate && right.typ == FormulaDate {
			return inferred{typ: FormulaDuration, arity: ArityOne}, nil
		}
		if err := requireNumberOperands(node, left, right); err != nil {
			return inferred{}, err
		}
		if node.Op == "%" {
			// FR-144: `%` is defined over integers only. Where the operands are
			// LITERALS the answer is knowable now, and a static refusal is
			// strictly better than a per-record problem — so the fractional
			// literal is caught here, naming round() as the remedy, and the
			// non-literal case is caught at evaluation with the same wording.
			if err := requireIntegralLiteral(node.Left, "left"); err != nil {
				return inferred{}, err
			}
			if err := requireIntegralLiteral(node.Right, "right"); err != nil {
				return inferred{}, err
			}
			return inferred{typ: FormulaNumber, arity: ArityOne, scale: 0}, nil
		}
		return inferred{typ: FormulaNumber, arity: ArityOne, scale: widerScale(left.scale, right.scale)}, nil

	case "==", "!=", "<", "<=", ">", ">=":
		return inferComparison(node, left, right)
	}
	return inferred{}, newFormulaError(FormulaErrSyntax, node.Offset, "", "unknown operator %q", node.Op)
}

// inferComparison types a comparison and applies R-16 and R-13 STATICALLY.
//
// The comparison itself is delegated to the ONE comparator at evaluation time
// (FR-142, §8's single-implementation rule). What happens here is narrower and
// is the part that must not wait: a comparison whose two sides are in different
// domains, or over a presentation value, or an ordering operator over a `many`
// operand, is knowable from the declarations alone. Leaving it to runtime would
// produce exactly the silent FALSE that FR-143a exists to remove.
func inferComparison(node *BinaryOp, left, right inferred) (inferred, *FormulaError) {
	for _, side := range []struct {
		name string
		got  inferred
	}{{"left", left}, {"right", right}} {
		if side.got.typ == FormulaPresentation {
			return inferred{}, newFormulaError(FormulaErrType, node.Offset,
				"a comparable value",
				"R-16: a presentation value does not compare — `link()`, `icon()`, `format()` and `file.asLink()` produce something to DISPLAY, and the %s side of `%s` is one", side.name, node.Op)
		}
		// A duration has no comparator domain (FormulaPropertyType gives it no
		// PropertyType), so a comparison over one would reach the comparator
		// with nothing to compare and answer FALSE with a runtime problem. The
		// author is told at WRITE time instead, with the remedy, because a
		// duration comparison has an obvious faithful rewrite.
		if side.got.typ == FormulaDuration {
			return inferred{}, newFormulaError(FormulaErrType, node.Offset,
				"a comparable value",
				"a duration does not compare — read `.days` (or `.hours`, `.minutes`, `.seconds`, `.milliseconds`) from it and compare that number; the %s side of `%s` is a duration", side.name, node.Op)
		}
	}
	if left.typ != FormulaAbsent && right.typ != FormulaAbsent && left.typ != right.typ {
		return inferred{}, newFormulaError(FormulaErrType, node.Offset,
			"two operands in the same domain",
			"R-1: `%s` compares a %s with a %s, which are different domains and would answer FALSE on every record with nothing reported", node.Op, left.typ, right.typ)
	}
	if isFormulaOrderingOp(node.Op) {
		if left.arity == ArityMany || right.arity == ArityMany {
			return inferred{}, newFormulaError(FormulaErrType, node.Offset,
				"a single-valued operand",
				"R-13: `%s` is not defined over a list — compare an element, or use `contains`", node.Op)
		}
		if left.typ == FormulaBoolean || right.typ == FormulaBoolean {
			return inferred{}, newFormulaError(FormulaErrType, node.Offset,
				"`==` or `!=`",
				"R-17: a truth value compares by equality only; `%s` is not defined over it", node.Op)
		}
	}
	return inferred{typ: FormulaBoolean, arity: ArityOne}, nil
}

func isFormulaOrderingOp(op string) bool {
	switch op {
	case "<", "<=", ">", ">=":
		return true
	}
	return false
}

func requireNumberOperands(node *BinaryOp, left, right inferred) *FormulaError {
	check := func(side string, got inferred) *FormulaError {
		if got.typ == FormulaNumber || got.typ == FormulaAbsent {
			return nil
		}
		if got.typ == FormulaDuration {
			return newFormulaError(FormulaErrType, node.Offset, "a number",
				"a duration is not a number — read `.days`, `.hours`, `.minutes`, `.seconds` or `.milliseconds` from it first, and `%s`'s %s operand is a duration", node.Op, side)
		}
		return newFormulaError(FormulaErrType, node.Offset, "a number",
			"`%s` is arithmetic and is defined over numbers, but its %s operand is %s", node.Op, side, got.typ)
	}
	if err := check("left", left); err != nil {
		return err
	}
	return check("right", right)
}

// requireIntegralLiteral enforces FR-144's integer-only `%` where the operand is
// a literal and the answer is therefore already knowable.
func requireIntegralLiteral(n FormulaNode, side string) *FormulaError {
	lit, ok := n.(*NumberLit)
	if !ok {
		return nil
	}
	// A literal that does not parse is NOT reported here. inferNode already
	// refused this exact literal with a message about the number itself;
	// raising a second refusal about `%` would send the author to the operator
	// when the fault is the operand. One fault, one refusal — which is why the
	// parse result is a CONDITION on the fractional check rather than an early
	// return.
	if d, derr := ParseDecimal(lit.Text); derr == nil && d.IsFractional() {
		return newFormulaError(FormulaErrType, lit.Offset, "a whole number",
			"FR-144: `%%` is defined over integers only, and the %s operand `%s` is not one — wrap it in round()", side, lit.Text)
	}
	return nil
}

func widerScale(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// inferCall types a call: arity first, then argument types, then the result.
func inferCall(node *Call, env FormulaEnv) (inferred, *FormulaError) {
	sig, ok := formulaFunctions[node.Name]
	if !ok {
		return inferred{}, unknownFunctionError(node.Offset, node.Name)
	}
	if len(node.Args) < sig.min || len(node.Args) > sig.max {
		return inferred{}, newFormulaError(FormulaErrArity, node.Offset,
			argumentCountPhrase(sig.min, sig.max),
			"`%s` was called with %d argument(s)", callDisplayName(node), len(node.Args))
	}

	args := make([]inferred, 0, len(node.Args))
	for _, a := range node.Args {
		got, err := inferNode(a, env)
		if err != nil {
			return inferred{}, err
		}
		args = append(args, got)
	}

	if err := refuseDurationArgument(node, args); err != nil {
		return inferred{}, err
	}

	switch node.Name {
	case "if":
		return inferIf(node, args)
	case "isType":
		return inferIsType(node)
	case "list":
		return inferList(node, args)
	case "toFixed":
		return inferToFixed(node, args)
	case "round":
		return inferRound(node, args)
	case "mean":
		if args[0].arity != ArityMany {
			return inferred{}, newFormulaError(FormulaErrType, node.Offset, "a list of numbers",
				"`mean` averages a LIST; its argument is a single %s", args[0].typ)
		}
		if args[0].typ != FormulaNumber && args[0].typ != FormulaAbsent {
			return inferred{}, newFormulaError(FormulaErrType, node.Offset, "a list of numbers",
				"`mean` averages numbers; its argument is a list of %s", args[0].typ)
		}
		return inferred{typ: FormulaNumber, arity: ArityOne, scale: FormulaDefaultScale}, nil
	case "date":
		if args[0].typ != FormulaText && args[0].typ != FormulaDate && args[0].typ != FormulaAbsent {
			return inferred{}, newFormulaError(FormulaErrType, node.Offset, "text or a date",
				"`date` converts text or a date; its argument is %s", args[0].typ)
		}
		return inferred{typ: FormulaDate, arity: ArityOne}, nil
	case "today", "now":
		return inferred{typ: FormulaDate, arity: ArityOne}, nil
	case "time":
		if args[0].typ != FormulaDate && args[0].typ != FormulaAbsent {
			return inferred{}, newFormulaError(FormulaErrType, node.Offset, "a date",
				"`.time()` is defined on a date; its receiver is %s", args[0].typ)
		}
		return inferred{typ: FormulaPresentation, arity: ArityOne}, nil
	case "contains":
		if args[1].typ != FormulaText && args[1].typ != FormulaAbsent {
			return inferred{}, newFormulaError(FormulaErrType, node.Offset, "text to look for",
				"`contains` looks for TEXT; its second argument is %s", args[1].typ)
		}
		return inferred{typ: FormulaBoolean, arity: ArityOne}, nil
	case "format", "link", "icon", "file.asLink":
		return inferred{typ: FormulaPresentation, arity: ArityOne}, nil
	case "file.hasTag", "file.inFolder":
		if args[0].typ != FormulaText && args[0].typ != FormulaAbsent {
			return inferred{}, newFormulaError(FormulaErrType, node.Offset, "text",
				"`%s` takes text; it was given %s", node.Name, args[0].typ)
		}
		return inferred{typ: FormulaBoolean, arity: ArityOne}, nil
	case "file.hasLink":
		if args[0].typ != FormulaText && args[0].typ != FormulaLink && args[0].typ != FormulaAbsent {
			return inferred{}, newFormulaError(FormulaErrType, node.Offset, "text or a link",
				"`file.hasLink` takes text or a link; it was given %s", args[0].typ)
		}
		return inferred{typ: FormulaBoolean, arity: ArityOne}, nil
	}
	return inferred{}, unknownFunctionError(node.Offset, node.Name)
}

// durationTolerantFunctions are the three functions a duration may legally be an
// argument of.
//
// `if` and `list` merely CARRY their operands — a duration in either is still a
// duration, and formula_set.go refuses a formula whose declared result is one,
// so nothing escapes. `isType` inspects a type rather than using a value. Every
// other function would have to do something WITH a duration, and there is
// nothing it can do: `toFixed`/`round`/`mean` want a number and already refuse
// it, while `format`/`link`/`icon`/`contains` would render it — through a text
// conversion this package does not define, which is how a duration would come
// out as the empty string with no error.
var durationTolerantFunctions = map[string]bool{"if": true, "list": true, "isType": true}

func refuseDurationArgument(node *Call, args []inferred) *FormulaError {
	if durationTolerantFunctions[node.Name] {
		return nil
	}
	for i, a := range args {
		if a.typ != FormulaDuration {
			continue
		}
		pos := node.Offset
		if i < len(node.Args) {
			pos = node.Args[i].Pos()
		}
		return newFormulaError(FormulaErrType, pos, "a number, or another value this function is defined over",
			"`%s` has no meaning over a duration — read `.days`, `.hours`, `.minutes`, `.seconds` or `.milliseconds` from it first; argument %d is a duration",
			callDisplayName(node), i+1)
	}
	return nil
}

// inferIsType types `x.isType("number")`.
//
// The type NAME must be a literal, for the same reason `toFixed`'s scale must
// be (FR-144): a guard whose predicate is computed per record is not a
// declaration, and this one exists precisely to be a declaration the author can
// read. The RESULT is a plain boolean — never absent — because that is what
// makes it a usable guard: `isType` answers a question ABOUT a value, including
// the question "is there one", so propagating absence through it would make
// `!cost.isType("number")` absent on exactly the records it was written to
// catch.
func inferIsType(node *Call) (inferred, *FormulaError) {
	lit, ok := node.Args[1].(*TextLit)
	if !ok {
		return inferred{}, newFormulaError(FormulaErrType, node.Args[1].Pos(), isTypeNameList(),
			"`isType`'s type name must be written literally — a type name computed per record is not something the author or this checker can read")
	}
	if !isTypeNames[lit.Value] {
		return inferred{}, newFormulaError(FormulaErrType, lit.Offset, isTypeNameList(),
			"`isType(%q)` names a type this grammar does not test for", lit.Value)
	}
	return inferred{typ: FormulaBoolean, arity: ArityOne}, nil
}

// inferIf is FR-143a's headline rule: the branches must AGREE, or one be absent.
func inferIf(node *Call, args []inferred) (inferred, *FormulaError) {
	cond := args[0]
	if cond.typ != FormulaBoolean && cond.typ != FormulaAbsent {
		return inferred{}, newFormulaError(FormulaErrType, node.Offset, "a truth value",
			"`if`'s condition is a truth value; it was given %s", cond.typ)
	}

	then := args[1]
	els := inferred{typ: FormulaAbsent, arity: ArityOne}
	if len(args) == 3 {
		els = args[2]
	}

	switch {
	case then.typ == FormulaAbsent:
		return withArity(els, widerArity(then.arity, els.arity)), nil
	case els.typ == FormulaAbsent:
		return withArity(then, widerArity(then.arity, els.arity)), nil
	case then.typ != els.typ:
		return inferred{}, newFormulaError(FormulaErrType, node.Offset,
			"two branches of the same type, or one branch omitted",
			"FR-143a: `if`'s branches disagree — the then-branch is %s and the else-branch is %s. A formula has ONE static type; two would compare FALSE on whichever records took the other branch, with nothing reported", then.typ, els.typ)
	}
	out := then
	out.scale = widerScale(then.scale, els.scale)
	return withArity(out, widerArity(then.arity, els.arity)), nil
}

// inferList types `list(...)`: every element must agree, and the result is MANY.
func inferList(node *Call, args []inferred) (inferred, *FormulaError) {
	out := inferred{typ: FormulaAbsent, arity: ArityMany}
	for i, a := range args {
		if a.typ == FormulaAbsent {
			continue
		}
		if out.typ == FormulaAbsent {
			out.typ = a.typ
			out.scale = a.scale
			continue
		}
		if a.typ != out.typ {
			return inferred{}, newFormulaError(FormulaErrType, node.Args[i].Pos(),
				"elements of one type",
				"FR-143a: `list` element %d is %s but an earlier element is %s — a list has ONE element type", i+1, a.typ, out.typ)
		}
		out.scale = widerScale(out.scale, a.scale)
	}
	return out, nil
}

// inferToFixed types `toFixed(x, n)`: n must be a WHOLE NUMBER LITERAL, because
// it is the DECLARED SCALE (FR-144) and a scale that varied per record would be
// the same silent-type failure one layer down.
func inferToFixed(node *Call, args []inferred) (inferred, *FormulaError) {
	if args[0].typ != FormulaNumber && args[0].typ != FormulaAbsent {
		return inferred{}, newFormulaError(FormulaErrType, node.Offset, "a number",
			"`toFixed` rounds a number; it was given %s", args[0].typ)
	}
	scale, err := literalScaleArg(node, node.Args[1], "toFixed")
	if err != nil {
		return inferred{}, err
	}
	return inferred{typ: FormulaNumber, arity: ArityOne, scale: scale}, nil
}

func inferRound(node *Call, args []inferred) (inferred, *FormulaError) {
	if args[0].typ != FormulaNumber && args[0].typ != FormulaAbsent {
		return inferred{}, newFormulaError(FormulaErrType, node.Offset, "a number",
			"`round` rounds a number; it was given %s", args[0].typ)
	}
	if len(node.Args) == 1 {
		return inferred{typ: FormulaNumber, arity: ArityOne, scale: 0}, nil
	}
	scale, err := literalScaleArg(node, node.Args[1], "round")
	if err != nil {
		return inferred{}, err
	}
	return inferred{typ: FormulaNumber, arity: ArityOne, scale: scale}, nil
}

// literalScaleArg reads a declared scale argument.
func literalScaleArg(node *Call, arg FormulaNode, fn string) (int32, *FormulaError) {
	lit, ok := arg.(*NumberLit)
	if !ok {
		return 0, newFormulaError(FormulaErrType, arg.Pos(), "a whole-number literal",
			"`%s`'s scale must be written literally — FR-144 requires the scale to be DECLARED, and a scale computed per record is not a declaration", fn)
	}
	d, derr := ParseDecimal(lit.Text)
	if derr != nil || d.IsFractional() {
		return 0, newFormulaError(FormulaErrType, lit.Offset, "a whole-number literal",
			"`%s`'s scale `%s` is not a whole number", fn, lit.Text)
	}
	n, ok := d.Int64()
	if !ok || n < 0 || n > int64(maxFormulaScale) {
		return 0, newFormulaError(FormulaErrType, lit.Offset,
			fmt.Sprintf("a scale between 0 and %d", maxFormulaScale),
			"`%s`'s scale `%s` is outside the range this package can render exactly", fn, lit.Text)
	}
	return int32(n), nil
}

// maxFormulaScale bounds a DECLARED scale to the package's own decimal domain.
// It is maxDecimalScale restated for the formula layer rather than a second,
// independently drifting number.
const maxFormulaScale = maxDecimalScale

func withArity(in inferred, a FormulaArity) inferred {
	in.arity = a
	return in
}

func widerArity(a, b FormulaArity) FormulaArity {
	if a == ArityMany || b == ArityMany {
		return ArityMany
	}
	return ArityOne
}

func argumentCountPhrase(lo, hi int) string {
	switch {
	case lo == hi:
		return fmt.Sprintf("exactly %d argument(s)", lo)
	case hi >= maxFormulaNodes:
		// An open-ended maximum: `list()` is the only function with one, and
		// its real bound is the node cap, not an argument count.
		return fmt.Sprintf("at least %d argument(s)", lo)
	}
	return fmt.Sprintf("between %d and %d arguments", lo, hi)
}

func callDisplayName(node *Call) string {
	if node.Source != "" {
		return node.Source
	}
	return node.Name
}

// FormulaTypeNames lists the static types, for a refusal that must show the set.
func FormulaTypeNames() []string {
	names := []string{
		string(FormulaNumber), string(FormulaText), string(FormulaDate),
		string(FormulaBoolean), string(FormulaLink), string(FormulaPresentation),
		string(FormulaDuration),
	}
	sort.Strings(names)
	return names
}
