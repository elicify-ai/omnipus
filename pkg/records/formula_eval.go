// Omnipus — ADR-068 D24.3 / spec FR-142, FR-144..FR-146: evaluation, in Go,
// per candidate, memoized.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// FR-142: a formula value is computed IN GO, per candidate, after narrowing.
// Nothing about a formula ever reaches SQL — `propindex.Selector` keeps exactly
// its three fields and ruling R-A is untouched by this whole layer. That is not
// a performance choice; it is the same choice §8.1 made for every comparison,
// for the same reason: SQLite's answers to these questions are not ours.
//
// COMPARISONS INSIDE AN EXPRESSION DELEGATE TO THE ONE COMPARATOR. `a > b`
// written inside a formula is decided by Comparator.Evaluate, exactly as the
// same comparison written as a filter leaf would be. §8's single-implementation
// rule is why: a second implementation of `>` would agree with the first on
// every case anybody tested and diverge on the Turkish dotted İ, or on an
// unresolved relation, or on an empty list — the cases the comparator exists
// for.
//
// MEMOIZATION IS A REQUIREMENT, NOT AN OPTIMISATION (FR-146). Each distinct
// formula is evaluated ONCE per candidate and its result reused by every leaf,
// sort key and aggregate that names it. Without it the cost is multiplicative —
// 64 leaves × 64 nodes × 50,000 candidates = 204.8M steps — and the caps in
// formula_set.go are enforcing a bound that does not describe the work.
//
// ABSENCE PROPAGATES (FR-145 / R-14). Any arithmetic or function step over an
// absent operand yields ABSENT. Not zero, not the empty string, not false. The
// one sanctioned way to give absence a value is `if()`, which treats an absent
// condition as false — the same discipline R-2 imposes one layer down, extended
// one layer up.
// ---------------------------------------------------------------------------

// FormulaCandidate is the one record a formula is being evaluated over.
//
// It is an INTERFACE, and narrow on purpose: the formula layer must not know
// how a candidate was assembled, whether its properties came from `note_props`
// or from a `Record` in memory, or which child-table statement streamed its
// tags. It asks two questions and takes the answers.
//
// ok=false means the candidate could not supply the value at all — a different
// thing from a value that is absent, which is a PropertyValue in StateAbsent.
// The distinction matters: an absent value propagates as absence (R-14), while
// an unavailable one is a problem the caller should see.
type FormulaCandidate interface {
	// FormulaProperty resolves a declared property of the record.
	FormulaProperty(name string) (PropertyValue, bool)
	// FormulaFileProperty resolves one of FR-130's thirteen `file.*` virtual
	// properties.
	FormulaFileProperty(name string) (PropertyValue, bool)
}

// FormulaResult is one formula's value for one candidate.
type FormulaResult struct {
	// Name is the formula's name.
	Name string
	// Type and Arity are the STATIC declaration (FR-143a) — they are the same
	// for every candidate, by construction, and they are repeated here so a
	// consumer holding only a result never has to look the declaration up.
	Type  FormulaType
	Arity FormulaArity
	// Absent is R-14's propagated absence.
	Absent bool
	// Scale is the DECLARED scale a number crossed the boundary at (FR-144).
	Scale int32
	// Rounded says the exact rational did not fit at Scale and the value shown
	// is a rounding of it. FR-144: "a rounded value is labelled as rounded" —
	// an unlabelled rounded number is the failure FR-152 records.
	Rounded bool
	// Problems are R-4/R-15's named problems: a division by zero, a `%` over a
	// fractional operand, an operand that did not conform. A problem NEVER
	// becomes a value.
	Problems []ComparisonProblem

	values []TypedValue
	texts  []string
}

// Values returns the typed values the formula produced. Empty when Absent.
func (r FormulaResult) Values() []TypedValue {
	out := make([]TypedValue, len(r.values))
	copy(out, r.values)
	return out
}

// Display renders a presentation-typed result. R-16/FR-215: this is the ONLY
// thing a presentation value is for — it never compares.
func (r FormulaResult) Display() []string {
	out := make([]string, len(r.texts))
	copy(out, r.texts)
	return out
}

// PropertyValue hands the result to the comparator wearing a declaration.
//
// This is the seam FR-143a describes: "the comparator sees the formula's
// declaration exactly as it sees a schema property's". The synthesised Property
// carries the formula's ONE static type, its arity, and its SOURCE in the
// Formula field — so anything inspecting the operand can see that the
// declaration came from an expression and read the expression that produced it.
//
// ok=false for a PRESENTATION result, and that false is the enforcement of
// R-16: there is no PropertyValue for a display value, so a caller cannot
// accidentally compare one. It is a refusal expressed in the type system rather
// than in a runtime check somebody can forget to call.
func (r FormulaResult) PropertyValue(source string) (PropertyValue, bool) {
	pt, ok := FormulaPropertyType(r.Type)
	if !ok {
		return PropertyValue{}, false
	}
	prop := &Property{
		Name:    "formula." + r.Name,
		Type:    pt,
		Many:    r.Arity == ArityMany,
		Formula: source,
	}
	pv := PropertyValue{Property: prop, State: StateAbsent}
	if !r.Absent {
		pv.State = StatePresent
		pv.Values = r.Values()
	}
	return pv, true
}

// FormulaEvaluator evaluates a validated set over one candidate at a time.
//
// It is NOT safe for concurrent use, and that is deliberate rather than an
// oversight: the memo is per-candidate state, and sharing an evaluator across
// goroutines would share the memo, which is a wrong-answer bug rather than a
// race the detector would catch. One evaluator per scan.
type FormulaEvaluator struct {
	set        *FormulaSet
	comparator Comparator

	// now is snapshotted ONCE (FR-146's last clause) so `now()` and `today()`
	// give the same answer for every candidate in one response. A per-call
	// clock read would let a query spanning a midnight boundary put some
	// records on one side of `due < today()` and some on the other, which is a
	// response that is internally inconsistent and has no error to show for it.
	now time.Time

	candidate FormulaCandidate
	memo      map[string]FormulaResult
	// inProgress is the evaluation-time twin of formula_set.go's write-time
	// cycle guard. The set is acyclic by construction, so this can only fire on
	// a FormulaSet assembled by some future path that skipped validation — and
	// when it does it must be an error, not a hang.
	inProgress map[string]bool
	steps      int
}

// maxEvalStepsPerCandidate is FR-146's per-candidate work budget, expressed in
// node visits.
//
// It is the view's total node budget, because memoization means a candidate can
// never visit more than every formula's nodes once. It is a BELT: the caps in
// formula_set.go are the primary bound and this fires only if a FormulaSet
// reached evaluation without passing them.
const maxEvalStepsPerCandidate = maxFormulaNodesPerView

// NewFormulaEvaluator builds an evaluator over a validated set.
//
// `now` is the snapshot for the whole query. Pass the same instant for every
// candidate in one response; passing time.Now() per candidate is the bug the
// snapshot exists to prevent.
func NewFormulaEvaluator(set *FormulaSet, c Comparator, now time.Time) *FormulaEvaluator {
	return &FormulaEvaluator{
		set:        set,
		comparator: c,
		now:        now.UTC(),
		memo:       map[string]FormulaResult{},
		inProgress: map[string]bool{},
	}
}

// Begin starts a new candidate and CLEARS the memo.
//
// Forgetting this call is the one way to get a wrong answer out of this file:
// every candidate would receive the first candidate's formula values, silently,
// with no error anywhere. It is a separate method rather than a parameter on
// Evaluate precisely so the reset has a name a reader can look for.
func (e *FormulaEvaluator) Begin(c FormulaCandidate) {
	e.candidate = c
	e.memo = make(map[string]FormulaResult, e.set.Len())
	e.steps = 0
}

// Evaluate returns one formula's value for the current candidate, memoized.
func (e *FormulaEvaluator) Evaluate(name string) (FormulaResult, bool) {
	if e.set == nil {
		return FormulaResult{}, false
	}
	if got, ok := e.memo[name]; ok {
		return got, true
	}
	decl, ok := e.set.Get(name)
	if !ok {
		return FormulaResult{}, false
	}
	if e.inProgress[name] {
		return FormulaResult{
			Name: name, Type: decl.Type, Arity: decl.Arity, Absent: true,
			Problems: []ComparisonProblem{{
				Code:   CompareNonConforming,
				Detail: fmt.Sprintf("formula %s refers to itself; it was not evaluated", name),
			}},
		}, true
	}
	e.inProgress[name] = true
	val, problems := e.eval(decl.Root)
	delete(e.inProgress, name)

	res := FormulaResult{
		Name:     name,
		Type:     decl.Type,
		Arity:    decl.Arity,
		Absent:   val.absent,
		Scale:    decl.Scale,
		Rounded:  val.rounded,
		Problems: problems,
	}
	res.values, res.texts, res.Rounded = val.materialize(decl.Scale, val.rounded)
	if len(res.values) == 0 && len(res.texts) == 0 {
		res.Absent = true
	}
	e.memo[name] = res
	return res, true
}

// ---------------------------------------------------------------------------
// The internal value
// ---------------------------------------------------------------------------

// fitem is one element of a formula value. Exactly one field is meaningful,
// chosen by the enclosing fval's typ.
type fitem struct {
	num  *big.Rat
	text string
	date DateValue
	flag bool
	link Wikilink
}

// fval is a subexpression's value. `absent` and a zero-length `items` are NOT
// the same: `list()` is present and empty (R-3's empty list is a value), while
// an absent operand is absence.
type fval struct {
	typ     FormulaType
	absent  bool
	items   []fitem
	rounded bool
}

func absentOf(t FormulaType) fval { return fval{typ: t, absent: true} }

func numberVal(r *big.Rat) fval {
	return fval{typ: FormulaNumber, items: []fitem{{num: r}}}
}

func boolVal(b bool) fval { return fval{typ: FormulaBoolean, items: []fitem{{flag: b}}} }

func textVal(s string) fval { return fval{typ: FormulaText, items: []fitem{{text: s}}} }

func dateVal(d DateValue) fval { return fval{typ: FormulaDate, items: []fitem{{date: d}}} }

func displayVal(s string) fval { return fval{typ: FormulaPresentation, items: []fitem{{text: s}}} }

// single returns the one item of a scalar value.
func (v fval) single() (fitem, bool) {
	if v.absent || len(v.items) != 1 {
		return fitem{}, false
	}
	return v.items[0], true
}

// materialize turns the internal value into what a consumer sees: TypedValues
// for a comparable type, display strings for a presentation one.
//
// This is FR-144's BOUNDARY — the one place a rational becomes a decimal, at
// the DECLARED scale, rounded half-even, with the rounding LABELLED. It happens
// once, at the end, rather than at every arithmetic step, which is the whole
// point of carrying rationals internally.
func (v fval) materialize(scale int32, alreadyRounded bool) ([]TypedValue, []string, bool) {
	if v.absent {
		return nil, nil, alreadyRounded
	}
	rounded := alreadyRounded
	if v.typ == FormulaPresentation {
		texts := make([]string, 0, len(v.items))
		for _, it := range v.items {
			texts = append(texts, it.text)
		}
		return nil, texts, rounded
	}
	values := make([]TypedValue, 0, len(v.items))
	for _, it := range v.items {
		switch v.typ {
		case FormulaNumber:
			d, r := ratToDecimal(it.num, scale)
			rounded = rounded || r
			values = append(values, TypedValue{Type: TypeDecimal, Number: d, Raw: d.String()})
		case FormulaText:
			values = append(values, TypedValue{Type: TypeText, Text: it.text, Raw: it.text})
		case FormulaDate:
			values = append(values, TypedValue{Type: TypeDate, Date: it.date, Raw: it.date.String()})
		case FormulaLink:
			values = append(values, TypedValue{Type: TypeRelation, Link: it.link, Raw: it.link.Raw})
		case FormulaBoolean:
			raw := "false"
			if it.flag {
				raw = "true"
			}
			values = append(values, TypedValue{Type: PropertyType("checkbox"), Text: raw, Raw: raw})
		}
	}
	return values, nil, rounded
}

// ---------------------------------------------------------------------------
// Evaluation
// ---------------------------------------------------------------------------

func (e *FormulaEvaluator) eval(n FormulaNode) (fval, []ComparisonProblem) {
	e.steps++
	if e.steps > maxEvalStepsPerCandidate {
		return fval{absent: true}, []ComparisonProblem{{
			Code: CompareNonConforming,
			Detail: fmt.Sprintf("FR-146: evaluating this view's formulas for one record exceeded the budget of %d steps; no value was produced",
				maxEvalStepsPerCandidate),
		}}
	}

	switch node := n.(type) {
	case *NumberLit:
		d, err := ParseDecimal(node.Text)
		if err != nil {
			return fval{absent: true}, []ComparisonProblem{{
				Code: CompareNonConforming, Detail: fmt.Sprintf("the literal %q is not an exact number", node.Text),
			}}
		}
		return numberVal(ratFromDecimal(d)), nil

	case *TextLit:
		return textVal(node.Value), nil

	case *BoolLit:
		return boolVal(node.Value), nil

	case *Ref:
		return e.evalRef(node)

	case *UnaryOp:
		return e.evalUnary(node)

	case *BinaryOp:
		return e.evalBinary(node)

	case *FieldAccess:
		recv, problems := e.eval(node.Receiver)
		if recv.absent {
			return absentOf(FormulaNumber), problems
		}
		it, ok := recv.single()
		if !ok {
			return absentOf(FormulaNumber), problems
		}
		// `.hour` — the only parenless accessor FR-143's snapshot documents.
		return numberVal(new(big.Rat).SetInt64(int64(it.date.Instant.UTC().Hour()))), problems

	case *Call:
		return e.evalCall(node)
	}
	return fval{absent: true}, nil
}

func (e *FormulaEvaluator) evalRef(node *Ref) (fval, []ComparisonProblem) {
	switch node.Kind {
	case RefFormula:
		res, ok := e.Evaluate(node.Name)
		if !ok {
			return fval{absent: true}, []ComparisonProblem{{
				Code: CompareNonConforming, Detail: fmt.Sprintf("formula.%s is not defined", node.Name),
			}}
		}
		return fvalFromResult(res), res.Problems

	case RefFile:
		if e.candidate == nil {
			return fval{typ: fileVirtualProperties[node.Name], absent: true}, nil
		}
		pv, ok := e.candidate.FormulaFileProperty(node.Name)
		if !ok {
			return fval{typ: fileVirtualProperties[node.Name], absent: true}, nil
		}
		return fvalFromPropertyValue(fileVirtualProperties[node.Name], pv)
	}

	if e.candidate == nil {
		return fval{absent: true}, nil
	}
	pv, ok := e.candidate.FormulaProperty(node.Name)
	if !ok {
		return fval{absent: true}, nil
	}
	typ, tok := formulaTypeOfProperty(propertyTypeOf(pv.Property))
	if !tok {
		return fval{absent: true}, nil
	}
	return fvalFromPropertyValue(typ, pv)
}

// fvalFromPropertyValue lifts a resolved property into the formula value model.
//
// R-4 is preserved exactly: a NON-CONFORMING property is a reported problem and
// an absent value, never a coerced one. That is the difference between this
// layer being a thin wrapper over the record model and being a second, laxer
// interpretation of it.
func fvalFromPropertyValue(typ FormulaType, pv PropertyValue) (fval, []ComparisonProblem) {
	switch pv.State {
	case StateAbsent:
		return absentOf(typ), nil
	case StateNonConforming:
		return absentOf(typ), []ComparisonProblem{{
			Code:     CompareNonConforming,
			Property: propertyNameOf(pv.Property),
			Detail:   fmt.Sprintf("%s does not conform to its declared type, so no formula value was computed from it", propertyNameOf(pv.Property)),
		}}
	}

	out := fval{typ: typ}
	for _, v := range pv.Values {
		switch typ {
		case FormulaNumber:
			out.items = append(out.items, fitem{num: ratFromDecimal(v.Number)})
		case FormulaText:
			out.items = append(out.items, fitem{text: textOfTypedValue(v)})
		case FormulaDate:
			out.items = append(out.items, fitem{date: v.Date})
		case FormulaLink:
			out.items = append(out.items, fitem{link: v.Link})
		case FormulaBoolean:
			out.items = append(out.items, fitem{flag: FoldEqual(strings.TrimSpace(v.Text), "true")})
		}
	}
	return out, nil
}

// propertyTypeOf and propertyNameOf read a PropertyValue's declaration without
// assuming it has one. A hand-built PropertyValue with a nil Property reaches
// the comparator today (filter.go synthesises literal operands), so this layer
// must not be the one place that panics on it — R-11's "total and never panics"
// applies to everything the comparator can reach, and a formula operand is one.
func propertyTypeOf(p *Property) PropertyType {
	if p == nil {
		return ""
	}
	return p.Type
}

func propertyNameOf(p *Property) string {
	if p == nil {
		return "the property"
	}
	return p.Name
}

func textOfTypedValue(v TypedValue) string {
	if v.Type == TypeEnum {
		return v.Enum.Name
	}
	if v.Text != "" {
		return v.Text
	}
	return v.Raw
}

func fvalFromResult(res FormulaResult) fval {
	if res.Absent {
		return absentOf(res.Type)
	}
	out := fval{typ: res.Type, rounded: res.Rounded}
	if res.Type == FormulaPresentation {
		for _, t := range res.texts {
			out.items = append(out.items, fitem{text: t})
		}
		return out
	}
	for _, v := range res.values {
		switch res.Type {
		case FormulaNumber:
			out.items = append(out.items, fitem{num: ratFromDecimal(v.Number)})
		case FormulaText:
			out.items = append(out.items, fitem{text: textOfTypedValue(v)})
		case FormulaDate:
			out.items = append(out.items, fitem{date: v.Date})
		case FormulaLink:
			out.items = append(out.items, fitem{link: v.Link})
		case FormulaBoolean:
			out.items = append(out.items, fitem{flag: FoldEqual(v.Text, "true")})
		}
	}
	return out
}

func (e *FormulaEvaluator) evalUnary(node *UnaryOp) (fval, []ComparisonProblem) {
	operand, problems := e.eval(node.Operand)
	if operand.absent {
		// R-14: absence propagates.
		if node.Op == "!" {
			return absentOf(FormulaBoolean), problems
		}
		return absentOf(FormulaNumber), problems
	}
	it, ok := operand.single()
	if !ok {
		return absentOf(operand.typ), problems
	}
	if node.Op == "!" {
		return boolVal(!it.flag), problems
	}
	return numberVal(new(big.Rat).Neg(it.num)), problems
}

func (e *FormulaEvaluator) evalBinary(node *BinaryOp) (fval, []ComparisonProblem) {
	left, lp := e.eval(node.Left)
	right, rp := e.eval(node.Right)
	problems := append(append([]ComparisonProblem{}, lp...), rp...)

	switch node.Op {
	case "&&", "||":
		// R-14: `if()` is the sanctioned way to give absence a value, so a
		// boolean operator over an absent operand yields ABSENT rather than
		// treating it as false. Treating it as false here would make
		// `!(absent && x)` true, which is R-2's exact trap one layer up.
		if left.absent || right.absent {
			return absentOf(FormulaBoolean), problems
		}
		l, lok := left.single()
		r, rok := right.single()
		if !lok || !rok {
			return absentOf(FormulaBoolean), problems
		}
		if node.Op == "&&" {
			return boolVal(l.flag && r.flag), problems
		}
		return boolVal(l.flag || r.flag), problems

	case "+", "-", "*", "/", "%":
		return e.evalArithmetic(node, left, right, problems)
	}
	return e.evalComparison(node, left, right, problems)
}

// evalArithmetic is FR-144's exact-rational core.
func (e *FormulaEvaluator) evalArithmetic(node *BinaryOp, left, right fval, problems []ComparisonProblem) (fval, []ComparisonProblem) {
	if left.absent || right.absent {
		return absentOf(FormulaNumber), problems
	}
	l, lok := left.single()
	r, rok := right.single()
	if !lok || !rok {
		return absentOf(FormulaNumber), problems
	}
	rounded := left.rounded || right.rounded

	switch node.Op {
	case "+":
		return withRounded(numberVal(new(big.Rat).Add(l.num, r.num)), rounded), problems
	case "-":
		return withRounded(numberVal(new(big.Rat).Sub(l.num, r.num)), rounded), problems
	case "*":
		return withRounded(numberVal(new(big.Rat).Mul(l.num, r.num)), rounded), problems
	case "/":
		if r.num.Sign() == 0 {
			// FR-144: division by zero is an ABSENT result plus a NAMED
			// problem, never a silent zero. A silent zero is the shape that
			// makes a budget report show a healthy total.
			return absentOf(FormulaNumber), append(problems, ComparisonProblem{
				Code:   CompareNonConforming,
				Detail: "FR-144: division by zero — the formula produced no value for this record",
				Remedy: "guard the divisor, for example with if(divisor != 0, a / divisor)",
			})
		}
		return withRounded(numberVal(new(big.Rat).Quo(l.num, r.num)), rounded), problems
	case "%":
		m, ok := ratMod(l.num, r.num)
		if !ok {
			return absentOf(FormulaNumber), append(problems, ComparisonProblem{
				Code:   CompareNonConforming,
				Detail: "FR-144: `%` is defined over integers only, and one of its operands was not a whole number (or the divisor was zero)",
				Remedy: "wrap the operand in round()",
			})
		}
		return withRounded(numberVal(m), rounded), problems
	}
	return absentOf(FormulaNumber), problems
}

func withRounded(v fval, rounded bool) fval {
	v.rounded = rounded
	return v
}

// evalComparison DELEGATES to the one comparator (FR-142, §8's
// single-implementation rule).
//
// It builds two operands wearing declarations — exactly what a filter leaf hands
// the comparator — and returns the comparator's answer and the comparator's
// problems verbatim. Nothing about `>` is re-decided here.
func (e *FormulaEvaluator) evalComparison(node *BinaryOp, left, right fval, problems []ComparisonProblem) (fval, []ComparisonProblem) {
	op := formulaComparisonOperator(node.Op)
	if op == "" {
		return absentOf(FormulaBoolean), problems
	}
	lp, lok := left.operand("left operand")
	rp, rok := right.operand("right operand")
	if !lok || !rok {
		// R-16 again, at runtime: only a presentation value reaches here, and
		// the static check in inferComparison has already refused it at write
		// time. This is the belt for a hand-built tree.
		return absentOf(FormulaBoolean), append(problems, ComparisonProblem{
			Code:     CompareOperatorNotDefined,
			Operator: op,
			Detail:   "R-16: a presentation value does not compare",
		})
	}
	answer, cp := e.comparator.Evaluate(op, lp, rp)
	return boolVal(answer), append(problems, cp...)
}

func formulaComparisonOperator(op string) Operator {
	switch op {
	case "==":
		return OpEqual
	case "!=":
		return OpNotEqual
	case "<":
		return OpLess
	case "<=":
		return OpLessOrEqual
	case ">":
		return OpGreater
	case ">=":
		return OpGreaterOrEqual
	}
	return ""
}

// operand dresses an intermediate value as a PropertyValue so the comparator can
// read it. It is the in-expression twin of FormulaResult.PropertyValue.
func (v fval) operand(name string) (PropertyValue, bool) {
	pt, ok := FormulaPropertyType(v.typ)
	if !ok {
		return PropertyValue{}, false
	}
	prop := &Property{Name: name, Type: pt, Many: len(v.items) > 1}
	pv := PropertyValue{Property: prop, State: StateAbsent}
	if !v.absent {
		pv.State = StatePresent
		values, _, _ := v.materialize(FormulaDefaultScale, false)
		pv.Values = values
	}
	return pv, true
}

// ---------------------------------------------------------------------------
// Functions
// ---------------------------------------------------------------------------

func (e *FormulaEvaluator) evalCall(node *Call) (fval, []ComparisonProblem) {
	// `if` is evaluated LAZILY: the branch not taken is not evaluated, so
	// `if(divisor != 0, total / divisor, 0)` does not raise a division-by-zero
	// problem on the records the guard was written for. Eager evaluation would
	// make the guard useless and the problem list dishonest.
	if node.Name == "if" {
		return e.evalIf(node)
	}

	args := make([]fval, 0, len(node.Args))
	var problems []ComparisonProblem
	for _, a := range node.Args {
		v, p := e.eval(a)
		args = append(args, v)
		problems = append(problems, p...)
	}

	switch node.Name {
	case "list":
		return e.evalList(args), problems
	case "toFixed", "round":
		return e.evalRound(node, args, problems)
	case "mean":
		return e.evalMean(args, problems)
	case "date":
		return evalDateFn(args), problems
	case "today":
		return dateVal(DateValue{Instant: e.now.Truncate(24 * time.Hour)}), problems
	case "now":
		return dateVal(DateValue{Instant: e.now, HasTime: true}), problems
	case "time":
		if args[0].absent {
			return absentOf(FormulaPresentation), problems
		}
		it, _ := args[0].single()
		return displayVal(it.date.Instant.UTC().Format("15:04:05")), problems
	case "contains":
		return evalContains(args), problems
	case "format":
		return evalFormatFn(args), problems
	case "link":
		return evalLinkFn(args), problems
	case "icon":
		if args[0].absent {
			return absentOf(FormulaPresentation), problems
		}
		it, _ := args[0].single()
		return displayVal(it.text), problems
	case "file.asLink":
		return e.evalFileAsLink(args), problems
	case "file.hasTag":
		return e.evalFileHas("file.tags", args[0], true), problems
	case "file.hasLink":
		return e.evalFileHas("file.links", args[0], false), problems
	case "file.inFolder":
		return e.evalInFolder(args[0]), problems
	}
	return fval{absent: true}, problems
}

// evalIf is R-14's one sanctioned way to give absence a value: an ABSENT
// condition is FALSE, not absent.
func (e *FormulaEvaluator) evalIf(node *Call) (fval, []ComparisonProblem) {
	cond, problems := e.eval(node.Args[0])
	taken := false
	if !cond.absent {
		if it, ok := cond.single(); ok {
			taken = it.flag
		}
	}
	if taken {
		v, p := e.eval(node.Args[1])
		return v, append(problems, p...)
	}
	if len(node.Args) == 3 {
		v, p := e.eval(node.Args[2])
		return v, append(problems, p...)
	}
	// Two-argument if(): the missing branch IS absence, which is what FR-143a
	// means by "or one branch be absent".
	return fval{absent: true}, problems
}

func (e *FormulaEvaluator) evalList(args []fval) fval {
	out := fval{typ: FormulaAbsent}
	for _, a := range args {
		if a.absent {
			continue
		}
		if out.typ == FormulaAbsent {
			out.typ = a.typ
		}
		out.rounded = out.rounded || a.rounded
		out.items = append(out.items, a.items...)
	}
	return out
}

// evalRound implements FR-144's declared-scale boundary INSIDE an expression.
//
// The scale is a literal (the type checker enforced that), so the rounding is
// declared, and the result carries `rounded` onward so a later step cannot
// launder it back into an exact-looking number.
func (e *FormulaEvaluator) evalRound(node *Call, args []fval, problems []ComparisonProblem) (fval, []ComparisonProblem) {
	if args[0].absent {
		return absentOf(FormulaNumber), problems
	}
	it, ok := args[0].single()
	if !ok {
		return absentOf(FormulaNumber), problems
	}
	scale := int32(0)
	if len(args) == 2 {
		if s, sok := args[1].single(); sok && s.num != nil && s.num.IsInt() {
			scale = int32(s.num.Num().Int64())
		}
	}
	d, rounded := ratToDecimal(it.num, scale)
	out := numberVal(ratFromDecimal(d))
	out.rounded = rounded || args[0].rounded
	return out, problems
}

func (e *FormulaEvaluator) evalMean(args []fval, problems []ComparisonProblem) (fval, []ComparisonProblem) {
	src := args[0]
	if src.absent || len(src.items) == 0 {
		return absentOf(FormulaNumber), problems
	}
	sum := new(big.Rat)
	for _, it := range src.items {
		if it.num == nil {
			return absentOf(FormulaNumber), problems
		}
		sum.Add(sum, it.num)
	}
	// Exact: the sum is a rational and the count is an integer, so the mean is
	// a rational. Nothing rounds until the boundary.
	out := numberVal(new(big.Rat).Quo(sum, new(big.Rat).SetInt64(int64(len(src.items)))))
	out.rounded = src.rounded
	return out, problems
}

func evalDateFn(args []fval) fval {
	if args[0].absent {
		return absentOf(FormulaDate)
	}
	it, ok := args[0].single()
	if !ok {
		return absentOf(FormulaDate)
	}
	if args[0].typ == FormulaDate {
		// `x.date()` — truncate an instant to its day, in UTC. FR-130 fixes
		// UTC as the reading, "stated here so two implementers cannot pick
		// two".
		y, m, d := it.date.Instant.UTC().Date()
		return dateVal(DateValue{Instant: time.Date(y, m, d, 0, 0, 0, 0, time.UTC)})
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, strings.TrimSpace(it.text)); err == nil {
			return dateVal(DateValue{Instant: t.UTC(), HasTime: layout != "2006-01-02"})
		}
	}
	return absentOf(FormulaDate)
}

func evalContains(args []fval) fval {
	if args[0].absent || args[1].absent {
		return absentOf(FormulaBoolean)
	}
	needle, ok := args[1].single()
	if !ok {
		return absentOf(FormulaBoolean)
	}
	// FR-011a: text comparison in this package is full-Unicode case folded,
	// through FoldKey and nothing else. strings.Contains over raw bytes would
	// disagree with every other text comparison in the package on `straße`.
	folded := FoldKey(needle.text)
	for _, it := range args[0].items {
		if strings.Contains(FoldKey(formulaItemText(args[0].typ, it)), folded) {
			return boolVal(true)
		}
	}
	return boolVal(false)
}

func formulaItemText(typ FormulaType, it fitem) string {
	switch typ {
	case FormulaLink:
		return it.link.Target
	case FormulaDate:
		return it.date.String()
	case FormulaNumber:
		if it.num == nil {
			return ""
		}
		d, _ := ratToDecimal(it.num, FormulaDefaultScale)
		return d.String()
	}
	return it.text
}

func evalFormatFn(args []fval) fval {
	if args[0].absent || args[1].absent {
		return absentOf(FormulaPresentation)
	}
	spec, _ := args[1].single()
	parts := make([]string, 0, len(args[0].items))
	for _, it := range args[0].items {
		parts = append(parts, formulaItemText(args[0].typ, it))
	}
	value := strings.Join(parts, ", ")
	if strings.Contains(spec.text, "{}") {
		return displayVal(strings.ReplaceAll(spec.text, "{}", value))
	}
	return displayVal(value)
}

func evalLinkFn(args []fval) fval {
	if args[0].absent {
		return absentOf(FormulaPresentation)
	}
	target := strings.Join(collectText(args[0]), ", ")
	if len(args) == 2 && !args[1].absent {
		display, _ := args[1].single()
		return displayVal("[[" + target + "|" + display.text + "]]")
	}
	return displayVal("[[" + target + "]]")
}

func (e *FormulaEvaluator) evalFileAsLink(args []fval) fval {
	name := e.fileText("file.name")
	if name == "" {
		return absentOf(FormulaPresentation)
	}
	if len(args) == 1 && !args[0].absent {
		display, _ := args[0].single()
		return displayVal("[[" + name + "|" + display.text + "]]")
	}
	return displayVal("[[" + name + "]]")
}

// evalFileHas implements `file.hasTag` and `file.hasLink`.
//
// FR-134 fixes hasTag's semantics as HIERARCHY-AWARE — `a` matches `a` and
// `a/b` — and expresses that in a filter as `{any: [{tags,=,x},{tags,LIKE,x/%}]}`.
// The formula form must answer the same question the translated filter does, or
// a base whose tag clause imports as a filter and whose formula uses the method
// gives two different answers to one question.
func (e *FormulaEvaluator) evalFileHas(property string, arg fval, hierarchical bool) fval {
	if arg.absent {
		return absentOf(FormulaBoolean)
	}
	needle, ok := arg.single()
	if !ok {
		return absentOf(FormulaBoolean)
	}
	want := FoldKey(strings.TrimSpace(formulaItemText(arg.typ, needle)))
	if want == "" {
		return boolVal(false)
	}
	for _, have := range e.fileTexts(property) {
		got := FoldKey(have)
		if got == want {
			return boolVal(true)
		}
		if hierarchical && strings.HasPrefix(got, want+"/") {
			return boolVal(true)
		}
	}
	return boolVal(false)
}

// evalInFolder is FR-134's `file.inFolder(x)`: the folder AND its descendants.
func (e *FormulaEvaluator) evalInFolder(arg fval) fval {
	if arg.absent {
		return absentOf(FormulaBoolean)
	}
	needle, ok := arg.single()
	if !ok {
		return absentOf(FormulaBoolean)
	}
	want := FoldKey(strings.Trim(strings.TrimSpace(needle.text), "/"))
	folder := FoldKey(strings.Trim(e.fileText("file.folder"), "/"))
	if want == "" {
		return boolVal(true)
	}
	return boolVal(folder == want || strings.HasPrefix(folder, want+"/"))
}

func (e *FormulaEvaluator) fileText(name string) string {
	texts := e.fileTexts(name)
	if len(texts) == 0 {
		return ""
	}
	return texts[0]
}

func (e *FormulaEvaluator) fileTexts(name string) []string {
	if e.candidate == nil {
		return nil
	}
	pv, ok := e.candidate.FormulaFileProperty(name)
	if !ok || pv.State != StatePresent {
		return nil
	}
	out := make([]string, 0, len(pv.Values))
	for _, v := range pv.Values {
		if v.Type == TypeRelation || v.Type == TypePerson {
			out = append(out, v.Link.Target)
			continue
		}
		if v.Text != "" {
			out = append(out, v.Text)
			continue
		}
		out = append(out, v.Raw)
	}
	return out
}

func collectText(v fval) []string {
	out := make([]string, 0, len(v.items))
	for _, it := range v.items {
		out = append(out, formulaItemText(v.typ, it))
	}
	sort.SliceStable(out, func(i, j int) bool { return false })
	return out
}
