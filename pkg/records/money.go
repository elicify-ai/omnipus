// Omnipus — ADR-068 D3/O-2: the `money` property type.
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
// ADR-068 D3 names what `money` closes: "two loose fields nothing keeps
// together, over binary floats". So:
//
//   FR-012  a money value carries amount, ISO-4217 currency and scale TOGETHER;
//           a value missing currency is REJECTED.
//   FR-013  money arithmetic is exact decimal.
//   FR-014  summing across currencies is REFUSED, listing the currencies present.
//
// FR-014 is the one people try to "help" with. Do not. ADR-068 O-2 is explicit:
// no FX conversion, no rate table. A total that silently mixes SGD and EUR is
// worse than no total, because a reader cannot tell it happened. The refusal
// carries the currency list precisely so the caller can ask a better question.
// ---------------------------------------------------------------------------

// Money is an amount in a single currency, held exactly.
//
// Amount is in MINOR UNITS at Scale (ADR-068 O-2): 349.98 SGD is
// Amount=34998, Scale=2, Currency="SGD".
type Money struct {
	Amount   Decimal // value = Amount, already carrying its own scale
	Currency string  // ISO-4217 alphabetic code, uppercase
}

// Scale reports the number of minor-unit digits this value was declared with.
func (m Money) Scale() int32 { return m.Amount.Scale() }

// String renders "349.98 SGD".
func (m Money) String() string { return m.Amount.String() + " " + m.Currency }

// MinorUnits returns the amount as an integer count of minor units at Scale.
func (m Money) MinorUnits() string { return m.Amount.Unscaled().String() }

// ---------------------------------------------------------------------------
// Errors that must stay distinguishable — callers report each differently.
// ---------------------------------------------------------------------------

// MissingCurrencyError is FR-012's rejection. It is its own type because
// "349.98" and "349.98 QQQ" fail for genuinely different reasons and an
// operator fixes them differently.
type MissingCurrencyError struct {
	Amount string // the amount as written, so the report can quote the source
}

func (e *MissingCurrencyError) Error() string {
	return fmt.Sprintf("money value %q has no currency: a money value must carry amount and ISO-4217 currency together (expected e.g. %q or {amount: %s, currency: SGD})", e.Amount, e.Amount+" SGD", e.Amount)
}

// UnknownCurrencyError names a code that is not ISO-4217.
type UnknownCurrencyError struct {
	Code string
}

func (e *UnknownCurrencyError) Error() string {
	return fmt.Sprintf("currency %q is not an ISO-4217 alphabetic code (expected three uppercase letters, e.g. \"SGD\")", e.Code)
}

// CrossCurrencyError is FR-014. It carries every currency that was present, in
// sorted order, because "refused" without the list leaves the caller with no
// next move.
type CrossCurrencyError struct {
	Currencies []string
}

func (e *CrossCurrencyError) Error() string {
	return fmt.Sprintf("cannot sum money across currencies: %s present. Omnipus does not convert between currencies (ADR-068 O-2) — group by currency and total each separately", strings.Join(e.Currencies, ", "))
}

// ---------------------------------------------------------------------------
// Arithmetic
// ---------------------------------------------------------------------------

// SumMoney adds money values exactly, and REFUSES if more than one currency is
// present (FR-014).
//
// An empty input is not an error and not a zero of some assumed currency — it
// returns ok=false with no error, because "the total of nothing" has no
// currency to be denominated in and inventing one would be exactly the silent
// assumption this package exists to remove.
func SumMoney(values []Money) (total Money, ok bool, err error) {
	if len(values) == 0 {
		return Money{}, false, nil
	}

	// Collect the distinct currencies FIRST, so the refusal can name all of
	// them rather than only the first two encountered.
	seen := map[string]struct{}{}
	for _, v := range values {
		seen[v.Currency] = struct{}{}
	}
	if len(seen) > 1 {
		list := make([]string, 0, len(seen))
		for c := range seen {
			list = append(list, c)
		}
		sort.Strings(list)
		return Money{}, false, &CrossCurrencyError{Currencies: list}
	}

	sum := values[0].Amount
	for _, v := range values[1:] {
		next, addErr := sum.Add(v.Amount)
		if addErr != nil {
			return Money{}, false, fmt.Errorf("summing money: %w", addErr)
		}
		sum = next
	}
	return Money{Amount: sum, Currency: values[0].Currency}, true, nil
}

// CurrenciesPresent lists the distinct currencies in a set, sorted. Callers
// building a problem list use it to report what a refused total contained.
func CurrenciesPresent(values []Money) []string {
	seen := map[string]struct{}{}
	for _, v := range values {
		seen[v.Currency] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// CompareMoney implements §8 R-6: money compares only WITHIN one currency.
// Across currencies every operator is false, which the caller reports rather
// than swallowing. ok=false means "not comparable", not "less than".
func CompareMoney(a, b Money) (cmp int, ok bool) {
	if a.Currency != b.Currency {
		return 0, false
	}
	return a.Amount.Cmp(b.Amount), true
}

// ---------------------------------------------------------------------------
// Currency validation
// ---------------------------------------------------------------------------

// ValidateCurrency checks an ISO-4217 alphabetic code.
//
// The check is EXACT-CASE. ISO-4217 codes are uppercase, and normalising "sgd"
// silently would be the same class of helpfulness that produces `Won`, `won`
// and `Closed Won` in one column (ADR-068 D4). A lowercase code is rejected
// with the expected form named, which takes an operator one keystroke to fix.
func ValidateCurrency(code string) error {
	if _, ok := iso4217Codes[code]; !ok {
		return &UnknownCurrencyError{Code: code}
	}
	return nil
}

// iso4217Codes is a DATA TABLE, not logic. It holds ISO-4217 alphabetic codes
// as published at the time of writing (2026-08).
//
// It deliberately ALSO contains recently withdrawn codes (ANG, CUC, HRK, SLL,
// VEF, ZWL, MRO, STD, BYR, LTL, LVL). A vault is a historical record: a note
// about a 2019 deal denominated in HRK is correct data, and rejecting it
// because Croatia has since adopted the euro would be the tool overruling the
// operator's own history.
//
// Maintenance note: an omission here surfaces as a LOUD rejection naming the
// code (UnknownCurrencyError), never as a silent mis-parse — so the failure
// mode of this table going stale is a one-line fix, reported by the user.
var iso4217Codes = func() map[string]struct{} {
	codes := strings.Fields(`
		AED AFN ALL AMD ANG AOA ARS AUD AWG AZN
		BAM BBD BDT BGN BHD BIF BMD BND BOB BOV BRL BSD BTN BWP BYN BYR BZD
		CAD CDF CHE CHF CHW CLF CLP CNY COP COU CRC CUC CUP CVE CZK
		DJF DKK DOP DZD
		EGP ERN ETB EUR
		FJD FKP
		GBP GEL GHS GIP GMD GNF GTQ GYD
		HKD HNL HRK HTG HUF
		IDR ILS INR IQD IRR ISK
		JMD JOD JPY
		KES KGS KHR KMF KPW KRW KWD KYD KZT
		LAK LBP LKR LRD LSL LTL LVL LYD
		MAD MDL MGA MKD MMK MNT MOP MRO MRU MUR MVR MWK MXN MXV MYR MZN
		NAD NGN NIO NOK NPR NZD
		OMR
		PAB PEN PGK PHP PKR PLN PYG
		QAR
		RON RSD RUB RWF
		SAR SBD SCR SDG SEK SGD SHP SLE SLL SOS SRD SSP STD STN SVC SYP SZL
		THB TJS TMT TND TOP TRY TTD TWD TZS
		UAH UGX USD USN UYI UYU UYW UZS
		VED VEF VES VND VUV
		WST
		XAF XAG XAU XBA XBB XBC XBD XCD XCG XDR XOF XPD XPF XPT XSU XTS XUA XXX
		YER
		ZAR ZMW ZWG ZWL
	`)
	m := make(map[string]struct{}, len(codes))
	for _, c := range codes {
		m[c] = struct{}{}
	}
	return m
}()
