// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-074 D7 (test 18, wire half): evidence_quote flows through BOTH
// generated-type converters — toWireJudgeVerdict (REST Message.verdict
// shape) and toJudgeVerdictFrame (asyncapi WS frame) — as an optional,
// EMPTY-SAFE field: a populated quote crosses verbatim, an empty one stays
// absent from the wire (nil pointer, omitempty), matching the "no quote
// line on fail-closed/pre-D7 verdicts" render rule.
package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func judgeVerdictWithQuotes() task.JudgeVerdict {
	return task.JudgeVerdict{
		ID:           "v1",
		Scope:        task.VerdictScopeTask,
		TaskID:       "t1",
		Round:        1,
		Met:          false,
		Model:        "test-model",
		JudgedAt:     "2026-09-06T12:00:00Z",
		JudgeAgentID: "judge",
		PerCriterion: []task.CriterionVerdict{
			{CriterionID: "c1", Met: true, Reason: "quoted", EvidenceQuote: "--- PASS: TestX"},
			{CriterionID: "c2", Met: false, Reason: "fail-closed, no quote"},
		},
	}
}

func TestToWireJudgeVerdict_EvidenceQuote(t *testing.T) {
	out := toWireJudgeVerdict(judgeVerdictWithQuotes())
	require.Len(t, out.PerCriterion, 2)

	require.NotNil(t, out.PerCriterion[0].EvidenceQuote)
	assert.Equal(t, "--- PASS: TestX", *out.PerCriterion[0].EvidenceQuote)

	// Empty quote → absent from the wire, never a present "".
	assert.Nil(t, out.PerCriterion[1].EvidenceQuote)
}

func TestToJudgeVerdictFrame_EvidenceQuote(t *testing.T) {
	f := toJudgeVerdictFrame(judgeVerdictWithQuotes())
	require.Len(t, f.PerCriterion, 2)

	require.NotNil(t, f.PerCriterion[0].EvidenceQuote)
	assert.Equal(t, "--- PASS: TestX", *f.PerCriterion[0].EvidenceQuote)
	assert.Nil(t, f.PerCriterion[1].EvidenceQuote)
}
