// Omnipus — tests for FR-012, FR-013, FR-014 and FR-020b (ADR-068 D3, O-2).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"
)

const moneyFixture = `
schema_version: 1
type: widget
properties:
  name: { type: text }
  arr:  { type: money }
`

func moneyProp(t *testing.T) (*SchemaSet, *Property) {
	t.Helper()
	set := loadSet(t, map[string]string{"widget.yaml": moneyFixture})
	widget, _ := set.Get("widget")
	p, _ := widget.Property("arr")
	return set, p
}

// mustMoney parses a money literal through the real value path.
func mustMoney(t *testing.T, p *Property, text string) Money {
	t.Helper()
	tv, err := ParseValue(p, Node{Kind: KindScalar, Text: text})
	if err != nil {
		t.Fatalf("parsing money %q: %v", text, err)
	}
	return tv.Money
}

// TestMoney_RefusesCrossCurrencySum covers FR-012, FR-013 and FR-014 — spec §7
// test 4, US-2 scenario 2.3.
func TestMoney_RefusesCrossCurrencySum(t *testing.T) {
	_, arr := moneyProp(t)

	t.Run("FR-014 a total across currencies is refused, listing the currencies", func(t *testing.T) {
		// US-2.3: "Given records in four currencies, When a total is requested,
		// Then no combined total is returned; the currencies present are
		// listed instead."
		values := []Money{
			mustMoney(t, arr, "349.98 SGD"),
			mustMoney(t, arr, "100.00 EUR"),
			mustMoney(t, arr, "12.50 GBP"),
			mustMoney(t, arr, "7.00 USD"),
		}
		total, ok, err := SumMoney(values)
		if ok {
			t.Fatalf("FR-014 / US-2.3: no combined total may be returned across currencies; got %s", total)
		}
		if err == nil {
			t.Fatalf("FR-014: the refusal must be reported, not silent")
		}
		var cross *CrossCurrencyError
		if !errors.As(err, &cross) {
			t.Fatalf("expected a *CrossCurrencyError so a caller can list the currencies; got %T: %v", err, err)
		}
		want := []string{"EUR", "GBP", "SGD", "USD"}
		if !reflect.DeepEqual(cross.Currencies, want) {
			t.Fatalf("FR-014 requires the currencies present to be listed; want %v, got %v", want, cross.Currencies)
		}
		for _, c := range want {
			if !strings.Contains(err.Error(), c) {
				t.Fatalf("the human-readable refusal must name %q; got %q", c, err.Error())
			}
		}
		if total.Currency != "" || !total.Amount.IsZero() {
			t.Fatalf("a refused total must not leak a partial figure; got %+v", total)
		}
	})

	t.Run("FR-014 even two currencies are refused", func(t *testing.T) {
		_, _, err := SumMoney([]Money{
			mustMoney(t, arr, "1.00 SGD"),
			mustMoney(t, arr, "1.00 EUR"),
		})
		var cross *CrossCurrencyError
		if !errors.As(err, &cross) {
			t.Fatalf("expected a cross-currency refusal, got %v", err)
		}
		if !reflect.DeepEqual(cross.Currencies, []string{"EUR", "SGD"}) {
			t.Fatalf("want [EUR SGD], got %v", cross.Currencies)
		}
	})

	t.Run("ADR-068 O-2 the refusal does not convert", func(t *testing.T) {
		// O-2 is explicit: no FX conversion, no rate table. A refusal that
		// quietly picked a base currency would be the defect, not the fix.
		_, ok, err := SumMoney([]Money{
			mustMoney(t, arr, "1.00 USD"),
			mustMoney(t, arr, "1.00 EUR"),
		})
		if ok || err == nil {
			t.Fatalf("O-2 forbids conversion; the only correct outcome is refusal")
		}
		if strings.Contains(strings.ToLower(err.Error()), "converted") {
			t.Fatalf("the message must not imply conversion happened: %q", err.Error())
		}
	})

	t.Run("FR-013 a single-currency sum is exact", func(t *testing.T) {
		// 0.1 + 0.2 is the canonical binary-floating-point failure: in float64
		// it is 0.30000000000000004. FR-013 says exact decimal, so it is 0.3.
		total, ok, err := SumMoney([]Money{
			mustMoney(t, arr, "0.10 EUR"),
			mustMoney(t, arr, "0.20 EUR"),
		})
		if !ok || err != nil {
			t.Fatalf("a single-currency sum must succeed: ok=%v err=%v", ok, err)
		}
		if got := total.String(); got != "0.30 EUR" {
			t.Fatalf("FR-013: exact decimal addition must give 0.30 EUR, got %s", got)
		}
	})

	t.Run("FR-013 a sum over many values stays exact", func(t *testing.T) {
		// 100 x 0.01 EUR. In float64 this accumulates visible error.
		values := make([]Money, 0, 100)
		for i := 0; i < 100; i++ {
			values = append(values, mustMoney(t, arr, "0.01 EUR"))
		}
		total, ok, err := SumMoney(values)
		if !ok || err != nil {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		if got := total.String(); got != "1.00 EUR" {
			t.Fatalf("FR-013: 100 x 0.01 EUR must be exactly 1.00 EUR, got %s", got)
		}
	})

	t.Run("FR-013 mixed scales in one currency add exactly", func(t *testing.T) {
		total, ok, err := SumMoney([]Money{
			mustMoney(t, arr, "1.5 EUR"),
			mustMoney(t, arr, "2.25 EUR"),
		})
		if !ok || err != nil {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		if got := total.String(); got != "3.75 EUR" {
			t.Fatalf("want 3.75 EUR, got %s", got)
		}
	})

	t.Run("the total of nothing is not zero of an assumed currency", func(t *testing.T) {
		total, ok, err := SumMoney(nil)
		if err != nil {
			t.Fatalf("an empty set is not an error: %v", err)
		}
		if ok {
			t.Fatalf("there is no currency to denominate the total of nothing in; got %s", total)
		}
	})

	t.Run("§8 R-6 money compares only within one currency", func(t *testing.T) {
		a := mustMoney(t, arr, "1.00 EUR")
		b := mustMoney(t, arr, "2.00 SGD")
		if _, ok := CompareMoney(a, b); ok {
			t.Fatalf("§8 R-6: across currencies every operator is false, so the values must not be comparable")
		}
		c := mustMoney(t, arr, "2.00 EUR")
		cmp, ok := CompareMoney(a, c)
		if !ok || cmp >= 0 {
			t.Fatalf("within one currency 1.00 < 2.00; got cmp=%d ok=%v", cmp, ok)
		}
	})
}

// TestMoney_MissingCurrencyIsRejected covers FR-012 and DS-1's "349.98, no
// currency" row.
func TestMoney_MissingCurrencyIsRejected(t *testing.T) {
	set, arr := moneyProp(t)

	t.Run("FR-012 a bare amount is rejected", func(t *testing.T) {
		_, verr := ParseValue(arr, Node{Kind: KindScalar, Text: "349.98"})
		if verr == nil {
			t.Fatalf("FR-012 / DS-1: a money value with no currency must be REJECTED")
		}
		if verr.Code != FindingMoneyNoCurrency {
			t.Fatalf("expected %q, got %q (%s)", FindingMoneyNoCurrency, verr.Code, verr.Reason)
		}
		if verr.Expected == "" {
			t.Fatalf("the rejection must name the expected shape")
		}
		if !strings.Contains(verr.Expected, "currency") {
			t.Fatalf("the expected shape must mention the currency; got %q", verr.Expected)
		}
	})

	t.Run("FR-012 a mapping with no currency is rejected", func(t *testing.T) {
		rec := ParseRecord("a.md", []byte("---\ntype: widget\narr: {amount: 349.98}\n---\n"))
		rep := ValidateRecord(set, rec, ValidateOptions{})
		if rep.Valid() {
			t.Fatalf("FR-012: amount without currency must be rejected")
		}
		if got := rep.Errors()[0].Code; got != FindingMoneyNoCurrency {
			t.Fatalf("expected %q, got %q", FindingMoneyNoCurrency, got)
		}
	})

	t.Run("FR-012 the whole record report names the record", func(t *testing.T) {
		rec := ParseRecord("deals/acme.md", []byte("---\ntype: widget\narr: 349.98\n---\n"))
		rep := ValidateRecord(set, rec, ValidateOptions{})
		if rep.Valid() {
			t.Fatalf("expected a fault")
		}
		if rep.Errors()[0].RecordPath != "deals/acme.md" {
			t.Fatalf("the finding must name the record; got %q", rep.Errors()[0].RecordPath)
		}
	})

	t.Run("FR-012 amount, currency and scale travel together", func(t *testing.T) {
		cases := []struct {
			text      string
			wantText  string
			wantCur   string
			wantMinor string
			wantScale int32
		}{
			{"349.98 SGD", "349.98 SGD", "SGD", "34998", 2},
			{"SGD 349.98", "349.98 SGD", "SGD", "34998", 2},
			{"1000 JPY", "1000 JPY", "JPY", "1000", 0},
			{"-12.345 EUR", "-12.345 EUR", "EUR", "-12345", 3},
		}
		for _, tc := range cases {
			m := mustMoney(t, arr, tc.text)
			if m.String() != tc.wantText {
				t.Fatalf("%q: want %q, got %q", tc.text, tc.wantText, m.String())
			}
			if m.Currency != tc.wantCur {
				t.Fatalf("%q: want currency %q, got %q", tc.text, tc.wantCur, m.Currency)
			}
			if m.MinorUnits() != tc.wantMinor {
				t.Fatalf("%q: ADR-068 O-2 stores integer minor units; want %s, got %s", tc.text, tc.wantMinor, m.MinorUnits())
			}
			if m.Scale() != tc.wantScale {
				t.Fatalf("%q: want scale %d, got %d", tc.text, tc.wantScale, m.Scale())
			}
		}
	})

	t.Run("FR-012 the explicit minor-units form is read as minor units", func(t *testing.T) {
		// ADR-068 O-2: amount is an INTEGER count of minor units at scale.
		rec := ParseRecord("a.md", []byte("---\ntype: widget\narr: {amount: 34998, currency: SGD, scale: 2}\n---\n"))
		pv := ResolveProperty(rec, arr)
		if pv.State != StatePresent || len(pv.Values) != 1 {
			t.Fatalf("expected one value, got state=%v findings=%v", pv.State, pv.Findings)
		}
		if got := pv.Values[0].Money.String(); got != "349.98 SGD" {
			t.Fatalf("O-2: {amount: 34998, scale: 2} is 349.98; got %s", got)
		}
	})

	t.Run("a decimal amount alongside an explicit scale is refused as ambiguous", func(t *testing.T) {
		// Reading 349.98 with scale 2 as either 349.98 or 3.4998 is a coin
		// toss. This package does not toss coins over money.
		rec := ParseRecord("a.md", []byte("---\ntype: widget\narr: {amount: 349.98, currency: SGD, scale: 2}\n---\n"))
		rep := ValidateRecord(set, rec, ValidateOptions{})
		if rep.Valid() {
			t.Fatalf("an ambiguous money value must be rejected, not guessed at")
		}
		if got := rep.Errors()[0].Code; got != FindingMoneyMalformed {
			t.Fatalf("expected %q, got %q", FindingMoneyMalformed, got)
		}
	})

	t.Run("FR-012 a non-ISO-4217 currency is rejected naming the code", func(t *testing.T) {
		_, verr := ParseValue(arr, Node{Kind: KindScalar, Text: "10.00 QQQ"})
		if verr == nil {
			t.Fatalf("FR-012 requires an ISO-4217 currency")
		}
		if verr.Code != FindingMoneyBadCurrency {
			t.Fatalf("expected %q, got %q", FindingMoneyBadCurrency, verr.Code)
		}
		if !strings.Contains(verr.Reason, "QQQ") {
			t.Fatalf("the rejection must name the offending code; got %q", verr.Reason)
		}
	})

	t.Run("a lowercase currency is rejected, not silently normalised", func(t *testing.T) {
		// ADR-068 D4's ethos applied to currency: silently accepting `sgd`
		// creates a second de-facto spelling of one currency.
		if _, verr := ParseValue(arr, Node{Kind: KindScalar, Text: "10.00 sgd"}); verr == nil {
			t.Fatalf("`sgd` must be rejected; ISO-4217 codes are uppercase")
		}
	})
}

// TestMoney_NoBinary64InThePath covers FR-020b at the value level: a money
// amount that binary floating point cannot represent must survive byte-exact.
func TestMoney_NoBinary64InThePath(t *testing.T) {
	_, arr := moneyProp(t)

	// Every one of these is unrepresentable in float64. If any float appeared
	// anywhere in the parse path, the round trip would drift.
	cases := []string{
		"0.10 EUR",
		"349.98 SGD",
		"1.005 USD",
		"70.07 GBP",
		"9007199254740993.00 USD", // 2^53 + 1, in the units place
		"0.000000000001 USD",      // scale 12, the maximum a money value may carry
	}
	for _, text := range cases {
		m := mustMoney(t, arr, text)
		if got := m.String(); got != text {
			t.Fatalf("FR-020b: %q must survive the value path byte-exact, got %q", text, got)
		}
	}

	t.Run("2^53+1 is held exactly", func(t *testing.T) {
		m := mustMoney(t, arr, "90071992547409931 USD")
		want := new(big.Int)
		want.SetString("90071992547409931", 10)
		if m.Amount.Unscaled().Cmp(want) != 0 {
			t.Fatalf("want %s, got %s", want, m.Amount.Unscaled())
		}
	})
}

// ---------------------------------------------------------------------------
// The money SCALE bound, on every path that can mint a scale
// ---------------------------------------------------------------------------

// moneyForms enumerates every syntax a money AMOUNT can arrive in. The bound
// tests below sweep all of them, because the defect these tests exist for was
// not "the bound is wrong" — it was "the bound was applied to one of the two
// paths, and the one it missed is the form a real vault actually contains".
// Enumerating the forms in one place is what makes a NEW form's omission
// visible: add a parse form without adding it here and the sweep still passes,
// so the list is the thing to keep honest.
var moneyForms = []struct {
	name string
	node func(amount string) Node
}{
	{"inline, amount first", func(a string) Node {
		return Node{Kind: KindScalar, Text: a + " USD"}
	}},
	{"inline, currency first", func(a string) Node {
		return Node{Kind: KindScalar, Text: "USD " + a}
	}},
	{"mapping, scale inferred", func(a string) Node {
		return Node{
			Kind:   KindMapping,
			Keys:   []string{"amount", "currency"},
			Fields: map[string]Node{"amount": {Kind: KindScalar, Text: a}, "currency": {Kind: KindScalar, Text: "USD"}},
		}
	}},
}

// amountWithScale builds an amount literal carrying exactly n fractional
// digits, without any exponent: 0 -> "1", 1 -> "1.1", 3 -> "1.001".
func amountWithScale(n int) string {
	if n <= 0 {
		return "1"
	}
	digits := make([]byte, n)
	for i := range digits {
		digits[i] = '0'
	}
	digits[n-1] = '1'
	return "1." + string(digits)
}

// TestMoney_ScaleIsBoundedOnEveryParsePath is FR-012 / ADR-068 O-2 against
// RecordMoney.yaml's `scale: maximum 12`.
//
// The reported defect: parseMoneyMapping bounded the scale but parseMoney did
// not, so "1.000000000000000000000000000001 SGD" — the INLINE form, the one a
// real vault contains — was accepted at scale 31 and could never be serialised
// back to the caller. A bound enforced on one path and not the other is not a
// bound, so this sweeps the bound across every form and every scale either side
// of it rather than asserting the two reported strings.
func TestMoney_ScaleIsBoundedOnEveryParsePath(t *testing.T) {
	_, arr := moneyProp(t)

	for _, form := range moneyForms {
		for scale := 0; scale <= 20; scale++ {
			amount := amountWithScale(scale)
			tv, verr := ParseValue(arr, form.node(amount))

			if scale <= maxMoneyScale {
				if verr != nil {
					t.Fatalf("%s: %q has scale %d, within the bound of %d, and must be accepted; rejected with %v",
						form.name, amount, scale, maxMoneyScale, verr)
				}
				if got := tv.Money.Scale(); got != int32(scale) {
					t.Fatalf("%s: %q must carry scale %d, got %d", form.name, amount, scale, got)
				}
				continue
			}

			if verr == nil {
				t.Fatalf("%s: %q has scale %d, beyond the wire maximum of %d — accepting it puts a value on disk that no caller can ever read back (it parsed as %s)",
					form.name, amount, scale, maxMoneyScale, tv.Money)
			}
			if verr.Code != FindingMoneyMalformed {
				t.Fatalf("%s: expected %q, got %q (%s)", form.name, FindingMoneyMalformed, verr.Code, verr.Reason)
			}
			if !strings.Contains(verr.Reason, "12") {
				t.Fatalf("%s: the rejection must name the bound the operator has to fit inside; got %q", form.name, verr.Reason)
			}
		}
	}

	t.Run("the two measured cases, by name", func(t *testing.T) {
		for _, text := range []string{
			"1.000000000000000000000000000001 SGD", // scale 31
			"1.1234567890123456 SGD",               // scale 16
		} {
			if tv, verr := ParseValue(arr, Node{Kind: KindScalar, Text: text}); verr == nil {
				t.Fatalf("%q must be rejected; it parsed as %s at scale %d", text, tv.Money, tv.Money.Scale())
			}
		}
	})

	t.Run("a plain number keeps the wider bound", func(t *testing.T) {
		// The tighter bound is money's, and it comes from RecordMoney.yaml. A
		// `number` property has no such wire constraint, so tightening it here
		// too would be this package inventing a rule.
		set := loadSet(t, map[string]string{"widget.yaml": "schema_version: 1\ntype: widget\nproperties:\n  count: { type: number }\n"})
		widget, _ := set.Get("widget")
		count, _ := widget.Property("count")
		if _, verr := ParseValue(count, Node{Kind: KindScalar, Text: amountWithScale(31)}); verr != nil {
			t.Fatalf("a number at scale 31 is within maxDecimalScale and must still be accepted; got %v", verr)
		}
	})
}

// TestMoney_ExponentNotationIsRefused covers the second half of the same
// defect: "1e3 USD" parsed to unscaled=1 at scale -3, so Scale() was NEGATIVE
// and MinorUnits() said "1" for one thousand dollars. RecordMoney.scale has
// `minimum: 0` and RecordMoney.amount's pattern forbids an exponent outright,
// so the value was accepted on disk and unrepresentable on the wire — and a
// wire encoding built from Unscaled()+Scale() would have been off by 1000x.
//
// It is REFUSED rather than normalised to 1000-at-scale-0 because this package
// does not reshape money (value.go: "NOTHING IS COERCED"); the operator's fix
// is to write the number out, which the rejection says.
func TestMoney_ExponentNotationIsRefused(t *testing.T) {
	_, arr := moneyProp(t)

	exponents := []string{"1e3", "1E3", "1e-2", "1.5e2", "1e0", "-1e3", "12345e-2", "1e12"}

	for _, form := range moneyForms {
		for _, amount := range exponents {
			tv, verr := ParseValue(arr, form.node(amount))
			if verr == nil {
				t.Fatalf("%s: %q must be refused in a money amount; it parsed as %s at scale %d (MinorUnits %q)",
					form.name, amount, tv.Money, tv.Money.Scale(), tv.Money.MinorUnits())
			}
			if verr.Code != FindingMoneyMalformed {
				t.Fatalf("%s: %q — expected %q, got %q (%s)", form.name, amount, FindingMoneyMalformed, verr.Code, verr.Reason)
			}
			if !strings.Contains(verr.Reason, "exponent") {
				t.Fatalf("%s: %q — the rejection must say WHAT is wrong so the operator can fix it; got %q", form.name, amount, verr.Reason)
			}
		}
	}

	t.Run("the measured case, by name", func(t *testing.T) {
		if tv, verr := ParseValue(arr, Node{Kind: KindScalar, Text: "1e3 USD"}); verr == nil {
			t.Fatalf("\"1e3 USD\" must be refused; it parsed with Scale()=%d and MinorUnits()=%q",
				tv.Money.Scale(), tv.Money.MinorUnits())
		}
	})

	t.Run("a plain number still accepts exponent notation", func(t *testing.T) {
		set := loadSet(t, map[string]string{"widget.yaml": "schema_version: 1\ntype: widget\nproperties:\n  count: { type: number }\n"})
		widget, _ := set.Get("widget")
		count, _ := widget.Property("count")
		tv, verr := ParseValue(count, Node{Kind: KindScalar, Text: "1e3"})
		if verr != nil {
			t.Fatalf("`number` has no wire constraint against exponents; got %v", verr)
		}
		if got := tv.Number.String(); got != "1000" {
			t.Fatalf("1e3 is 1000; got %s", got)
		}
	})
}

// TestMoney_MinorUnitsAlwaysDescribesTheValue is the invariant the two defects
// above both broke, asserted directly and over the whole accepted input space
// rather than over a list of examples.
//
// The wire carries a money value as the PAIR (amount = minor units, scale), and
// that pair is only meaningful if
//
//	MinorUnits x 10^-Scale == the value, exactly, with 0 <= Scale <= 12.
//
// The oracle is exact rational arithmetic reconstructed from the wire pair, and
// it is compared against the parsed value. Nothing here reads the parser's own
// internals, so the two cannot agree by construction.
func TestMoney_MinorUnitsAlwaysDescribesTheValue(t *testing.T) {
	_, arr := moneyProp(t)

	var amounts []string
	for scale := 0; scale <= maxMoneyScale; scale++ {
		amounts = append(amounts, amountWithScale(scale), "-"+amountWithScale(scale))
	}
	amounts = append(amounts,
		"0", "0.00", "-0.00", "349.98", "-349.98", "70.07", "1.005",
		"9007199254740993", "9007199254740993.00",
		"1234567890123456789012345678901234567890.12",
		"000123.4500",
	)

	checked := 0
	for _, form := range moneyForms {
		for _, amount := range amounts {
			tv, verr := ParseValue(arr, form.node(amount))
			if verr != nil {
				t.Fatalf("%s: %q should parse; got %v", form.name, amount, verr)
			}
			m := tv.Money
			checked++

			if m.Scale() < 0 || m.Scale() > maxMoneyScale {
				t.Fatalf("%s: %q parsed with Scale()=%d; the wire requires 0..%d", form.name, amount, m.Scale(), maxMoneyScale)
			}

			minor, ok := new(big.Int).SetString(m.MinorUnits(), 10)
			if !ok {
				t.Fatalf("%s: %q gave MinorUnits()=%q, which is not an integer", form.name, amount, m.MinorUnits())
			}

			// Rebuild the value from the wire pair alone and compare exactly.
			fromWire := new(big.Rat).SetInt(minor)
			fromWire.Quo(fromWire, new(big.Rat).SetInt(pow10(int64(m.Scale()))))
			if want := ratOf(m.Amount); fromWire.Cmp(want) != 0 {
				t.Fatalf("%s: %q — the wire pair {amount: %s, scale: %d} means %s, but the parsed value is %s. A caller encoding this value would be wrong and nothing would say so",
					form.name, amount, m.MinorUnits(), m.Scale(), fromWire.RatString(), want.RatString())
			}

			// And the round trip through the pair must reproduce the Decimal.
			if !NewDecimal(minor, m.Scale()).Equal(m.Amount) {
				t.Fatalf("%s: %q — {amount: %s, scale: %d} does not round-trip back to %s",
					form.name, amount, m.MinorUnits(), m.Scale(), m.Amount)
			}
		}
	}

	// The explicit-scale mapping form takes its scale directly rather than
	// inferring it, so it is checked separately — same invariant.
	for scale := 0; scale <= maxMoneyScale; scale++ {
		node := Node{
			Kind: KindMapping,
			Keys: []string{"amount", "currency", "scale"},
			Fields: map[string]Node{
				"amount":   {Kind: KindScalar, Text: "34998"},
				"currency": {Kind: KindScalar, Text: "USD"},
				"scale":    {Kind: KindScalar, Text: itoaTest(scale)},
			},
		}
		tv, verr := ParseValue(arr, node)
		if verr != nil {
			t.Fatalf("explicit scale %d should parse; got %v", scale, verr)
		}
		checked++
		if tv.Money.MinorUnits() != "34998" {
			t.Fatalf("explicit scale %d: minor units must survive verbatim (O-2); got %q", scale, tv.Money.MinorUnits())
		}
		if tv.Money.Scale() != int32(scale) {
			t.Fatalf("explicit scale %d: got Scale()=%d", scale, tv.Money.Scale())
		}
	}

	if checked < 100 {
		t.Fatalf("the sweep checked only %d values; that is not coverage of an input space", checked)
	}
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// FR-014's currency list
// ---------------------------------------------------------------------------

// TestMoney_CurrenciesPresent covers the half of FR-014 that had no test at
// all: "a cross-currency sum is REFUSED with the currencies present listed".
// The refusal was tested; the list was not, and CurrenciesPresent — which the
// wire's RecordAggregateResult.currencies_present is filled from — had zero
// callers and zero assertions.
//
// It now has both: SumMoney builds its refusal list through it, so these
// assertions cover the exported helper AND the refusal at once.
func TestMoney_CurrenciesPresent(t *testing.T) {
	_, arr := moneyProp(t)
	m := func(text string) Money { return mustMoney(t, arr, text) }

	cases := []struct {
		name   string
		values []Money
		want   []string
	}{
		{"empty input gives an empty list, not nil", nil, []string{}},
		{"one currency", []Money{m("1.00 USD"), m("2.00 USD")}, []string{"USD"}},
		{"duplicates collapse", []Money{m("1.00 EUR"), m("2.00 EUR"), m("3.00 EUR")}, []string{"EUR"}},
		{"sorted, not insertion order", []Money{m("1.00 USD"), m("2.00 EUR"), m("3.00 SGD")}, []string{"EUR", "SGD", "USD"}},
		{"three currencies, several of each", []Money{m("1.00 SGD"), m("2.00 USD"), m("3.00 SGD"), m("4.00 EUR"), m("5.00 USD")}, []string{"EUR", "SGD", "USD"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CurrenciesPresent(c.values)
			if got == nil {
				t.Fatalf("the result must be usable without a nil check — a caller ranges over it and marshals it")
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("CurrenciesPresent = %v, want %v", got, c.want)
			}
		})
	}

	t.Run("FR-014: the refusal lists exactly what CurrenciesPresent reports", func(t *testing.T) {
		values := []Money{m("1.00 USD"), m("2.00 EUR"), m("3.00 SGD"), m("4.00 EUR")}
		_, ok, err := SumMoney(values)
		if ok || err == nil {
			t.Fatalf("FR-014: a sum across currencies must be REFUSED; got ok=%v err=%v", ok, err)
		}
		var cross *CrossCurrencyError
		if !errors.As(err, &cross) {
			t.Fatalf("the refusal must be a CrossCurrencyError so a caller can read the list; got %T", err)
		}
		if !reflect.DeepEqual(cross.Currencies, CurrenciesPresent(values)) {
			t.Fatalf("the refusal listed %v but CurrenciesPresent reports %v — two answers to one question",
				cross.Currencies, CurrenciesPresent(values))
		}
		if !reflect.DeepEqual(cross.Currencies, []string{"EUR", "SGD", "USD"}) {
			t.Fatalf("the refusal must name every currency present, sorted; got %v", cross.Currencies)
		}
		for _, code := range cross.Currencies {
			if !strings.Contains(cross.Error(), code) {
				t.Fatalf("the message must name %q — a refusal without the list leaves the caller with no next move; got %q", code, cross.Error())
			}
		}
	})
}

// TestMoney_DeclaredScaleFormIsBoundedToo closes the one road that does NOT go
// through parseMoneyAmount. value.go claims {amount, currency, scale} cannot
// breach either bound because its amount must be an integer literal and its
// scale is range-checked; a claim in a comment is not an enforcement, so this
// asserts it.
func TestMoney_DeclaredScaleFormIsBoundedToo(t *testing.T) {
	_, arr := moneyProp(t)

	declared := func(amount, scale string) Node {
		return Node{
			Kind: KindMapping,
			Keys: []string{"amount", "currency", "scale"},
			Fields: map[string]Node{
				"amount":   {Kind: KindScalar, Text: amount},
				"currency": {Kind: KindScalar, Text: "USD"},
				"scale":    {Kind: KindScalar, Text: scale},
			},
		}
	}

	rejected := []struct {
		name          string
		amount, scale string
	}{
		{"exponent in the amount", "1e3", "0"},
		{"exponent with a declared scale", "1e3", "2"},
		{"a fractional amount alongside a declared scale is ambiguous (O-2)", "349.98", "2"},
		{"scale one past the wire maximum", "34998", "13"},
		{"scale far past the wire maximum", "34998", "100"},
		{"a negative scale, which the wire cannot express", "34998", "-1"},
		{"an exponent in the scale itself", "34998", "1e1"},
		{"a fractional scale", "34998", "2.5"},
	}
	for _, c := range rejected {
		t.Run(c.name, func(t *testing.T) {
			tv, verr := ParseValue(arr, declared(c.amount, c.scale))
			if verr == nil {
				t.Fatalf("{amount: %s, currency: USD, scale: %s} must be rejected; it parsed as %s at scale %d (MinorUnits %q)",
					c.amount, c.scale, tv.Money, tv.Money.Scale(), tv.Money.MinorUnits())
			}
			if verr.Code != FindingMoneyMalformed {
				t.Fatalf("expected %q, got %q (%s)", FindingMoneyMalformed, verr.Code, verr.Reason)
			}
		})
	}

	t.Run("the boundary itself is accepted", func(t *testing.T) {
		tv, verr := ParseValue(arr, declared("34998", "12"))
		if verr != nil {
			t.Fatalf("scale 12 is the wire maximum and must be accepted; got %v", verr)
		}
		if tv.Money.Scale() != 12 || tv.Money.MinorUnits() != "34998" {
			t.Fatalf("got scale %d minor units %q", tv.Money.Scale(), tv.Money.MinorUnits())
		}
	})
}
