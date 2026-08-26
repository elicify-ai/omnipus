// Omnipus — a money value that is wrong must be refused, naming what is wrong.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// One theme, four faults
//
// Everything in this file is the same requirement said four ways: a money value
// that is WRONG or MISTYPED must be refused, and the refusal must name the
// token the operator actually has to change. The failures it locks down were
// all silent or misdirected — the worst two kinds of wrong answer, because
// neither leaves the reader anything to act on.
// ---------------------------------------------------------------------------

// TestMoney_AnUnknownKeyIsRefusedNamingTheKey is the 100x defect.
//
// `{amount: 34998, currency: SGD, scal: 2}` parsed as 34998 SGD. The author
// wrote 349.98 and dropped one letter from `scale`; the parser read the three
// keys it recognised, ignored the fourth, and reported nothing. RecordMoney.yaml
// is `additionalProperties: false`, so the wire refuses this mapping — the Go
// parser accepted it, which is a gap between what a vault may hold and what a
// caller may ever read back.
func TestMoney_AnUnknownKeyIsRefusedNamingTheKey(t *testing.T) {
	set, arr := moneyProp(t)

	t.Run("the measured case: `scal` for `scale`, end to end through a real note", func(t *testing.T) {
		rec := ParseRecord("deals/acme.md", []byte("---\ntype: widget\narr: {amount: 34998, currency: SGD, scal: 2}\n---\n"))
		rep := ValidateRecord(set, rec, ValidateOptions{})
		if rep.Valid() {
			read := "nothing"
			if pv := ResolveProperty(rec, arr); len(pv.Values) > 0 {
				read = pv.Values[0].Money.String()
			}
			t.Fatalf("a one-letter typo in `scale` must be REFUSED, not read as %s — the note says 349.98 and the parser answered a hundred times that, in silence", read)
		}
		f := rep.Errors()[0]
		if f.Code != FindingMoneyMalformed {
			t.Fatalf("expected %q, got %q (%s)", FindingMoneyMalformed, f.Code, f.Reason)
		}
		if !strings.Contains(f.Reason, "scal") {
			t.Fatalf("the refusal must name the offending key so the fix is one keystroke; got %q", f.Reason)
		}
		if !strings.Contains(f.Reason, "scale") {
			t.Fatalf("the refusal must name the key set that WAS expected, or the operator is left guessing which spelling is right; got %q", f.Reason)
		}
		if f.RecordPath != "deals/acme.md" {
			t.Fatalf("the finding must name the record; got %q", f.RecordPath)
		}
	})

	t.Run("an unknown key is refused even when the rest of the value is perfect", func(t *testing.T) {
		// This is the shape that made the defect invisible: nothing else is
		// wrong, so nothing else raises a finding.
		node := Node{
			Kind: KindMapping,
			Keys: []string{"amount", "currency", "note"},
			Fields: map[string]Node{
				"amount":   {Kind: KindScalar, Text: "349.98"},
				"currency": {Kind: KindScalar, Text: "SGD"},
				"note":     {Kind: KindScalar, Text: "from the signed quote"},
			},
		}
		tv, verr := ParseValue(arr, node)
		if verr == nil {
			t.Fatalf("`note` is not a money key and its meaning is being thrown away; that must be reported, not swallowed (parsed as %s)", tv.Money)
		}
		if !strings.Contains(verr.Reason, "note") {
			t.Fatalf("the refusal must name `note`; got %q", verr.Reason)
		}
	})

	t.Run("several unknown keys are all named, in the order they were written", func(t *testing.T) {
		node := Node{
			Kind: KindMapping,
			Keys: []string{"amount", "zeta", "currency", "alpha"},
			Fields: map[string]Node{
				"amount":   {Kind: KindScalar, Text: "1"},
				"currency": {Kind: KindScalar, Text: "USD"},
				"zeta":     {Kind: KindScalar, Text: "x"},
				"alpha":    {Kind: KindScalar, Text: "y"},
			},
		}
		_, verr := ParseValue(arr, node)
		if verr == nil {
			t.Fatalf("two unknown keys must be refused")
		}
		zeta, alpha := strings.Index(verr.Reason, "zeta"), strings.Index(verr.Reason, "alpha")
		if zeta < 0 || alpha < 0 {
			t.Fatalf("every unknown key must be named, not just the first; got %q", verr.Reason)
		}
		if zeta > alpha {
			t.Fatalf("the keys must be listed in the order the author wrote them (`zeta` before `alpha`), so the report reads against the file; got %q", verr.Reason)
		}
	})

	t.Run("a key reaching Fields without Keys does not escape the check", func(t *testing.T) {
		// Keys is document order and Fields is what the parser reads from.
		// Whichever route a key arrives by, it is a key the author wrote.
		node := Node{
			Kind: KindMapping,
			Keys: []string{"amount", "currency"},
			Fields: map[string]Node{
				"amount":   {Kind: KindScalar, Text: "1"},
				"currency": {Kind: KindScalar, Text: "USD"},
				"scal":     {Kind: KindScalar, Text: "2"},
			},
		}
		if _, verr := ParseValue(arr, node); verr == nil {
			t.Fatalf("an unknown key present in Fields must be refused whichever route it arrived by")
		}
	})

	t.Run("the closed set itself is still accepted", func(t *testing.T) {
		// A guard that refuses real vault data is worse than the defect it
		// closes, so the three legitimate keys are asserted here too.
		node := Node{
			Kind: KindMapping,
			Keys: []string{"amount", "currency", "scale"},
			Fields: map[string]Node{
				"amount":   {Kind: KindScalar, Text: "34998"},
				"currency": {Kind: KindScalar, Text: "SGD"},
				"scale":    {Kind: KindScalar, Text: "2"},
			},
		}
		tv, verr := ParseValue(arr, node)
		if verr != nil {
			t.Fatalf("{amount, currency, scale} is the documented minor-units form and must parse; got %v", verr)
		}
		if got := tv.Money.String(); got != "349.98 SGD" {
			t.Fatalf("want 349.98 SGD, got %s", got)
		}
	})
}

// TestMoney_AMalformedAmountNamesTheMalformedToken is the misdirected report.
//
// The inline parser tries the currency-first order ("SGD 349.98") whenever the
// first field is not a decimal. It used to make that swap on ANY failure, so
// every non-exponent malformation reported the CURRENCY as the bad amount:
// "1,000 USD" told the operator `"USD" is not an amount`, which is both untrue
// and the one field they had written correctly.
func TestMoney_AMalformedAmountNamesTheMalformedToken(t *testing.T) {
	_, arr := moneyProp(t)

	malformed := []struct {
		text       string
		badToken   string
		neverBlame string
	}{
		{"1,000 USD", "1,000", "USD"},
		{"1.2.3 USD", "1.2.3", "USD"},
		{"0x1e USD", "0x1e", "USD"},
		{"349,98 SGD", "349,98", "SGD"},
	}
	for _, c := range malformed {
		t.Run(c.text, func(t *testing.T) {
			tv, verr := ParseValue(arr, Node{Kind: KindScalar, Text: c.text})
			if verr == nil {
				t.Fatalf("%q is not a money value; it parsed as %s", c.text, tv.Money)
			}
			if verr.Code != FindingMoneyMalformed {
				t.Fatalf("expected %q, got %q (%s)", FindingMoneyMalformed, verr.Code, verr.Reason)
			}
			if !strings.Contains(verr.Reason, c.badToken) {
				t.Fatalf("the refusal must name %q, the token the operator has to fix; got %q", c.badToken, verr.Reason)
			}
			if strings.Contains(verr.Reason, "\""+c.neverBlame+"\" is not an amount") {
				t.Fatalf("%q is the CURRENCY and it is correct — blaming it sends the operator to fix the one field that was right; got %q", c.neverBlame, verr.Reason)
			}
		})
	}

	t.Run("the currency-first form a human really writes still parses", func(t *testing.T) {
		// The swap exists for this, and tightening it must not cost it.
		m := mustMoney(t, arr, "SGD 349.98")
		if m.String() != "349.98 SGD" || m.MinorUnits() != "34998" || m.Scale() != 2 {
			t.Fatalf("got %s (minor units %q, scale %d)", m, m.MinorUnits(), m.Scale())
		}
	})

	t.Run("an exponent is still reported as an exponent, not as a bad currency", func(t *testing.T) {
		// The two-step design exists for this case; the fix must not lose it.
		_, verr := ParseValue(arr, Node{Kind: KindScalar, Text: "1e3 USD"})
		if verr == nil {
			t.Fatalf("\"1e3 USD\" must be refused")
		}
		if !strings.Contains(verr.Reason, "exponent") {
			t.Fatalf("the operator's fix is to write the number out, and only the word `exponent` says so; got %q", verr.Reason)
		}
		if !strings.Contains(verr.Reason, "1e3") {
			t.Fatalf("the refusal must quote the amount, not the currency; got %q", verr.Reason)
		}
	})

	t.Run("when neither field is an amount the amount-first reading is reported", func(t *testing.T) {
		_, verr := ParseValue(arr, Node{Kind: KindScalar, Text: "USD SGD"})
		if verr == nil {
			t.Fatalf("\"USD SGD\" carries no amount at all and must be refused")
		}
		if !strings.Contains(verr.Reason, "USD") {
			t.Fatalf("got %q", verr.Reason)
		}
	})
}

// TestMoney_AWrongShapedFieldIsNotReportedAsMissing is the fault told as the
// wrong fault.
//
// `{amount: [1], currency: SGD}` reported "no `amount`" — telling the operator
// to add a field they had already written. Every other rejection in this
// package names the shape it found; these two did not.
func TestMoney_AWrongShapedFieldIsNotReportedAsMissing(t *testing.T) {
	_, arr := moneyProp(t)

	mapping := func(fields map[string]Node, keys ...string) Node {
		return Node{Kind: KindMapping, Keys: keys, Fields: fields}
	}

	t.Run("an amount written as a list names the shape, not an absence", func(t *testing.T) {
		node := mapping(map[string]Node{
			"amount":   {Kind: KindSequence, Items: []Node{{Kind: KindScalar, Text: "1"}}},
			"currency": {Kind: KindScalar, Text: "SGD"},
		}, "amount", "currency")
		_, verr := ParseValue(arr, node)
		if verr == nil {
			t.Fatalf("a list is not an amount and must be refused")
		}
		if strings.Contains(verr.Reason, "no `amount`") {
			t.Fatalf("the amount IS there, in the wrong shape — `no amount` tells the operator to add what they already wrote; got %q", verr.Reason)
		}
		if !strings.Contains(verr.Reason, "`amount`") || !strings.Contains(verr.Reason, KindSequence.String()) {
			t.Fatalf("the refusal must name the field and the shape it found (%q); got %q", KindSequence.String(), verr.Reason)
		}
	})

	t.Run("an amount written as a nested mapping names the shape too", func(t *testing.T) {
		node := mapping(map[string]Node{
			"amount":   {Kind: KindMapping, Keys: []string{"value"}, Fields: map[string]Node{"value": {Kind: KindScalar, Text: "1"}}},
			"currency": {Kind: KindScalar, Text: "SGD"},
		}, "amount", "currency")
		_, verr := ParseValue(arr, node)
		if verr == nil {
			t.Fatalf("a nested mapping is not an amount and must be refused")
		}
		if !strings.Contains(verr.Reason, KindMapping.String()) {
			t.Fatalf("the refusal must name the shape it found; got %q", verr.Reason)
		}
	})

	t.Run("a currency written as a list is not reported as a missing currency", func(t *testing.T) {
		node := mapping(map[string]Node{
			"amount":   {Kind: KindScalar, Text: "349.98"},
			"currency": {Kind: KindSequence, Items: []Node{{Kind: KindScalar, Text: "SGD"}}},
		}, "amount", "currency")
		_, verr := ParseValue(arr, node)
		if verr == nil {
			t.Fatalf("a list is not a currency and must be refused")
		}
		if verr.Code == FindingMoneyNoCurrency {
			t.Fatalf("the currency IS there, in the wrong shape; reporting it as absent is the same misdirection as `no amount` (%s)", verr.Reason)
		}
		if !strings.Contains(verr.Reason, "`currency`") || !strings.Contains(verr.Reason, KindSequence.String()) {
			t.Fatalf("the refusal must name the field and the shape it found; got %q", verr.Reason)
		}
	})

	t.Run("a genuinely absent amount still reports as absent", func(t *testing.T) {
		// The split must not cost the true case its correct message.
		node := mapping(map[string]Node{"currency": {Kind: KindScalar, Text: "SGD"}}, "currency")
		_, verr := ParseValue(arr, node)
		if verr == nil || !strings.Contains(verr.Reason, "no `amount`") {
			t.Fatalf("a mapping with no amount must say so; got %v", verr)
		}
	})

	t.Run("an explicitly null amount is absent, per FR-007/R-3", func(t *testing.T) {
		node := mapping(map[string]Node{
			"amount":   {Kind: KindNull},
			"currency": {Kind: KindScalar, Text: "SGD"},
		}, "amount", "currency")
		_, verr := ParseValue(arr, node)
		if verr == nil || !strings.Contains(verr.Reason, "no `amount`") {
			t.Fatalf("a key with no value is not a value; got %v", verr)
		}
	})

	t.Run("a genuinely absent currency still reports as a missing currency", func(t *testing.T) {
		node := mapping(map[string]Node{"amount": {Kind: KindScalar, Text: "349.98"}}, "amount")
		_, verr := ParseValue(arr, node)
		if verr == nil {
			t.Fatalf("FR-012: an amount with no currency must be rejected")
		}
		if verr.Code != FindingMoneyNoCurrency {
			t.Fatalf("expected %q, got %q (%s)", FindingMoneyNoCurrency, verr.Code, verr.Reason)
		}
	})
}

// TestMoney_EveryLegitimateFormStillParses is the counterweight to all of the
// above. A parser tightened until it refuses real vault data is a worse defect
// than any of the four it closed, so every form the package documents as
// accepted is asserted here with its exact value, minor units and scale.
func TestMoney_EveryLegitimateFormStillParses(t *testing.T) {
	set, arr := moneyProp(t)

	inline := []struct {
		text      string
		want      string
		currency  string
		minor     string
		wantScale int32
	}{
		{"349.98 SGD", "349.98 SGD", "SGD", "34998", 2},
		{"SGD 349.98", "349.98 SGD", "SGD", "34998", 2},
		{"1000 JPY", "1000 JPY", "JPY", "1000", 0},
		{"-12.50 USD", "-12.50 USD", "USD", "-1250", 2},
		{"USD -12.50", "-12.50 USD", "USD", "-1250", 2},
		{"0.000000000001 USD", "0.000000000001 USD", "USD", "1", 12},
	}
	for _, c := range inline {
		t.Run(c.text, func(t *testing.T) {
			m := mustMoney(t, arr, c.text)
			if m.String() != c.want {
				t.Fatalf("want %q, got %q", c.want, m.String())
			}
			if m.Currency != c.currency {
				t.Fatalf("want currency %q, got %q", c.currency, m.Currency)
			}
			if m.MinorUnits() != c.minor {
				t.Fatalf("ADR-068 O-2 stores integer minor units; want %s, got %s", c.minor, m.MinorUnits())
			}
			if m.Scale() != c.wantScale {
				t.Fatalf("want scale %d, got %d", c.wantScale, m.Scale())
			}
		})
	}

	t.Run("the mapping form with an inferred scale", func(t *testing.T) {
		rec := ParseRecord("a.md", []byte("---\ntype: widget\narr: {amount: 349.98, currency: SGD}\n---\n"))
		rep := ValidateRecord(set, rec, ValidateOptions{})
		if !rep.Valid() {
			t.Fatalf("{amount, currency} is an accepted form; got %v", rep.Errors())
		}
		pv := ResolveProperty(rec, arr)
		m := pv.Values[0].Money
		if m.String() != "349.98 SGD" || m.MinorUnits() != "34998" || m.Scale() != 2 {
			t.Fatalf("got %s (minor units %q, scale %d)", m, m.MinorUnits(), m.Scale())
		}
	})

	t.Run("the declared-scale mapping form", func(t *testing.T) {
		rec := ParseRecord("a.md", []byte("---\ntype: widget\narr: {amount: 34998, currency: SGD, scale: 2}\n---\n"))
		rep := ValidateRecord(set, rec, ValidateOptions{})
		if !rep.Valid() {
			t.Fatalf("{amount, currency, scale} is the documented minor-units form; got %v", rep.Errors())
		}
		pv := ResolveProperty(rec, arr)
		m := pv.Values[0].Money
		if m.String() != "349.98 SGD" || m.MinorUnits() != "34998" || m.Scale() != 2 {
			t.Fatalf("got %s (minor units %q, scale %d)", m, m.MinorUnits(), m.Scale())
		}
	})

	t.Run("a declared scale of zero, the JPY shape", func(t *testing.T) {
		rec := ParseRecord("a.md", []byte("---\ntype: widget\narr: {amount: 1000, currency: JPY, scale: 0}\n---\n"))
		rep := ValidateRecord(set, rec, ValidateOptions{})
		if !rep.Valid() {
			t.Fatalf("a zero-minor-unit currency is ordinary data; got %v", rep.Errors())
		}
		pv := ResolveProperty(rec, arr)
		if got := pv.Values[0].Money.String(); got != "1000 JPY" {
			t.Fatalf("want 1000 JPY, got %s", got)
		}
	})

	t.Run("keys in any order, since a mapping has no order", func(t *testing.T) {
		rec := ParseRecord("a.md", []byte("---\ntype: widget\narr: {scale: 2, currency: SGD, amount: 34998}\n---\n"))
		rep := ValidateRecord(set, rec, ValidateOptions{})
		if !rep.Valid() {
			t.Fatalf("YAML mapping keys carry no order and neither may the parser; got %v", rep.Errors())
		}
		pv := ResolveProperty(rec, arr)
		if got := pv.Values[0].Money.String(); got != "349.98 SGD" {
			t.Fatalf("want 349.98 SGD, got %s", got)
		}
	})
}
