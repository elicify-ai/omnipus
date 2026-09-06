// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_status_criteria.go — ADR-074 D5.2 / judgment-first FR-011: maps the
// internal compiled-criteria breakdown (task.AcceptanceCriterion, carried on
// agent.GoalStatusChangedPayload's `queued` pending-confirm emission) onto
// generated.GoalStatusFrame's optional `criteria` wire field, so the SPA's
// goal echo card itemizes exactly what will run. Mirrors the
// toJudgeVerdictFrame converter pattern: generated types only, inline
// anonymous-struct literals matching the codegen output exactly.
package gateway

import (
	"slices"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// setGoalStatusCriteria fills f.Criteria from in. A nil/empty in leaves
// f.Criteria nil — the optional wire field stays absent on every
// round/lifecycle emission (only the `queued` pending-confirm payload
// carries a breakdown). The element type is the generated anonymous struct;
// elements are grown zero-valued and assigned field-by-field so the one
// unavoidable type spelling is confined to the two per-kind payload
// pointers.
func setGoalStatusCriteria(f *generated.GoalStatusFrame, in []task.AcceptanceCriterion) {
	if len(in) == 0 {
		return
	}
	f.Criteria = slices.Grow(f.Criteria, len(in))[:len(in)]
	for i := range in {
		src := &in[i]
		dst := &f.Criteria[i]
		dst.Kind = string(src.Kind)
		dst.Judgment = string(src.Judgment)
		if dst.Judgment == "" {
			// Defensive backfill (fix-wave finding, mirrors the Status
			// pending-default below): every persisted criterion should already
			// carry an explicit judgment via normalizeCriteria/InferJudgment,
			// but this converter also serves pre-normalization callers (e.g.
			// compiledGoalCriteriaFor's back-compat fallback) — never emit an
			// empty judgment onto the wire, where it is a required enum field.
			dst.Judgment = string(task.JudgmentBoolean)
		}
		if src.Provenance != "" {
			p := string(src.Provenance)
			dst.Provenance = &p
		}
		dst.Text = src.Text
		dst.Status = string(src.Status)
		if dst.Status == "" {
			// Compiled-but-unjudged criteria are pending by definition; the
			// wire enum requires an explicit value (NFR-2: never default to
			// met — pending is the fail-closed zero state).
			dst.Status = string(task.CritPending)
		}
		dst.Author.Kind = src.Author.Kind
		dst.Author.Id = src.Author.ID
		if src.ID != "" {
			id := src.ID
			dst.Id = &id
		}
		if src.Check != nil {
			dst.Check = &struct {
				Command          string `json:"command"`
				ExpectedExitCode int    `json:"expected_exit_code"`
			}{Command: src.Check.Command, ExpectedExitCode: src.Check.ExpectedExitCode}
		}
		if src.Behavior != nil {
			var minCount, maxCount *int
			if src.Behavior.MinCount != nil {
				v := *src.Behavior.MinCount
				minCount = &v
			}
			if src.Behavior.MaxCount != nil {
				v := *src.Behavior.MaxCount
				maxCount = &v
			}
			var scope *string
			if src.Behavior.Scope != "" {
				s := string(src.Behavior.Scope)
				scope = &s
			}
			dst.Behavior = &struct {
				MaxCount *int    `json:"max_count,omitempty"`
				MinCount *int    `json:"min_count,omitempty"`
				Scope    *string `json:"scope,omitempty"`
				Tool     string  `json:"tool"`
			}{MaxCount: maxCount, MinCount: minCount, Scope: scope, Tool: src.Behavior.Tool}
		}
	}
}
