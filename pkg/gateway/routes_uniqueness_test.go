//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestRegisterHTTPHandler_NoDuplicatePatterns guards against the class of bug
// where two route registrations for the same path silently collide — Go's
// underlying mux takes the last registration, which can downgrade a fully
// wrapped chain (withAuth → CSRF → RequireNotBypass) to a lighter one
// without anyone noticing. This exact scenario shipped in commit edb5a57
// and had to be fixed in b95edc4; the test is the structural defense so it
// never recurs.
//
// Implementation: scan the compiled `rest.go` source for every
// `cm.RegisterHTTPHandler("<pattern>", ...)` call and assert each literal
// pattern appears at most once across the entire file.
func TestRegisterHTTPHandler_NoDuplicatePatterns(t *testing.T) {
	data, err := os.ReadFile("rest.go")
	if err != nil {
		t.Fatalf("read rest.go: %v", err)
	}

	// Match cm.RegisterHTTPHandler( followed by a quoted pattern.
	// Captures the pattern string between the quotes.
	re := regexp.MustCompile(`cm\.RegisterHTTPHandler\(\s*"([^"]+)"`)
	matches := re.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		t.Fatalf("expected at least one RegisterHTTPHandler call in rest.go; regex pattern may be stale")
	}

	counts := make(map[string]int, len(matches))
	for _, m := range matches {
		counts[m[1]]++
	}

	var duplicates []string
	for pattern, n := range counts {
		if n > 1 {
			duplicates = append(duplicates, pattern+" ("+itoa(n)+")")
		}
	}
	if len(duplicates) > 0 {
		t.Fatalf(
			"rest.go registers these patterns more than once — the last registration silently wins and can strip middleware (e.g. RequireNotBypass):\n  %s",
			strings.Join(duplicates, "\n  "),
		)
	}
}

// itoa is a tiny strconv-free integer-to-string helper used only for building
// the test failure message.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 8)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}
