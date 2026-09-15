// rest_task_wire.go: Translate task records to and from the wire: criteria, Definition of Done, status mapping

package gateway

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// rollupIndex groups every task in a per-request snapshot by ParentTaskID, so
// a single upstream task.Store.List call can serve computeRollup for every
// task returned by a list-shaped endpoint. Before this, computeRollup issued
// one additional full Store.List scan PER RETURNED TASK, making
// GET /api/v1/tasks (and GET /tasks/{id}/subtasks) O(n^2) in the number of
// task files on disk — 200 returned tasks against a 2000-task store meant
// ~400k file reads for one request (fix-wave finding #1a). nil is a valid,
// common value of this type: every single-task endpoint (get/create/patch/
// stop/restart/...) passes nil and computeRollup falls back to its own,
// bounded (one-call) List.
type rollupIndex map[string][]task.Task

// buildRollupIndex fetches every task in the store ONCE — an unfiltered
// Store.List, matching computeRollup's own pre-existing (unscoped-by-
// workspace) ParentTaskID filter semantics exactly — and groups the results
// by ParentTaskID for O(1) lookups. handleTaskList and handleTaskSubtasks
// build this once per request and thread it through toWireTask/computeRollup
// for every task in the batch, replacing the previous
// one-List-call-per-returned-task pattern.
func (a *restAPI) buildRollupIndex() (rollupIndex, error) {
	all, err := a.taskStore.List(task.Filter{})
	if err != nil {
		return nil, err
	}
	idx := make(rollupIndex, len(all))
	for _, t := range all {
		if t.ParentTaskID == "" {
			continue
		}
		idx[t.ParentTaskID] = append(idx[t.ParentTaskID], t)
	}
	return idx, nil
}

// taskGoalIndex maps a task-owned goal record by its owner task id (ADR-086
// D2/D5, GOAL-FR-029). A task's Definition of Done lives on its paired goal
// record, never on the task record itself, so projecting `dod` onto the wire
// requires a goal-store lookup — this is the batch-caller index for that
// lookup, mirroring rollupIndex exactly (goal.Store.GetByOwner scans the
// WHOLE goals directory per call, so a list endpoint calling it once per
// returned task would be the same per-item-List-call trap rollupIndex's own
// doc comment already names). nil is a valid, common value: every
// single-task endpoint (get/create/patch/stop/restart/...) passes nil and
// toWireTask falls back to one direct GetByOwner call.
type taskGoalIndex map[string]*goal.Goal

// buildTaskGoalIndex fetches every goal record ONCE (goal.Store.List) and
// indexes the task-owned ones by their owner task id. Mirrors
// buildRollupIndex exactly — see taskGoalIndex's doc comment for why this
// exists at all.
func (a *restAPI) buildTaskGoalIndex() (taskGoalIndex, error) {
	all, _, err := tools.GoalStoreForTasks(a.taskStore).List()
	if err != nil {
		return nil, err
	}
	idx := make(taskGoalIndex, len(all))
	for i := range all {
		g := all[i]
		if g.OwnerKind != gen.GoalOwnerKindTask || g.OwnerID == "" {
			continue
		}
		idx[g.OwnerID] = &g
	}
	return idx, nil
}

// taskLastActivityAt resolves Task.last_activity_at for an in-progress task:
// the LATER of (a) the live progress stamp of its running turn — which moves
// on every streamed reasoning and tool-call-argument delta (UAT E-15c) — and
// (b) the last write to its session transcript (a tool result, an assistant
// message; the session store's cached UpdatedAt moves on every append).
// Returns ok=false when the task is not in progress or no evidence exists —
// the field is then simply absent on the wire, never fabricated.
func (a *restAPI) taskLastActivityAt(t task.Task) (time.Time, bool) {
	if t.Status != task.StatusInProgress {
		return time.Time{}, false
	}
	var live, transcript time.Time
	if a.liveTaskActivity != nil {
		if at, ok := a.liveTaskActivity.LiveTaskLastActivity(t.ID); ok {
			live = at
		}
	}
	if t.SessionID != "" {
		if store := a.resolveSessionStore(t.SessionID); store != nil {
			if meta, err := store.GetMeta(t.SessionID); err == nil && meta != nil && !meta.UpdatedAt.IsZero() {
				transcript = meta.UpdatedAt
			}
		}
	}
	// Local-time skew defence: a stamp from the future (clock jump between
	// nodes) still renders as "just now", never a negative age.
	now := time.Now()
	switch {
	case live.After(transcript) && !live.After(now):
		return live, true
	case transcript.After(live) && !transcript.After(now):
		return transcript, true
	case live.After(now) || transcript.After(now):
		// Both candidates in the future — fall back to the later one anyway;
		// the SPA clamps negative ages to "just now".
		if live.After(transcript) {
			return live, true
		}
		return transcript, true
	default:
		return time.Time{}, false
	}
}

// taskAssigneeWarning resolves Task.assignee_warning (founder decision
// 2026-09-15): why t's assigned agent cannot finish it, from the SAME answer
// the task run's pre-run check and the agent task tools use
// (agent.AgentLoop.TaskAssigneeCannotFinish), judged against the criteria and
// Definition of Done on its goal record g — else the task's own criteria.
// Returns "" when the task has no agent, is done or failed, is a checklist
// card, or nothing knowable stops the agent.
//
// This surface warns and never refuses. The agent task tools reject the same
// assignment, but an operator using the task form may assign first and fix
// the agent's permissions afterwards — ADR-049 D2 rule 5's split, and the
// planning-goals-spec scenario "the same shape via the human UI path is
// accepted with a warning". A run of such a task ends Failed at once with this
// same text.
func (a *restAPI) taskAssigneeWarning(t task.Task, g *goal.Goal) string {
	if a.agentLoop == nil || t.AgentID == "" || t.Scratchpad || task.IsTerminal(t.Status) {
		return ""
	}
	var judged []task.AcceptanceCriterion
	if g != nil && len(g.Criteria) > 0 {
		judged = append(judged, g.Criteria...)
	} else {
		judged = append(judged, t.Criteria...)
	}
	if g != nil {
		judged = append(judged, g.DoD...)
	}
	return a.agentLoop.TaskAssigneeCannotFinish(t.AgentID, judged)
}

// --- wire mapping -----------------------------------------------------------

// wireTodo mirrors the gen.Task.Todos element inline type.
// not-wire-format: this is a local alias for the generated inline struct; see
// the gen.Task.Todos field for the authoritative shape.
type wireTodo = struct {
	Status gen.TaskTodosStatus `json:"status"`
	Text   string              `json:"text"`
}

// toWireTask converts an internal task.Task to the generated wire type, filling
// the read-time agent_name and rollup fields from the registry / store. idx is
// an optional shared rollupIndex (see its doc comment) for batch callers; pass
// nil for a single-task response. gidx is the analogous optional shared
// taskGoalIndex for `dod` (ADR-086 D5) — pass nil for a single-task response.
//
// It returns a non-nil error ONLY when the task's paired goal record exists as
// far as we know but could not be READ (a storage fault, a corrupt record).
// That is not the same thing as a task having no Definition of Done, and the
// wire type cannot say the difference: `dod` is omitempty, so a read failure
// used to render byte-identically to "this task genuinely has none" (silent-
// failure finding SF-6). With D-C making a Definition of Done mandatory at
// creation AND edit, "none" is no longer a believable answer — a reader seeing
// an empty DoD would conclude the task has none and act on it. So the error
// travels up and the handler answers 500 instead of inventing an empty field.
// goal.ErrOwnerNotFound is NOT an error here: a task with no goal record at all
// really does have no dod, which is the normal state for a pre-D-C task.
func (a *restAPI) toWireTask(t task.Task, idx rollupIndex, gidx taskGoalIndex) (gen.Task, error) {
	out := gen.Task{
		Id:          t.ID,
		Title:       t.Title,
		Action:      gen.TaskAction(t.Action),
		Status:      gen.TaskStatus(t.Status),
		WorkspaceId: t.WorkspaceID,
		Owner:       t.Owner,
		CreatedBy:   t.CreatedBy,
	}
	if out.Action == "" {
		out.Action = gen.TaskAction(task.ActionLLM)
	}

	prio := t.EffectivePriority()
	out.Priority = &prio
	surface := gen.TaskSurface(t.EffectiveSurface())
	out.Surface = &surface

	if t.Description != "" {
		out.Description = ptr(t.Description)
	}
	if t.Prompt != "" {
		out.Prompt = ptr(t.Prompt)
	}
	if t.AgentID != "" {
		out.AgentId = ptr(t.AgentID)
		if name := a.resolveAgentName(t.AgentID); name != "" {
			out.AgentName = ptr(name)
		}
	}
	if len(t.BlockedBy) > 0 {
		bb := append([]string{}, t.BlockedBy...)
		out.BlockedBy = &bb
	}
	if len(t.Todos) > 0 {
		todos := make([]wireTodo, 0, len(t.Todos))
		for _, td := range t.Todos {
			todos = append(todos, wireTodo{Text: td.Text, Status: gen.TaskTodosStatus(td.Status)})
		}
		out.Todos = &todos
	}
	if t.ParentTaskID != "" {
		out.ParentTaskId = ptr(t.ParentTaskID)
	}
	if t.PlanID != "" {
		out.PlanId = ptr(t.PlanID)
	}
	if len(t.WriteSet) > 0 {
		ws := append([]string{}, t.WriteSet...)
		out.WriteSet = &ws
	}
	if t.Stream != "" {
		out.Stream = ptr(t.Stream)
	}
	if t.IsJoin {
		out.IsJoin = ptr(t.IsJoin)
	}
	if len(t.Tags) > 0 {
		tags := append([]string{}, t.Tags...)
		out.Tags = &tags
	}
	// GOAL-FR-003/FR-029/FR-048 (ADR-086 D5): BOTH judged lists — criteria
	// and Definition of Done — live on the task's paired goal record and the
	// wire reads them from there, one source of truth. The task record's own
	// Criteria dual-write is write-only for the not-yet-repointed writers;
	// reading it here is what made a retried task's card show the previous
	// run's criterion ticks beside a freshly-reset DoD (the goal record is
	// the side Reactivate resets). gidx (batch caller) or a direct GetByOwner
	// (single-task caller) resolves the record; a task with no goal record at
	// all (a legacy, pre-D-C task — GOAL-FR-023/FR-048) has neither list,
	// which is the normal, non-error state, and falls back to the task
	// record's criteria for continuity.
	var g *goal.Goal
	if gidx != nil {
		g = gidx[t.ID]
	} else if a.taskStore != nil {
		found, gErr := tools.GoalStoreForTasks(a.taskStore).GetByOwner(gen.GoalOwnerKindTask, t.ID)
		switch {
		case gErr == nil:
			g = found
		case errors.Is(gErr, goal.ErrOwnerNotFound):
			// Genuinely no paired record. Normal state, not an error.
		default:
			// A real read failure. Do NOT fall through and emit a task with
			// judged lists read from a possibly-stale second copy — that is
			// indistinguishable from a task that has none (SF-6).
			return gen.Task{}, fmt.Errorf(
				"read paired goal record for task %q: %w", t.ID, gErr)
		}
	}
	if g != nil && len(g.Criteria) > 0 {
		out.Criteria = toWireCriteria(g.Criteria)
	} else if len(t.Criteria) > 0 {
		out.Criteria = toWireCriteria(t.Criteria)
	}
	if g != nil && len(g.DoD) > 0 {
		out.Dod = toWireDod(g.DoD)
	}
	// Founder decision 2026-09-15: a task whose agent cannot finish it as
	// configured says so, next to the agent picker. Read-time only; it warns
	// and never refuses (see taskAssigneeWarning).
	if msg := a.taskAssigneeWarning(t, g); msg != "" {
		out.AssigneeWarning = &struct {
			Field   gen.TaskAssigneeWarningField `json:"field"`
			Message string                       `json:"message"`
		}{Field: gen.TaskAssigneeWarningFieldAgentId, Message: msg}
	}
	// The goal's tries (issue #710): the tries its current (or last) run has
	// used and the try limit that run started with, both read off the goal
	// record — the card shows them beside the task attempt counter, never
	// mixed with it.
	if g != nil {
		if g.Round > 0 {
			out.JudgeRounds = ptr(g.Round)
		}
		if g.MaxRounds >= 1 {
			out.GoalMaxRounds = ptr(g.MaxRounds)
		}
	}
	if t.AttemptCount > 0 {
		out.AttemptCount = ptr(t.AttemptCount)
	}
	if t.MaxAttempts != nil {
		out.MaxAttempts = ptr(*t.MaxAttempts)
	}
	// effective_max_attempts: the task attempt limit (how many fresh runs this
	// task gets), resolved by the SAME function the task executor enforces
	// (tools.EffectiveTaskMaxAttempts): the task's own max_attempts, else the
	// global planning.task_max_attempts, else the default of 3. It is not the
	// goal try limit — goal tries and task attempts are separate limits
	// (founder decision 2026-09-14, issue #710).
	var planning config.PlanningConfig
	if a.agentLoop != nil {
		if cfg := a.agentLoop.GetConfig(); cfg != nil {
			planning = cfg.Planning
		}
	}
	out.EffectiveMaxAttempts = ptr(tools.EffectiveTaskMaxAttempts(planning, &t))
	if t.Trigger != nil {
		out.Trigger = toWireTrigger(t.Trigger)
	}
	if t.Due != "" {
		if ts, err := time.Parse(time.RFC3339, t.Due); err == nil {
			out.Due = &ts
		}
	}
	if t.SourceChannel != "" {
		out.SourceChannel = ptr(t.SourceChannel)
	}
	if t.SourceChatID != "" {
		out.SourceChatId = ptr(t.SourceChatID)
	}
	if t.SessionID != "" {
		out.SessionId = ptr(t.SessionID)
	}
	if t.Result != "" {
		out.Result = ptr(t.Result)
	}
	if t.CancelReason != "" {
		cr := gen.TaskCancelReason(t.CancelReason)
		out.CancelReason = &cr
	}
	if len(t.Artifacts) > 0 {
		arts := append([]string{}, t.Artifacts...)
		out.Artifacts = &arts
	}

	out.CreatedAt = parseTimeOrNow(t.CreatedAt)
	out.UpdatedAt = parseTimeOrNow(t.UpdatedAt)
	if t.StartedAt != "" {
		if ts, err := time.Parse(time.RFC3339, t.StartedAt); err == nil {
			out.StartedAt = &ts
		}
	}
	if t.CompletedAt != "" {
		if ts, err := time.Parse(time.RFC3339, t.CompletedAt); err == nil {
			out.CompletedAt = &ts
		}
	}
	// Founder decision 2026-09-14: last_activity_at, read-time only, only
	// while the task is in progress (see taskLastActivityAt).
	if at, ok := a.taskLastActivityAt(t); ok {
		at = at.UTC()
		out.LastActivityAt = &at
	}

	// Read-time rollup: live child sub-agent runs (parent_task_id == t.ID).
	out.Rollup = a.computeRollup(t.ID, idx)
	return out, nil
}

// writeWireTask renders t and writes it as the response body, or answers 500
// when the task could not be rendered COMPLETELY — today that means its paired
// goal record (its Definition of Done) could not be read. Every single-task
// endpoint (get / create / patch / stop / restart / …) goes through here so
// that they all fail the same way; the batch endpoints keep their own
// buildTaskGoalIndex error branch, which already 500s.
//
// Answering 500 rather than a 200 with the DoD quietly missing is the point
// (SF-6): `dod` is omitempty, so a partial render is indistinguishable on the
// wire from a task that genuinely has no Definition of Done.
func (a *restAPI) writeWireTask(w http.ResponseWriter, status int, t task.Task) {
	wire, err := a.toWireTask(t, nil, nil)
	if err != nil {
		slog.Error("rest: could not render task completely", "task_id", t.ID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf(
			"task %q could not be read completely: its Definition of Done is unavailable", t.ID))
		return
	}
	if status == http.StatusCreated {
		jsonCreated(w, wire)
		return
	}
	jsonOK(w, wire)
}

// toWireTrigger maps an internal trigger to the gen.Task.Trigger inline type.
// The field type is an anonymous struct wrapping gen.Task_Trigger_Config so we
// build it in place and return via a temp gen.Task to extract the pointer type.
func toWireTrigger(tr *task.Trigger) *struct {
	Config gen.Task_Trigger_Config `json:"config"`
	Type   gen.TaskTriggerType     `json:"type"`
} {
	cfg := gen.Task_Trigger_Config{}
	if tr.Config.AtMs != nil {
		v := *tr.Config.AtMs
		cfg.AtMs = &v
	}
	if tr.Config.EveryMs != nil {
		v := *tr.Config.EveryMs
		cfg.EveryMs = &v
	}
	if tr.Config.CronExpr != nil {
		v := *tr.Config.CronExpr
		cfg.CronExpr = &v
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

// buildTrigger constructs an internal trigger from its primitive parts. The
// three generated request structs (Task/TaskCreateRequest/TaskUpdateRequest) each
// have their own anonymous trigger type with an identically-shaped config, so
// the callers decompose them and pass the primitives here.
func buildTrigger(
	kind string,
	atMs, everyMs *int64,
	cronExpr, rrule *string,
	dtstartMs *int64,
	tz *string,
) *task.Trigger {
	tr := &task.Trigger{Type: task.TriggerType(kind)}
	if atMs != nil {
		v := *atMs
		tr.Config.AtMs = &v
	}
	if everyMs != nil {
		v := *everyMs
		tr.Config.EveryMs = &v
	}
	if cronExpr != nil {
		v := *cronExpr
		tr.Config.CronExpr = &v
	}
	if rrule != nil {
		v := *rrule
		tr.Config.Rrule = &v
	}
	if dtstartMs != nil {
		v := *dtstartMs
		tr.Config.DtstartMs = &v
	}
	if tz != nil {
		v := *tz
		tr.Config.Tz = &v
	}
	return tr
}

// toWireCriteria converts internal acceptance criteria to the gen.Task.Criteria
// inline wire shape (read path — GET/POST/PATCH responses).
// wireCriterionJudgment returns a criterion's judgment for the wire, backfilling
// it via task.InferJudgment when empty. Persisted criteria authored before the
// ADR-080 judgment field carry no judgment; the response schema requires one
// (enum {boolean,quantitative,artifact}), so emitting "" makes the SPA's zod
// validation reject the whole payload. Mirrors goal_status_criteria.go's
// defensive backfill so a legacy task/plan is never returned schema-invalid.
func wireCriterionJudgment(c task.AcceptanceCriterion) task.JudgmentKind {
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

func toWireCriteria(cs []task.AcceptanceCriterion) *[]struct {
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
			Judgment: gen.TaskCriteriaJudgment(wireCriterionJudgment(c)),
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

// criteriaFromCreateWire converts the gen.TaskCreateRequest.Criteria inline
// wire shape to internal acceptance criteria (create path).
func criteriaFromCreateWire(items []struct {
	Author struct {
		Id   string                                  `json:"id"`
		Kind gen.TaskCreateRequestCriteriaAuthorKind `json:"kind"`
	} `json:"author"`
	Behavior *struct {
		MaxCount *int                                        `json:"max_count,omitempty"`
		MinCount *int                                        `json:"min_count,omitempty"`
		Scope    *gen.TaskCreateRequestCriteriaBehaviorScope `json:"scope,omitempty"`
		Tool     string                                      `json:"tool"`
	} `json:"behavior,omitempty"`
	Check *struct {
		Command          string `json:"command"`
		ExpectedExitCode int    `json:"expected_exit_code"`
	} `json:"check,omitempty"`
	ClauseCount *int                                     `json:"clause_count,omitempty"`
	Id          *string                                  `json:"id,omitempty"`
	Judgment    *gen.TaskCreateRequestCriteriaJudgment   `json:"judgment,omitempty"`
	Kind        *gen.TaskCreateRequestCriteriaKind       `json:"kind,omitempty"`
	Provenance  *gen.TaskCreateRequestCriteriaProvenance `json:"provenance,omitempty"`
	Status      gen.TaskCreateRequestCriteriaStatus      `json:"status"`
	Text        string                                   `json:"text"`
}) []task.AcceptanceCriterion {
	out := make([]task.AcceptanceCriterion, 0, len(items))
	for _, it := range items {
		c := task.AcceptanceCriterion{
			Text:   it.Text,
			Status: task.CriterionStatus(it.Status),
			Author: task.CriterionAuthor{Kind: string(it.Author.Kind), ID: it.Author.Id},
		}
		// ADR-074 D2 (spec FR-002) / ADR-080 D-TYPES: the gateway performs NO
		// kind/judgment defaulting — an absent kind or judgment passes THROUGH
		// as empty and is inferred downstream by the store's normalizeCriteria
		// (task.InferCriterionKind / task.InferJudgment).
		if it.Kind != nil {
			c.Kind = task.CriterionKind(*it.Kind)
		}
		if it.Judgment != nil {
			c.Judgment = task.JudgmentKind(*it.Judgment)
		}
		if it.Provenance != nil {
			c.Provenance = task.CriterionProvenance(*it.Provenance)
		}
		if it.Id != nil {
			c.ID = *it.Id
		}
		if it.Check != nil {
			c.Check = &task.CriterionCheck{Command: it.Check.Command, ExpectedExitCode: it.Check.ExpectedExitCode}
		}
		if it.Behavior != nil {
			c.Behavior = behaviorFromWire(it.Behavior.Tool, it.Behavior.MinCount, it.Behavior.MaxCount, it.Behavior.Scope)
		}
		out = append(out, c)
	}
	return out
}

// criteriaFromUpdateWire converts the gen.TaskUpdateRequest.Criteria inline
// wire shape to internal acceptance criteria (PATCH path).
func criteriaFromUpdateWire(items []struct {
	Author struct {
		Id   string                                  `json:"id"`
		Kind gen.TaskUpdateRequestCriteriaAuthorKind `json:"kind"`
	} `json:"author"`
	Behavior *struct {
		MaxCount *int                                        `json:"max_count,omitempty"`
		MinCount *int                                        `json:"min_count,omitempty"`
		Scope    *gen.TaskUpdateRequestCriteriaBehaviorScope `json:"scope,omitempty"`
		Tool     string                                      `json:"tool"`
	} `json:"behavior,omitempty"`
	Check *struct {
		Command          string `json:"command"`
		ExpectedExitCode int    `json:"expected_exit_code"`
	} `json:"check,omitempty"`
	ClauseCount *int                                     `json:"clause_count,omitempty"`
	Id          *string                                  `json:"id,omitempty"`
	Judgment    *gen.TaskUpdateRequestCriteriaJudgment   `json:"judgment,omitempty"`
	Kind        *gen.TaskUpdateRequestCriteriaKind       `json:"kind,omitempty"`
	Provenance  *gen.TaskUpdateRequestCriteriaProvenance `json:"provenance,omitempty"`
	Status      gen.TaskUpdateRequestCriteriaStatus      `json:"status"`
	Text        string                                   `json:"text"`
}) []task.AcceptanceCriterion {
	out := make([]task.AcceptanceCriterion, 0, len(items))
	for _, it := range items {
		c := task.AcceptanceCriterion{
			Text:   it.Text,
			Status: task.CriterionStatus(it.Status),
			Author: task.CriterionAuthor{Kind: string(it.Author.Kind), ID: it.Author.Id},
		}
		// ADR-074 D2 (spec FR-002) / ADR-080 D-TYPES: the gateway performs NO
		// kind/judgment defaulting — an absent kind or judgment passes THROUGH
		// as empty and is inferred downstream by the store's normalizeCriteria
		// (task.InferCriterionKind / task.InferJudgment).
		if it.Kind != nil {
			c.Kind = task.CriterionKind(*it.Kind)
		}
		if it.Judgment != nil {
			c.Judgment = task.JudgmentKind(*it.Judgment)
		}
		if it.Provenance != nil {
			c.Provenance = task.CriterionProvenance(*it.Provenance)
		}
		if it.Id != nil {
			c.ID = *it.Id
		}
		if it.Check != nil {
			c.Check = &task.CriterionCheck{Command: it.Check.Command, ExpectedExitCode: it.Check.ExpectedExitCode}
		}
		if it.Behavior != nil {
			c.Behavior = behaviorFromWire(it.Behavior.Tool, it.Behavior.MinCount, it.Behavior.MaxCount, it.Behavior.Scope)
		}
		out = append(out, c)
	}
	return out
}

// toWireDod converts internal Definition-of-Done criteria to the gen.Task.Dod
// inline wire shape (read path — GET/POST/PATCH responses). GOAL-FR-021/
// FR-029/FR-048: dod is a NEW field (ADR-086) — the task's DoD list lives on
// its paired goal record (pkg/goal), never on the task record itself, so
// callers of this function read from a *goal.Goal, not from task.Task.
//
// This is a distinct, near-duplicate function rather than a shared one with
// toWireCriteria because oapi-codegen emits TWO separate named anonymous-
// struct-element types for Task.Criteria and Task.Dod even though their
// shapes are structurally identical (same reason toWireCriteria/
// toWirePlanDoD in rest_plans.go cannot be shared either — see toWirePlanDoD's
// own doc comment).
func toWireDod(cs []task.AcceptanceCriterion) *[]struct {
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
			Judgment: gen.TaskDodJudgment(wireCriterionJudgment(c)),
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

// dodFromCreateWire converts the gen.TaskCreateRequest.Dod inline wire shape
// to internal acceptance criteria (create path). Mirrors criteriaFromCreateWire
// exactly — see toWireDod's doc comment for why this cannot be shared.
func dodFromCreateWire(items []struct {
	Author struct {
		Id   string                             `json:"id"`
		Kind gen.TaskCreateRequestDodAuthorKind `json:"kind"`
	} `json:"author"`
	Behavior *struct {
		MaxCount *int                                   `json:"max_count,omitempty"`
		MinCount *int                                   `json:"min_count,omitempty"`
		Scope    *gen.TaskCreateRequestDodBehaviorScope `json:"scope,omitempty"`
		Tool     string                                 `json:"tool"`
	} `json:"behavior,omitempty"`
	Check *struct {
		Command          string `json:"command"`
		ExpectedExitCode int    `json:"expected_exit_code"`
	} `json:"check,omitempty"`
	ClauseCount *int                                `json:"clause_count,omitempty"`
	Id          *string                             `json:"id,omitempty"`
	Judgment    *gen.TaskCreateRequestDodJudgment   `json:"judgment,omitempty"`
	Kind        *gen.TaskCreateRequestDodKind       `json:"kind,omitempty"`
	Provenance  *gen.TaskCreateRequestDodProvenance `json:"provenance,omitempty"`
	Status      gen.TaskCreateRequestDodStatus      `json:"status"`
	Text        string                              `json:"text"`
}) []task.AcceptanceCriterion {
	out := make([]task.AcceptanceCriterion, 0, len(items))
	for _, it := range items {
		c := task.AcceptanceCriterion{
			Text:   it.Text,
			Status: task.CriterionStatus(it.Status),
			Author: task.CriterionAuthor{Kind: string(it.Author.Kind), ID: it.Author.Id},
		}
		if it.Kind != nil {
			c.Kind = task.CriterionKind(*it.Kind)
		}
		if it.Judgment != nil {
			c.Judgment = task.JudgmentKind(*it.Judgment)
		}
		if it.Provenance != nil {
			c.Provenance = task.CriterionProvenance(*it.Provenance)
		}
		if it.Id != nil {
			c.ID = *it.Id
		}
		if it.Check != nil {
			c.Check = &task.CriterionCheck{Command: it.Check.Command, ExpectedExitCode: it.Check.ExpectedExitCode}
		}
		if it.Behavior != nil {
			c.Behavior = behaviorFromWire(it.Behavior.Tool, it.Behavior.MinCount, it.Behavior.MaxCount, it.Behavior.Scope)
		}
		out = append(out, c)
	}
	return out
}

// dodFromUpdateWire converts the gen.TaskUpdateRequest.Dod inline wire shape
// to internal acceptance criteria (PATCH path). Mirrors criteriaFromUpdateWire
// exactly — see toWireDod's doc comment for why this cannot be shared.
func dodFromUpdateWire(items []struct {
	Author struct {
		Id   string                             `json:"id"`
		Kind gen.TaskUpdateRequestDodAuthorKind `json:"kind"`
	} `json:"author"`
	Behavior *struct {
		MaxCount *int                                   `json:"max_count,omitempty"`
		MinCount *int                                   `json:"min_count,omitempty"`
		Scope    *gen.TaskUpdateRequestDodBehaviorScope `json:"scope,omitempty"`
		Tool     string                                 `json:"tool"`
	} `json:"behavior,omitempty"`
	Check *struct {
		Command          string `json:"command"`
		ExpectedExitCode int    `json:"expected_exit_code"`
	} `json:"check,omitempty"`
	ClauseCount *int                                `json:"clause_count,omitempty"`
	Id          *string                             `json:"id,omitempty"`
	Judgment    *gen.TaskUpdateRequestDodJudgment   `json:"judgment,omitempty"`
	Kind        *gen.TaskUpdateRequestDodKind       `json:"kind,omitempty"`
	Provenance  *gen.TaskUpdateRequestDodProvenance `json:"provenance,omitempty"`
	Status      gen.TaskUpdateRequestDodStatus      `json:"status"`
	Text        string                              `json:"text"`
}) []task.AcceptanceCriterion {
	out := make([]task.AcceptanceCriterion, 0, len(items))
	for _, it := range items {
		c := task.AcceptanceCriterion{
			Text:   it.Text,
			Status: task.CriterionStatus(it.Status),
			Author: task.CriterionAuthor{Kind: string(it.Author.Kind), ID: it.Author.Id},
		}
		if it.Kind != nil {
			c.Kind = task.CriterionKind(*it.Kind)
		}
		if it.Judgment != nil {
			c.Judgment = task.JudgmentKind(*it.Judgment)
		}
		if it.Provenance != nil {
			c.Provenance = task.CriterionProvenance(*it.Provenance)
		}
		if it.Id != nil {
			c.ID = *it.Id
		}
		if it.Check != nil {
			c.Check = &task.CriterionCheck{Command: it.Check.Command, ExpectedExitCode: it.Check.ExpectedExitCode}
		}
		if it.Behavior != nil {
			c.Behavior = behaviorFromWire(it.Behavior.Tool, it.Behavior.MinCount, it.Behavior.MaxCount, it.Behavior.Scope)
		}
		out = append(out, c)
	}
	return out
}

// computeRollup returns the read-time roll-up of live child sub-agent runs for
// parentID, or nil when there are none. A "live" child is one that is not yet
// terminal. Never stored on the task record (Detail #6).
//
// When idx is non-nil (a batch caller's shared rollupIndex, see its doc
// comment), children are read from the index — an in-memory grouping already
// built from ONE Store.List call — instead of issuing a fresh List call here.
// idx == nil (every single-task call site) preserves the original behavior:
// one bounded List call scoped to this parentID.
func (a *restAPI) computeRollup(parentID string, idx rollupIndex) *[]struct {
	AgentId string               `json:"agent_id"`
	Label   string               `json:"label"`
	Status  gen.TaskRollupStatus `json:"status"`
} {
	var children []task.Task
	if idx != nil {
		children = idx[parentID]
	} else {
		var err error
		children, err = a.taskStore.List(task.Filter{ParentTaskID: parentID, ParentTaskIDSet: true})
		if err != nil {
			return nil
		}
	}
	if len(children) == 0 {
		return nil
	}
	type rollupItem = struct {
		AgentId string               `json:"agent_id"`
		Label   string               `json:"label"`
		Status  gen.TaskRollupStatus `json:"status"`
	}
	items := make([]rollupItem, 0, len(children))
	for _, c := range children {
		if task.IsTerminal(c.Status) {
			continue
		}
		items = append(items, rollupItem{
			AgentId: c.AgentID,
			Label:   c.Title,
			Status:  gen.TaskRollupStatus(c.Status),
		})
	}
	if len(items) == 0 {
		return nil
	}
	return &items
}

// The REST surface has NO plan-linkage validator of its own. POST /tasks and
// PATCH /tasks/{id} both call tools.ValidateTaskPlanMembership (pkg/tools/
// plan.go) directly — the one choke point shared with the create_task and
// create_task_in_workspace agent tools, so "may this task join this plan?"
// has exactly one answer in this codebase. A REST-local wrapper (the retired
// validateTaskPlanID, and its errTaskPlanStoreUnavailable sentinel) is what
// let the rule drift here in the first place: it rejected only TERMINAL
// plans, so the UI could attach a member to an approved or running plan that
// plan-lint would never see. Do not reintroduce one.
//
// Path-traversal safety needs no separate validateEntityID call here either:
// plan.Store.Get runs pkg/plan's own validateID on every lookup, with the
// identical "/", "\", "..", NUL" rejection, and the error surfaces as a 400
// exactly like every other rejection below.

// resolveAgentName returns the display name for an agent ID from the registry,
// or "" when unknown.
func (a *restAPI) resolveAgentName(agentID string) string {
	if a.agentLoop == nil {
		return ""
	}
	reg := a.agentLoop.GetRegistry()
	if reg == nil {
		return ""
	}
	if ag, ok := reg.GetAgent(agentID); ok {
		return ag.Name
	}
	return ""
}

// behaviorFromWire converts a `kind: behavior` criterion's inline wire
// fields to task.CriterionBehavior. Collapses the four byte-identical
// FROM-wire behavior blocks (planDoDFromCreateWire/planDoDFromUpdateWire in
// rest_plans.go, criteriaFromCreateWire/criteriaFromUpdateWire in
// rest_tasks.go) that previously duplicated this same conversion once per
// generated wire-request variant — S is the per-variant generated Scope enum
// type (gen.PlanCreateRequestDodBehaviorScope, gen.TaskCriteriaBehaviorScope,
// etc.), always ~string so a direct conversion to task.BehaviorScope works.
//
// minCount is passed straight through as a pointer — NO default-1
// materialization here. task.CriterionBehavior.MinCount is itself *int
// (fix-wave finding #5): an omitted min_count stays nil on the wire and
// task.validateCriterionBehavior (pkg/task/criterion.go) — via
// EffectiveMinCount() at read time — owns defaulting nil to 1. Duplicating
// that default here would be a second source of truth for the same rule.
//
// The TO-wire direction (toWireCriteria/toWirePlanDoD's anonymous-struct
// literals) is intentionally NOT collapsed here — oapi-codegen generates a
// DISTINCT anonymous struct type per response context, so those literals
// cannot share one helper without reintroducing the wire-shape duplication
// Constraint #8 exists to prevent (see each call site's own comment).
func behaviorFromWire[S ~string](tool string, minCount, maxCount *int, scope *S) *task.CriterionBehavior {
	beh := &task.CriterionBehavior{Tool: tool, MinCount: minCount, MaxCount: maxCount}
	if scope != nil {
		beh.Scope = task.BehaviorScope(*scope)
	}
	return beh
}

// --- evidence / verdicts / stop (ADR-049 D2, Wave 2-C1 deferred REST paths) --

// toWireEvidenceRecord converts an internal task.EvidenceRecord to the
// generated wire type.
func toWireEvidenceRecord(r task.EvidenceRecord) gen.EvidenceRecord {
	return gen.EvidenceRecord{
		Id:           r.ID,
		TaskId:       r.TaskID,
		CriterionId:  r.CriterionID,
		Attempt:      r.Attempt,
		Command:      r.Command,
		ExitCode:     r.ExitCode,
		Output:       r.Output,
		Truncated:    r.Truncated,
		TimedOut:     r.TimedOut,
		PolicyDenied: r.PolicyDenied,
		RecordedAt:   parseTimeOrNow(r.RecordedAt),
	}
}

// toWireJudgeVerdict converts an internal task.JudgeVerdict to the generated
// wire type.
func toWireJudgeVerdict(v task.JudgeVerdict) gen.JudgeVerdict {
	out := gen.JudgeVerdict{
		Id:           v.ID,
		Scope:        gen.JudgeVerdictScope(v.Scope),
		Round:        v.Round,
		Met:          v.Met,
		Model:        v.Model,
		JudgedAt:     parseTimeOrNow(v.JudgedAt),
		JudgeAgentId: v.JudgeAgentID,
	}
	if v.TaskID != "" {
		out.TaskId = ptr(v.TaskID)
	}
	if v.PlanID != "" {
		out.PlanId = ptr(v.PlanID)
	}
	// Fix-wave finding #3: PerCriterion is a required array on the wire
	// (openapi_types.gen.go, no `omitempty`) — a nil slice marshals as JSON
	// `null`, which fails the SPA's zod schema for a required array and gets
	// dropped. An empty (zero-criteria) verdict must still round-trip as `[]`,
	// so start from a non-nil, empty slice rather than appending onto a nil
	// one.
	out.PerCriterion = make([]struct {
		CriterionId string `json:"criterion_id"`
		Evidence    *[]struct {
			Part   string  `json:"part"`
			Quote  string  `json:"quote"`
			Source *string `json:"source,omitempty"`
			Target *string `json:"target,omitempty"`
		} `json:"evidence,omitempty"`
		EvidenceQuote  *string                                     `json:"evidence_quote,omitempty"`
		EvidenceSource *gen.JudgeVerdictPerCriterionEvidenceSource `json:"evidence_source,omitempty"`
		EvidenceTarget *string                                     `json:"evidence_target,omitempty"`
		Met            bool                                        `json:"met"`
		Provenance     *gen.JudgeVerdictPerCriterionProvenance     `json:"provenance,omitempty"`
		Reason         string                                      `json:"reason"`
	}, 0, len(v.PerCriterion))
	for _, c := range v.PerCriterion {
		// ADR-074 D7: optional + empty-safe — an empty quote (fail-closed /
		// pre-D7 verdicts) stays absent from the wire, never "".
		var quote *string
		if c.EvidenceQuote != "" {
			quote = ptr(c.EvidenceQuote)
		}
		// ADR-084 D-B / JUDGE-FR-070a: Evidence, EvidenceSource,
		// EvidenceTarget and Provenance are REPORTING-only fields — they
		// never gate a verdict, they explain one. They DO exist on
		// task.CriterionVerdict (pkg/task/verdict.go); a stale comment here
		// claimed the Go fields were still to come and dropped all four,
		// so the same verdict arrived complete over the WS frame
		// (replay.go's toJudgeVerdictFrame maps them) and stripped over
		// REST — meaning a page reload silently erased the judge's evidence.
		// This mapping mirrors toJudgeVerdictFrame field-for-field; the only
		// difference is the generated REST shape's typed enums and its
		// POINTER-to-slice evidence field.
		var evidenceSource *gen.JudgeVerdictPerCriterionEvidenceSource
		if c.EvidenceSource != "" {
			es := gen.JudgeVerdictPerCriterionEvidenceSource(c.EvidenceSource)
			evidenceSource = &es
		}
		var evidenceTarget *string
		if c.EvidenceTarget != "" {
			evidenceTarget = ptr(c.EvidenceTarget)
		}
		var provenance *gen.JudgeVerdictPerCriterionProvenance
		if c.Provenance != "" {
			p := gen.JudgeVerdictPerCriterionProvenance(c.Provenance)
			provenance = &p
		}
		var evidence *[]struct {
			Part   string  `json:"part"`
			Quote  string  `json:"quote"`
			Source *string `json:"source,omitempty"`
			Target *string `json:"target,omitempty"`
		}
		if len(c.Evidence) > 0 {
			entries := make([]struct {
				Part   string  `json:"part"`
				Quote  string  `json:"quote"`
				Source *string `json:"source,omitempty"`
				Target *string `json:"target,omitempty"`
			}, 0, len(c.Evidence))
			for _, e := range c.Evidence {
				var src *string
				if e.Source != "" {
					src = ptr(e.Source)
				}
				var tgt *string
				if e.Target != "" {
					tgt = ptr(e.Target)
				}
				entries = append(entries, struct {
					Part   string  `json:"part"`
					Quote  string  `json:"quote"`
					Source *string `json:"source,omitempty"`
					Target *string `json:"target,omitempty"`
				}{Part: e.Part, Quote: e.Quote, Source: src, Target: tgt})
			}
			evidence = &entries
		}
		out.PerCriterion = append(out.PerCriterion, struct {
			CriterionId string `json:"criterion_id"`
			Evidence    *[]struct {
				Part   string  `json:"part"`
				Quote  string  `json:"quote"`
				Source *string `json:"source,omitempty"`
				Target *string `json:"target,omitempty"`
			} `json:"evidence,omitempty"`
			EvidenceQuote  *string                                     `json:"evidence_quote,omitempty"`
			EvidenceSource *gen.JudgeVerdictPerCriterionEvidenceSource `json:"evidence_source,omitempty"`
			EvidenceTarget *string                                     `json:"evidence_target,omitempty"`
			Met            bool                                        `json:"met"`
			Provenance     *gen.JudgeVerdictPerCriterionProvenance     `json:"provenance,omitempty"`
			Reason         string                                      `json:"reason"`
		}{
			CriterionId:    c.CriterionID,
			Evidence:       evidence,
			EvidenceQuote:  quote,
			EvidenceSource: evidenceSource,
			EvidenceTarget: evidenceTarget,
			Met:            c.Met,
			Provenance:     provenance,
			Reason:         c.Reason,
		})
	}
	return out
}
