// Omnipus — ADR-068 D24.3 / spec FR-140, FR-143: the write-path parser.
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
// This is the ONLY parser in the formula layer, and FR-140 says where it may be
// called from: the WRITE path — `knowledge_configure` and the importer — and
// the view LOADER, which re-checks a hand-edited file. It is never reached from
// `knowledge_find`, because `knowledge_find` accepts no text expression at all;
// a query names a formula as `formula.<name>`, a reference to something already
// validated and stored.
//
// That placement is the design, not an implementation detail. A parser on the
// query path would turn a typo into an empty result — a wrong answer that looks
// exactly like a right one — and no amount of care downstream recovers from it.
// Here a typo is a REFUSAL, at write time, naming the position.
//
// The grammar is CLOSED (FR-143) and pinned to Obsidian's Bases syntax
// reference as fetched 2026-08-30. Precedence, lowest binding first:
//
//	||
//	&&
//	== !=
//	< <= > >=
//	+ -
//	* / %
//	unary ! -
//	postfix .method(...) and the parenless accessors (.hour, .days, .length, …)
//	literals, references, ( … )
//
// Reordering that ladder changes what stored formulas MEAN, so it is asserted
// by test rather than left to a reader's memory.
// ---------------------------------------------------------------------------

// formulaFunctions is FR-143's closed function set, with each function's arity.
//
// A method call is the same node as a plain call (see Call's documentation), so
// `min`/`max` here count the receiver: `x.toFixed(2)` and `toFixed(x, 2)` both
// arrive as two arguments and are checked once.
var formulaFunctions = map[string]struct{ min, max int }{
	"if":       {2, 3},
	"toFixed":  {2, 2},
	"mean":     {1, 1},
	"round":    {1, 2},
	"date":     {1, 1},
	"today":    {0, 0},
	"now":      {0, 0},
	"format":   {2, 2},
	"list":     {0, maxFormulaNodes},
	"link":     {1, 2},
	"icon":     {1, 1},
	"contains": {2, 2},
	"time":     {1, 1},

	// isType is the snapshot's `any.isType(type): boolean`. The receiver is the
	// first argument, exactly as it is for every other method (see Call's
	// documentation), so the arity counts two.
	"isType": {2, 2},

	// FR-134's four file methods. They carry the `file.` prefix in the node so
	// nothing can call `hasTag(x)` bare — the receiver is not optional, it is
	// part of the name.
	"file.hasTag":   {1, 1},
	"file.inFolder": {1, 1},
	"file.hasLink":  {1, 1},
	"file.asLink":   {0, 1},
}

// fileMethodNames is the set of segments admitted after `file.` when a `(`
// follows. `file.tags` is a PROPERTY and `file.hasTag(...)` is a METHOD; the
// parser tells them apart by the parenthesis, and this set is what makes
// `file.tags(...)` a named refusal instead of an unknown function.
var fileMethodNames = map[string]bool{
	"hasTag": true, "inFolder": true, "hasLink": true, "asLink": true,
}

// fileVirtualProperties is FR-130's thirteen. The map is the enforcement: a
// `file.` name outside it is refused BY NAME listing the thirteen, which is
// FR-024's posture applied to the file namespace.
var fileVirtualProperties = map[string]FormulaType{
	"file.name":       FormulaText,
	"file.path":       FormulaText,
	"file.folder":     FormulaText,
	"file.ext":        FormulaText,
	"file.mtime":      FormulaDate,
	"file.ctime":      FormulaDate,
	"file.size":       FormulaNumber,
	"file.tags":       FormulaText,
	"file.links":      FormulaLink,
	"file.embeds":     FormulaLink,
	"file.backlinks":  FormulaLink,
	"file.properties": FormulaText,
	// FR-130: `file.file` is a formula/display operand only, never a filter
	// target. It types as a presentation value, which is exactly what makes a
	// comparison over it refuse under R-16 without a second rule.
	"file.file": FormulaPresentation,
}

// fileManyProperties records which of the thirteen are `many`.
var fileManyProperties = map[string]bool{
	"file.tags": true, "file.links": true, "file.embeds": true,
	"file.backlinks": true, "file.properties": true,
}

// accessorRule is one parenless postfix accessor: what it may be read FROM and
// what it produces.
//
// The receiver requirement is the whole value of the table. `.days` is defined
// on a DURATION and nothing else, so `due.days` — reading `.days` off a date,
// which is the mistake somebody makes within a minute of learning the accessor
// exists — is a REFUSAL naming the receiver's type, not a silent zero.
type accessorRule struct {
	// receiver is the type this accessor is defined on. Empty means the
	// accessor is defined by ARITY instead — `.length` is the only one, and it
	// reads a LIST of anything.
	receiver FormulaType
	// requiresMany says the receiver must be a list.
	requiresMany bool
	// result is the accessor's static result type.
	result FormulaType
	// scale is the DECLARED scale a number result carries (FR-144). It is 0 for
	// an accessor that can only ever be a whole number and FormulaDefaultScale
	// for one that can be fractional — `.days` of 3 days 5 hours is 3.2083…,
	// and rounding it here would be an invented answer rather than a declared
	// one. Upstream's own remedy for a whole number is `.days.round(0)`, which
	// this grammar parses unchanged.
	scale int32
	// receiverPhrase is what a refusal says the receiver should have been.
	receiverPhrase string
}

// accessorFields is the parenless postfix set.
//
// FR-143's sentence documents `.hour`. The duration fields and `list.length`
// come from the SAME pinned snapshot (the Bases syntax reference as fetched
// 2026-08-30) — its "Duration Type" table lists `duration.days`, `.hours`,
// `.minutes`, `.seconds` and `.milliseconds` as "Total <unit> in duration", and
// its "List Functions" section lists `list.length`. They were missing from the
// transcription, not from the snapshot: the founder's own eighteen `.base`
// files use `.days` sixty-five times and `.length` once, so a grammar without
// them cannot read a real vault at all.
//
// THE DATE FIELD FAMILY — FR-143 pin advanced 2026-09-01 (see the spec's FR-143
// revision entry and ADR-068 §D24.3a). The seven singular fields below are the
// COMPLETE "Date type › Fields" table of the Bases FUNCTIONS reference as
// fetched 2026-09-01 from https://obsidian.md/help/bases/functions, transcribed
// whole rather than mined for the two that unblocked a view:
//
//	date.year | number | The year of the date
//	date.month | number | The month of the date (1–12)
//	date.day | number | The day of the month
//	date.hour | number | The hour (0–23)
//	date.minute | number | The minute (0–59)
//	date.second | number | The second (0–59)
//	date.millisecond | number | The millisecond (0–999)
//
// Only `.hour` had been transcribed before. Adopting the table whole is the
// point: a snapshot taken two rows at a time is not a snapshot, and the six
// additions are one table, one receiver, one result type and one meaning
// (extract a calendar component from a date). There is no `.week`, `.quarter`
// or `.dayOfWeek` to decide about — the reference defines none, so none is here.
//
// SINGULAR IS A DATE, PLURAL IS A DURATION, and that is the reference's own
// distinction, not ours: `.second` is the seconds field of a clock time (0–59)
// while `.seconds` is a whole duration expressed in seconds. `.hour`/`.hours`
// already carried that split before this revision; `.day`/`.days`,
// `.minute`/`.minutes`, `.second`/`.seconds` and `.millisecond`/`.milliseconds`
// now do too. Reading the wrong one is a typed REFUSAL naming the receiver, not
// a plausible wrong number — which is the whole reason accessorRule carries a
// receiver at all.
//
// WHAT WAS DELIBERATELY NOT ADOPTED on 2026-09-01: the same fetch shows upstream
// has RESTRUCTURED its duration model — the "Duration Type" field table this
// file cites above is gone from both pages, and "Date arithmetic" now says
// subtracting two dates yields "the millisecond difference" (a number) rather
// than a duration object. Adopting that would DELETE `.days` and its four
// siblings and change what `dateA - dateB` means. That is a behavioural
// revision, not an addition, it breaks sixty-five working uses in the founder's
// own vault, and it is out of scope for this one. The duration model therefore
// stays pinned at 2026-08-30 and the divergence is recorded — named, dated and
// owed a decision — rather than silently inherited. See the spec's FR-143
// revision entry.
//
// `string.length` is in the snapshot too and is deliberately NOT here. JavaScript
// counts UTF-16 code units, Go counts bytes or runes, and the three disagree on
// every non-BMP character; a length that quietly means one of three things is
// worse than a refusal that names the gap. A list length has no such ambiguity.
var accessorFields = map[string]accessorRule{
	// The Date type › Fields table, whole (pin advanced 2026-09-01).
	"year":        {receiver: FormulaDate, result: FormulaNumber, scale: 0, receiverPhrase: "a date"},
	"month":       {receiver: FormulaDate, result: FormulaNumber, scale: 0, receiverPhrase: "a date"},
	"day":         {receiver: FormulaDate, result: FormulaNumber, scale: 0, receiverPhrase: "a date"},
	"minute":      {receiver: FormulaDate, result: FormulaNumber, scale: 0, receiverPhrase: "a date"},
	"second":      {receiver: FormulaDate, result: FormulaNumber, scale: 0, receiverPhrase: "a date"},
	"millisecond": {receiver: FormulaDate, result: FormulaNumber, scale: 0, receiverPhrase: "a date"},

	"hour":         {receiver: FormulaDate, result: FormulaNumber, scale: 0, receiverPhrase: "a date"},
	"days":         {receiver: FormulaDuration, result: FormulaNumber, scale: FormulaDefaultScale, receiverPhrase: "a duration, which is what subtracting one date from another produces"},
	"hours":        {receiver: FormulaDuration, result: FormulaNumber, scale: FormulaDefaultScale, receiverPhrase: "a duration, which is what subtracting one date from another produces"},
	"minutes":      {receiver: FormulaDuration, result: FormulaNumber, scale: FormulaDefaultScale, receiverPhrase: "a duration, which is what subtracting one date from another produces"},
	"seconds":      {receiver: FormulaDuration, result: FormulaNumber, scale: FormulaDefaultScale, receiverPhrase: "a duration, which is what subtracting one date from another produces"},
	"milliseconds": {receiver: FormulaDuration, result: FormulaNumber, scale: FormulaDefaultScale, receiverPhrase: "a duration, which is what subtracting one date from another produces"},
	"length":       {requiresMany: true, result: FormulaNumber, scale: 0, receiverPhrase: "a list"},
}

// isAccessorField reports whether a name is one of the parenless accessors.
func isAccessorField(name string) bool {
	_, ok := accessorFields[name]
	return ok
}

// ParseFormula parses one expression into a tree, or refuses naming the byte
// position and the reason (FR-140).
//
// It does NOT type-check and it does NOT apply FR-146's caps: those are
// separate passes with separate refusal codes, because "this does not parse",
// "this does not type" and "this is too big" are three different things for the
// author to fix and collapsing them produces a message that helps with none.
// ValidateFormulaSet runs all three in order.
func ParseFormula(src string) (FormulaNode, *FormulaError) {
	toks, err := lexFormula(src)
	if err != nil {
		return nil, err
	}
	p := &formulaParser{toks: toks, src: src}
	node, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tokEOF {
		t := p.peek()
		return nil, newFormulaError(FormulaErrSyntax, t.offset, "the end of the expression",
			"the expression continues with %s %q after a complete expression", t.kind, t.text)
	}
	return node, nil
}

type formulaParser struct {
	toks []formulaToken
	pos  int
	src  string
	// depth guards the RECURSIVE descent itself. FR-146's depth cap is checked
	// after parsing, over the finished tree — which is too late to help if the
	// parser has already blown the stack building it. `((((((…` is a one-line
	// expression that recurses once per byte, so the recursion needs its own
	// bound, set generously above the tree cap so the tree cap is what an
	// author actually meets.
	depth int
}

// maxParseDepth bounds the parser's own recursion. See formulaParser.depth.
const maxParseDepth = 128

func (p *formulaParser) peek() formulaToken { return p.toks[p.pos] }
func (p *formulaParser) next() formulaToken { t := p.toks[p.pos]; p.pos++; return t }
func (p *formulaParser) peekN(n int) formulaToken {
	if p.pos+n >= len(p.toks) {
		return p.toks[len(p.toks)-1]
	}
	return p.toks[p.pos+n]
}

// binaryLevels is the precedence ladder, loosest first. Level i binds looser
// than level i+1.
var binaryLevels = [][]string{
	{"||"},
	{"&&"},
	{"==", "!="},
	{"<", "<=", ">", ">="},
	{"+", "-"},
	{"*", "/", "%"},
}

func (p *formulaParser) parseExpr(level int) (FormulaNode, *FormulaError) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxParseDepth {
		return nil, newFormulaError(FormulaErrTooLarge, p.peek().offset,
			fmt.Sprintf("an expression nested no more than %d levels deep", maxParseDepth),
			"the expression nests too deeply to parse")
	}

	if level >= len(binaryLevels) {
		return p.parseUnary()
	}

	left, err := p.parseExpr(level + 1)
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind != tokOp || !containsString(binaryLevels[level], t.text) {
			return left, nil
		}
		p.next()
		right, rerr := p.parseExpr(level + 1)
		if rerr != nil {
			return nil, rerr
		}
		left = &BinaryOp{Offset: t.offset, Op: t.text, Left: left, Right: right}
	}
}

func (p *formulaParser) parseUnary() (FormulaNode, *FormulaError) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxParseDepth {
		return nil, newFormulaError(FormulaErrTooLarge, p.peek().offset,
			fmt.Sprintf("an expression nested no more than %d levels deep", maxParseDepth),
			"the expression nests too deeply to parse")
	}
	t := p.peek()
	if t.kind == tokOp && (t.text == "!" || t.text == "-") {
		p.next()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &UnaryOp{Offset: t.offset, Op: t.text, Operand: operand}, nil
	}
	return p.parsePostfix()
}

// parsePostfix applies `.method(...)` and `.hour` to whatever a primary
// produced, so `date(x).hour` and `(a + b).toFixed(2)` both parse.
func (p *formulaParser) parsePostfix() (FormulaNode, *FormulaError) {
	node, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tokDot {
		dot := p.next()
		name := p.peek()
		if name.kind != tokIdent {
			return nil, newFormulaError(FormulaErrSyntax, dot.offset, "a method or field name",
				"the `.` at position %d is not followed by a name", dot.offset)
		}
		p.next()
		if p.peek().kind == tokLParen {
			args, aerr := p.parseArgs()
			if aerr != nil {
				return nil, aerr
			}
			if _, ok := formulaFunctions[name.text]; !ok {
				return nil, unknownFunctionError(name.offset, name.text)
			}
			node = &Call{
				Offset: node.Pos(),
				Name:   name.text,
				Source: name.text,
				Args:   append([]FormulaNode{node}, args...),
			}
			continue
		}
		if !isAccessorField(name.text) {
			return nil, newFormulaError(FormulaErrUnknownReference, name.offset, accessorFieldList(),
				"`.%s` is not a field the formula grammar defines", name.text)
		}
		node = &FieldAccess{Offset: node.Pos(), Receiver: node, Field: name.text}
	}
	return node, nil
}

func (p *formulaParser) parsePrimary() (FormulaNode, *FormulaError) {
	t := p.peek()
	switch t.kind {
	case tokNumber:
		p.next()
		return &NumberLit{Offset: t.offset, Text: t.text}, nil
	case tokText:
		p.next()
		return &TextLit{Offset: t.offset, Value: t.text}, nil
	case tokLParen:
		p.next()
		inner, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tokRParen {
			return nil, newFormulaError(FormulaErrSyntax, t.offset, "a closing )",
				"the `(` at position %d is never closed", t.offset)
		}
		p.next()
		return inner, nil
	case tokIdent:
		return p.parseIdentPath()
	case tokEOF:
		return nil, newFormulaError(FormulaErrSyntax, t.offset, "a value, a name or (",
			"the expression ends where a value was expected")
	}
	return nil, newFormulaError(FormulaErrSyntax, t.offset, "a value, a name or (",
		"%s %q is not where an expression can start", t.kind, t.text)
}

// parseIdentPath reads a dotted name and decides what it refers to: a call, a
// file method, a file virtual property, a formula reference, a property, or a
// property with an accessor.
func (p *formulaParser) parseIdentPath() (FormulaNode, *FormulaError) {
	first := p.next()
	if first.text == "true" || first.text == "false" {
		return &BoolLit{Offset: first.offset, Value: first.text == "true"}, nil
	}

	segs := []string{first.text}
	offsets := []int{first.offset}
	for p.peek().kind == tokDot && p.peekN(1).kind == tokIdent {
		p.next()
		id := p.next()
		segs = append(segs, id.text)
		offsets = append(offsets, id.offset)
		if p.peek().kind == tokLParen {
			break
		}
	}
	source := strings.Join(segs, ".")
	last := len(segs) - 1

	// A `(` here means the last segment is being CALLED.
	if p.peek().kind == tokLParen {
		args, err := p.parseArgs()
		if err != nil {
			return nil, err
		}
		switch {
		case len(segs) == 1:
			if _, ok := formulaFunctions[segs[0]]; !ok {
				return nil, unknownFunctionError(first.offset, segs[0])
			}
			return &Call{Offset: first.offset, Name: segs[0], Source: source, Args: args}, nil

		case len(segs) == 2 && segs[0] == "file":
			if !fileMethodNames[segs[1]] {
				return nil, newFormulaError(FormulaErrUnknownFunction, offsets[1], fileMethodList(),
					"`file.%s(…)` is not a file method the formula grammar defines", segs[1])
			}
			name := "file." + segs[1]
			return &Call{Offset: first.offset, Name: name, Source: source, Args: args}, nil

		default:
			// `a.b.c(…)` where the receiver is itself a reference: fold the
			// receiver into the first argument, exactly as parsePostfix does.
			recv, err := p.reference(segs[:last], offsets[0], strings.Join(segs[:last], "."))
			if err != nil {
				return nil, err
			}
			if _, ok := formulaFunctions[segs[last]]; !ok {
				return nil, unknownFunctionError(offsets[last], segs[last])
			}
			return &Call{
				Offset: first.offset,
				Name:   segs[last],
				Source: source,
				Args:   append([]FormulaNode{recv}, args...),
			}, nil
		}
	}

	// No `(`: a reference, possibly with a trailing parenless accessor.
	//
	// `formula.<name>` is excluded, because a formula is allowed to be CALLED
	// `length` (or `days`, or `hour`). Without the exclusion the accessor rule
	// steals the name: `formula.length` would resolve its receiver as the bare
	// word `formula`, which is not a value, and the author would be told to
	// "name the formula you mean" about an expression that already did.
	if len(segs) > 1 && isAccessorField(segs[last]) && !(len(segs) == 2 && segs[0] == "formula") {
		recv, err := p.reference(segs[:last], offsets[0], strings.Join(segs[:last], "."))
		if err != nil {
			return nil, err
		}
		return &FieldAccess{Offset: first.offset, Receiver: recv, Field: segs[last]}, nil
	}
	return p.reference(segs, first.offset, source)
}

// reference resolves a dotted path with no call and no accessor into one of the
// three RefKinds, or refuses naming the path.
func (p *formulaParser) reference(segs []string, offset int, source string) (FormulaNode, *FormulaError) {
	switch {
	case len(segs) == 1:
		switch segs[0] {
		case "file":
			return nil, newFormulaError(FormulaErrUnknownReference, offset, fileVirtualPropertyList(),
				"`file` on its own is not a value; name one of the file properties")
		case "formula":
			return nil, newFormulaError(FormulaErrUnknownReference, offset, "formula.<name>",
				"`formula` on its own is not a value; name the formula you mean")
		}
		return &Ref{Offset: offset, Kind: RefProperty, Name: segs[0], Source: source}, nil

	case len(segs) == 2 && segs[0] == "file":
		name := "file." + segs[1]
		if _, ok := fileVirtualProperties[name]; !ok {
			return nil, newFormulaError(FormulaErrUnknownReference, offset, fileVirtualPropertyList(),
				"`%s` is not one of the file properties", name)
		}
		return &Ref{Offset: offset, Kind: RefFile, Name: name, Source: source}, nil

	case len(segs) == 2 && segs[0] == "formula":
		return &Ref{Offset: offset, Kind: RefFormula, Name: segs[1], Source: source}, nil
	}

	return nil, newFormulaError(FormulaErrUnknownReference, offset,
		"a property name, `file.<property>` or `formula.<name>`",
		"`%s` is not a name the formula grammar can resolve", source)
}

// parseArgs reads `( a, b, c )`, with the `(` still unconsumed.
func (p *formulaParser) parseArgs() ([]FormulaNode, *FormulaError) {
	open := p.next() // the (
	var args []FormulaNode
	if p.peek().kind == tokRParen {
		p.next()
		return args, nil
	}
	for {
		arg, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		switch p.peek().kind {
		case tokComma:
			p.next()
			continue
		case tokRParen:
			p.next()
			return args, nil
		default:
			t := p.peek()
			return nil, newFormulaError(FormulaErrSyntax, t.offset, ", or )",
				"the argument list opened at position %d is not closed", open.offset)
		}
	}
}

func unknownFunctionError(offset int, name string) *FormulaError {
	return newFormulaError(FormulaErrUnknownFunction, offset, formulaFunctionList(),
		"`%s` is not a function the formula grammar defines", name)
}

// formulaFunctionList renders the closed set for a refusal. FR-143: a name
// outside the set is refused BY NAME, LISTING the supported set — a refusal that
// says only "unknown function" makes the author guess at a closed set they
// cannot see.
func formulaFunctionList() string {
	names := make([]string, 0, len(formulaFunctions))
	for n := range formulaFunctions {
		names = append(names, n)
	}
	sort.Strings(names)
	return "one of " + strings.Join(names, ", ")
}

func fileMethodList() string {
	names := make([]string, 0, len(fileMethodNames))
	for n := range fileMethodNames {
		names = append(names, "file."+n+"()")
	}
	sort.Strings(names)
	return "one of " + strings.Join(names, ", ")
}

func fileVirtualPropertyList() string {
	names := make([]string, 0, len(fileVirtualProperties))
	for n := range fileVirtualProperties {
		names = append(names, n)
	}
	sort.Strings(names)
	return "one of " + strings.Join(names, ", ")
}

func accessorFieldList() string {
	names := make([]string, 0, len(accessorFields))
	for n := range accessorFields {
		names = append(names, "."+n)
	}
	sort.Strings(names)
	return "one of " + strings.Join(names, ", ")
}

// isTypeNameList renders isType's closed argument set for a refusal.
func isTypeNameList() string {
	names := make([]string, 0, len(isTypeNames))
	for n := range isTypeNames {
		names = append(names, `"`+n+`"`)
	}
	sort.Strings(names)
	return "one of " + strings.Join(names, ", ")
}

func containsString(set []string, s string) bool {
	for _, v := range set {
		if v == s {
			return true
		}
	}
	return false
}
