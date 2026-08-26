// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"runtime"
	"strings"
	"testing"
)

// TestFrontmatter_CyclicAliasIsRefusedNotFatal is the regression guard for
// CRIT-001.
//
// A YAML alias may point at one of its own ancestors. Following aliases
// blindly recursed forever, and the failure was `fatal error: stack overflow`
// — a FATAL runtime error, not a panic, so recover() could not catch it. One
// six-line note in the operator's vault killed the whole gateway process.
//
// This test cannot be written as "assert it does not crash", because a crash
// takes the test binary with it. It asserts the OBSERVABLE contract instead:
// the note is refused by name, with a reason. If the guard is removed this
// test does not fail politely — the process dies and the package reports no
// result at all, which is itself unmistakable.
func TestFrontmatter_CyclicAliasIsRefusedNotFatal(t *testing.T) {
	for name, src := range map[string]string{
		"self-referential sequence": "---\ntype: widget\na: &x\n  - *x\n---\nbody\n",
		"self-referential mapping":  "---\ntype: widget\na: &x\n  k: *x\n---\nbody\n",
		"mutual reference":          "---\ntype: widget\na: &x\n  - &y\n    - *x\n---\nbody\n",
	} {
		t.Run(name, func(t *testing.T) {
			rec := ParseRecord("cycle.md", []byte(src))
			if rec.ParseError == "" {
				t.Fatalf("a cyclic alias must be REFUSED and reported; got a clean parse.\n" +
					"Silently accepting it means the note appears to have empty frontmatter, " +
					"validates as an ordinary note, and disappears from answers that still " +
					"report complete — the defect ADR-068 exists to remove.")
			}
			if !strings.Contains(rec.ParseError, "refers to itself") {
				t.Fatalf("the refusal must name the real cause so an operator can fix the file.\ngot: %s", rec.ParseError)
			}
		})
	}
}

// TestFrontmatter_AmplifyingAliasesAreBounded is the regression guard for
// CRIT-002 — the "billion laughs" shape.
//
// Aliases that each reference several earlier aliases expand multiplicatively.
// Measured before the fix: 210 bytes of frontmatter produced 66,430 nodes and
// 8.3 MB of heap — roughly 40,000x — with every added line multiplying by nine
// again. A cycle check alone does not stop this: nothing here is cyclic.
//
// The assertion is on HEAP GROWTH, not on wall-clock, because a time-based
// bound would be flaky under load and would pass on a fast machine while the
// defect remained.
func TestFrontmatter_AmplifyingAliasesAreBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("---\ntype: widget\na0: &a0 [x, x, x, x, x, x, x, x, x]\n")
	for i := 1; i <= 8; i++ {
		b.WriteString("a")
		b.WriteRune(rune('0' + i))
		b.WriteString(": &a")
		b.WriteRune(rune('0' + i))
		b.WriteString(" [")
		for j := 0; j < 9; j++ {
			if j > 0 {
				b.WriteString(", ")
			}
			b.WriteString("*a")
			b.WriteRune(rune('0' + i - 1))
		}
		b.WriteString("]\n")
	}
	b.WriteString("---\nbody\n")
	src := []byte(b.String())

	if len(src) > 2048 {
		t.Fatalf("fixture should be small — that is the point; got %d bytes", len(src))
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	rec := ParseRecord("amplify.md", src)
	runtime.ReadMemStats(&after)

	grew := int64(after.TotalAlloc) - int64(before.TotalAlloc)
	const budget = 32 << 20 // 32 MB: far above a bounded parse, far below the 8.3 MB x 9-per-line growth
	if grew > budget {
		t.Fatalf("%d bytes of frontmatter allocated %d bytes — the expansion is unbounded.\n"+
			"parseErr=%q", len(src), grew, rec.ParseError)
	}
	if rec.ParseError == "" {
		t.Fatalf("an expansion this large must be refused by name, not silently truncated to empty frontmatter")
	}
}

// TestFrontmatter_OrdinaryNestingStillParses is the anti-vacuity control.
//
// Without it, both tests above pass trivially if the bound is set so low that
// every real note is refused. The operator's own vault peaks in the low
// hundreds of nodes.
func TestFrontmatter_OrdinaryNestingStillParses(t *testing.T) {
	var b strings.Builder
	b.WriteString("---\ntype: company\nname: Acme\n")
	for i := 0; i < 200; i++ {
		b.WriteString("p")
		b.WriteString(strings.Repeat("x", 1+i%5))
		b.WriteString(": value\n")
	}
	b.WriteString("nested:\n  a:\n    b:\n      c: deep\n---\nbody\n")
	rec := ParseRecord("ordinary.md", []byte(b.String()))
	if rec.ParseError != "" {
		t.Fatalf("an ordinary note must still parse; the bound is too tight.\ngot: %s", rec.ParseError)
	}
	if _, ok := rec.Frontmatter.Values["name"]; !ok {
		t.Fatal("expected the frontmatter to be readable")
	}
}
