// Omnipus — ADR-068 D24.3 / spec FR-143: the closed grammar's tokenizer.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS, AND WHAT "CLOSED" MEANS HERE
//
// FR-143 pins the grammar to Obsidian's Bases syntax reference AS FETCHED
// 2026-08-30 (https://obsidian.md/help/bases/syntax). "Closed" is not a style
// preference: an open grammar against a drifting upstream is a moving target
// nobody notices moving (review finding F12), so anything outside the snapshot
// is REFUSED BY NAME and adopting a newer snapshot is a specification revision
// with its own diff, never a silent code change.
//
// The tokenizer is therefore allowed to reject characters, and does. A byte
// that starts no token in the snapshot is an error naming its position, not an
// "unknown token" the parser gets to shrug at later.
//
// POSITIONS ARE BYTE OFFSETS. FR-140 requires every refusal to name the
// position; a rune index would name a different place than an editor's column
// for any formula containing non-ASCII text, and formulas quote note titles.
// ---------------------------------------------------------------------------

// formulaTokenKind classifies one lexeme.
type formulaTokenKind int

const (
	tokEOF formulaTokenKind = iota
	tokNumber
	tokText
	tokIdent
	tokOp
	tokLParen
	tokRParen
	tokComma
	tokDot
)

func (k formulaTokenKind) String() string {
	switch k {
	case tokEOF:
		return "end of expression"
	case tokNumber:
		return "number"
	case tokText:
		return "text literal"
	case tokIdent:
		return "name"
	case tokOp:
		return "operator"
	case tokLParen:
		return "("
	case tokRParen:
		return ")"
	case tokComma:
		return ","
	case tokDot:
		return "."
	}
	return "formulaToken"
}

// formulaToken is one lexeme with the byte offset it started at.
type formulaToken struct {
	kind   formulaTokenKind
	text   string
	offset int
}

// FormulaError is a write-time refusal from the formula layer.
//
// It is a distinct type from ValueError and ComparisonProblem because it is
// raised at a distinct time and by a distinct authority: this is the WRITE path
// refusing to store something (FR-140), not the read path reporting what a
// record said. Its fields are what a refusal must carry to be actionable —
// which formula, where in it, why, and what would have been accepted.
type FormulaError struct {
	// Formula is the formula's name in the view, empty when a bare expression
	// is being parsed outside a view.
	Formula string
	// Offset is the 0-based BYTE offset into the expression source. -1 when the
	// fault is about the formula as a whole (a cycle, a cap) rather than a
	// position inside it.
	Offset int
	// Reason is the human sentence.
	Reason string
	// Expected names what would have been accepted, where a closed set exists.
	Expected string
	// Code classifies the fault for callers that branch on it.
	Code FormulaErrorCode
	// Path is FR-148's cycle path, `a → b → a`, empty otherwise.
	Path []string
}

// FormulaErrorCode classifies a formula refusal.
type FormulaErrorCode string

const (
	// FormulaErrSyntax is an expression that does not parse (FR-140).
	FormulaErrSyntax FormulaErrorCode = "formula_syntax"
	// FormulaErrUnknownFunction is a name outside FR-143's closed set.
	FormulaErrUnknownFunction FormulaErrorCode = "formula_unknown_function"
	// FormulaErrUnknownReference is a `file.*` name outside FR-130's thirteen,
	// or a dotted path the grammar does not admit.
	FormulaErrUnknownReference FormulaErrorCode = "formula_unknown_reference"
	// FormulaErrArity is a function called with the wrong number of arguments.
	FormulaErrArity FormulaErrorCode = "formula_wrong_argument_count"
	// FormulaErrType is FR-143a/R-18: a static type that does not check —
	// including `if(c, 1, "x")`, whose branches disagree.
	FormulaErrType FormulaErrorCode = "formula_type"
	// FormulaErrTooLarge is one of FR-146's three caps (nodes, depth, count).
	FormulaErrTooLarge FormulaErrorCode = "formula_too_large"
	// FormulaErrCycle is FR-148: the reference graph contains a cycle.
	FormulaErrCycle FormulaErrorCode = "formula_cycle"
	// FormulaErrName is an unusable formula name.
	FormulaErrName FormulaErrorCode = "formula_invalid_name"
)

func (e *FormulaError) Error() string {
	var b strings.Builder
	if e.Formula != "" {
		b.WriteString("formula " + e.Formula + ": ")
	}
	b.WriteString(e.Reason)
	if e.Offset >= 0 {
		fmt.Fprintf(&b, " at position %d", e.Offset)
	}
	if e.Expected != "" {
		b.WriteString("; expected " + e.Expected)
	}
	return b.String()
}

// newFormulaError builds a positioned refusal. Offset is always a real byte
// offset here; the whole-formula faults construct the struct directly with -1.
func newFormulaError(code FormulaErrorCode, offset int, expected, format string, args ...any) *FormulaError {
	return &FormulaError{
		Offset:   offset,
		Reason:   fmt.Sprintf(format, args...),
		Expected: expected,
		Code:     code,
	}
}

// maxFormulaSourceBytes bounds the SOURCE a parser will look at.
//
// FR-146's caps are stated over the parsed TREE, and a tree cannot be counted
// before it is built — so a megabyte of `((((((...` would be tokenized and
// half-parsed before any node cap could fire. The source bound is what makes
// the node bound reachable safely. 4 KiB is roughly sixty times the longest
// formula in the founder's eighteen bases, so it refuses nothing real.
const maxFormulaSourceBytes = 4096

// lexFormula turns an expression into tokens, or refuses naming a position.
func lexFormula(src string) ([]formulaToken, *FormulaError) {
	if len(src) > maxFormulaSourceBytes {
		return nil, newFormulaError(FormulaErrTooLarge, 0,
			fmt.Sprintf("at most %d bytes", maxFormulaSourceBytes),
			"the expression is %d bytes long", len(src))
	}

	var out []formulaToken
	i := 0
	for i < len(src) {
		c := src[i]

		// Whitespace, including the Unicode kind a paste can carry in.
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i++
			continue
		}
		if c >= utf8.RuneSelf {
			r, size := utf8.DecodeRuneInString(src[i:])
			if unicode.IsSpace(r) {
				i += size
				continue
			}
			if isIdentRune(r) {
				start := i
				i = scanIdent(src, i)
				out = append(out, formulaToken{kind: tokIdent, text: src[start:i], offset: start})
				continue
			}
			return nil, newFormulaError(FormulaErrSyntax, i, "",
				"the expression contains %q, which starts nothing the formula grammar admits", string(r))
		}

		switch {
		case c >= '0' && c <= '9':
			start := i
			var err *FormulaError
			i, err = scanNumber(src, i)
			if err != nil {
				return nil, err
			}
			out = append(out, formulaToken{kind: tokNumber, text: src[start:i], offset: start})

		case c == '"' || c == '\'':
			start := i
			value, next, err := scanText(src, i)
			if err != nil {
				return nil, err
			}
			i = next
			out = append(out, formulaToken{kind: tokText, text: value, offset: start})

		case isIdentStartByte(c):
			start := i
			i = scanIdent(src, i)
			out = append(out, formulaToken{kind: tokIdent, text: src[start:i], offset: start})

		case c == '(':
			out = append(out, formulaToken{kind: tokLParen, text: "(", offset: i})
			i++
		case c == ')':
			out = append(out, formulaToken{kind: tokRParen, text: ")", offset: i})
			i++
		case c == ',':
			out = append(out, formulaToken{kind: tokComma, text: ",", offset: i})
			i++
		case c == '.':
			out = append(out, formulaToken{kind: tokDot, text: ".", offset: i})
			i++

		default:
			op, next, err := scanOperator(src, i)
			if err != nil {
				return nil, err
			}
			i = next
			out = append(out, formulaToken{kind: tokOp, text: op, offset: i - len(op)})
		}
	}

	out = append(out, formulaToken{kind: tokEOF, offset: len(src)})
	return out, nil
}

func isIdentStartByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// scanIdent consumes a name. Names may contain non-ASCII letters because a
// vault's property keys do — `fällig`, `プロジェクト` — and refusing them here
// would refuse a legitimate property by an accident of the tokenizer.
func scanIdent(src string, i int) int {
	for i < len(src) {
		r, size := utf8.DecodeRuneInString(src[i:])
		if !isIdentRune(r) {
			return i
		}
		i += size
	}
	return i
}

// scanNumber consumes a numeric literal AS TEXT.
//
// It deliberately does no arithmetic and produces no number: the text goes to
// ParseDecimal later, which is exact. A tokenizer that returned a parsed value
// would be the one place in the whole path where a binary float could enter
// (FR-013/FR-020b), and it would be invisible, because the token would look
// right.
//
// It also refuses `1.2.3` here rather than leaving a second `.2` to be read as a
// field access on a number — a shape that would otherwise fail much later with a
// message about accessors.
func scanNumber(src string, i int) (int, *FormulaError) {
	start := i
	seenDot := false
	seenExp := false
	for i < len(src) {
		c := src[i]
		switch {
		case c >= '0' && c <= '9':
			i++
		case c == '.' && !seenDot && !seenExp && i+1 < len(src) && src[i+1] >= '0' && src[i+1] <= '9':
			seenDot = true
			i++
		case c == '.' && (seenDot || seenExp):
			return 0, newFormulaError(FormulaErrSyntax, i, "one decimal point",
				"the number starting at position %d has more than one decimal point", start)
		case (c == 'e' || c == 'E') && !seenExp && i+1 < len(src) && isExponentTail(src, i+1):
			seenExp = true
			i++
			if src[i] == '+' || src[i] == '-' {
				i++
			}
		default:
			return i, nil
		}
	}
	return i, nil
}

func isExponentTail(src string, i int) bool {
	if i < len(src) && (src[i] == '+' || src[i] == '-') {
		i++
	}
	return i < len(src) && src[i] >= '0' && src[i] <= '9'
}

// scanText consumes a quoted literal and returns its UNESCAPED value.
//
// The escape set is deliberately tiny — \\ \" \' \n \t — because a formula
// quotes note titles and enum values, not program text. An unknown escape is
// REFUSED naming the set rather than passed through: passing `\d` through as
// `d` is how a LIKE pattern's escape and a string's escape quietly diverge.
func scanText(src string, i int) (string, int, *FormulaError) {
	quote := src[i]
	start := i
	i++
	var b strings.Builder
	for i < len(src) {
		c := src[i]
		if c == quote {
			return b.String(), i + 1, nil
		}
		if c == '\\' {
			if i+1 >= len(src) {
				break
			}
			switch src[i+1] {
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			case '\'':
				b.WriteByte('\'')
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				return "", 0, newFormulaError(FormulaErrSyntax, i, `one of \\ \" \' \n \t`,
					"the text literal contains the escape %q, which the formula grammar does not define", src[i:i+2])
			}
			i += 2
			continue
		}
		b.WriteByte(c)
		i++
	}
	return "", 0, newFormulaError(FormulaErrSyntax, start, "a closing "+string(quote),
		"the text literal opened at position %d is never closed", start)
}

// formulaOperators is FR-143's operator surface, longest first so `&&` is never
// read as two `&` and `<=` is never read as `<` then `=`.
var formulaOperators = []string{"&&", "||", "==", "!=", "<=", ">=", "+", "-", "*", "/", "%", "!", "<", ">"}

// scanOperator consumes one operator, or refuses.
//
// THE ONE CASE WORTH THE COMMENT: a bare `=`. Obsidian's expression syntax
// spells equality `==`, and this package's FILTER vocabulary spells it `=`
// (ruling R-B, SQL's names). Somebody will write `=` in a formula because they
// just wrote it in a filter. The refusal therefore names `==` explicitly rather
// than saying "unknown operator", because the caller is one character away from
// correct and a generic message does not tell them which character.
func scanOperator(src string, i int) (string, int, *FormulaError) {
	for _, op := range formulaOperators {
		if strings.HasPrefix(src[i:], op) {
			return op, i + len(op), nil
		}
	}
	if src[i] == '=' {
		return "", 0, newFormulaError(FormulaErrSyntax, i, "==",
			"the formula grammar spells equality `==`; `=` is the FILTER vocabulary's spelling and is not an expression operator")
	}
	if src[i] == '&' || src[i] == '|' {
		return "", 0, newFormulaError(FormulaErrSyntax, i, "&& or ||",
			"the formula grammar has no single %q; the boolean operators are `&&` and `||`", string(src[i]))
	}
	return "", 0, newFormulaError(FormulaErrSyntax, i, formulaOperatorList(),
		"the expression contains %q, which is not an operator the formula grammar admits", string(src[i]))
}

func formulaOperatorList() string {
	// Source order, not the longest-first matching order — a message should
	// read the way the specification lists them.
	return "one of + - * / % ( ) ! && || == != < <= > >="
}
