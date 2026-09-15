// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
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

// accumulationSource builds the shape that defeats a PER-PROPERTY budget:
// the anchors a0..a3 are declared ONCE, and then nprops properties each hold a
// single alias to a3. Every `bN` expands to ~8,200 nodes on its own — well
// under the allowance — so no property is ever individually refusable. Only
// their SUM is.
func accumulationSource(nprops int) []byte {
	var b strings.Builder
	b.WriteString("---\ntype: widget\na0: &a0 [x, x, x, x, x, x, x, x, x]\n")
	for i := 1; i <= 3; i++ {
		fmt.Fprintf(&b, "a%d: &a%d [", i, i)
		for j := 0; j < 9; j++ {
			if j > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "*a%d", i-1)
		}
		b.WriteString("]\n")
	}
	for i := 0; i < nprops; i++ {
		fmt.Fprintf(&b, "b%d: *a3\n", i)
	}
	b.WriteString("---\nbody\n")
	return []byte(b.String())
}

// TestFrontmatter_AmplificationAccumulatesAcrossProperties is the regression
// guard for the defect that survived the first fix: the budget was allocated
// FRESH INSIDE the converter, and the converter is called once per frontmatter
// KEY, so nothing bounded the document.
//
// TestFrontmatter_AmplifyingAliasesAreBounded above cannot catch this. Its
// fixture breaches the allowance on a SINGLE property (a4 alone expands past
// it), so the whole-frontmatter refusal short-circuits before a second property
// is ever converted — it proves the single-property case and only that.
//
// Measured on the per-property version, with every property individually legal:
//
//	nprops=   1  srcBytes=   232  alloc=      1,804,816  parseErr=""
//	nprops=  50  srcBytes=   664  alloc=     43,016,752  parseErr=""
//	nprops= 200  srcBytes= 2,114  alloc=    169,122,552  parseErr=""
//	nprops=1000  srcBytes=10,114  alloc=    841,927,912  parseErr=""
//
// 10 KB of frontmatter allocating 842 MB and reporting NOTHING — larger than
// the 40,000x the original amplification bug was filed for, and silent where
// the original at least died loudly. The trees are RETAINED on
// Record.Frontmatter.Values, not transient.
func TestFrontmatter_AmplificationAccumulatesAcrossProperties(t *testing.T) {
	// STEP 1 — establish the premise. If ONE property of this shape is already
	// refusable, the test below would pass for the wrong reason and would prove
	// nothing about accumulation.
	one := accumulationSource(1)
	if rec := ParseRecord("one.md", one); rec.ParseError != "" {
		t.Fatalf("PREMISE BROKEN: a single property of this shape must be individually LEGAL,\n"+
			"otherwise the accumulation assertion below is satisfied by the single-property\n"+
			"guard and proves nothing new. convertMaxNodes is %d; lower the fixture's anchor\n"+
			"depth until one property fits inside it.\ngot: %s", convertMaxNodes, rec.ParseError)
	}

	// STEP 2 — the same properties, repeated. Each is still individually legal;
	// only the document is not.
	const nprops = 200
	src := accumulationSource(nprops)
	if len(src) > 4096 {
		t.Fatalf("fixture should be small — that is the point; got %d bytes", len(src))
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	rec := ParseRecord("accumulate.md", src)
	runtime.ReadMemStats(&after)
	grew := int64(after.TotalAlloc) - int64(before.TotalAlloc)

	if rec.ParseError == "" {
		t.Fatalf("%d properties, each individually under the allowance, must be refused as a\n"+
			"DOCUMENT. A clean parse here means the budget is per-property again and %d bytes\n"+
			"of frontmatter just allocated %d bytes of retained tree with nothing reported.",
			nprops, len(src), grew)
	}
	// The refusal must come from the NODE bound, not from the cycle guard —
	// nothing in this fixture is cyclic, and a cycle-guard false positive
	// refusing it would make this test green while the accumulation hole stayed
	// open.
	if !strings.Contains(rec.ParseError, "nodes across the whole note") {
		t.Fatalf("the refusal must name the document-wide node bound as the cause.\ngot: %s", rec.ParseError)
	}
	// FR-026: named, with the reason. The offending property is still called out.
	if !strings.Contains(rec.ParseError, `property "b`) {
		t.Fatalf("the refusal must still name the offending property.\ngot: %s", rec.ParseError)
	}
	if rec.Path != "accumulate.md" {
		t.Fatalf("the refused note must still be reported by name; got path %q", rec.Path)
	}
	// And the refusal must be whole-frontmatter, never a partial map.
	if len(rec.Frontmatter.Values) != 0 || len(rec.Frontmatter.Keys) != 0 {
		t.Fatalf("a refused note must expose NO properties, not the ones read before the bound\n"+
			"was hit: a partial map validates as an ordinary note and vanishes from answers\n"+
			"that still report complete. got %d values, %d keys",
			len(rec.Frontmatter.Values), len(rec.Frontmatter.Keys))
	}

	// The measurement, not merely the error string: allocation must stop
	// scaling with the note. Measured 169,122,552 bytes for this exact fixture
	// before the fix, ~2.2 MB after.
	const budget = 32 << 20
	if grew > budget {
		t.Fatalf("%d bytes of frontmatter allocated %d bytes — the expansion is still unbounded\n"+
			"across properties even though it is reported. parseErr=%q",
			len(src), grew, rec.ParseError)
	}
}

// TestFrontmatter_OrdinaryNestingStillParses is the anti-vacuity control.
//
// Without it, every test above passes trivially if the bound is set so low that
// every real note is refused. The operator's own 751-note vault peaks in the
// LOW HUNDREDS of nodes.
//
// This control was itself nearly vacuous once. It built its keys as
// `"p" + strings.Repeat("x", 1+i%5)`, which yields five distinct spellings, so
// 200 iterations produced 8 DISTINCT KEYS and 195 duplicate-key problems — and
// a duplicate key never reaches the conversion path at all. It proved that an
// eight-key note parses. The keys are now genuinely distinct, and the
// duplicate-key count is asserted to be ZERO so it cannot silently degenerate
// again.
func TestFrontmatter_OrdinaryNestingStillParses(t *testing.T) {
	const nprops = 200

	var b strings.Builder
	b.WriteString("---\ntype: company\nname: Acme\n")
	for i := 0; i < nprops; i++ {
		fmt.Fprintf(&b, "p%03d: value\n", i)
	}
	// A generated index note is the realistic upper end of a real vault: a long
	// list of relations under one key. If the bound cannot admit this, it is
	// too tight for the vault it is meant to protect.
	b.WriteString("links:\n")
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&b, "  - \"[[Note %04d]]\"\n", i)
	}
	b.WriteString("nested:\n  a:\n    b:\n      c: deep\n---\nbody\n")

	rec := ParseRecord("ordinary.md", []byte(b.String()))
	if rec.ParseError != "" {
		t.Fatalf("an ordinary note must still parse; the bound is too tight.\ngot: %s", rec.ParseError)
	}

	// The keys must actually be distinct. A duplicate key is skipped before
	// conversion, so a fixture full of duplicates exercises almost nothing and
	// would let an arbitrarily tight bound look safe.
	if n := len(rec.Frontmatter.Problems); n != 0 {
		t.Fatalf("the control fixture must contain NO duplicate keys, or it proves nothing\n"+
			"about how many properties the bound admits; got %d problems, first: %s",
			n, rec.Frontmatter.Problems[0])
	}
	// type + name + nprops + links + nested
	if want := nprops + 4; len(rec.Frontmatter.Keys) != want {
		t.Fatalf("expected %d distinct properties to survive the parse, got %d", want, len(rec.Frontmatter.Keys))
	}
	for i := 0; i < nprops; i++ {
		key := fmt.Sprintf("p%03d", i)
		if _, ok := rec.Frontmatter.Values[key]; !ok {
			t.Fatalf("property %q went missing from an ordinary note", key)
		}
	}
	if _, ok := rec.Frontmatter.Values["name"]; !ok {
		t.Fatal("expected the frontmatter to be readable")
	}
	links, ok := rec.Frontmatter.Values["links"]
	if !ok || links.Kind != KindSequence || len(links.Items) != 1000 {
		t.Fatalf("a 1000-item relation list must survive; got ok=%v kind=%v items=%d",
			ok, links.Kind, len(links.Items))
	}
}
