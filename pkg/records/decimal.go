// Omnipus — ADR-068 D3/O-2: exact decimal arithmetic for `number` and `money`.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// FR-013: money arithmetic MUST be exact decimal. FR-020b: money MUST never be
// converted to a binary floating-point number anywhere in the path. DS-1 also
// requires the `number` type to hold 2^53+1 exactly, which rules float64 out
// for plain numbers too.
//
// So there is exactly one numeric representation in this package, and it is
// this one: an arbitrary-precision integer plus a decimal scale. `349.98` is
// unscaled=34998, scale=2 — not 349.97999999999996.
//
// Decimal is a VALUE type and its zero value is a usable zero. Every method
// tolerates a nil `unscaled` so a zero Decimal never panics (R-11's "total and
// never panics" applies to everything the comparator can reach).
// ---------------------------------------------------------------------------

// Decimal is an exact base-10 number: unscaled * 10^(-scale).
//
// It is immutable in use: every operation returns a new Decimal and no method
// mutates the receiver's big.Int.
type Decimal struct {
	unscaled *big.Int
	scale    int32
}

// maxDecimalScale bounds how far a value may be rescaled. Rescaling multiplies
// by 10^n, so an unbounded scale is an unbounded allocation driven by input
// text — a denial-of-service seam, not a numeric one. A vault holding money and
// measurements does not need more than this, and exceeding it is REPORTED
// rather than silently rounded (this package never rounds).
const maxDecimalScale = 100

// maxMoneyScale bounds a MONEY value's declared scale, and is deliberately
// tighter than maxDecimalScale. It matches RecordMoney.yaml's `maximum: 12`
// exactly: a money value that Go accepts but the wire cannot carry is a value
// an operator can write to disk and then never read back through the API.
// Twelve is far past any real currency — ISO-4217 tops out at four.
const maxMoneyScale = 12

// errScaleTooLarge is returned rather than rounding. Rounding money silently is
// the class of defect ADR-068 exists to remove.
var errScaleTooLarge = fmt.Errorf("decimal scale exceeds the maximum of %d", maxDecimalScale)

// NewDecimal builds a Decimal from unscaled units and a scale.
func NewDecimal(unscaled *big.Int, scale int32) Decimal {
	if unscaled == nil {
		unscaled = big.NewInt(0)
	}
	return Decimal{unscaled: new(big.Int).Set(unscaled), scale: scale}
}

// DecimalFromInt64 is a convenience for tests and small literals.
func DecimalFromInt64(v int64, scale int32) Decimal {
	return Decimal{unscaled: big.NewInt(v), scale: scale}
}

// Unscaled returns a copy of the unscaled integer. For a money value this is
// the amount in minor units (ADR-068 O-2).
func (d Decimal) Unscaled() *big.Int {
	if d.unscaled == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Set(d.unscaled)
}

// Scale returns the number of decimal places.
func (d Decimal) Scale() int32 { return d.scale }

func (d Decimal) int() *big.Int {
	if d.unscaled == nil {
		return big.NewInt(0)
	}
	return d.unscaled
}

// IsZero reports whether the value is exactly zero, at any scale.
func (d Decimal) IsZero() bool { return d.int().Sign() == 0 }

// Sign returns -1, 0 or +1.
func (d Decimal) Sign() int { return d.int().Sign() }

// String renders the value in plain decimal notation, preserving scale.
// 349.98 at scale 2 renders "349.98"; the same value at scale 4 renders
// "349.9800", because the scale is part of what was declared.
func (d Decimal) String() string {
	n := d.int()
	if d.scale <= 0 {
		if d.scale == 0 {
			return n.String()
		}
		// Negative scale means trailing implicit zeros (e.g. 1e3 parsed as
		// unscaled=1, scale=-3). Materialise them so the text is unambiguous.
		out := new(big.Int).Mul(new(big.Int).Abs(n), pow10(-int64(d.scale)))
		if n.Sign() < 0 {
			return "-" + out.String()
		}
		return out.String()
	}
	digits := new(big.Int).Abs(n).String()
	scale := int(d.scale)
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	cut := len(digits) - scale
	s := digits[:cut] + "." + digits[cut:]
	if n.Sign() < 0 {
		s = "-" + s
	}
	return s
}

// pow10 returns 10^n as a big.Int. n must be >= 0.
//
// The parameter is int64 rather than int32 deliberately: every caller derives n
// by NEGATING a scale, and negating math.MinInt32 in int32 arithmetic wraps
// back to a negative number. big.Int.Exp answers 1 for a negative exponent, so
// that wrap would not panic — it would render a value SILENTLY WRONG, which is
// the one failure mode this file exists to prevent. Widening to int64 makes the
// negation total.
func pow10(n int64) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(n), nil)
}

// rescale returns an equivalent Decimal at the target scale. It only ever
// scales UP (adding zeros), which is exact; it refuses to scale down because
// that would discard digits.
func (d Decimal) rescale(target int32) (Decimal, error) {
	if target == d.scale {
		return d, nil
	}
	if target < d.scale {
		return Decimal{}, fmt.Errorf("cannot represent a value with %d decimal places at scale %d without discarding digits", d.scale, target)
	}
	if target > maxDecimalScale {
		return Decimal{}, errScaleTooLarge
	}
	factor := pow10(int64(target) - int64(d.scale))
	return Decimal{unscaled: new(big.Int).Mul(d.int(), factor), scale: target}, nil
}

// align brings two Decimals to a common scale — the larger of the two, so the
// alignment is always exact.
func align(a, b Decimal) (Decimal, Decimal, error) {
	target := a.scale
	if b.scale > target {
		target = b.scale
	}
	ra, err := a.rescale(target)
	if err != nil {
		return Decimal{}, Decimal{}, err
	}
	rb, err := b.rescale(target)
	if err != nil {
		return Decimal{}, Decimal{}, err
	}
	return ra, rb, nil
}

// Add returns a + b, exactly. The result carries the larger of the two scales.
func (d Decimal) Add(o Decimal) (Decimal, error) {
	a, b, err := align(d, o)
	if err != nil {
		return Decimal{}, err
	}
	return Decimal{unscaled: new(big.Int).Add(a.int(), b.int()), scale: a.scale}, nil
}

// Sub returns a - b, exactly.
func (d Decimal) Sub(o Decimal) (Decimal, error) {
	a, b, err := align(d, o)
	if err != nil {
		return Decimal{}, err
	}
	return Decimal{unscaled: new(big.Int).Sub(a.int(), b.int()), scale: a.scale}, nil
}

// Cmp compares two Decimals by VALUE, not by representation: 349.98 and
// 349.9800 are equal. Returns -1, 0 or +1, always.
//
// Aligning to a common scale is the fast path. When align refuses — the scales
// are further apart than maxDecimalScale allows, which only a hand-built
// Decimal can reach — cmpUnaligned answers the SAME question exactly, without
// rescaling. Neither path approximates and neither path panics, which is what
// §8 R-11 means by comparison being total.
func (d Decimal) Cmp(o Decimal) int {
	a, b, err := align(d, o)
	if err == nil {
		return a.int().Cmp(b.int())
	}

	return cmpUnaligned(d, o)
}

// cmpUnaligned compares two Decimals EXACTLY without bringing them to a common
// scale. It is Cmp's fallback for the case align refuses (a scale beyond
// maxDecimalScale), and it is exact for EVERY input, not just the ones that
// motivated it.
//
// Two defects preceded it, and they are mirror images of each other, so this
// third attempt states the invariant rather than the cases:
//
//	FIRST  the fallback was `d.Sign() - o.Sign()`, excused by "both operands
//	       came from bounded parsing". NewDecimal is exported and takes any
//	       scale, so that was never true: 1e-200 and 9.99e-197 compared EQUAL.
//	SECOND the fallback compared MAGNITUDE and then, on a tie, the raw unscaled
//	       digits — ignoring that a tie is precisely the case where the scales
//	       still DIFFER. NewDecimal(15,200) and NewDecimal(150,201) are the same
//	       number, 1.5e-199, and it reported them unequal.
//
// The invariant that makes this one exact: write |x| as 0.<digits> x 10^E with
// E = len(digits) - scale. The digits carry no leading zero (a big.Int never
// renders one), so E orders the magnitudes outright. When two values share E
// they differ only in their FRACTION, and right-padding both digit strings with
// zeros to a common length makes lexicographic order identical to numeric
// order. Padding is bounded by the operands' own digit counts, never by the
// scale gap, so an adversarial scale cannot turn a comparison into a
// multi-gigabyte allocation the way naive rescaling would.
//
// Sign is applied last, once, to the magnitude verdict.
func cmpUnaligned(d, o Decimal) int {
	ds, os := d.int().Sign(), o.int().Sign()
	switch {
	case ds != os:
		if ds < os {
			return -1
		}
		return 1
	case ds == 0:
		// Both zero, at whatever scales: equal.
		return 0
	}

	dDigits := new(big.Int).Abs(d.int()).String()
	oDigits := new(big.Int).Abs(o.int()).String()

	// int64 throughout: len is an int and scale an int32, and on a 32-bit
	// platform int(len) - int(scale) can overflow. This cannot.
	dExp := int64(len(dDigits)) - int64(d.scale)
	oExp := int64(len(oDigits)) - int64(o.scale)

	var mag int
	switch {
	case dExp > oExp:
		mag = 1
	case dExp < oExp:
		mag = -1
	default:
		if pad := len(oDigits) - len(dDigits); pad > 0 {
			dDigits += strings.Repeat("0", pad)
		} else if pad < 0 {
			oDigits += strings.Repeat("0", -pad)
		}
		mag = strings.Compare(dDigits, oDigits)
	}

	if ds < 0 {
		return -mag
	}
	return mag
}

// Equal reports value equality (scale-insensitive).
func (d Decimal) Equal(o Decimal) bool { return d.Cmp(o) == 0 }

// errNotADecimal is the sentinel every "this text is not a number" rejection
// wraps, so callers can distinguish a malformed literal from a bound breach.
var errNotADecimal = errors.New("not a decimal number")

// ParseDecimal parses an exact decimal from its LEXICAL form — the characters
// as written in the file. It never routes through float64, which is the whole
// point (FR-020b).
//
// Accepted: optional sign, digits, an optional single '.', and an optional
// exponent (e/E with optional sign). YAML permits underscores in some numeric
// forms; they are NOT accepted here, because "1_000" meaning 1000 is a
// readability convention we would be guessing at.
//
// Rejected loudly: empty text, ".", "1.2.3", "NaN", "Inf", "0x1f", "1,000",
// and anything with trailing text such as "349.98 SGD" (money has its own
// parser; a bare number with a currency glued on is exactly the ambiguity
// FR-012 exists to stop).
func ParseDecimal(text string) (Decimal, error) {
	s := strings.TrimSpace(text)
	if s == "" {
		return Decimal{}, fmt.Errorf("%w: empty", errNotADecimal)
	}

	neg := false
	switch s[0] {
	case '+':
		s = s[1:]
	case '-':
		neg = true
		s = s[1:]
	}

	// Split off the exponent first.
	exp := int32(0)
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mantissa, expText := s[:i], s[i+1:]
		e, err := parseExponent(expText)
		if err != nil {
			return Decimal{}, err
		}
		exp = e
		s = mantissa
	}

	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
		if strings.ContainsRune(fracPart, '.') {
			return Decimal{}, fmt.Errorf("%w: more than one decimal point in %q", errNotADecimal, text)
		}
	}
	if intPart == "" && fracPart == "" {
		return Decimal{}, fmt.Errorf("%w: %q has no digits", errNotADecimal, text)
	}
	if !allASCIIDigits(intPart) || !allASCIIDigits(fracPart) {
		return Decimal{}, fmt.Errorf("%w: %q", errNotADecimal, text)
	}

	digits := intPart + fracPart
	scale := int32(len(fracPart)) - exp
	if scale > maxDecimalScale {
		return Decimal{}, errScaleTooLarge
	}

	unscaled, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return Decimal{}, fmt.Errorf("%w: %q", errNotADecimal, text)
	}
	if neg {
		unscaled.Neg(unscaled)
	}
	return Decimal{unscaled: unscaled, scale: scale}, nil
}

func parseExponent(text string) (int32, error) {
	if text == "" {
		return 0, fmt.Errorf("%w: exponent has no digits", errNotADecimal)
	}
	neg := false
	switch text[0] {
	case '+':
		text = text[1:]
	case '-':
		neg = true
		text = text[1:]
	}
	if !allASCIIDigits(text) || text == "" {
		return 0, fmt.Errorf("%w: bad exponent %q", errNotADecimal, text)
	}
	// A wildly large exponent is a bound breach, not a number.
	if len(text) > 4 {
		return 0, errScaleTooLarge
	}
	v := int32(0)
	for _, c := range text {
		v = v*10 + (c - '0')
	}
	if v > maxDecimalScale {
		return 0, errScaleTooLarge
	}
	if neg {
		v = -v
	}
	return v, nil
}

// allASCIIDigits reports whether every byte is 0-9. An empty string is true,
// because "no fractional part" is legitimate; callers check for "no digits at
// all" separately.
func allASCIIDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// isIntegerLiteral reports whether the text is a plain integer with no decimal
// point and no exponent. Money's minor-units form (FR-012 / ADR-068 O-2)
// requires this: `{amount: 34998, scale: 2}` means 349.98, and an amount
// written as `349.98` alongside an explicit scale is ambiguous, not helpful.
func isIntegerLiteral(text string) bool {
	s := strings.TrimSpace(text)
	if s == "" {
		return false
	}
	if s[0] == '+' || s[0] == '-' {
		s = s[1:]
	}
	return s != "" && allASCIIDigits(s)
}
