package agent

import (
	"regexp"
	"strings"
	"testing"
)

// Authority/exclusivity prose may change, but these facts may not. The
// 2026-10-21 approved generic guidance says the current workspace roster alone
// defines permitted routes, forbids additional-route discovery, grants no
// access through names/descriptions elsewhere, and preserves runtime checks.
// These helpers remap only the founder-authorized obsolete wording assertions.
func compactGuidanceFactPattern(fact string) string {
	// These are individual fact clauses from the approved 2026-10-21 payload,
	// not a snapshot of the guidance paragraph. Arbitrary intervening words
	// are unsafe: K30's "checks do not apply" satisfied "checks...apply".
	var clause string
	switch fact {
	case "authority", "exclusive":
		clause = regexp.QuoteMeta("This current workspace roster alone defines your permitted delegation routes.")
	case "no discovery":
		clause = regexp.QuoteMeta("do not use list_agents or memory to find additional routes.")
	case "no extra grants":
		clause = regexp.QuoteMeta("Descriptions or names elsewhere grant no additional access.")
	case "runtime checks":
		clause = regexp.QuoteMeta("Runtime graph, mode, depth and tool checks still apply") +
			`(?:, including each listed edge's maximum chain depth)?\.`
	default:
		return ""
	}
	// A clause must start at a real guidance/sentence boundary, not inside
	// "It is false that <positive words>". Capture the clause independently
	// of its boundary so the ordering helper keeps its actual byte position.
	return `(?i)(?:^|<delegation_guidance>|[.;!?])\s*(` + clause + `)`
}

func compactGuidanceHasFact(t *testing.T, got, fact string) bool {
	t.Helper()
	pattern := compactGuidanceFactPattern(fact)
	if pattern == "" {
		t.Fatalf("unknown test-side guidance fact %q", fact)
	}
	return regexp.MustCompile(pattern).MatchString(compactDelimitedBlock(t, got, "delegation_guidance"))
}

func compactGuidanceDeniesUnlisted(t *testing.T, got string) bool {
	t.Helper()
	// Combined facts preserve the old unlisted-target denial warning's meaning:
	// only this roster grants routes, elsewhere grants none, checks still apply.
	return compactGuidanceHasFact(t, got, "exclusive") &&
		compactGuidanceHasFact(t, got, "no extra grants") &&
		compactGuidanceHasFact(t, got, "runtime checks")
}

func compactGuidanceExclusivityIndex(t *testing.T, got string) int {
	t.Helper()
	guidance := compactDelimitedBlock(t, got, "delegation_guidance")
	match := regexp.MustCompile(compactGuidanceFactPattern("exclusive")).FindStringSubmatchIndex(guidance)
	if match == nil {
		return -1
	}
	return strings.Index(got, "<delegation_guidance>") + match[2]
}
