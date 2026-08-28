// Omnipus — AC-8.9: the comparator's case fold, asserted as literal pairs.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// FR-011a requires case-insensitive matching in FULL Unicode. The requirement
// is one line and there are two obvious ways to implement it, BOTH WRONG, and
// wrong in OPPOSITE directions — so a test that only checks "does it ignore
// case?" over an ASCII fixture passes against every wrong implementation.
//
// AC-8.9 therefore names SIX LITERAL PAIRS with a stated expectation each, and
// states the discriminating property explicitly:
//
//	AC-8.9 fails if the comparator produces the same six answers as
//	strings.ToLower, or the same six as strings.EqualFold.
//
// TestFold_DisagreesWithBothStdlibFunctions asserts exactly that, so the
// criterion cannot be satisfied by an implementation that folds nothing in a
// fixture that happens to be ASCII.
//
// Every expectation below was EXECUTED against golang.org/x/text v0.41.0
// before it was written down. None was reasoned to.
// ---------------------------------------------------------------------------

// foldPair is one AC-8.9 row. The `why` field is not decoration: it is printed
// in every failure message, because the Turkish row in particular looks like a
// bug to anyone who has not read the reason.
type foldPair struct {
	ac    string
	left  string
	right string
	match bool
	why   string
}

// ac89Pairs are AC-8.9a..f, verbatim from the specification's table.
var ac89Pairs = []foldPair{
	{
		ac: "AC-8.9a", left: "straße", right: "STRASSE", match: true,
		why: "German ß needs FULL folding (ß → ss). strings.ToLower says false and strings.EqualFold says false, " +
			"because the Go standard library performs only SIMPLE folding — a rune-for-rune map. " +
			"This is the cell that fails if anyone substitutes a stdlib call.",
	},
	{
		ac: "AC-8.9b", left: "σίσυφος", right: "ΣΊΣΥΦΟΣ", match: true,
		why: "Greek final sigma. strings.ToLower says false and strings.EqualFold says true — " +
			"THE TWO STDLIB FUNCTIONS DISAGREE WITH EACH OTHER on this pair, which is why neither is a permitted default: " +
			"a reviewer who checks one and is satisfied has checked the one that happens to suit their fixture.",
	},
	{
		ac: "AC-8.9c", left: "müller", right: "MÜLLER", match: true,
		why: "German umlaut. Every mechanism agrees here. It is in the set as the ORDINARY case, " +
			"so the fixture is not all edge — a suite of only exotic rows stops telling you the common path works.",
	},
	{
		ac: "AC-8.9d", left: "łódź", right: "ŁÓDŹ", match: true,
		why: "Polish. The control row: a fixture containing only rows like this one discriminates nothing, " +
			"because ToLower, EqualFold and cases.Fold all pass it.",
	},
	{
		ac: "AC-8.9e", left: "istanbul", right: "İSTANBUL", match: false,
		why: "TURKISH DOTTED İ AND PLAIN i ARE DIFFERENT LETTERS. This MUST NOT match, and the FALSE is CORRECT " +
			"BEHAVIOUR RATHER THAN A GAP. strings.ToLower says true here, which is the classic TURKISH-I BUG — " +
			"a WRONG MATCH, the failure direction nobody notices, because a wrong match looks like a feature. " +
			"cases.Fold maps İ to i + U+0307 (COMBINING DOT ABOVE) and correctly keeps the two apart. " +
			"DO NOT 'FIX' THIS ASSERTION: making it pass means reintroducing the Turkish-I bug.",
	},
	{
		ac: "AC-8.9f", left: "ﬁle", right: "file", match: true,
		why: "The ﬁ ligature (U+FB01). False under BOTH stdlib functions. It is not a language case — " +
			"it is here as the SECOND INDEPENDENT WITNESS that simple folding is not enough, " +
			"so the conclusion does not rest on German ß alone.",
	},
}

// TestFold_AC89Pairs is the criterion itself: six literal pairs, six stated
// expectations, each failure carrying the reason the row exists.
func TestFold_AC89Pairs(t *testing.T) {
	if len(ac89Pairs) != 6 {
		t.Fatalf("AC-8.9 names SIX pairs; this fixture has %d — a row was added or removed without amending the criterion", len(ac89Pairs))
	}
	for _, p := range ac89Pairs {
		t.Run(p.ac, func(t *testing.T) {
			got := FoldEqual(p.left, p.right)
			if got != p.match {
				verb := "MATCH"
				if !p.match {
					verb = "NOT MATCH"
				}
				t.Fatalf("%s: FoldEqual(%q, %q) = %v, want %v — %s must %s.\nREASON THIS ROW EXISTS: %s",
					p.ac, p.left, p.right, got, p.match, p.left, verb, p.why)
			}
			// Symmetry: an equality that holds one way round and not the other
			// would make a filter's answer depend on which operand the caller
			// wrote first, which R-12 forbids.
			if FoldEqual(p.right, p.left) != got {
				t.Fatalf("%s: FoldEqual is not symmetric on (%q, %q) — R-12 requires the same answer whichever side a value came from", p.ac, p.left, p.right)
			}
		})
	}
}

// TestFold_DisagreesWithBothStdlibFunctions is AC-8.9's DISCRIMINATING
// property, and it is the assertion that makes this file hard to fake.
//
// The six expectations above could, in principle, be satisfied by an
// implementation that got lucky. They cannot be satisfied by strings.ToLower or
// by strings.EqualFold, and this test proves that by running all three over the
// same fixture and requiring the answer vectors to DIFFER. An implementation
// that quietly swapped in a stdlib call fails here even if someone had also
// edited the expectations above to match it.
func TestFold_DisagreesWithBothStdlibFunctions(t *testing.T) {
	ours := make([]bool, 0, len(ac89Pairs))
	toLower := make([]bool, 0, len(ac89Pairs))
	equalFold := make([]bool, 0, len(ac89Pairs))
	for _, p := range ac89Pairs {
		ours = append(ours, FoldEqual(p.left, p.right))
		toLower = append(toLower, strings.ToLower(p.left) == strings.ToLower(p.right))
		equalFold = append(equalFold, strings.EqualFold(p.left, p.right))
	}

	same := func(a, b []bool) bool {
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	if same(ours, toLower) {
		t.Fatalf("AC-8.9: the fold produced the SAME six answers as strings.ToLower (%v).\n"+
			"strings.ToLower performs SIMPLE folding and collapses Turkish İ onto i, so agreeing with it means "+
			"either the implementation IS strings.ToLower or the expectations were edited to match it. Neither is permitted.", ours)
	}
	if same(ours, equalFold) {
		t.Fatalf("AC-8.9: the fold produced the SAME six answers as strings.EqualFold (%v).\n"+
			"strings.EqualFold performs SIMPLE folding and cannot match German ß against ss. Neither is permitted.", ours)
	}

	// And the two stdlib functions must still disagree WITH EACH OTHER over
	// this fixture. If they ever stop, the argument above loses one of its two
	// legs and the fixture needs a new discriminating row — better to be told
	// than to keep asserting a property that has quietly become vacuous.
	if same(toLower, equalFold) {
		t.Fatalf("the fixture no longer discriminates: strings.ToLower and strings.EqualFold now agree on all six pairs (%v). "+
			"AC-8.9b exists precisely because they disagree; add a row that separates them again", toLower)
	}
}

// TestFold_TurkishNegativeIsDeliberate states AC-8.9e's reason a second time,
// on its own, where nobody can miss it.
//
// It is stated twice ON PURPOSE. The row above lives in a table and a reader
// skimming for a red test sees only "expected true, got false" shaped output.
// This test exists so that the failure message a future engineer reads, when
// they "fix" the fold to make İSTANBUL match istanbul, tells them what they
// have just broken.
func TestFold_TurkishNegativeIsDeliberate(t *testing.T) {
	if FoldEqual("istanbul", "İSTANBUL") {
		t.Fatal(`"istanbul" and "İSTANBUL" MUST NOT match, and this is CORRECT BEHAVIOUR, NOT A BUG.

Turkish dotted capital İ (U+0130) and plain lowercase i are DIFFERENT LETTERS in Turkish;
Turkish also has a dotless ı (U+0131) whose capital is plain I. Folding İ onto i is the
classic TURKISH-I BUG. strings.ToLower commits it — it answers true for this pair — which
is a WRONG MATCH rather than a missing one, and a wrong match is the failure direction
nobody notices because it looks like the feature working.

cases.Fold() maps İ to "i" + U+0307 COMBINING DOT ABOVE, which keeps the two apart.

If you are reading this because you made this test fail: you have reintroduced the
Turkish-I bug. Do not change the assertion. Change the fold back.`)
	}

	// The mechanism, asserted rather than described, so the explanation above
	// cannot drift away from what the code does.
	const combiningDotAbove = "̇"
	if folded := FoldKey("İ"); folded != "i"+combiningDotAbove {
		t.Fatalf("FoldKey(İ) = %q; AC-8.9e's reasoning depends on it folding to i + U+0307 COMBINING DOT ABOVE, "+
			"which is WHY it stays distinct from a plain i. If x/text changed this mapping, the reasoning above needs re-checking", folded)
	}
	if FoldKey("i") == FoldKey("İ") {
		t.Fatal("plain i and dotted İ must not share a fold key")
	}
}

// TestFold_ChangesRuneCount pins the second consequence documented on FoldKey,
// because it is a real trap for anything that counts characters.
//
// LIKE's `_` matches exactly ONE character. If the pattern were counted against
// the raw subject and matched against the folded one, `_` would stand for a
// different number of characters on each side of the same comparison. The rule
// is therefore that LIKE's semantics are defined against the FOLDED subject AND
// the FOLDED pattern — and this test exists so that a future implementer meets
// the fact before they meet the bug.
func TestFold_ChangesRuneCount(t *testing.T) {
	cases := []struct {
		in       string
		rawRunes int
		fldRunes int
	}{
		{"straße", 6, 7},   // ß → ss
		{"ﬁle", 3, 4},      // ﬁ → fi
		{"İSTANBUL", 8, 9}, // İ → i + U+0307
		{"müller", 6, 6},   // unchanged, so the property is not universal
	}
	for _, c := range cases {
		raw := utf8.RuneCountInString(c.in)
		fld := utf8.RuneCountInString(FoldKey(c.in))
		if raw != c.rawRunes || fld != c.fldRunes {
			t.Fatalf("FoldKey(%q): raw runes = %d (want %d), folded runes = %d (want %d) — "+
				"folding CHANGES rune count, and any rule counting characters (LIKE's `_`) must be defined against the FOLDED subject and the FOLDED pattern",
				c.in, raw, c.rawRunes, fld, c.fldRunes)
		}
	}
}

// TestFold_ConcurrencySafe exercises the property that licenses the
// package-level Caser.
//
// cases.Fold() is documented "stateless and safe to use concurrently by
// multiple goroutines" (x/text/cases/cases.go:86-87) — the EXCEPTION to the
// general Caser rule that a Caser is stateful and must not be shared. That
// sentence is load-bearing: without it the package-level `folder` would be a
// data race and every comparison would need its own Caser or a sync.Pool.
//
// A passing run is not a proof of thread-safety; under `-race` a failing one is
// a proof of the opposite, which is the direction that matters.
func TestFold_ConcurrencySafe(t *testing.T) {
	const goroutines = 8
	const iterations = 200
	done := make(chan string, goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			for i := 0; i < iterations; i++ {
				for _, p := range ac89Pairs {
					if FoldEqual(p.left, p.right) != p.match {
						done <- p.ac + ": concurrent FoldEqual returned the wrong answer — the shared Caser is not stateless after all"
						return
					}
				}
			}
			done <- ""
		}()
	}
	for g := 0; g < goroutines; g++ {
		if msg := <-done; msg != "" {
			t.Fatal(msg)
		}
	}
}

// TestFold_IsIdempotentAndStable pins two properties the sort key depends on.
func TestFold_IsIdempotentAndStable(t *testing.T) {
	for _, p := range ac89Pairs {
		for _, s := range []string{p.left, p.right} {
			once := FoldKey(s)
			if twice := FoldKey(once); twice != once {
				t.Fatalf("FoldKey is not idempotent on %q: %q then %q — a sort key that changes when re-derived cannot give SC-014 a byte-identical result across a rebuild", s, once, twice)
			}
			if again := FoldKey(s); again != once {
				t.Fatalf("FoldKey(%q) returned %q then %q — the shared Caser is carrying state between calls", s, once, again)
			}
		}
	}
}

// TestFold_TotalOrderForSorting pins R-5(c) and R-5(d) at the function level.
// enum_test.go asserts the same rules through a schema; this asserts them on
// the primitive, so a failure says which layer broke.
func TestFold_TotalOrderForSorting(t *testing.T) {
	t.Run("R-5(c) the key is the folded form, not the raw bytes", func(t *testing.T) {
		// Executed evidence from ADR-068 D4: "Won" < "lost" is TRUE on raw
		// bytes and FALSE folded, because byte order puts every capitalised
		// value before every lowercase one.
		if !("Won" < "lost") {
			t.Fatal("fixture assumption broken: raw byte order must put \"Won\" before \"lost\"")
		}
		if FoldLess("Won", "lost") {
			t.Fatal("R-5(c): the sort key must be the FOLDED form — folded, \"won\" sorts AFTER \"lost\". " +
				"Sorting raw bytes would render Won/won/WON in three places while group_by collapsed them into one")
		}
	})

	t.Run("R-5(d) ties break on raw bytes, giving a TOTAL order", func(t *testing.T) {
		if FoldKey("Won") != FoldKey("won") {
			t.Fatal("fixture assumption broken: the two spellings must fold alike")
		}
		if !FoldLess("Won", "won") {
			t.Fatal("R-5(d): a tie on the folded key must break on RAW bytes; \"Won\" < \"won\" byte-wise")
		}
		if FoldLess("won", "Won") {
			t.Fatal("R-5(d): the tie-break must be antisymmetric")
		}
		if FoldCompare("Won", "won") >= 0 || FoldCompare("won", "Won") <= 0 || FoldCompare("won", "won") != 0 {
			t.Fatalf("FoldCompare must agree with FoldLess and return 0 only for identical raw values; got %d, %d, %d",
				FoldCompare("Won", "won"), FoldCompare("won", "Won"), FoldCompare("won", "won"))
		}
	})

	t.Run("the tie-break belongs to the SORT and must never reach the `<` operator", func(t *testing.T) {
		// This is the hazard the split exists to prevent, asserted so the
		// reason survives: `won = Won` is TRUE under FR-011a. If `<` were
		// implemented with FoldLess, `won < Won` would ALSO be true, and the
		// two answers contradict each other.
		if !FoldEqual("won", "Won") {
			t.Fatal("FR-011a: \"won\" and \"Won\" must compare EQUAL")
		}
		if !FoldLess("Won", "won") {
			t.Fatal("fixture assumption broken: FoldLess must order the tie")
		}
		// Both hold at once, which is correct for a SORT and would be a
		// contradiction for an OPERATOR. Stated here so the next person to
		// reach for FoldLess in a comparator finds this note first.
	})
}
