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
//	postfix .method(...) and .hour
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

// accessorFields is the parenless postfix set. FR-143's snapshot documents one.
var accessorFields = map[string]bool{"hour": true}

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
		if !accessorFields[name.text] {
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
	if len(segs) > 1 && accessorFields[segs[last]] {
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

func containsString(set []string, s string) bool {
	for _, v := range set {
		if v == s {
			return true
		}
	}
	return false
}
