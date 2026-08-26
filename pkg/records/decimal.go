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
//
// It is also the half-width of the scale range ParseDecimal can produce, which
// is what maxRescaleGap is derived from — see there.
const maxDecimalScale = 100

// maxRescaleGap bounds the DISTANCE a rescale may travel, which is the quantity
// that actually sizes the 10^n factor. maxDecimalScale bounds the DESTINATION,
// and the two are independent: a target of 0 is legal at ANY distance, so a
// far-negative scale aligning against scale 0 passed the destination check and
// then built the factor in full. Measured, for a single Cmp against 1:
//
//	scale -1000        3.5 KB
//	scale -100000      329 KB
//	scale -1000000     2.7 MB      (76 ms)
//	scale -10000000    33 MB       (3.1 SECONDS)
//
// and NewDecimal(1, math.MinInt32) would ask for 10^2147483648, about 890 MB.
//
// WHY IT IS TWICE maxDecimalScale, AND NOT maxDecimalScale ITSELF. The gap is a
// difference between two scales, so its bound has to admit the widest pair of
// IN-DOMAIN scales, not a single one. ParseDecimal yields scale in
// [-maxDecimalScale, +maxDecimalScale] — the upper end checked directly, the
// lower end because parseExponent caps the exponent at maxDecimalScale before
// negating it — so `1e100` (scale -100) and `1e-100` (scale +100) are both
// ordinary parsed values whose gap is 200. Bounding the gap at maxDecimalScale
// would have made SumMoney REFUSE that pair: a legitimate total, denied by a
// guard aimed at hand-built values. Twice the scale bound is therefore the
// tightest bound that refuses nothing a vault file can express, and it still
// caps the factor at 10^200 — an 84-byte integer.
//
// Bounding the gap costs no correctness for comparison, because Cmp's fallback
// (cmpUnaligned) is exact for exactly these pairs. Add and Sub have no such
// fallback and so REPORT errScaleTooLarge rather than doing the work.
const maxRescaleGap = 2 * maxDecimalScale

// maxRenderableScale bounds how far String will let the SCALE drive the length
// of its output. Past it the value is abbreviated rather than written out.
//
// THE SHAPE, WHICH IS THE SAME ONE maxRescaleGap FIXED IN align/rescale. Both
// of String's branches turn the scale into that many literal characters:
//
//	scale < 0   pow10(-scale) and multiply — 10^n, the same unbounded factor
//	            rescale used to build. NewDecimal(1, math.MinInt32) asks for
//	            10^2147483648, about 890 MB, and then renders it as a
//	            2,147,483,649-character string on top.
//	scale > 0   strings.Repeat("0", scale-len(digits)+1) — a DIFFERENT
//	            mechanism with the identical property. NewDecimal(1,
//	            math.MaxInt32) allocates 2 GB of '0' bytes and no pow10 is
//	            involved at all.
//
// The second one is why this bound is stated over the scale rather than bolted
// onto pow10: a guard on pow10 would have fixed the negative half and left the
// positive half — which is exactly the case-shaped fix that left this defect
// behind in the first place. The rule is about the WORK, not the mechanism:
// String may do work proportional to the value's OWN digit count, because those
// digits are the value's information and rendering them is the job; it may not
// do work proportional to a caller-supplied scale, because those characters
// carry no information the scale itself did not already carry.
//
// WHY ±maxDecimalScale IS THE RIGHT PLACE FOR IT, AND WHY NOTHING REAL MOVES.
// It is not a new number: it is the package's existing value domain, restated.
// ParseDecimal yields scale in [-maxDecimalScale, +maxDecimalScale] (the upper
// end checked directly, the lower end because parseExponent caps the exponent
// before negating it); money is tighter still at [0, maxMoneyScale]; and
// rescale — the only other producer, and so the only way Add and Sub can move a
// scale — refuses any target above maxDecimalScale and never lowers one. So
// EVERY Decimal a vault file can produce, and every Decimal this package
// derives from one, renders through the plain path byte for byte as before.
// Only a hand-built NewDecimal/DecimalFromInt64 reaches the abbreviation, which
// is precisely the population that reaches cmpUnaligned for the same reason.
//
// WHY ABBREVIATE RATHER THAN BOUND THE VALUE DOMAIN. Bounding Decimal's scale
// at construction was considered and rejected, and the reasoning is the same
// one that put maxRescaleGap on the work instead of on the value: NewDecimal
// returns no error, so a bound there could only clamp (silently wrong — the one
// failure mode this file exists to prevent), panic (R-11 requires comparison to
// be total), or break the API. It would also contradict cmpUnaligned, whose
// entire premise is that an out-of-bound scale is a LEGAL value that compares
// exactly. A renderer's inability to print something is not a reason to make it
// unrepresentable; the renderer says so instead.
const maxRenderableScale = maxDecimalScale

// renderScaleOutOfRange is String's disposition for a scale it will not write
// out: NAME the value instead of printing it.
//
// It loses nothing. (unscaled, scale) IS the value by definition — it is the
// same pair Unscaled() and Scale() return, and the same pair RecordMoney puts
// on the wire — so this abbreviates the RENDERING without abbreviating the
// number. A truncated digit string ("1000000…") could not say that: 10^1000 and
// 10^10000000 truncate to the same text.
//
// WHY IT IS NOT EXPONENT NOTATION ("1e-2147483648"), WHICH WAS THE OBVIOUS
// ALTERNATIVE. That form is exact too, and shorter, and it is still wrong here,
// because it would emit something that LOOKS like a literal this package
// accepts and is not:
//
//   - ParseDecimal REFUSES it. parseExponent caps the exponent at
//     maxDecimalScale and at four digits, so "1e-2147483648" comes back as
//     errScaleTooLarge. Today every in-domain String() output re-parses to an
//     equal value; emitting exponent notation would break that for exactly the
//     values a reader is most likely to want to copy.
//   - MONEY refuses exponent notation BY RULE (parseMoneyAmount rule 1), and
//     the error it raises when it does quotes d.String() as the plain form the
//     operator should type instead: "write it out in full, e.g. %s". A String
//     that answers in exponent notation makes that message advise the very
//     notation it is rejecting.
//
// So the marker is deliberately not number-shaped at all — angle brackets and
// prose, no bare digit run a parser or a reader could lift out and mistake for
// the value. A caller who feeds this to a number parser gets a loud rejection,
// which is the correct outcome; the danger this avoids is the quiet one.
func renderScaleOutOfRange(n *big.Int, scale int32) string {
	return fmt.Sprintf("<unscaled %s at scale %d: beyond the maximum of %d decimal places, not written out in full>",
		n.String(), scale, maxRenderableScale)
}

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
//
// Beyond ±maxDecimalScale it renders the ABBREVIATED form instead — see
// renderScaleOutOfRange, and see maxRenderableScale for why the bound is where
// it is and why abbreviating is the right disposition rather than a compromise.
func (d Decimal) String() string {
	n := d.int()

	// The bound is checked BEFORE either branch below, because both branches
	// materialise scale-many characters and both were unbounded. See
	// maxRenderableScale.
	if d.scale > maxRenderableScale || d.scale < -maxRenderableScale {
		return renderScaleOutOfRange(n, d.scale)
	}

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
//
// n is UNBOUNDED here on purpose — 10^n is an unbounded allocation and this
// function is not the place to decide how much is too much. Every caller bounds
// its own n before calling, and each does so against the quantity that is
// actually meaningful to it: rescale against maxRescaleGap (a distance between
// two scales), String against maxRenderableScale (a single scale). Collapsing
// those into one guard inside pow10 would have to pick one of the two bounds
// and would be wrong for the other caller.
func pow10(n int64) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(n), nil)
}

// rescale returns an equivalent Decimal at the target scale. It only ever
// scales UP (adding zeros), which is exact; it refuses to scale down because
// that would discard digits.
//
// It refuses on either bound — maxDecimalScale on the target scale, and
// maxRescaleGap on the distance travelled. The gap check lives here rather than
// in align because this is the function that builds the 10^n factor: bounding
// the work where the work happens covers every caller, present and future. For
// align's two calls the two placements are equivalent, since align refuses when
// either operand's rescale does and the larger gap is always
// target - min(a.scale, b.scale).
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
	// int64: target and d.scale are both int32, and target - d.scale in int32
	// arithmetic overflows for a far-negative scale — the very input this bound
	// exists to refuse. Widening makes the subtraction total.
	gap := int64(target) - int64(d.scale)
	if gap > maxRescaleGap {
		// Wrapped, not bare: errScaleTooLarge's own text names maxDecimalScale,
		// which is the wrong number for THIS refusal and would send a reader
		// looking for a scale of 101 when the scales might be 0 and -10000000.
		// The sentinel is preserved for errors.Is; only the detail is added.
		return Decimal{}, fmt.Errorf("%w: the two scales are %d apart, more than the maximum gap of %d", errScaleTooLarge, gap, maxRescaleGap)
	}
	factor := pow10(gap)
	return Decimal{unscaled: new(big.Int).Mul(d.int(), factor), scale: target}, nil
}

// align brings two Decimals to a common scale — the larger of the two, so the
// alignment is always exact. It refuses, rather than doing unbounded work, when
// either operand would have to travel further than maxRescaleGap to get there —
// see rescale.
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
//
// Unlike Cmp there is no unaligned fallback — a sum genuinely needs the common
// scale — so a pair whose scales are further apart than maxRescaleGap is
// REPORTED as errScaleTooLarge rather than materialised. Refusing is the whole
// point: the alternative is not a slow answer, it is an arbitrarily large one.
// No pair of PARSED values can reach that bound (see maxRescaleGap), so this
// refusal is unreachable from vault data and only a hand-built Decimal sees it.
func (d Decimal) Add(o Decimal) (Decimal, error) {
	a, b, err := align(d, o)
	if err != nil {
		return Decimal{}, err
	}
	return Decimal{unscaled: new(big.Int).Add(a.int(), b.int()), scale: a.scale}, nil
}

// Sub returns a - b, exactly. Like Add, it REPORTS errScaleTooLarge on a scale
// gap beyond maxRescaleGap rather than doing unbounded work.
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
// Aligning to a common scale is the fast path. When align refuses — either
// operand's scale is beyond maxDecimalScale, or the DISTANCE between them is
// beyond maxRescaleGap, neither of which a parsed value can reach —
// cmpUnaligned answers the SAME question exactly, without rescaling.
//
// Neither path approximates, neither path panics, and neither path does work
// unbounded by its operands' own digit counts. That third clause used to be
// false and its absence was the defect: align bounded only the TARGET scale, so
// Cmp against zero at scale -10_000_000 built a 33 MB integer and took 3.1
// seconds before ever reaching the fallback that would have answered it in
// microseconds. "Never returns" is not one of the outcomes §8 R-11 permits when
// it calls comparison total, so the bound on the gap is part of R-11, not an
// optimisation layered over it.
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
// scale gap.
//
// That bound is why an adversarial scale cannot turn a comparison into a
// multi-gigabyte allocation — but only because Cmp now REACHES this function
// for such a pair. The claim was previously made here and was false in
// practice: align bounded the target scale and not the gap, so it accepted the
// adversarial pair and did the multi-gigabyte rescale itself, and this
// guarded path ran only after the unguarded one had already paid. A fallback
// is worth nothing until the fast path declines the cases it exists to catch.
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
