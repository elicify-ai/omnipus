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
	switch fact {
	case "authority":
		return `(?is)(?:workspace[^.!?\n]*(?:roster|directory)|(?:roster|directory)[^.!?\n]*workspace)[^.!?\n]*(?:alone|complete|authoritative)[^.!?\n]*(?:permitted|delegation|routes)`
	case "no discovery":
		return `(?is)(?:do not|don't|never)[^.!?\n]*list_agents[^.!?\n]*memory[^.!?\n]*(?:target|route)`
	case "exclusive":
		return `(?is)(?:roster[^.!?\n]*alone[^.!?\n]*permitted|only permitted[^.!?\n]*(?:targets|routes))`
	case "no extra grants":
		return `(?is)(?:names|descriptions)[^.!?\n]*elsewhere[^.!?\n]*(?:no additional access|no additional[^.!?\n]*grant)`
	case "runtime checks":
		return `(?is)runtime[^.!?\n]*graph[^.!?\n]*mode[^.!?\n]*depth[^.!?\n]*tool[^.!?\n]*checks[^.!?\n]*apply`
	default:
		return ""
	}
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
	match := regexp.MustCompile(compactGuidanceFactPattern("exclusive")).FindStringIndex(guidance)
	if match == nil {
		return -1
	}
	return strings.Index(got, "<delegation_guidance>") + match[0]
}
