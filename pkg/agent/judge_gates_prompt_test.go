// License: MIT
// Copyright (c) 2026 Omnipus contributors
package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/require"
)

// TestBuildJudgeUserContent_DoDGatesRenderedAsGates pins ADR-080 D-DOD's
// split: outcome criteria (no provenance) keep the fail-closed rule under
// the prose-criteria heading; DoD items (any provenance) are rendered under
// the gates heading with the inverted burden of proof, so an empty output
// cannot fail "No secrets appear in the output".
//
// DIES ON: rendering every criterion under one heading again, dropping the
// gate rule text, or splitting on ProvenanceFloor only (a stated/workspace/
// inferred DoD item is a gate too — the ADR says the union is judged and
// provenance is what marks a DoD item).
func TestBuildJudgeUserContent_DoDGatesRenderedAsGates(t *testing.T) {
	criteria := []task.AcceptanceCriterion{
		{ID: "c-outcome", Kind: task.KindProse, Text: "The README documents the new flag."},
		{ID: "c-floor", Kind: task.KindProse, Provenance: task.ProvenanceFloor, Text: "No secrets or credentials appear in the output."},
		{ID: "c-stated", Kind: task.KindProse, Provenance: task.ProvenanceStated, Text: "No TODO comments remain."},
	}
	got, err := buildJudgeUserContent(criteria, nil, "", "", "", "")
	require.NoError(t, err)

	prose := strings.Index(got, "## Prose criteria to judge")
	gates := strings.Index(got, "## Definition-of-Done gates")
	require.NotEqual(t, -1, prose, "outcome criteria heading must be present")
	require.NotEqual(t, -1, gates, "gates heading must be present")
	require.Less(t, prose, gates, "outcome criteria come first, gates second")

	proseSection := got[prose:gates]
	gateSection := got[gates:]
	require.Contains(t, proseSection, `"c-outcome"`)
	require.NotContains(t, proseSection, `"c-floor"`)
	require.NotContains(t, proseSection, `"c-stated"`)
	require.Contains(t, gateSection, `"c-floor"`)
	require.Contains(t, gateSection, `"c-stated"`, "a stated DoD item is a gate too")
	require.NotContains(t, gateSection, `"c-outcome"`)

	// The rule that makes a gate judgeable when there is nothing to inspect.
	require.Contains(t, gateSection, "met=true unless the evidence")
	require.Contains(t, gateSection, "Absence of output")
	require.Contains(t, gateSection, "[REDACTED]")
}

// TestBuildJudgeUserContent_NoGatesNoGatesSection: a task/plan adjudication
// carries no DoD items; the prompt must not grow an empty gates section.
func TestBuildJudgeUserContent_NoGatesNoGatesSection(t *testing.T) {
	got, err := buildJudgeUserContent([]task.AcceptanceCriterion{
		{ID: "c1", Kind: task.KindProse, Text: "x"},
	}, nil, "", "", "", "")
	require.NoError(t, err)
	require.Contains(t, got, "## Prose criteria to judge")
	require.NotContains(t, got, "Definition-of-Done gates")
}

// TestBuildJudgeUserContent_OnlyGates: a goal whose every item is DoD (the
// floor alone, e.g. `/goal [check: true exit:0] please continue` after the
// machine criterion is verdicted by the engine) renders the gates section
// and no empty prose section.
func TestBuildJudgeUserContent_OnlyGates(t *testing.T) {
	got, err := buildJudgeUserContent(newFloorDoD(), nil, "", "", "", "")
	require.NoError(t, err)
	require.NotContains(t, got, "## Prose criteria to judge")
	require.Contains(t, got, "## Definition-of-Done gates")
	require.Contains(t, got, "goal-dod-floor-no-secrets")
	require.Contains(t, got, "goal-dod-floor-grounded-claims")
}
