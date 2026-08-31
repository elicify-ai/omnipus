// Omnipus — a load-bearing comment is checkable, so check it.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// `checkbox` became the eighth property type (FR-004c, ADR-068 D24.5) and the
// prose in this package stayed at seven — in doc.go's package comment, in
// schema.go's requirement index and in PropertyType's own doc. None of it
// changed behaviour, and that is exactly why it survived: nothing fails when a
// comment is wrong.
//
// It is not harmless. A comment stating a CLOSED SET's size is the thing the
// next reader trusts instead of counting, and this specific drift already cost
// a live defect on the tool side, where a description told models the seven
// types "are the seven … there is no eighth" over a write path that accepted
// the eighth. Doc-only, and it made `checkbox` unreachable in practice.
//
// So the claim is asserted rather than left to review. The scan is narrow on
// purpose: it looks for a COUNT stated about the property types, not for the
// word "seven", which has other legitimate uses.
// ---------------------------------------------------------------------------

// staleSevenClaim matches prose asserting the property-type count is seven.
var staleSevenClaim = regexp.MustCompile(`(?i)(seven property types|seven types|exactly seven|the seven\b)`)

// TestPropertyTypes_NoSourceCommentStillClaimsSeven guards the prose.
func TestPropertyTypes_NoSourceCommentStillClaimsSeven(t *testing.T) {
	// The anchor: the guard is only meaningful while the real count is eight.
	// If the set ever legitimately changes, this line fails first and sends the
	// author here rather than leaving a scan enforcing a number nobody meant.
	if len(PropertyTypes) != 8 {
		t.Fatalf("this guard asserts the prose matches a set of EIGHT; PropertyTypes now has %d (%v) — update the requirement, then the prose, then this test",
			len(PropertyTypes), PropertyTypes)
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		// COMMENT BLOCKS, not lines. `// the SEVEN` / `// PROPERTY TYPES` on
		// two consecutive lines is one sentence and one claim; a line-by-line
		// scan reads neither half as the defect and misses exactly the wording
		// that stood in doc.go.
		for _, blk := range commentBlocks(string(body)) {
			if !mentionsPropertyTypes(blk.text) {
				continue
			}
			// The correction itself has to be sayable. A comment that names
			// the stale count in order to forbid it is the opposite of the
			// defect.
			if strings.Contains(blk.text, "back to seven") || strings.Contains(blk.text, "through spec Draft 9") ||
				strings.Contains(blk.text, "still says about property types") {
				continue
			}
			if m := staleSevenClaim.FindString(blk.text); m != "" {
				t.Errorf("%s:%d states the property-type count as seven (%q); FR-004c made it eight — %s",
					name, blk.line, m, truncate(blk.text))
			}
		}
	}
}

// commentBlock is one run of consecutive `//` lines, flattened to a single
// string so a claim split across a line break still reads as one claim.
type commentBlock struct {
	line int // 1-based line the block starts on
	text string
}

func commentBlocks(src string) []commentBlock {
	var out []commentBlock
	var cur []string
	start := 0
	flush := func() {
		if len(cur) > 0 {
			out = append(out, commentBlock{line: start, text: strings.Join(cur, " ")})
			cur = nil
		}
	}
	for i, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			if len(cur) == 0 {
				start = i + 1
			}
			cur = append(cur, strings.TrimSpace(strings.TrimPrefix(trimmed, "//")))
			continue
		}
		flush()
	}
	flush()
	return out
}

func truncate(s string) string {
	const limit = 140
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}

// mentionsPropertyTypes keeps the scan off unrelated sevens: "seven" has to be
// about the property types for the line to be the defect this guards.
func mentionsPropertyTypes(line string) bool {
	l := strings.ToLower(line)
	for _, sig := range []string{"property type", "propertytype", "types exist", "types adr-068", "value fields"} {
		if strings.Contains(l, sig) {
			return true
		}
	}
	return false
}
