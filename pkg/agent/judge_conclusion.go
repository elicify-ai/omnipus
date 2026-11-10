// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// The Judge keeps its own time bounds (judge.timeout_sec, the judge round
// bounds and the unavailable-retry bound) — founder decision on issue #1306:
// "the judge needs to conclude, we can not just end turns". Every path where
// the check cannot complete therefore ends in an explicit, visible
// conclusion that carries BOTH the verdict ("not verified") and the work
// result the check was about, so the user or parent agent never loses the
// work and never sees a silent ending.

// judgeConclusionResultCap bounds the work result carried inside a
// not-verified conclusion, so the conclusion stays well inside every message
// and reason field it travels in (SessionMessageError.text allows 32 KiB).
const judgeConclusionResultCap = 8000

// notVerifiedConclusion builds the one conclusion text. cause says why the
// check could not complete; next says what happens now; workResult is the
// worker's own result/claim, appended verbatim (capped) when non-empty.
func notVerifiedConclusion(cause, next, workResult string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Not verified — the check could not complete (%s).", strings.TrimSpace(cause))
	if next = strings.TrimSpace(next); next != "" {
		b.WriteString(" ")
		b.WriteString(next)
	}
	if result := strings.TrimSpace(workResult); result != "" {
		if len(result) > judgeConclusionResultCap {
			cut := judgeConclusionResultCap
			for cut > 0 && !utf8.RuneStart(result[cut]) {
				cut--
			}
			result = result[:cut] + " … [truncated]"
		}
		b.WriteString("\n\nThe work result, unverified:\n")
		b.WriteString(result)
	}
	return b.String()
}
