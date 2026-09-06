// Omnipus — tool-policy submitted-map validation tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// These tests pin the two validators added to close the CLAUDE.md hard
// constraint 6 hole a live UAT round found on 2026-09-02: every REST write path
// that replaces an agent's tool-policy map was relying on
// ValidateToolPolicyCoverage to reject an incomplete or wildcard-bearing map,
// and that check structurally cannot do it — it counts a tool as covered when
// EITHER the global ceiling or the agent has an entry, and pkg/config/defaults.go
// seeds the ceiling with an explicit entry for the whole static catalog.
//
// Oracle discipline: every expected value below is derived from CLAUDE.md hard
// constraint 6's own wording ("explicit, literal, wildcard-free policy entry …
// for every agent", "Exception — MCP tools: … per-server mcp_<server>_*
// wildcard bulk policies remain the mechanism there"), not from reading the
// implementation back.
package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKnownTools(names ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

// TestValidateSubmittedToolPolicyMap_Complete_NoDefects: a map naming every
// known tool explicitly is the only shape hard constraint 6 accepts, and it
// must pass cleanly.
func TestValidateSubmittedToolPolicyMap_Complete_NoDefects(t *testing.T) {
	known := testKnownTools("bash", "read_file", "delete_task")
	submitted := map[string]string{
		"bash":        "deny",
		"read_file":   "allow",
		"delete_task": "ask",
	}
	defects := ValidateSubmittedToolPolicyMap(submitted, known)
	assert.True(t, defects.Empty(), "a complete, wildcard-free map must pass: %s", defects.String())
	assert.Empty(t, defects.Missing)
	assert.Empty(t, defects.Invalid)
}

// TestValidateSubmittedToolPolicyMap_OmittedTool_ReportedMissing is the exact
// UAT reproduction (batch 3 S48 / batch 4 S83/S84): a create/update body that
// omits `bash` entirely must be a defect, NOT something the server quietly
// backfills from its own deny-seeded baseline.
func TestValidateSubmittedToolPolicyMap_OmittedTool_ReportedMissing(t *testing.T) {
	known := testKnownTools("bash", "read_file", "stop_plan")
	submitted := map[string]string{
		"read_file": "allow",
		"stop_plan": "ask",
	}
	defects := ValidateSubmittedToolPolicyMap(submitted, known)
	require.False(t, defects.Empty())
	assert.Equal(t, []string{"bash"}, defects.Missing)
	assert.Empty(t, defects.Invalid)
	assert.Contains(t, defects.String(), "bash",
		"the rejection message must name the offending tool so the 400 is actionable")
}

// TestValidateSubmittedToolPolicyMap_EmptyAndNilMaps_MaximallyDefective is the
// batch-2 CRITICAL shape. `PUT /agents/{id}/tools` with a malformed body left
// the resolved map nil and persisted `"tools":{"builtin":{},"mcp":{}}`; the
// agent then resolved every tool from the permissive global ceiling alone and a
// `bash: deny` agent executed bash. "No map at all" must therefore be the most
// defective input there is, never a silent replace-with-empty.
func TestValidateSubmittedToolPolicyMap_EmptyAndNilMaps_MaximallyDefective(t *testing.T) {
	known := testKnownTools("bash", "read_file", "list_providers")

	nilDefects := ValidateSubmittedToolPolicyMap[string](nil, known)
	require.False(t, nilDefects.Empty(), "a nil submitted map must be rejected, not treated as a no-op")
	assert.Equal(t, []string{"bash", "list_providers", "read_file"}, nilDefects.Missing)

	emptyDefects := ValidateSubmittedToolPolicyMap(map[string]string{}, known)
	require.False(t, emptyDefects.Empty(), "an empty submitted map must be rejected")
	assert.Equal(t, []string{"bash", "list_providers", "read_file"}, emptyDefects.Missing)
}

// TestValidateSubmittedToolPolicyMap_WildcardKey_ReportedInvalid: the UAT sent a
// literal "*" key and got 201, with the wildcard stored inertly beside the real
// entries. Hard constraint 6 admits no wildcard for the static builtin catalog.
func TestValidateSubmittedToolPolicyMap_WildcardKey_ReportedInvalid(t *testing.T) {
	known := testKnownTools("bash", "read_file")
	submitted := map[string]string{
		"bash":       "deny",
		"read_file":  "allow",
		"*":          "allow",
		"browser_*":  "allow",
		"system.*":   "allow",
		"not_a_tool": "allow",
	}
	defects := ValidateSubmittedToolPolicyMap(submitted, known)
	require.False(t, defects.Empty())
	assert.Empty(t, defects.Missing, "every known tool WAS supplied; only the extra keys are at fault")
	assert.Equal(t, []string{"*", "browser_*", "not_a_tool", "system.*"}, defects.Invalid)
}

// TestValidateSubmittedToolPolicyMap_MCPKeysAccepted pins the ONE carve-out
// CLAUDE.md hard constraint 6 grants: MCP-server tool names are not statically
// knowable, so `mcp_<server>_<tool>` entries and `mcp_<server>_*` bulk keys must
// pass through untouched. Rejecting them would break the per-server bulk control
// in the SPA's tool-policy editor, which derives exactly that key shape.
func TestValidateSubmittedToolPolicyMap_MCPKeysAccepted(t *testing.T) {
	known := testKnownTools("bash")
	submitted := map[string]string{
		"bash":                  "deny",
		"mcp_context7_*":        "ask",
		"mcp_context7_querydoc": "allow",
	}
	defects := ValidateSubmittedToolPolicyMap(submitted, known)
	assert.True(t, defects.Empty(),
		"MCP-namespaced keys are the documented exception and must not be rejected: %s", defects.String())
}

// TestValidateSubmittedToolPolicyMap_ReportsBothDefectKindsTogether: a single
// 400 must tell the caller everything that is wrong, not stop at the first
// problem — otherwise fixing an incomplete map only earns a second rejection.
func TestValidateSubmittedToolPolicyMap_ReportsBothDefectKindsTogether(t *testing.T) {
	known := testKnownTools("bash", "read_file", "write_file")
	submitted := map[string]string{
		"bash": "deny",
		"*":    "allow",
	}
	defects := ValidateSubmittedToolPolicyMap(submitted, known)
	require.False(t, defects.Empty())
	assert.Equal(t, []string{"read_file", "write_file"}, defects.Missing)
	assert.Equal(t, []string{"*"}, defects.Invalid)
	msg := defects.String()
	assert.Contains(t, msg, "read_file")
	assert.Contains(t, msg, "*")
}

// TestValidateSubmittedToolPolicyMap_EmptyKnownTools_NoDefects mirrors
// ValidateToolPolicyCoverage's own contract: an empty catalog means "nothing to
// check", never "everything is a gap". A handler whose catalog failed to build
// must not start 400-ing every write.
func TestValidateSubmittedToolPolicyMap_EmptyKnownTools_NoDefects(t *testing.T) {
	assert.True(t, ValidateSubmittedToolPolicyMap(map[string]string{"x": "allow"}, nil).Empty())
	assert.True(t, ValidateSubmittedToolPolicyMap(map[string]string{"x": "allow"}, map[string]struct{}{}).Empty())
}

// TestValidateSubmittedToolPolicyMap_DefectMessageTruncatesButKeepsExactCount:
// a full-catalog gap is ~88 names; the message must stay readable while still
// reporting the true count, so an operator can tell "one tool missing" from
// "the whole map is gone".
func TestValidateSubmittedToolPolicyMap_DefectMessageTruncatesButKeepsExactCount(t *testing.T) {
	known := make(map[string]struct{}, 40)
	for i := 0; i < 40; i++ {
		known[string(rune('a'+i%26))+"_tool_"+string(rune('0'+i/26))+string(rune('0'+i%10))] = struct{}{}
	}
	defects := ValidateSubmittedToolPolicyMap[string](nil, known)
	msg := defects.String()
	assert.Contains(t, msg, "40 static builtin tool(s) have no explicit policy entry",
		"the exact count must survive truncation")
	assert.Contains(t, msg, "more", "a long list must be truncated rather than dumped whole")
	assert.LessOrEqual(t, strings.Count(msg, ","), maxNamedToolsInDefectMessage+2,
		"truncation must actually bound the number of names spelled out")
}

// NOTE (ADR-077 D5): the ValidateAgentOwnToolPolicyCoverage test suite that
// used to live here (TestValidateAgentOwnToolPolicyCoverage_DetectsHoleCoveredOnlyByGlobalCeiling,
// _EmptyPolicyMapIsReported, _AgentWithNoToolsBlockIsSkipped,
// _CompleteAgentMapIsClean, _NilInputs_NoFindings) is retired along with the
// function it tested. Under the ratified two-layer model, an agent riding the
// global ceiling for a tool it never mentions is the NORMAL, intended state —
// see ValidateAgentOwnToolPolicyCoverage's retirement comment in
// pkg/config/validate.go. Removed by operator decision, ADR-077 — do not
// reintroduce.
