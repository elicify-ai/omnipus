// Omnipus — ADR-068 D24.3 / spec FR-140..FR-148: the formula expression tree.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// FR-140 is the whole design of this layer in one sentence: THE PARSER LIVES IN
// THE WRITE PATH ONLY. A formula is parsed when it is written, stored as source
// text (FR-141), re-parsed when the view file is loaded, and evaluated in Go per
// candidate (FR-142). `knowledge_find` never sees an expression — a query names
// a formula only as `formula.<name>`, a reference to something already
// validated.
//
// The consequence that earns the design: AN UNPARSEABLE FORMULA NEVER EXISTS AT
// QUERY TIME, so a parse failure can never become an empty result. Every other
// arrangement — parse-on-read, parse-on-query, "best effort" evaluation — turns
// a typo into zero rows, which is the failure mode this whole specification is
// written against.
//
// This file holds the tree. It deliberately holds NO evaluation and NO type
// inference: node shapes here, positions and counting; typing in
// formula_type.go; evaluation in formula_eval.go. The counting functions
// (nodeCount, depth) are here because FR-146's three caps are stated over the
// TREE, not over the source text — 64 nodes and depth 8 per formula, 16
// formulas and 256 nodes per view.
// ---------------------------------------------------------------------------

// FormulaNode is one node of a parsed expression. It is a closed interface: the
// grammar is closed (FR-143), so the node set is closed too, and a new node
// type is a specification revision rather than a code change.
type FormulaNode interface {
	// Pos is the 0-based BYTE offset into the source expression where this node
	// begins. Every refusal names a position (FR-140), and a byte offset is the
	// only position that survives a formula containing non-ASCII text.
	Pos() int
	// nodes returns the number of tree nodes rooted here, counting this one.
	nodes() int
	// depth returns the height of the tree rooted here, counting this one.
	depth() int
	// children returns this node's operands, in source order.
	children() []FormulaNode
	// isFormulaNode keeps the interface closed to this package.
	isFormulaNode()
}

// ---------------------------------------------------------------------------
// Literals
// ---------------------------------------------------------------------------

// NumberLit is a numeric literal. It holds the SOURCE TEXT, never a parsed
// number, because parsing it here would be the one place a binary float could
// enter (FR-013/FR-020b): the text goes to ParseDecimal, which produces an
// exact Decimal, and evaluation lifts that into an exact rational.
type NumberLit struct {
	Offset int
	Text   string
}

// TextLit is a quoted string literal, already unescaped.
type TextLit struct {
	Offset int
	Value  string
}

// BoolLit is `true` or `false`.
type BoolLit struct {
	Offset int
	Value  bool
}

// ---------------------------------------------------------------------------
// References
// ---------------------------------------------------------------------------

// RefKind says what a dotted name refers to. The three kinds are disjoint by
// construction and the parser decides which applies, so evaluation never has to
// re-inspect a name.
type RefKind int

const (
	// RefProperty is a declared property of the record under evaluation.
	RefProperty RefKind = iota
	// RefFile is one of FR-130's thirteen `file.*` virtual properties.
	RefFile
	// RefFormula is `formula.<name>` — a reference to another formula in the
	// same view. FR-148: these form a graph that is checked for cycles.
	RefFormula
)

// Ref is a property, file-metadata or formula reference.
type Ref struct {
	Offset int
	Kind   RefKind
	// Name is the resolved name: the property name for RefProperty, the full
	// dotted `file.x` for RefFile, the bare formula name for RefFormula.
	Name string
	// Source is the dotted path exactly as written, for refusal messages.
	Source string
}

// ---------------------------------------------------------------------------
// Operators
// ---------------------------------------------------------------------------

// UnaryOp is `!` or `-`.
type UnaryOp struct {
	Offset  int
	Op      string
	Operand FormulaNode
}

// BinaryOp is one of FR-143's arithmetic, boolean or comparison operators.
type BinaryOp struct {
	Offset int
	Op     string
	Left   FormulaNode
	Right  FormulaNode
}

// ---------------------------------------------------------------------------
// Calls
// ---------------------------------------------------------------------------

// Call is a function call from FR-143's closed function set — `if`, `toFixed`,
// `mean`, `round`, `date`, `today`, `now`, `format`, `list`, `link`, `icon`,
// `contains` — and FR-134's four file methods, whose names carry the `file.`
// prefix (`file.hasTag`, `file.inFolder`, `file.hasLink`, `file.asLink`).
//
// A METHOD CALL IS THE SAME NODE. Obsidian writes `x.toFixed(2)` and
// `toFixed(x, 2)` for the same thing, and both parse to Call{Name: "toFixed",
// Args: [x, 2]} — the receiver is simply the first argument. This is not a
// shortcut: it means one arity rule and one type rule per function instead of
// two that can drift apart, which is the same single-implementation discipline
// §8 imposes on the comparator.
//
// FR-134 is worth restating here because the two halves look contradictory and
// are not: in a FILTER the file methods are TRANSLATIONS to ordinary leaves and
// never grammar — O-3 admits no function-call syntax in a structured filter
// object. In a FORMULA they are grammar, because FR-143 lists them in the
// closed function set. The distinction is the whole of O-3: the query path
// parses nothing; the write path parses everything.
type Call struct {
	Offset int
	Name   string
	// Source is the spelling the author used, so a refusal about
	// `file.mtime.toFixed(2)` does not quote back `toFixed`.
	Source string
	Args   []FormulaNode
}

// FieldAccess is a parenless postfix accessor — `.hour` is the only one
// FR-143's snapshot documents.
type FieldAccess struct {
	Offset   int
	Receiver FormulaNode
	Field    string
}

// ---------------------------------------------------------------------------
// Interface implementations — deliberately mechanical.
// ---------------------------------------------------------------------------

func (n *NumberLit) Pos() int   { return n.Offset }
func (n *TextLit) Pos() int     { return n.Offset }
func (n *BoolLit) Pos() int     { return n.Offset }
func (n *Ref) Pos() int         { return n.Offset }
func (n *UnaryOp) Pos() int     { return n.Offset }
func (n *BinaryOp) Pos() int    { return n.Offset }
func (n *Call) Pos() int        { return n.Offset }
func (n *FieldAccess) Pos() int { return n.Offset }

func (n *NumberLit) isFormulaNode()   {}
func (n *TextLit) isFormulaNode()     {}
func (n *BoolLit) isFormulaNode()     {}
func (n *Ref) isFormulaNode()         {}
func (n *UnaryOp) isFormulaNode()     {}
func (n *BinaryOp) isFormulaNode()    {}
func (n *Call) isFormulaNode()        {}
func (n *FieldAccess) isFormulaNode() {}

func (n *NumberLit) children() []FormulaNode { return nil }
func (n *TextLit) children() []FormulaNode   { return nil }
func (n *BoolLit) children() []FormulaNode   { return nil }
func (n *Ref) children() []FormulaNode       { return nil }

func (n *UnaryOp) children() []FormulaNode  { return []FormulaNode{n.Operand} }
func (n *BinaryOp) children() []FormulaNode { return []FormulaNode{n.Left, n.Right} }
func (n *Call) children() []FormulaNode     { return n.Args }

func (n *FieldAccess) children() []FormulaNode { return []FormulaNode{n.Receiver} }

func (n *NumberLit) nodes() int   { return 1 }
func (n *TextLit) nodes() int     { return 1 }
func (n *BoolLit) nodes() int     { return 1 }
func (n *Ref) nodes() int         { return 1 }
func (n *UnaryOp) nodes() int     { return countNodes(n) }
func (n *BinaryOp) nodes() int    { return countNodes(n) }
func (n *Call) nodes() int        { return countNodes(n) }
func (n *FieldAccess) nodes() int { return countNodes(n) }

func (n *NumberLit) depth() int   { return 1 }
func (n *TextLit) depth() int     { return 1 }
func (n *BoolLit) depth() int     { return 1 }
func (n *Ref) depth() int         { return 1 }
func (n *UnaryOp) depth() int     { return treeDepth(n) }
func (n *BinaryOp) depth() int    { return treeDepth(n) }
func (n *Call) depth() int        { return treeDepth(n) }
func (n *FieldAccess) depth() int { return treeDepth(n) }

// countNodes counts the tree rooted at n, counting n itself.
//
// It is ITERATIVE rather than recursive. A recursive counter would be the one
// place in this file that could stack-overflow on a hostile tree — and the
// counter is precisely what FR-146's caps are enforced with, so it has to be
// safe on a tree that has not yet passed them.
func countNodes(root FormulaNode) int {
	if root == nil {
		return 0
	}
	total := 0
	stack := []FormulaNode{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == nil {
			continue
		}
		total++
		stack = append(stack, n.children()...)
	}
	return total
}

// treeDepth returns the height of the tree rooted at n, counting n as 1.
//
// Iterative for the same reason countNodes is: it runs BEFORE the depth cap has
// been checked, so it must survive the tree the cap exists to refuse.
func treeDepth(root FormulaNode) int {
	if root == nil {
		return 0
	}
	type frame struct {
		node  FormulaNode
		depth int
	}
	best := 0
	stack := []frame{{root, 1}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.node == nil {
			continue
		}
		if f.depth > best {
			best = f.depth
		}
		for _, c := range f.node.children() {
			stack = append(stack, frame{c, f.depth + 1})
		}
	}
	return best
}

// FormulaNodeCount is FR-146's per-formula node count: the number of tree nodes
// in one parsed expression, the quantity the 64-node cap is stated over.
func FormulaNodeCount(n FormulaNode) int { return countNodes(n) }

// FormulaDepth is FR-146's per-formula depth: the height of the tree, the
// quantity the depth-8 cap is stated over.
func FormulaDepth(n FormulaNode) int { return treeDepth(n) }

// formulaRefs returns the names of the formulas this tree references, sorted and
// de-duplicated. It is the edge list FR-148's cycle walk runs over.
func formulaRefs(root FormulaNode) []string {
	seen := map[string]bool{}
	stack := []FormulaNode{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == nil {
			continue
		}
		if r, ok := n.(*Ref); ok && r.Kind == RefFormula {
			seen[r.Name] = true
		}
		stack = append(stack, n.children()...)
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// formulaRefPathString renders a cycle path the way FR-148 requires a refusal to
// name it: `a → b → a`.
func formulaRefPathString(path []string) string {
	return strings.Join(path, " → ")
}
