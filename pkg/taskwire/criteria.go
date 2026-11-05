// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package taskwire

// criteria.go: task list fields, todos, trigger, acceptance criteria and
// Definition of Done in their generated inline wire shapes (moved unchanged
// from pkg/gateway so the REST surface and the read tools share them).

import (
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// wireTodo mirrors the gen.Task.Todos element inline type.
// not-wire-format: this is a local alias for the generated inline struct; see
// the gen.Task.Todos field for the authoritative shape.
type wireTodo = struct {
	ActiveForm *string             `json:"active_form,omitempty"`
	Status     gen.TaskTodosStatus `json:"status"`
	Text       string              `json:"text"`
}

// wireListFields copies a task's list-valued fields (blocked_by, todos,
// write_set, tags) onto its wire form. Each is omitted when empty and copied,
// never aliased.
func wireListFields(out *gen.Task, t task.Task) {
	if len(t.BlockedBy) > 0 {
		bb := append([]string{}, t.BlockedBy...)
		out.BlockedBy = &bb
	}
	if len(t.Todos) > 0 {
		todos := make([]wireTodo, 0, len(t.Todos))
		for _, td := range t.Todos {
			todos = append(todos, wireTodoOf(td))
		}
		out.Todos = &todos
	}
	if len(t.WriteSet) > 0 {
		ws := append([]string{}, t.WriteSet...)
		out.WriteSet = &ws
	}
	if len(t.Tags) > 0 {
		tags := append([]string{}, t.Tags...)
		out.Tags = &tags
	}
}

// wireTrigger maps an internal trigger to the gen.Task.Trigger inline type.
// The field type is an anonymous struct wrapping gen.Task_Trigger_Config so we
// build it in place and return via a temp gen.Task to extract the pointer type.
func wireTrigger(tr *task.Trigger) *struct {
	Config gen.Task_Trigger_Config `json:"config"`
	Type   gen.TaskTriggerType     `json:"type"`
} {
	cfg := gen.Task_Trigger_Config{}
	if tr.Config.AtMs != nil {
		v := *tr.Config.AtMs
		cfg.AtMs = &v
	}
	if tr.Config.Rrule != nil {
		v := *tr.Config.Rrule
		cfg.Rrule = &v
	}
	if tr.Config.DtstartMs != nil {
		v := *tr.Config.DtstartMs
		cfg.DtstartMs = &v
	}
	if tr.Config.Tz != nil {
		v := *tr.Config.Tz
		cfg.Tz = &v
	}
	return &struct {
		Config gen.Task_Trigger_Config `json:"config"`
		Type   gen.TaskTriggerType     `json:"type"`
	}{Config: cfg, Type: gen.TaskTriggerType(tr.Type)}
}

// wireCriteria converts internal acceptance criteria to the gen.Task.Criteria
// inline wire shape (read path — GET/POST/PATCH responses).
// CriterionJudgment returns a criterion's judgment for the wire, backfilling
// it via task.InferJudgment when empty. Persisted criteria authored before the
// ADR-080 judgment field carry no judgment; the response schema requires one
// (enum {boolean,quantitative,artifact}), so emitting "" makes the SPA's zod
// validation reject the whole payload. Mirrors goal_status_criteria.go's
// defensive backfill so a legacy task/plan is never returned schema-invalid.
func CriterionJudgment(c task.AcceptanceCriterion) task.JudgmentKind {
	if c.Judgment != "" {
		return c.Judgment
	}
	// Resolve an empty (legacy) judgment inline — mirrors task.InferJudgment's
	// correlation (behavior→quantitative, check/prose→boolean). Inlined rather
	// than calling InferJudgment so this defensive wire backfill is not a new
	// central-inference call site (TestInferJudgment_CallSitesPinned).
	if c.Kind == task.KindBehavior {
		return task.JudgmentQuantitative
	}
	return task.JudgmentBoolean
}

func wireCriteria(cs []task.AcceptanceCriterion) *[]struct {
	Author struct {
		Id   string                     `json:"id"`
		Kind gen.TaskCriteriaAuthorKind `json:"kind"`
	} `json:"author"`
	Behavior *struct {
		MaxCount *int                           `json:"max_count,omitempty"`
		MinCount *int                           `json:"min_count,omitempty"`
		Scope    *gen.TaskCriteriaBehaviorScope `json:"scope,omitempty"`
		Tool     string                         `json:"tool"`
	} `json:"behavior,omitempty"`
	Check *struct {
		Command          string `json:"command"`
		ExpectedExitCode int    `json:"expected_exit_code"`
	} `json:"check,omitempty"`
	ClauseCount *int                        `json:"clause_count,omitempty"`
	Id          *string                     `json:"id,omitempty"`
	Judgment    gen.TaskCriteriaJudgment    `json:"judgment"`
	Kind        gen.TaskCriteriaKind        `json:"kind"`
	Provenance  *gen.TaskCriteriaProvenance `json:"provenance,omitempty"`
	Status      gen.TaskCriteriaStatus      `json:"status"`
	Text        string                      `json:"text"`
} {
	out := make([]struct {
		Author struct {
			Id   string                     `json:"id"`
			Kind gen.TaskCriteriaAuthorKind `json:"kind"`
		} `json:"author"`
		Behavior *struct {
			MaxCount *int                           `json:"max_count,omitempty"`
			MinCount *int                           `json:"min_count,omitempty"`
			Scope    *gen.TaskCriteriaBehaviorScope `json:"scope,omitempty"`
			Tool     string                         `json:"tool"`
		} `json:"behavior,omitempty"`
		Check *struct {
			Command          string `json:"command"`
			ExpectedExitCode int    `json:"expected_exit_code"`
		} `json:"check,omitempty"`
		ClauseCount *int                        `json:"clause_count,omitempty"`
		Id          *string                     `json:"id,omitempty"`
		Judgment    gen.TaskCriteriaJudgment    `json:"judgment"`
		Kind        gen.TaskCriteriaKind        `json:"kind"`
		Provenance  *gen.TaskCriteriaProvenance `json:"provenance,omitempty"`
		Status      gen.TaskCriteriaStatus      `json:"status"`
		Text        string                      `json:"text"`
	}, 0, len(cs))
	for _, c := range cs {
		item := struct { // not-wire-format: intermediate value built to match gen.Task.Criteria's oapi-codegen anonymous element type, not a parallel wire type
			Author struct {
				Id   string                     `json:"id"`
				Kind gen.TaskCriteriaAuthorKind `json:"kind"`
			} `json:"author"`
			Behavior *struct {
				MaxCount *int                           `json:"max_count,omitempty"`
				MinCount *int                           `json:"min_count,omitempty"`
				Scope    *gen.TaskCriteriaBehaviorScope `json:"scope,omitempty"`
				Tool     string                         `json:"tool"`
			} `json:"behavior,omitempty"`
			Check *struct {
				Command          string `json:"command"`
				ExpectedExitCode int    `json:"expected_exit_code"`
			} `json:"check,omitempty"`
			ClauseCount *int                        `json:"clause_count,omitempty"`
			Id          *string                     `json:"id,omitempty"`
			Judgment    gen.TaskCriteriaJudgment    `json:"judgment"`
			Kind        gen.TaskCriteriaKind        `json:"kind"`
			Provenance  *gen.TaskCriteriaProvenance `json:"provenance,omitempty"`
			Status      gen.TaskCriteriaStatus      `json:"status"`
			Text        string                      `json:"text"`
		}{
			Kind:     gen.TaskCriteriaKind(c.Kind),
			Judgment: gen.TaskCriteriaJudgment(CriterionJudgment(c)),
			Status:   gen.TaskCriteriaStatus(c.Status),
			Text:     c.Text,
		}
		if c.Provenance != "" {
			p := gen.TaskCriteriaProvenance(c.Provenance)
			item.Provenance = &p
		}
		// C-58/JUDGE-FR-006b: this field was declared in the anonymous wire
		// shape (matching the generated type) but never actually populated —
		// a real GET response carried clause_count:null even though the
		// persisted criterion always has one. ClauseCount is minimum:1 on
		// the schema, so a zero (never-normalized, pre-FR-006b) value is
		// left absent rather than emitted as an invalid 0.
		if c.ClauseCount > 0 {
			cc := c.ClauseCount
			item.ClauseCount = &cc
		}
		item.Author.Id = c.Author.ID
		item.Author.Kind = gen.TaskCriteriaAuthorKind(c.Author.Kind)
		if c.ID != "" {
			item.Id = ptr(c.ID)
		}
		if c.Check != nil {
			item.Check = &struct {
				Command          string `json:"command"`
				ExpectedExitCode int    `json:"expected_exit_code"`
			}{Command: c.Check.Command, ExpectedExitCode: c.Check.ExpectedExitCode}
		}
		if c.Behavior != nil {
			beh := &struct { // not-wire-format: intermediate value built to match gen.Task.Criteria's oapi-codegen anonymous element type, not a parallel wire type
				MaxCount *int                           `json:"max_count,omitempty"`
				MinCount *int                           `json:"min_count,omitempty"`
				Scope    *gen.TaskCriteriaBehaviorScope `json:"scope,omitempty"`
				Tool     string                         `json:"tool"`
			}{
				// MinCount/MaxCount are passed straight through — both are
				// already *int on task.CriterionBehavior (fix-wave finding
				// #5), so no ptr()-wrap is needed (or type-correct: wrapping
				// an already-*int value would produce **int). This also
				// preserves the nil/0 distinction on read: an
				// explicitly-zero MinCount round-trips as 0, not defaulted —
				// the wire's own `default: 1` (schema) is authoritative only
				// for an ABSENT create/update request field, never for what
				// GET echoes back (fix-wave finding #6).
				Tool:     c.Behavior.Tool,
				MinCount: c.Behavior.MinCount,
				MaxCount: c.Behavior.MaxCount,
			}
			if c.Behavior.Scope != "" {
				s := gen.TaskCriteriaBehaviorScope(c.Behavior.Scope)
				beh.Scope = &s
			}
			item.Behavior = beh
		}
		out = append(out, item)
	}
	return &out
}

// Dod converts internal Definition-of-Done criteria to the gen.Task.Dod
// inline wire shape (read path — GET/POST/PATCH responses). GOAL-FR-021/
// FR-029/FR-048: dod is a NEW field (ADR-086) — the task's DoD list lives on
// its paired goal record (pkg/goal), never on the task record itself, so
// callers of this function read from a *goal.Goal, not from task.Task.
//
// This is a distinct, near-duplicate function rather than a shared one with
// wireCriteria because oapi-codegen emits TWO separate named anonymous-
// struct-element types for Task.Criteria and Task.Dod even though their
// shapes are structurally identical (same reason wireCriteria/
// wirePlanDoD in plan.go cannot be shared either — see wirePlanDoD's
// own doc comment).
func Dod(cs []task.AcceptanceCriterion) *[]struct {
	Author struct {
		Id   string                `json:"id"`
		Kind gen.TaskDodAuthorKind `json:"kind"`
	} `json:"author"`
	Behavior *struct {
		MaxCount *int                      `json:"max_count,omitempty"`
		MinCount *int                      `json:"min_count,omitempty"`
		Scope    *gen.TaskDodBehaviorScope `json:"scope,omitempty"`
		Tool     string                    `json:"tool"`
	} `json:"behavior,omitempty"`
	Check *struct {
		Command          string `json:"command"`
		ExpectedExitCode int    `json:"expected_exit_code"`
	} `json:"check,omitempty"`
	ClauseCount *int                   `json:"clause_count,omitempty"`
	Id          *string                `json:"id,omitempty"`
	Judgment    gen.TaskDodJudgment    `json:"judgment"`
	Kind        gen.TaskDodKind        `json:"kind"`
	Provenance  *gen.TaskDodProvenance `json:"provenance,omitempty"`
	Status      gen.TaskDodStatus      `json:"status"`
	Text        string                 `json:"text"`
} {
	out := make([]struct {
		Author struct {
			Id   string                `json:"id"`
			Kind gen.TaskDodAuthorKind `json:"kind"`
		} `json:"author"`
		Behavior *struct {
			MaxCount *int                      `json:"max_count,omitempty"`
			MinCount *int                      `json:"min_count,omitempty"`
			Scope    *gen.TaskDodBehaviorScope `json:"scope,omitempty"`
			Tool     string                    `json:"tool"`
		} `json:"behavior,omitempty"`
		Check *struct {
			Command          string `json:"command"`
			ExpectedExitCode int    `json:"expected_exit_code"`
		} `json:"check,omitempty"`
		ClauseCount *int                   `json:"clause_count,omitempty"`
		Id          *string                `json:"id,omitempty"`
		Judgment    gen.TaskDodJudgment    `json:"judgment"`
		Kind        gen.TaskDodKind        `json:"kind"`
		Provenance  *gen.TaskDodProvenance `json:"provenance,omitempty"`
		Status      gen.TaskDodStatus      `json:"status"`
		Text        string                 `json:"text"`
	}, 0, len(cs))
	for _, c := range cs {
		item := struct { // not-wire-format: intermediate value built to match gen.Task.Dod's oapi-codegen anonymous element type, not a parallel wire type
			Author struct {
				Id   string                `json:"id"`
				Kind gen.TaskDodAuthorKind `json:"kind"`
			} `json:"author"`
			Behavior *struct {
				MaxCount *int                      `json:"max_count,omitempty"`
				MinCount *int                      `json:"min_count,omitempty"`
				Scope    *gen.TaskDodBehaviorScope `json:"scope,omitempty"`
				Tool     string                    `json:"tool"`
			} `json:"behavior,omitempty"`
			Check *struct {
				Command          string `json:"command"`
				ExpectedExitCode int    `json:"expected_exit_code"`
			} `json:"check,omitempty"`
			ClauseCount *int                   `json:"clause_count,omitempty"`
			Id          *string                `json:"id,omitempty"`
			Judgment    gen.TaskDodJudgment    `json:"judgment"`
			Kind        gen.TaskDodKind        `json:"kind"`
			Provenance  *gen.TaskDodProvenance `json:"provenance,omitempty"`
			Status      gen.TaskDodStatus      `json:"status"`
			Text        string                 `json:"text"`
		}{
			Kind:     gen.TaskDodKind(c.Kind),
			Judgment: gen.TaskDodJudgment(CriterionJudgment(c)),
			Status:   gen.TaskDodStatus(c.Status),
			Text:     c.Text,
		}
		if c.Provenance != "" {
			p := gen.TaskDodProvenance(c.Provenance)
			item.Provenance = &p
		}
		if c.ClauseCount > 0 {
			cc := c.ClauseCount
			item.ClauseCount = &cc
		}
		item.Author.Id = c.Author.ID
		item.Author.Kind = gen.TaskDodAuthorKind(c.Author.Kind)
		if c.ID != "" {
			item.Id = ptr(c.ID)
		}
		if c.Check != nil {
			item.Check = &struct {
				Command          string `json:"command"`
				ExpectedExitCode int    `json:"expected_exit_code"`
			}{Command: c.Check.Command, ExpectedExitCode: c.Check.ExpectedExitCode}
		}
		if c.Behavior != nil {
			beh := &struct { // not-wire-format: intermediate value built to match gen.Task.Dod's oapi-codegen anonymous element type, not a parallel wire type
				MaxCount *int                      `json:"max_count,omitempty"`
				MinCount *int                      `json:"min_count,omitempty"`
				Scope    *gen.TaskDodBehaviorScope `json:"scope,omitempty"`
				Tool     string                    `json:"tool"`
			}{
				Tool:     c.Behavior.Tool,
				MinCount: c.Behavior.MinCount,
				MaxCount: c.Behavior.MaxCount,
			}
			if c.Behavior.Scope != "" {
				s := gen.TaskDodBehaviorScope(c.Behavior.Scope)
				beh.Scope = &s
			}
			item.Behavior = beh
		}
		out = append(out, item)
	}
	return &out
}

func wireTodoOf(td task.Todo) wireTodo {
	out := wireTodo{Text: td.Text, Status: gen.TaskTodosStatus(td.Status)}
	if td.ActiveForm != "" {
		out.ActiveForm = &td.ActiveForm
	}
	return out
}
