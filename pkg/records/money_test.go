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

// TestMoney_NoFloat64InThePath covers FR-020b at the value level: a money
// amount that binary floating point cannot represent must survive byte-exact.
func TestMoney_NoFloat64InThePath(t *testing.T) {
	_, arr := moneyProp(t)

	// Every one of these is unrepresentable in float64. If any float appeared
	// anywhere in the parse path, the round trip would drift.
	cases := []string{
		"0.10 EUR",
		"349.98 SGD",
		"1.005 USD",
		"70.07 GBP",
		"9007199254740993.00 USD", // 2^53 + 1, in the units place
		"0.000000000000000001 USD",
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
