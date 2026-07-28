package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// ---------------------------------------------------------------------------
// Fakes
//
// Each fake mirrors its real store's filter semantics EXACTLY — in particular
// that an empty predicate value means "filter off", never "match nothing".
// That is the whole point: if the fakes fail closed on an empty owner, the
// fail-closed tests below would pass against a tool that has no guard at all,
// and the single most important property of this tool would be untested.
// ---------------------------------------------------------------------------

type fakeJobPlanStore struct {
	plans []plan.Plan
	err   error

	// mu guards calls only. The concurrency test drives one shared fake from
	// eight goroutines, so an unguarded counter would be a data race in the
	// TEST rather than in the code under test — and -race would report it as
	// though list_jobs were at fault.
	mu    sync.Mutex
	calls int
}

func (f *fakeJobPlanStore) List(filter plan.Filter) ([]plan.Plan, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := make([]plan.Plan, 0, len(f.plans))
	for _, p := range f.plans {
		if filter.WorkspaceID != "" && p.WorkspaceID != filter.WorkspaceID {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

type fakeJobTaskStore struct {
	tasks []task.Task
	err   error

	mu    sync.Mutex
	calls int
}

func (f *fakeJobTaskStore) List(filter task.Filter) ([]task.Task, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := make([]task.Task, 0, len(f.tasks))
	for _, tk := range f.tasks {
		if filter.WorkspaceID != "" && tk.WorkspaceID != filter.WorkspaceID {
			continue
		}
		// task.Filter treats an empty PlanID / AgentID / CreatedBy as "filter
		// off" — replicated so the tool's own predicates are what is tested.
		if filter.PlanID != "" && tk.PlanID != filter.PlanID {
			continue
		}
		if filter.AgentID != "" && tk.AgentID != filter.AgentID {
			continue
		}
		if filter.CreatedBy != "" && tk.CreatedBy != filter.CreatedBy {
			continue
		}
		out = append(out, tk)
	}
	return out, nil
}

type fakeJobLifecycleStore struct {
	records []session.LifecycleRecord
	err     error

	mu    sync.Mutex
	calls int
}

func (f *fakeJobLifecycleStore) List(filter session.LifecycleFilter) ([]session.LifecycleRecord, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := make([]session.LifecycleRecord, 0, len(f.records))
	for _, rec := range f.records {
		if filter.WorkspaceID != "" && rec.WorkspaceID != filter.WorkspaceID {
			continue
		}
		if filter.AgentID != "" && rec.AgentID != filter.AgentID {
			continue
		}
		// The real filter's clause, verbatim: an empty ParentAgentID matches
		// EVERY record. This is why the tool must fail closed before it gets
		// here.
		if filter.ParentAgentID != "" && rec.ParentAgentID != filter.ParentAgentID {
			continue
		}
		if filter.NonTerminalOnly && rec.Terminal() {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// callCount reads the guarded counter.
func (f *fakeJobPlanStore) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeJobTaskStore) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeJobLifecycleStore) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeAgentNamer map[string]string

func (f fakeAgentNamer) AgentDisplayName(agentID string) (string, bool) {
	name, ok := f[agentID]
	return name, ok
}

type fakeSessionResolver struct {
	live  map[string]bool
	calls int
}

func (f *fakeSessionResolver) ResolvableSessionIDs(ids []string) map[string]bool {
	f.calls++
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = f.live[id]
	}
	return out
}

type fakeCapSource struct {
	active, capMax         int
	reliable               bool
	observedAt, lastTickAt time.Time
}

func (f fakeCapSource) CapSnapshot() (int, int, bool, time.Time, time.Time) {
	return f.active, f.capMax, f.reliable, f.observedAt, f.lastTickAt
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func jobCtx(agentID, workspaceID string) context.Context {
	ctx := WithAgentID(context.Background(), agentID)
	return WithWorkspaceID(ctx, workspaceID)
}

func decodeRoster(t *testing.T, res *ToolResult) listJobsResponse {
	t.Helper()
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.ForLLM)
	}
	var out listJobsResponse
	if err := json.Unmarshal([]byte(res.ForLLM), &out); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, res.ForLLM)
	}
	return out
}

func rowIDs(rows []jobRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

// jobFixture bundles a wired tool with the stores behind it, so a test that
// only needs the tool does not have to discard three values to get at it.
type jobFixture struct {
	tool       *ListJobsTool
	plans      *fakeJobPlanStore
	tasks      *fakeJobTaskStore
	lifecycles *fakeJobLifecycleStore
}

// newJobFixture wires a tool over a fixture containing work for TWO agents in
// TWO workspaces, so every scoping test has something to leak if the scoping
// is wrong.
func newJobFixture() jobFixture {
	plans := &fakeJobPlanStore{plans: []plan.Plan{
		{ID: "pln-mia-w1", WorkspaceID: "ws1", Title: "Mia plan in ws1",
			OwnerAgentID: "mia", State: plan.StateRunning, PlanPhase: plan.PhaseDispatching},
		{ID: "pln-mia-w2", WorkspaceID: "ws2", Title: "Mia plan in ws2",
			OwnerAgentID: "mia", State: plan.StateApproved},
		{ID: "pln-jim-w1", WorkspaceID: "ws1", Title: "Jim secret plan",
			OwnerAgentID: "jim", State: plan.StateRunning},
	}}
	tasks := &fakeJobTaskStore{tasks: []task.Task{
		{ID: "tsk-mia-assigned", WorkspaceID: "ws1", Title: "Assigned to Mia",
			AgentID: "mia", Status: task.StatusInProgress},
		{ID: "tsk-jim-assigned", WorkspaceID: "ws1", Title: "Jim secret task",
			AgentID: "jim", Status: task.StatusInProgress},
	}}
	lifecycles := &fakeJobLifecycleStore{records: []session.LifecycleRecord{
		{SessionID: "ses-mia-child", WorkspaceID: "ws1", AgentID: "ray",
			ParentAgentID: "mia", State: session.LifecycleRunning},
		{SessionID: "ses-jim-child", WorkspaceID: "ws1", AgentID: "ava",
			ParentAgentID: "jim", State: session.LifecycleRunning},
	}}
	tool := NewListJobsTool(plans, tasks, lifecycles)
	tool.SetAgentNamer(func() JobAgentNamer { return fakeAgentNamer{"ray": "Ray", "ava": "Ava"} })
	tool.SetSessionResolver(func() JobSessionResolver {
		return &fakeSessionResolver{live: map[string]bool{"ses-mia-child": true, "ses-jim-child": true}}
	})
	return jobFixture{tool: tool, plans: plans, tasks: tasks, lifecycles: lifecycles}
}

// ---------------------------------------------------------------------------
// Scoping — the security control
// ---------------------------------------------------------------------------

// TestListJobs_EmptyPrincipalFailsClosed asserts the OUTCOME, not the filter.
//
// A test that checked "the ParentAgentID filter was set" would pass against a
// tool that set it to "" — which every one of the three stores reads as
// "filter off", returning every record in the installation. The property is:
// an error, zero rows, and not one byte of anybody's work in the output.
//
// This is not hypothetical. `list_tasks` has exactly this defect in production
// today: pkg/tools/task.go:60 assigns the possibly-empty ToolAgentID(ctx)
// straight into task.Filter, so it returns every task in every workspace when
// the principal is unresolved.
func TestListJobs_EmptyPrincipalFailsClosed(t *testing.T) {
	for _, principal := range []string{"", "   ", "\t\n ", " "} {
		t.Run(fmt.Sprintf("principal=%q", principal), func(t *testing.T) {
			tool := newJobFixture().tool
			res := tool.Execute(jobCtx(principal, "ws1"), map[string]any{})

			if !res.IsError {
				t.Fatalf("an unresolvable principal must return an ERROR, got a success: %s", res.ForLLM)
			}

			// Zero rows: the payload must not parse into a roster carrying any.
			var roster listJobsResponse
			if err := json.Unmarshal([]byte(res.ForLLM), &roster); err == nil && len(roster.Rows) > 0 {
				t.Fatalf("an unresolvable principal returned %d rows", len(roster.Rows))
			}

			// And nothing leaked in prose either.
			for _, secret := range []string{
				"pln-mia-w1", "pln-jim-w1", "tsk-mia-assigned", "tsk-jim-assigned",
				"ses-mia-child", "ses-jim-child", "Jim secret plan", "Jim secret task",
			} {
				if strings.Contains(res.ForLLM, secret) {
					t.Fatalf("fail-closed response leaked %q: %s", secret, res.ForLLM)
				}
			}
		})
	}
}

// TestListJobs_NonWhitespacePrincipalIsNotRejected is the companion: only a
// LEXICALLY empty id fails closed. A syntactically valid but unknown agent id
// succeeds with an empty roster — the tool must not perform a registry lookup
// to validate the principal, because that would turn an unknown-agent turn
// into an error instead of an honest "you have no background work".
func TestListJobs_NonWhitespacePrincipalIsNotRejected(t *testing.T) {
	tool := newJobFixture().tool
	res := tool.Execute(jobCtx("nobody-by-that-name", "ws1"), map[string]any{})

	roster := decodeRoster(t, res)
	if len(roster.Rows) != 0 {
		t.Fatalf("an unknown agent must see 0 rows, got %v", rowIDs(roster.Rows))
	}
	if roster.Notes != nil {
		t.Errorf("an empty roster is nominal: notes must be null, got %+v", roster.Notes)
	}
}

// TestListJobs_CrossAgentIsolation: exactly zero rows visible in both
// directions between two populated agents.
func TestListJobs_CrossAgentIsolation(t *testing.T) {
	tool := newJobFixture().tool

	mia := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))
	jim := decodeRoster(t, tool.Execute(jobCtx("jim", "ws1"), map[string]any{}))

	miaIDs := strings.Join(rowIDs(mia.Rows), ",")
	jimIDs := strings.Join(rowIDs(jim.Rows), ",")

	if len(mia.Rows) == 0 || len(jim.Rows) == 0 {
		t.Fatalf("fixture bug: both agents must have work (mia=%s jim=%s)", miaIDs, jimIDs)
	}
	for _, row := range mia.Rows {
		if strings.Contains(row.ID, "jim") {
			t.Errorf("mia's roster contains jim's row %q", row.ID)
		}
	}
	for _, row := range jim.Rows {
		if strings.Contains(row.ID, "mia") {
			t.Errorf("jim's roster contains mia's row %q", row.ID)
		}
	}
}

// TestListJobs_WorkspaceScopingIsLabelledNotSilent covers both modes. A
// workspace-less turn is legitimate, so it must NOT fail closed — but it must
// not present a cross-workspace roster as a scoped one either.
func TestListJobs_WorkspaceScopingIsLabelledNotSilent(t *testing.T) {
	tool := newJobFixture().tool

	scoped := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))
	if !scoped.WorkspaceScoped {
		t.Error("a workspace-bound turn must report workspace_scoped=true")
	}
	for _, row := range scoped.Rows {
		if row.WorkspaceID != "ws1" {
			t.Errorf("scoped roster leaked a row from %q: %q", row.WorkspaceID, row.ID)
		}
		if row.WorkspaceID == "" {
			t.Errorf("row %q carries no workspace_id — it is required in every scoping mode", row.ID)
		}
	}

	wide := decodeRoster(t, tool.Execute(jobCtx("mia", ""), map[string]any{}))
	if wide.WorkspaceScoped {
		t.Error("a workspace-less turn must report workspace_scoped=false, not silently widen")
	}
	seen := map[string]bool{}
	for _, row := range wide.Rows {
		seen[row.WorkspaceID] = true
		if strings.Contains(row.ID, "jim") {
			t.Errorf("the widened roster crossed the PRINCIPAL boundary too: %q", row.ID)
		}
	}
	if !seen["ws1"] || !seen["ws2"] {
		t.Errorf("a workspace-less turn must span every workspace for this principal, saw %v", seen)
	}
}

// TestListJobs_TaskAttributionIsAgentIDNamespaced is the release gate for the
// username/agent-id collision.
//
// A human user named "mia" creating a task in the SPA writes their USERNAME
// into Task.CreatedBy. If that field were used as the ownership predicate,
// every task that human ever created would appear — with its title — in agent
// mia's roster, on a tool whose entire thesis is "never see another
// principal's work".
func TestListJobs_TaskAttributionIsAgentIDNamespaced(t *testing.T) {
	tasks := &fakeJobTaskStore{tasks: []task.Task{
		{ID: "tsk-human", WorkspaceID: "ws1", Title: "Human mia's private task",
			CreatedBy: "mia", CreatedByAgentID: "", Status: task.StatusNext},
		{ID: "tsk-agent", WorkspaceID: "ws1", Title: "Agent mia dispatched this",
			CreatedBy: "mia", CreatedByAgentID: "mia", Status: task.StatusNext},
	}}
	tool := NewListJobsTool(nil, tasks, nil)

	roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{"kind": jobKindTask}))

	ids := rowIDs(roster.Rows)
	if contains(ids, "tsk-human") {
		t.Error("a task created by a HUMAN whose username collides with an agent id was disclosed to that agent")
	}
	if !contains(ids, "tsk-agent") {
		t.Errorf("a task genuinely created by the agent must appear, got %v", ids)
	}
	if strings.Contains(res(roster), "Human mia's private task") {
		t.Error("the human's task TITLE leaked into the roster")
	}
}

func res(r listJobsResponse) string {
	b, _ := json.Marshal(r)
	return string(b)
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// TestListJobs_TaskOwnershipUnion: an agent executing a task somebody ELSE
// assigned to it is the most literal reading of "what am I still working on?",
// and a created-by-only predicate returns nothing for it. A task matching both
// readings appears exactly once.
func TestListJobs_TaskOwnershipUnion(t *testing.T) {
	tasks := &fakeJobTaskStore{tasks: []task.Task{
		{ID: "tsk-assigned", WorkspaceID: "ws1", Title: "Assigned by a human",
			AgentID: "mia", CreatedBy: "alice", Status: task.StatusInProgress},
		{ID: "tsk-dispatched", WorkspaceID: "ws1", Title: "Mia created for Ray",
			AgentID: "ray", CreatedByAgentID: "mia", Status: task.StatusNext},
		{ID: "tsk-both", WorkspaceID: "ws1", Title: "Mia created for herself",
			AgentID: "mia", CreatedByAgentID: "mia", Status: task.StatusNext},
		{ID: "tsk-plan-member", WorkspaceID: "ws1", Title: "A plan member",
			AgentID: "mia", PlanID: "pln-1", Status: task.StatusNext},
	}}
	tool := NewListJobsTool(nil, tasks, nil)

	roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))

	byID := map[string][]jobRow{}
	for _, row := range roster.Rows {
		byID[row.ID] = append(byID[row.ID], row)
	}

	if len(byID["tsk-assigned"]) != 1 || byID["tsk-assigned"][0].Relation != relationRuns {
		t.Errorf("an assigned task must appear once with relation=runs, got %+v", byID["tsk-assigned"])
	}
	if len(byID["tsk-dispatched"]) != 1 || byID["tsk-dispatched"][0].Relation != relationDispatched {
		t.Errorf("a dispatched task must appear once with relation=dispatched, got %+v", byID["tsk-dispatched"])
	}
	if len(byID["tsk-both"]) != 1 {
		t.Errorf("a task matching BOTH readings must appear exactly once, got %d", len(byID["tsk-both"]))
	} else if byID["tsk-both"][0].Relation != relationRuns {
		t.Errorf("runs must win when both readings match, got %q", byID["tsk-both"][0].Relation)
	}
	if len(byID["tsk-plan-member"]) != 0 {
		t.Error("plan-member tasks must not appear — the plan itself is already a row")
	}
}

// TestListJobs_PlanOwnershipIsOwnerAgentIDNotOwner: the plan-side mirror of the
// same namespace hazard. Plan.Owner is a username on the REST path.
func TestListJobs_PlanOwnershipIsOwnerAgentIDNotOwner(t *testing.T) {
	plans := &fakeJobPlanStore{plans: []plan.Plan{
		{ID: "pln-human", WorkspaceID: "ws1", Title: "Authored by human mia",
			Owner: "mia", OwnerAgentID: "jim", State: plan.StateRunning},
		{ID: "pln-agent", WorkspaceID: "ws1", Title: "Run by agent mia",
			Owner: "alice", OwnerAgentID: "mia", State: plan.StateRunning},
	}}
	tool := NewListJobsTool(plans, nil, nil)

	roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{"kind": jobKindPlan}))

	ids := rowIDs(roster.Rows)
	if contains(ids, "pln-human") {
		t.Error("plan matched on Owner (a username), not OwnerAgentID")
	}
	// A plan authored by a human but RUN by this agent is legitimately the
	// agent's own work and must appear.
	if !contains(ids, "pln-agent") {
		t.Errorf("a human-authored plan run by this agent must appear, got %v", ids)
	}
}

// ---------------------------------------------------------------------------
// Subagent linkage
// ---------------------------------------------------------------------------

// TestListJobs_SubagentParentageDoesNotLeakSubtree: ParentAgentID is the ONLY
// parent linkage. ParentDurableKey is SHARED between a parent and every
// descendant, so inferring parentage from it would hand a caller its
// grandchildren, its cousins and its siblings.
func TestListJobs_SubagentParentageDoesNotLeakSubtree(t *testing.T) {
	const sharedTranscript = "chat-123"
	lifecycles := &fakeJobLifecycleStore{records: []session.LifecycleRecord{
		{SessionID: "ses-child", WorkspaceID: "ws1", AgentID: "ray",
			ParentAgentID: "mia", ParentDurableKey: sharedTranscript, State: session.LifecycleRunning},
		// A grandchild: same shared transcript key, but its parent is Ray.
		{SessionID: "ses-grandchild", WorkspaceID: "ws1", AgentID: "ava",
			ParentAgentID: "ray", ParentDurableKey: sharedTranscript, State: session.LifecycleRunning},
		// A sibling delegated by somebody else on the same transcript.
		{SessionID: "ses-cousin", WorkspaceID: "ws1", AgentID: "ava",
			ParentAgentID: "jim", ParentDurableKey: sharedTranscript, State: session.LifecycleRunning},
	}}
	tool := NewListJobsTool(nil, nil, lifecycles)
	tool.SetSessionResolver(func() JobSessionResolver {
		return &fakeSessionResolver{live: map[string]bool{"ses-child": true}}
	})

	roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))

	ids := rowIDs(roster.Rows)
	if len(ids) != 1 || ids[0] != "ses-child" {
		t.Fatalf("only direct children may appear, got %v", ids)
	}
}

// TestListJobs_SubagentGenerationIsNewestOnly runs BOTH with and without
// include_terminal, because that is where a naive implementation breaks: a
// resumed session leaves the superseded generation behind as a legitimate
// terminal record that would otherwise surface under include_terminal=true and
// show one delegation twice.
//
// The two subtests are the two resume mechanisms spawnCorrectiveFollowUp
// branches on (pkg/tools/delegate.go), and they are NOT redundant:
//
//   - native warm resume reuses the session id and bumps Generation. The real
//     *session.LifecycleStore already collapses this upstream (one .jsonl per
//     session id, List returns the tail via Load), so this shape can only reach
//     the tool through a different JobLifecycleLister.
//   - 3P cold respawn mints a NEW session id and links back via ResumedFrom.
//     That one DOES reach the tool as two live records against the real store,
//     and a dedupe keyed on session id cannot collapse it — which is precisely
//     the bug this test's second case exists to catch.
func TestListJobs_SubagentGenerationIsNewestOnly(t *testing.T) {
	cases := []struct {
		name    string
		records []session.LifecycleRecord
		// wantID is the session id of the single surviving row.
		wantID string
	}{
		{
			// Native: same session id, Generation+1. ResumedFrom is the session's
			// OWN id here — that self-link must never be read as supersession, or
			// the live row would be dropped along with the stale one.
			name:   "native_warm_resume_same_session_id",
			wantID: "ses-1",
			records: []session.LifecycleRecord{
				{SessionID: "ses-1", Generation: 0, WorkspaceID: "ws1", AgentID: "ray",
					ParentAgentID: "mia", State: session.LifecycleFailed, FailedReason: "interrupted"},
				{SessionID: "ses-1", Generation: 1, WorkspaceID: "ws1", AgentID: "ray",
					ParentAgentID: "mia", State: session.LifecycleRunning, ResumedFrom: "ses-1"},
			},
		},
		{
			// 3P: a NEW session id, so both records are distinct rows to any
			// id-keyed dedupe. ResumedFrom is the only thing linking them.
			name:   "3p_cold_respawn_new_session_id",
			wantID: "ses-2",
			records: []session.LifecycleRecord{
				{SessionID: "ses-1", Generation: 0, WorkspaceID: "ws1", AgentID: "ray",
					ParentAgentID: "mia", Is3P: true,
					State: session.LifecycleFailed, FailedReason: "interrupted"},
				{SessionID: "ses-2", Generation: 1, WorkspaceID: "ws1", AgentID: "ray",
					ParentAgentID: "mia", Is3P: true,
					State: session.LifecycleRunning, ResumedFrom: "ses-1"},
			},
		},
		{
			// A chain collapses transitively: every intermediate link is named
			// by its successor's ResumedFrom.
			name:   "3p_respawn_chain_collapses_to_the_tail",
			wantID: "ses-3",
			records: []session.LifecycleRecord{
				{SessionID: "ses-1", Generation: 0, WorkspaceID: "ws1", AgentID: "ray",
					ParentAgentID: "mia", Is3P: true, State: session.LifecycleFailed},
				{SessionID: "ses-2", Generation: 1, WorkspaceID: "ws1", AgentID: "ray",
					ParentAgentID: "mia", Is3P: true, State: session.LifecycleFailed,
					ResumedFrom: "ses-1"},
				{SessionID: "ses-3", Generation: 2, WorkspaceID: "ws1", AgentID: "ray",
					ParentAgentID: "mia", Is3P: true, State: session.LifecycleRunning,
					ResumedFrom: "ses-2"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool := NewListJobsTool(nil, nil, &fakeJobLifecycleStore{records: tc.records})
			tool.SetSessionResolver(func() JobSessionResolver {
				return &fakeSessionResolver{live: map[string]bool{
					"ses-1": true, "ses-2": true, "ses-3": true,
				}}
			})

			for _, includeTerminal := range []bool{false, true} {
				args := map[string]any{"include_terminal": includeTerminal}
				roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), args))

				if len(roster.Rows) != 1 {
					t.Fatalf("include_terminal=%v: one delegation must yield one row, got %d: %v",
						includeTerminal, len(roster.Rows), rowIDs(roster.Rows))
				}
				row := roster.Rows[0]
				if row.ID != tc.wantID {
					t.Errorf("include_terminal=%v: want the surviving session %q, got %q",
						includeTerminal, tc.wantID, row.ID)
				}
				if row.Status != jobStatusRunning {
					t.Errorf("include_terminal=%v: the newest generation is running, got %q",
						includeTerminal, row.Status)
				}
				// The superseded generation must not be counted as suppressed
				// either — it is not a row the caller is missing, it is a row
				// that no longer exists.
				if roster.Notes != nil && roster.Notes.TerminalSuppressed["subagent"] != 0 {
					t.Errorf("include_terminal=%v: a superseded generation was counted as suppressed",
						includeTerminal)
				}
			}
		})
	}
}

// TestListJobs_ActionableTracksProcessLifetimeAndTerminality: a handle that
// will fail on use must say so, rather than leaving the caller to discover it
// by failing.
func TestListJobs_ActionableTracksProcessLifetimeAndTerminality(t *testing.T) {
	lifecycles := &fakeJobLifecycleStore{records: []session.LifecycleRecord{
		{SessionID: "ses-live", WorkspaceID: "ws1", AgentID: "ray",
			ParentAgentID: "mia", State: session.LifecycleRunning},
		{SessionID: "ses-orphan", WorkspaceID: "ws1", AgentID: "ray",
			ParentAgentID: "mia", State: session.LifecycleRunning},
		{SessionID: "ses-dead", WorkspaceID: "ws1", AgentID: "ray",
			ParentAgentID: "mia", State: session.LifecycleFailed, FailedReason: "interrupted"},
	}}
	plans := &fakeJobPlanStore{plans: []plan.Plan{
		{ID: "pln-done", WorkspaceID: "ws1", Title: "Finished", OwnerAgentID: "mia", State: plan.StateDone},
		{ID: "pln-live", WorkspaceID: "ws1", Title: "Live", OwnerAgentID: "mia", State: plan.StateRunning},
	}}
	resolver := &fakeSessionResolver{live: map[string]bool{"ses-live": true}}
	tool := NewListJobsTool(plans, nil, lifecycles)
	tool.SetSessionResolver(func() JobSessionResolver { return resolver })

	roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{"include_terminal": true}))

	want := map[string]bool{
		"ses-live":   true,  // live and resolvable
		"ses-orphan": false, // durable record survived a restart; the index did not
		"ses-dead":   false, // terminal
		"pln-live":   true,
		"pln-done":   false, // terminal: execute_plan will not run a done plan
	}
	for _, row := range roster.Rows {
		expected, known := want[row.ID]
		if !known {
			t.Fatalf("unexpected row %q", row.ID)
		}
		if row.Actionable != expected {
			t.Errorf("%s: want actionable=%v, got %v", row.ID, expected, row.Actionable)
		}
	}

	// The delegate index is guarded by the hottest mutex in the delegation
	// path, so it is read once per CALL, never once per row.
	if resolver.calls != 1 {
		t.Errorf("the session resolver must be called exactly once per call, got %d", resolver.calls)
	}
}

// TestListJobs_SubagentLabelFallsBackToRawAgentID: durable records outlive the
// agents they name, so an unresolvable id is a NORMAL case. The label must
// never be empty and must never be an error.
func TestListJobs_SubagentLabelFallsBackToRawAgentID(t *testing.T) {
	lifecycles := &fakeJobLifecycleStore{records: []session.LifecycleRecord{
		{SessionID: "ses-known", WorkspaceID: "ws1", AgentID: "ray",
			ParentAgentID: "mia", State: session.LifecycleRunning},
		{SessionID: "ses-deleted", WorkspaceID: "ws1", AgentID: "agent-that-was-deleted",
			ParentAgentID: "mia", State: session.LifecycleRunning},
	}}
	tool := NewListJobsTool(nil, nil, lifecycles)
	tool.SetAgentNamer(func() JobAgentNamer { return fakeAgentNamer{"ray": "Ray"} })

	roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))

	labels := map[string]string{}
	for _, row := range roster.Rows {
		labels[row.ID] = row.Label
	}
	if labels["ses-known"] != "Ray" {
		t.Errorf("a resolvable agent must use its display name, got %q", labels["ses-known"])
	}
	if labels["ses-deleted"] != "agent-that-was-deleted" {
		t.Errorf("a deleted agent must fall back to the raw id, got %q", labels["ses-deleted"])
	}
}

// ---------------------------------------------------------------------------
// Honesty — nothing is ever silently dropped
// ---------------------------------------------------------------------------

// TestListJobs_EmptyRosterIsNominalAndNotAnError: "nothing to report" must cost
// the caller nothing, and `notes: null` is what makes it distinguishable from
// a malformed response.
func TestListJobs_EmptyRosterIsNominalAndNotAnError(t *testing.T) {
	tool := NewListJobsTool(&fakeJobPlanStore{}, &fakeJobTaskStore{}, &fakeJobLifecycleStore{})

	res := tool.Execute(jobCtx("mia", "ws1"), map[string]any{})
	roster := decodeRoster(t, res)

	if len(roster.Rows) != 0 {
		t.Fatalf("want 0 rows, got %d", len(roster.Rows))
	}
	if roster.Notes != nil {
		t.Errorf("an empty roster carries no counters at all: got %+v", roster.Notes)
	}
	if !strings.Contains(res.ForLLM, `"notes":null`) {
		t.Errorf("notes must be PRESENT and null, so the caller can tell it from a missing field: %s", res.ForLLM)
	}
}

// TestListJobs_TerminalSuppressionIsCountedNotSilent is the post-restart case.
//
// After a boot sweep reconciles every one of a caller's sessions to
// failed(interrupted), a default call returns zero rows. Without the count,
// that response is BYTE-IDENTICAL to a caller who never had any background
// work — and the agent concludes its work never existed.
func TestListJobs_TerminalSuppressionIsCountedNotSilent(t *testing.T) {
	lifecycles := &fakeJobLifecycleStore{records: []session.LifecycleRecord{
		{SessionID: "ses-1", WorkspaceID: "ws1", AgentID: "ray", ParentAgentID: "mia",
			State: session.LifecycleFailed, FailedReason: "interrupted"},
		{SessionID: "ses-2", WorkspaceID: "ws1", AgentID: "ray", ParentAgentID: "mia",
			State: session.LifecycleFailed, FailedReason: "interrupted"},
		{SessionID: "ses-3", WorkspaceID: "ws1", AgentID: "ray", ParentAgentID: "mia",
			State: session.LifecycleCompleted},
	}}
	// Both tools are wired identically apart from the lifecycle contents, so
	// the ONLY difference between the two responses is the swept caller's
	// terminal population — which is the thing under test.
	swept := NewListJobsTool(&fakeJobPlanStore{}, &fakeJobTaskStore{}, lifecycles)
	quiet := NewListJobsTool(&fakeJobPlanStore{}, &fakeJobTaskStore{}, &fakeJobLifecycleStore{})

	sweptRoster := decodeRoster(t, swept.Execute(jobCtx("mia", "ws1"), map[string]any{}))
	quietRoster := decodeRoster(t, quiet.Execute(jobCtx("mia", "ws1"), map[string]any{}))

	if len(sweptRoster.Rows) != 0 || len(quietRoster.Rows) != 0 {
		t.Fatalf("both callers see 0 live rows; that is the premise of this test")
	}
	if sweptRoster.Notes == nil {
		t.Fatal("a swept caller's response is indistinguishable from having no work at all")
	}
	if got := sweptRoster.Notes.TerminalSuppressed["subagent"]; got != 3 {
		t.Errorf("terminal_suppressed[subagent]: want 3, got %d", got)
	}
	if got := sweptRoster.Notes.TerminalSuppressed["total"]; got != 3 {
		t.Errorf("terminal_suppressed[total]: want 3, got %d", got)
	}
	if quietRoster.Notes != nil {
		t.Errorf("a caller with genuinely no work must report nothing: got %+v", quietRoster.Notes)
	}

	// And the caller can recover the rows by asking for them.
	recovered := decodeRoster(t, swept.Execute(jobCtx("mia", "ws1"), map[string]any{"include_terminal": true}))
	if len(recovered.Rows) != 3 {
		t.Errorf("include_terminal must recover all 3 rows, got %d", len(recovered.Rows))
	}
}

// TestListJobs_PerKindStoreErrorIsExplicitNotAShortList: one kind failing must
// never be laundered into a plausible-looking short list. A short list that
// looks complete is the worst possible output.
func TestListJobs_PerKindStoreErrorIsExplicitNotAShortList(t *testing.T) {
	plans := &fakeJobPlanStore{err: errors.New("open plans dir: permission denied")}
	tasks := &fakeJobTaskStore{tasks: []task.Task{
		{ID: "tsk-1", WorkspaceID: "ws1", Title: "Still here", AgentID: "mia", Status: task.StatusInProgress},
	}}
	tool := NewListJobsTool(plans, tasks, &fakeJobLifecycleStore{})

	roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))

	if len(roster.Rows) != 1 || roster.Rows[0].ID != "tsk-1" {
		t.Fatalf("the healthy kind must still return its rows, got %v", rowIDs(roster.Rows))
	}
	if roster.Notes == nil || len(roster.Notes.Errors) != 1 {
		t.Fatalf("the failed kind must produce an explicit error entry, got notes %+v", roster.Notes)
	}
	if roster.Notes.Errors[0].Kind != jobKindPlan {
		t.Errorf("the error entry must name the failed kind, got %q", roster.Notes.Errors[0].Kind)
	}
	if !strings.Contains(roster.Notes.Errors[0].Message, "permission denied") {
		t.Errorf("the error entry must carry the cause, got %q", roster.Notes.Errors[0].Message)
	}
}

// TestListJobs_AllStoresFailingYieldsThreeErrorEntries.
func TestListJobs_AllStoresFailingYieldsThreeErrorEntries(t *testing.T) {
	tool := NewListJobsTool(
		&fakeJobPlanStore{err: errors.New("plans down")},
		&fakeJobTaskStore{err: errors.New("tasks down")},
		&fakeJobLifecycleStore{err: errors.New("sessions down")},
	)

	roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))

	if roster.Notes == nil || len(roster.Notes.Errors) != 3 {
		t.Fatalf("want 3 per-kind error entries, got %+v", roster.Notes)
	}
	if len(roster.Rows) != 0 {
		t.Errorf("want 0 rows when every store is down, got %d", len(roster.Rows))
	}
}

// TestListJobs_NilStoreIsReportedNotSilentlyEmpty.
func TestListJobs_NilStoreIsReportedNotSilentlyEmpty(t *testing.T) {
	tool := NewListJobsTool(nil, nil, nil)

	roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))

	if roster.Notes == nil || len(roster.Notes.Errors) != 3 {
		t.Fatalf("an unwired store must be reported, not read as 'no work': got %+v", roster.Notes)
	}
}

// TestListJobs_ScanCeilingIsSpentOnTheCallersOwnRecords is the ordering
// regression guard: the ownership predicate must run BEFORE the scan ceiling,
// never after it.
//
// The fixture reproduces the real stores' orderings, which are adversarial for
// the wrong ordering rather than neutral to it. plan.Store.List sorts CreatedAt
// ASCENDING and task.Store.List sorts EffectivePriority then CreatedAt
// ascending, so the records a ceiling applied first would KEEP are the oldest
// and the highest-priority — i.e. it would discard exactly the caller's current
// work, which is the one thing the tool exists to report.
//
// The assertion is the OUTCOME the caller sees, not the ordering of two
// statements: three owned plans and three owned tasks sitting behind a wall of
// other principals' records must come back as SIX ROWS. Under the wrong
// ordering they come back as `rows: []` plus a notes.scan_truncated marker the
// model has no reason to interpret — and an empty roster reads as "I have no
// outstanding work".
func TestListJobs_ScanCeilingIsSpentOnTheCallersOwnRecords(t *testing.T) {
	const ceiling = 10

	// 50 other-principal records ahead of the caller's own, mirroring the sort
	// the real stores apply.
	plans := &fakeJobPlanStore{}
	tasks := &fakeJobTaskStore{}
	for i := 0; i < 50; i++ {
		plans.plans = append(plans.plans, plan.Plan{
			ID: fmt.Sprintf("pln-jim-%02d", i), WorkspaceID: "ws1", Title: "Jim plan",
			OwnerAgentID: "jim", State: plan.StateRunning,
			CreatedAt: fmt.Sprintf("2020-01-01T00:%02d:00Z", i),
		})
		tasks.tasks = append(tasks.tasks, task.Task{
			ID: fmt.Sprintf("tsk-jim-%02d", i), WorkspaceID: "ws1", Title: "Jim task",
			AgentID: "jim", Status: task.StatusInProgress,
		})
	}
	for i := 0; i < 3; i++ {
		plans.plans = append(plans.plans, plan.Plan{
			ID: fmt.Sprintf("pln-mia-%02d", i), WorkspaceID: "ws1", Title: "Mia plan",
			OwnerAgentID: "mia", State: plan.StateRunning,
			CreatedAt: fmt.Sprintf("2026-01-01T00:%02d:00Z", i),
		})
		tasks.tasks = append(tasks.tasks, task.Task{
			ID: fmt.Sprintf("tsk-mia-%02d", i), WorkspaceID: "ws1", Title: "Mia task",
			AgentID: "mia", Status: task.StatusInProgress,
		})
	}

	tool := NewListJobsTool(plans, tasks, nil)
	tool.SetScanCeiling(ceiling)

	roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))

	got := rowIDs(roster.Rows)
	want := map[string]bool{
		"pln-mia-00": true, "pln-mia-01": true, "pln-mia-02": true,
		"tsk-mia-00": true, "tsk-mia-01": true, "tsk-mia-02": true,
	}
	if len(got) != len(want) {
		t.Fatalf("the caller owns %d records behind 100 of somebody else's and must get all of them "+
			"(ceiling=%d); got %d rows: %v", len(want), ceiling, len(got), got)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("unexpected row %q — the roster must contain the caller's records only", id)
		}
	}

	// And the marker must be ABSENT: the caller owns 3 of each, far below the
	// ceiling, so nothing about this response is a lower bound. A marker here
	// would mean scan_truncated still tracks the store's population rather than
	// the caller's, which is the state that makes it uninterpretable.
	if roster.Notes != nil && len(roster.Notes.ScanTruncated) != 0 {
		t.Errorf("scan_truncated must describe the CALLER's population, not the store's; got %+v",
			roster.Notes.ScanTruncated)
	}
}

// TestListJobs_ScanCeilingCountsTheCallersOwnPopulation is the other half: when
// the caller genuinely owns more than the ceiling, the marker fires and its
// `present` is the caller's own count — not the store's total, which would
// leak the size of other principals' work and make the number meaningless.
func TestListJobs_ScanCeilingCountsTheCallersOwnPopulation(t *testing.T) {
	plans := &fakeJobPlanStore{}
	for i := 0; i < 40; i++ {
		plans.plans = append(plans.plans, plan.Plan{
			ID: fmt.Sprintf("pln-jim-%02d", i), WorkspaceID: "ws1", Title: "Jim plan",
			OwnerAgentID: "jim", State: plan.StateRunning,
		})
	}
	for i := 0; i < 12; i++ {
		plans.plans = append(plans.plans, plan.Plan{
			ID: fmt.Sprintf("pln-mia-%02d", i), WorkspaceID: "ws1", Title: "Mia plan",
			OwnerAgentID: "mia", State: plan.StateRunning,
		})
	}
	tool := NewListJobsTool(plans, nil, nil)
	tool.SetScanCeiling(5)

	roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{"kind": jobKindPlan}))

	if roster.Notes == nil {
		t.Fatal("a bounded scan must be reported, never presented as a complete one")
	}
	marker, ok := roster.Notes.ScanTruncated[jobKindPlan]
	if !ok {
		t.Fatalf("want a scan_truncated entry for plan, got %+v", roster.Notes.ScanTruncated)
	}
	if marker.Scanned != 5 || marker.Present != 12 {
		t.Errorf("want scanned=5 present=12 (the CALLER's 12 plans, not the store's 52); got %+v", marker)
	}
	if len(roster.Rows) != 5 {
		t.Fatalf("want the ceiling's worth of the caller's own rows, got %d: %v",
			len(roster.Rows), rowIDs(roster.Rows))
	}
	for _, id := range rowIDs(roster.Rows) {
		if !strings.HasPrefix(id, "pln-mia-") {
			t.Errorf("row %q is not the caller's — the ceiling must be spent on owned records", id)
		}
	}
}

// TestListJobs_ScanCeilingIsReportedAsALowerBound: crossing the ceiling must
// be VISIBLE, because on a store that grows monotonically and is never swept,
// exceeding it is the steady state rather than the exception. A bounded scan
// presented as a complete one silently re-opens every under-reporting bug the
// counters exist to close.
func TestListJobs_ScanCeilingIsReportedAsALowerBound(t *testing.T) {
	records := make([]session.LifecycleRecord, 0, 30)
	for i := 0; i < 30; i++ {
		records = append(records, session.LifecycleRecord{
			SessionID: fmt.Sprintf("ses-%02d", i), WorkspaceID: "ws1", AgentID: "ray",
			ParentAgentID: "mia", State: session.LifecycleRunning,
		})
	}
	tool := NewListJobsTool(nil, nil, &fakeJobLifecycleStore{records: records})
	tool.SetScanCeiling(10)

	roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))

	if roster.Notes == nil {
		t.Fatal("a bounded scan must be reported, never presented as a complete one")
	}
	marker, ok := roster.Notes.ScanTruncated[jobKindSubagent]
	if !ok {
		t.Fatalf("want a scan_truncated entry for subagent, got %+v", roster.Notes.ScanTruncated)
	}
	if marker.Scanned != 10 || marker.Present != 30 {
		t.Errorf("want scanned=10 present=30, got scanned=%d present=%d", marker.Scanned, marker.Present)
	}
	if len(roster.Rows) != 10 {
		t.Errorf("want the ceiling's worth of rows, got %d", len(roster.Rows))
	}

	// A second run at a different configured ceiling proves the value is read
	// rather than hardcoded.
	tool.SetScanCeiling(5)
	again := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))
	if again.Notes.ScanTruncated[jobKindSubagent].Scanned != 5 {
		t.Errorf("the ceiling must be configurable, got %+v", again.Notes.ScanTruncated)
	}

	// And below the ceiling, no marker at all — a response WITHOUT it is exact.
	tool.SetScanCeiling(1000)
	exact := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))
	if exact.Notes != nil && len(exact.Notes.ScanTruncated) != 0 {
		t.Errorf("a below-ceiling scan must carry no marker, got %+v", exact.Notes.ScanTruncated)
	}
}

// TestListJobs_TruncationAlwaysReportsACount at the Execute level, over a
// realistic mixed population.
func TestListJobs_TruncationAlwaysReportsACount(t *testing.T) {
	plans := &fakeJobPlanStore{}
	for i := 0; i < 400; i++ {
		plans.plans = append(plans.plans, plan.Plan{
			ID: fmt.Sprintf("pln-%03d", i), WorkspaceID: "ws1", Title: "queued plan",
			OwnerAgentID: "mia", State: plan.StateApproved,
		})
	}
	tasks := &fakeJobTaskStore{}
	for i := 0; i < 400; i++ {
		tasks.tasks = append(tasks.tasks, task.Task{
			ID: fmt.Sprintf("tsk-%03d", i), WorkspaceID: "ws1", Title: "running task",
			AgentID: "mia", Status: task.StatusInProgress,
		})
	}
	lifecycles := &fakeJobLifecycleStore{}
	for i := 0; i < 3; i++ {
		lifecycles.records = append(lifecycles.records, session.LifecycleRecord{
			SessionID: fmt.Sprintf("ses-%d", i), WorkspaceID: "ws1", AgentID: "ray",
			ParentAgentID: "mia", State: session.LifecycleNeedsInput,
		})
	}
	tool := NewListJobsTool(plans, tasks, lifecycles)

	roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))

	if roster.Notes == nil || roster.Notes.Omitted == nil {
		t.Fatalf("803 jobs against a 75-row live maximum must report omissions, got %+v", roster.Notes)
	}
	if roster.Notes.Omitted.ByStatus[jobStatusQueued] != 375 {
		t.Errorf("omitted queued: want 375, got %d", roster.Notes.Omitted.ByStatus[jobStatusQueued])
	}
	if roster.Notes.Omitted.ByStatus[jobStatusRunning] != 375 {
		t.Errorf("omitted running: want 375, got %d", roster.Notes.Omitted.ByStatus[jobStatusRunning])
	}
	if _, present := roster.Notes.Omitted.ByStatus[jobStatusBlocked]; present {
		t.Errorf("no blocked rows were dropped, so no blocked entry may appear: %v",
			roster.Notes.Omitted.ByStatus)
	}
	// The three attention=caller rows are the ones the caller most needs.
	blocked := 0
	for _, row := range roster.Rows {
		if row.Status == jobStatusBlocked {
			blocked++
			if row.Attention != attentionCaller {
				t.Errorf("a needs_input subagent must flag attention=caller, got %q", row.Attention)
			}
		}
	}
	if blocked != 3 {
		t.Fatalf("all 3 blocked rows must survive an 800-row queued/running population, got %d", blocked)
	}

	sumKind, sumStatus := 0, 0
	for _, v := range roster.Notes.Omitted.ByKind {
		sumKind += v
	}
	for _, v := range roster.Notes.Omitted.ByStatus {
		sumStatus += v
	}
	if sumKind != roster.Notes.TotalOmitted || sumStatus != roster.Notes.TotalOmitted {
		t.Errorf("both key spaces must sum to total_omitted=%d, got by_kind=%d by_status=%d",
			roster.Notes.TotalOmitted, sumKind, sumStatus)
	}
}

// TestListJobs_UnmappedNativeStateIsCounted at the Execute level.
func TestListJobs_UnmappedNativeStateIsCounted(t *testing.T) {
	plans := &fakeJobPlanStore{plans: []plan.Plan{
		{ID: "pln-weird", WorkspaceID: "ws1", Title: "From a newer build",
			OwnerAgentID: "mia", State: plan.State("teleporting")},
	}}
	tool := NewListJobsTool(plans, nil, nil)

	roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{"kind": jobKindPlan}))

	if len(roster.Rows) != 1 {
		t.Fatalf("an unmappable state must not drop the row, got %d rows", len(roster.Rows))
	}
	if roster.Notes == nil || roster.Notes.Unmapped[jobKindPlan] != 1 {
		t.Errorf("want unmapped[plan]=1, got %+v", roster.Notes)
	}
	if !strings.HasPrefix(roster.Rows[0].NativeStatus, unknownNativeStatusPrefix) {
		t.Errorf("want a marked native_status, got %q", roster.Rows[0].NativeStatus)
	}
}

// ---------------------------------------------------------------------------
// Arguments
// ---------------------------------------------------------------------------

// TestListJobs_UnknownArgumentIsIgnoredNotRejected. An LLM passing `relation`
// — a field it just read off a response row — must not lose a whole turn to a
// validation error.
func TestListJobs_UnknownArgumentIsIgnoredNotRejected(t *testing.T) {
	tool := newJobFixture().tool

	roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{
		"relation": "runs",
		"since":    "yesterday",
	}))

	if len(roster.Rows) == 0 {
		t.Fatal("an unknown argument must not suppress the roster")
	}
	if roster.Notes == nil {
		t.Fatal("an ignored argument must be reported")
	}
	want := []string{"relation", "since"}
	if strings.Join(roster.Notes.IgnoredArgs, ",") != strings.Join(want, ",") {
		t.Errorf("want ignored_args %v, got %v", want, roster.Notes.IgnoredArgs)
	}
}

// TestListJobs_LimitAboveMaximumIsClampedNotRejected.
func TestListJobs_LimitAboveMaximumIsClampedNotRejected(t *testing.T) {
	tool := newJobFixture().tool

	roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{"limit": float64(250)}))
	if roster.Notes == nil || roster.Notes.LimitClampedTo != hardLimitMax {
		t.Errorf("want limit_clamped_to=%d, got %+v", hardLimitMax, roster.Notes)
	}

	// At or below the hard maximum, no clamp is reported even though the live
	// bound is far lower — 200 is a legal request, it just cannot produce 200
	// live rows.
	within := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{"limit": float64(200)}))
	if within.Notes != nil && within.Notes.LimitClampedTo != 0 {
		t.Errorf("limit=200 is within the maximum and must not report a clamp, got %+v", within.Notes)
	}
}

// TestListJobs_TerminalStatusImpliesIncludeTerminal. Asking "what failed?" and
// getting an empty roster would teach the caller nothing had failed.
func TestListJobs_TerminalStatusImpliesIncludeTerminal(t *testing.T) {
	plans := &fakeJobPlanStore{plans: []plan.Plan{
		{ID: "pln-dead", WorkspaceID: "ws1", Title: "It broke", OwnerAgentID: "mia",
			State: plan.StateFailed, FailedReason: plan.FailedReasonJudgeRoundsExhausted},
	}}
	tool := NewListJobsTool(plans, nil, nil)

	roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{"status": jobStatusFailed}))

	if len(roster.Rows) != 1 || roster.Rows[0].ID != "pln-dead" {
		t.Fatalf("status=failed must return the failed row without also passing include_terminal, got %v",
			rowIDs(roster.Rows))
	}
}

// TestListJobs_ArgumentValidation: a KNOWN argument carrying an invalid value
// is the one hard error, and it returns zero rows.
func TestListJobs_ArgumentValidation(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
	}{
		{"kind out of enum", map[string]any{"kind": "shell"}},
		{"kind wrong type", map[string]any{"kind": 7}},
		{"status out of enum", map[string]any{"status": "in_progress"}},
		{"include_terminal wrong type", map[string]any{"include_terminal": "yes"}},
		{"include_drafts wrong type", map[string]any{"include_drafts": 1}},
		{"limit zero", map[string]any{"limit": float64(0)}},
		{"limit negative", map[string]any{"limit": float64(-5)}},
		{"limit fractional", map[string]any{"limit": 2.5}},
		{"limit wrong type", map[string]any{"limit": "ten"}},
		{"label_contains too long", map[string]any{"label_contains": strings.Repeat("x", 65)}},
		{"label_contains wrong type", map[string]any{"label_contains": true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool := newJobFixture().tool
			res := tool.Execute(jobCtx("mia", "ws1"), tc.args)

			if !res.IsError {
				t.Fatalf("want a validation error, got: %s", res.ForLLM)
			}
			var roster listJobsResponse
			if err := json.Unmarshal([]byte(res.ForLLM), &roster); err == nil && len(roster.Rows) > 0 {
				t.Fatalf("a validation error must return zero rows, got %d", len(roster.Rows))
			}
		})
	}
}

// TestListJobs_LabelContainsReachesPastTheGroupBound: bounds are hard at 25 per
// live group, and kind/status alone cannot reach row 26. Without a narrowing
// predicate the tool's headline use case — find the handle for the job I lost —
// fails deterministically once a caller has more than 25 jobs in a group.
func TestListJobs_LabelContainsReachesPastTheGroupBound(t *testing.T) {
	plans := &fakeJobPlanStore{}
	for i := 0; i < 60; i++ {
		title := "Routine sweep"
		if i == 55 {
			title = "Migrate the audit chain to HMAC"
		}
		plans.plans = append(plans.plans, plan.Plan{
			ID: fmt.Sprintf("pln-%03d", i), WorkspaceID: "ws1", Title: title,
			OwnerAgentID: "mia", State: plan.StateRunning, PlanPhase: plan.PhaseDispatching,
			StartedAt: fmt.Sprintf("2026-07-27T00:%02d:00Z", i),
		})
	}
	tool := NewListJobsTool(plans, nil, nil)

	// Without the filter the row is past the bound and unreachable.
	wide := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))
	if contains(rowIDs(wide.Rows), "pln-055") {
		t.Fatal("fixture bug: the target row is inside the bound, so nothing is being tested")
	}

	narrowed := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{
		"label_contains": "AUDIT chain",
	}))
	if len(narrowed.Rows) != 1 || narrowed.Rows[0].ID != "pln-055" {
		t.Fatalf("a case-insensitive label filter must reach past the bound, got %v", rowIDs(narrowed.Rows))
	}
	// Counters stay exact against the FILTERED population: nothing was
	// truncated once the filter narrowed it to one row.
	if narrowed.Notes != nil && narrowed.Notes.TotalOmitted != 0 {
		t.Errorf("counters must be exact against the filtered population, got %+v", narrowed.Notes)
	}
}

// TestListJobs_DraftsExcludedByDefault.
func TestListJobs_DraftsExcludedByDefault(t *testing.T) {
	plans := &fakeJobPlanStore{plans: []plan.Plan{
		{ID: "pln-draft", WorkspaceID: "ws1", Title: "Half written", OwnerAgentID: "mia", State: plan.StateDraft},
		{ID: "pln-approved", WorkspaceID: "ws1", Title: "Ready", OwnerAgentID: "mia", State: plan.StateApproved},
	}}
	tool := NewListJobsTool(plans, nil, nil)

	def := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))
	if contains(rowIDs(def.Rows), "pln-draft") {
		t.Error("drafts must be excluded by default")
	}
	if !contains(rowIDs(def.Rows), "pln-approved") {
		t.Error("approved plans must appear by default")
	}

	withDrafts := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{"include_drafts": true}))
	if len(withDrafts.Rows) != 2 {
		t.Fatalf("include_drafts must return both, got %v", rowIDs(withDrafts.Rows))
	}
	// Approved ranks above draft inside the queued group.
	if withDrafts.Rows[0].ID != "pln-approved" {
		t.Errorf("approved must rank above draft, got %v", rowIDs(withDrafts.Rows))
	}
}

// TestListJobs_ApprovedPlanIsQueuedNotRunning: there is no `queued` plan state,
// so `approved` IS the queue. Reporting it as running would tell the caller
// work is in flight that has not started.
func TestListJobs_ApprovedPlanIsQueuedNotRunning(t *testing.T) {
	plans := &fakeJobPlanStore{plans: []plan.Plan{
		{ID: "pln-waiting", WorkspaceID: "ws1", Title: "Cap-waiting", OwnerAgentID: "mia",
			State: plan.StateApproved},
	}}
	tool := NewListJobsTool(plans, nil, nil)

	roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))

	if len(roster.Rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(roster.Rows))
	}
	if roster.Rows[0].Status != jobStatusQueued {
		t.Errorf("an approved plan must report queued, got %q", roster.Rows[0].Status)
	}
	if roster.Rows[0].StartedAt != "" {
		t.Errorf("a queued plan has never started, got started_at=%q", roster.Rows[0].StartedAt)
	}
}

// ---------------------------------------------------------------------------
// Cap pressure
// ---------------------------------------------------------------------------

// TestListJobs_CapFieldsDistinguishAStoppedEngineFromASaturatedOne.
//
// cap_active=0 is the entire signal. Omitting it — whether through an
// omit-when-zero rule or through a suppress-when-stale rule — makes "a dead
// engine will never start my plan" indistinguishable from "a healthy queue,
// wait", and the agent then waits forever on work nothing will start. A
// stopped engine's snapshot is ALWAYS stale, so suppressing on staleness
// deletes the answer in exactly the state the field exists for.
func TestListJobs_CapFieldsDistinguishAStoppedEngineFromASaturatedOne(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	plans := &fakeJobPlanStore{plans: []plan.Plan{
		{ID: "pln-waiting", WorkspaceID: "ws1", Title: "Cap-waiting", OwnerAgentID: "mia",
			State: plan.StateApproved},
	}}

	t.Run("stopped engine emits a stale but present pair", func(t *testing.T) {
		tool := NewListJobsTool(plans, nil, nil)
		tool.SetNow(func() time.Time { return now })
		tool.SetCapSnapshotSource(func() JobCapSnapshotSource {
			return fakeCapSource{
				active: 0, capMax: 16, reliable: true,
				observedAt: now.Add(-30 * time.Minute),
				lastTickAt: now.Add(-30 * time.Minute),
			}
		})

		roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))

		if roster.CapActive == nil || roster.CapMax == nil {
			t.Fatal("the cap pair must be emitted even when stale — omitting it destroys the signal")
		}
		if *roster.CapActive != 0 || *roster.CapMax != 16 {
			t.Errorf("want cap_active=0 cap_max=16, got %d/%d", *roster.CapActive, *roster.CapMax)
		}
		if roster.CapObservedAt == "" {
			t.Error("cap_observed_at must be emitted so the caller can see the age rather than have it hidden")
		}
		if roster.EngineRunning == nil || *roster.EngineRunning {
			t.Error("engine_running must be false when the tick heartbeat is older than the bound")
		}
	})

	t.Run("saturated engine reports the GLOBAL count", func(t *testing.T) {
		// The caller owns ONE plan; 16 slots are consumed by other agents in
		// other workspaces. A count re-derived from this tool's own
		// owner-scoped list would report 0/16 — "far below cap, nothing will
		// ever start it" — and the agent would intervene on healthy work.
		tool := NewListJobsTool(plans, nil, nil)
		tool.SetNow(func() time.Time { return now })
		tool.SetCapSnapshotSource(func() JobCapSnapshotSource {
			return fakeCapSource{
				active: 16, capMax: 16, reliable: true,
				observedAt: now.Add(-5 * time.Second),
				lastTickAt: now.Add(-5 * time.Second),
			}
		})

		roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))

		if roster.CapActive == nil || *roster.CapActive != 16 {
			t.Fatalf("want the engine's global count 16, got %v", roster.CapActive)
		}
		if roster.EngineRunning == nil || !*roster.EngineRunning {
			t.Error("a freshly ticked engine must report engine_running=true")
		}
	})

	t.Run("unreliable snapshot omits the pair but keeps the heartbeat", func(t *testing.T) {
		tool := NewListJobsTool(plans, nil, nil)
		tool.SetNow(func() time.Time { return now })
		tool.SetCapSnapshotSource(func() JobCapSnapshotSource {
			return fakeCapSource{
				active: 3, capMax: 16, reliable: false,
				observedAt: now.Add(-1 * time.Second),
				lastTickAt: now.Add(-1 * time.Second),
			}
		})

		roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))

		if roster.CapActive != nil || roster.CapMax != nil {
			t.Error("an unreliable snapshot must omit the pair rather than report a wrong number")
		}
		if roster.CapObservedAt != "" {
			t.Error("cap_observed_at must be omitted with the pair")
		}
		if roster.EngineRunning == nil {
			t.Error("engine_running comes from the tick heartbeat, not the snapshot, so it survives")
		}
	})

	t.Run("no snapshot yet omits the pair", func(t *testing.T) {
		tool := NewListJobsTool(plans, nil, nil)
		tool.SetNow(func() time.Time { return now })
		tool.SetCapSnapshotSource(func() JobCapSnapshotSource {
			return fakeCapSource{capMax: 16, reliable: true, lastTickAt: now.Add(-1 * time.Second)}
		})

		roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))

		if roster.CapActive != nil || roster.CapMax != nil {
			t.Error("an engine that has never admitted has no snapshot to report")
		}
		if roster.EngineRunning == nil || !*roster.EngineRunning {
			t.Error("engine_running must still be emitted")
		}
	})

	t.Run("no engine wired omits everything", func(t *testing.T) {
		tool := NewListJobsTool(plans, nil, nil)
		roster := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))

		if roster.CapActive != nil || roster.CapMax != nil || roster.EngineRunning != nil {
			t.Error("with no engine wired, every cap field must be absent")
		}
	})
}

// ---------------------------------------------------------------------------
// Contract-level properties
// ---------------------------------------------------------------------------

// TestListJobs_NoCrossScopeReuse fails if any roster memo or cache is ever
// introduced.
//
// A memo keyed on the principal serves a workspace-less turn's cross-workspace
// roster to a later scoped turn — cross-workspace disclosure against the P0
// scoping control — and returns the previous answer to every narrowed call. A
// memo keyed on the full argument set fixes those but bounds no cost, because
// an agent varying `limit` bypasses it. The test asserts the PROPERTY rather
// than the absence of a field, so reintroducing a cache fails the suite instead
// of silently passing it.
func TestListJobs_NoCrossScopeReuse(t *testing.T) {
	tool := newJobFixture().tool

	// A workspace-less call immediately followed by a scoped one.
	wide := decodeRoster(t, tool.Execute(jobCtx("mia", ""), map[string]any{}))
	scoped := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))

	if !scoped.WorkspaceScoped {
		t.Error("the scoped call returned the previous call's workspace_scoped=false")
	}
	for _, row := range scoped.Rows {
		if row.WorkspaceID != "ws1" {
			t.Errorf("the scoped call served the widened roster: row %q from %q", row.ID, row.WorkspaceID)
		}
	}
	if len(wide.Rows) <= len(scoped.Rows) {
		t.Fatalf("fixture bug: the widened roster must be strictly larger (wide=%d scoped=%d)",
			len(wide.Rows), len(scoped.Rows))
	}

	// A narrowed call immediately following a default one.
	_ = decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{}))
	plansOnly := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{"kind": jobKindPlan}))
	for _, row := range plansOnly.Rows {
		if row.Kind != jobKindPlan {
			t.Errorf("kind=plan returned a %q row — the previous call's roster was reused", row.Kind)
		}
	}
	if len(plansOnly.Rows) == 0 {
		t.Error("kind=plan must still return the plan rows")
	}
}

// TestListJobs_KindArgumentReadsOnlyThatStore.
func TestListJobs_KindArgumentReadsOnlyThatStore(t *testing.T) {
	f := newJobFixture()

	_ = f.tool.Execute(jobCtx("mia", "ws1"), map[string]any{"kind": jobKindPlan})

	if n := f.plans.callCount(); n != 1 {
		t.Errorf("the plan store must be read once, got %d", n)
	}
	if tn, ln := f.tasks.callCount(), f.lifecycles.callCount(); tn != 0 || ln != 0 {
		t.Errorf("kind=plan must not touch the other stores (tasks=%d lifecycles=%d)", tn, ln)
	}
}

// TestListJobs_ReadOnly: every call must leave every store untouched.
func TestListJobs_ReadOnly(t *testing.T) {
	f := newJobFixture()
	before := fmt.Sprintf("%v|%v|%v", f.plans.plans, f.tasks.tasks, f.lifecycles.records)

	for i := 0; i < 5; i++ {
		_ = f.tool.Execute(jobCtx("mia", "ws1"), map[string]any{"include_terminal": true})
	}

	if after := fmt.Sprintf("%v|%v|%v", f.plans.plans, f.tasks.tasks, f.lifecycles.records); after != before {
		t.Error("list_jobs mutated a store")
	}
}

// TestListJobs_RowShapeIsComplete: every field the response contract requires
// is present on every row, in both scoping modes.
func TestListJobs_RowShapeIsComplete(t *testing.T) {
	tool := newJobFixture().tool

	for _, workspace := range []string{"ws1", ""} {
		res := tool.Execute(jobCtx("mia", workspace), map[string]any{"include_terminal": true})
		roster := decodeRoster(t, res)
		if len(roster.Rows) == 0 {
			t.Fatalf("workspace=%q: fixture bug, no rows", workspace)
		}

		var raw struct {
			Rows []map[string]json.RawMessage `json:"rows"`
		}
		if err := json.Unmarshal([]byte(res.ForLLM), &raw); err != nil {
			t.Fatalf("workspace=%q: %v", workspace, err)
		}
		required := []string{
			"kind", "id", "label", "status", "native_status", "relation", "attention",
			"started_at", "last_activity_at", "workspace_id", "actionable", "intentionally_stopped",
		}
		for i, row := range raw.Rows {
			for _, field := range required {
				if _, ok := row[field]; !ok {
					t.Errorf("workspace=%q row %d is missing required field %q", workspace, i, field)
				}
			}
		}
	}
}

// TestListJobs_ResponseSizeBound runs over a 4-byte-rune corpus and an ASCII
// mirror, and asserts the SAME bound for both — because a fixture whose
// alphabet decides pass/fail is not a bound at all.
func TestListJobs_ResponseSizeBound(t *testing.T) {
	// 95 = 75 live + 20 terminal, the maximum row count any response carries.
	const maxResponseBytes = maxRows*(labelMaxBytes+nativeStatusMaxBytes+512) + 2048

	for _, corpus := range []struct {
		name string
		rune string
	}{
		{"emoji 4-byte runes", "🐙"},
		{"ascii mirror", "x"},
	} {
		t.Run(corpus.name, func(t *testing.T) {
			bigLabel := strings.Repeat(corpus.rune, 10_000)
			bigReason := strings.Repeat(corpus.rune, 4_000)

			plans := &fakeJobPlanStore{}
			for i := 0; i < 200; i++ {
				plans.plans = append(plans.plans, plan.Plan{
					ID: fmt.Sprintf("pln-%03d", i), WorkspaceID: "ws1", Title: bigLabel,
					OwnerAgentID: "mia", State: plan.StateRunning,
					PlanPhase: plan.PhaseDispatching, PausedReason: bigReason,
					StartedAt: "2026-07-27T00:00:00Z",
				})
			}
			lifecycles := &fakeJobLifecycleStore{}
			for i := 0; i < 300; i++ {
				lifecycles.records = append(lifecycles.records, session.LifecycleRecord{
					SessionID: fmt.Sprintf("ses-%03d", i), WorkspaceID: "ws1",
					AgentID: bigLabel, ParentAgentID: "mia",
					State: session.LifecycleFailed, FailedReason: bigReason,
				})
			}
			tool := NewListJobsTool(plans, nil, lifecycles)

			res := tool.Execute(jobCtx("mia", "ws1"), map[string]any{
				"include_terminal": true, "limit": float64(hardLimitMax),
			})
			roster := decodeRoster(t, res)

			if n := len(res.ForLLM); n > maxResponseBytes {
				t.Errorf("response is %d bytes, exceeding the %d-byte bound", n, maxResponseBytes)
			}
			if len(roster.Rows) > maxRows {
				t.Errorf("response carries %d rows, exceeding the %d-row maximum", len(roster.Rows), maxRows)
			}
			for _, row := range roster.Rows {
				if n := len([]rune(row.Label)); n > labelMaxRunes {
					t.Fatalf("label is %d runes, exceeding %d", n, labelMaxRunes)
				}
				if n := len([]rune(row.NativeStatus)); n > nativeStatusMaxRunes {
					t.Fatalf("native_status is %d runes, exceeding %d", n, nativeStatusMaxRunes)
				}
			}
		})
	}
}

// TestListJobs_EmittedRowsAreRedactedAndBounded is the end-to-end version of
// the free-text pipeline: whatever the unit tests prove about the helpers,
// what matters is that a secret sitting in a plan title or a pause reason
// never reaches the caller's context — and that both fields come back bounded.
//
// The pause reason matters as much as the title: PausedReason is a bare string
// that no validator ever sees, so a wrapped error carrying a credential-bearing
// URL lands in native_status, which is REQUIRED on every row and therefore
// subject to exactly the same treatment as the label.
func TestListJobs_EmittedRowsAreRedactedAndBounded(t *testing.T) {
	const longSecret = "sk-live-0123456789abcdefghijklmnopqrstuvwxyz"
	const shortSecret = "hunter2" // 7 BYTES — below FilterMinLength's gate

	cfg := &config.Config{}
	cfg.Tools.FilterSensitiveData = true
	cfg.RegisterSensitiveValues([]string{longSecret, shortSecret})

	plans := &fakeJobPlanStore{plans: []plan.Plan{
		{ID: "pln-1", WorkspaceID: "ws1", OwnerAgentID: "mia", State: plan.StateRunning,
			Title:        "Deploy with " + longSecret + " " + strings.Repeat("padding ", 400),
			PausedReason: "upstream rejected https://user:" + shortSecret + "@example.com/api"},
	}}
	tool := NewListJobsTool(plans, nil, nil)
	tool.SetConfig(func() *config.Config { return cfg })

	res := tool.Execute(jobCtx("mia", "ws1"), map[string]any{"kind": jobKindPlan})
	roster := decodeRoster(t, res)

	if len(roster.Rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(roster.Rows))
	}
	row := roster.Rows[0]

	for _, secret := range []string{longSecret, shortSecret} {
		if strings.Contains(res.ForLLM, secret) {
			t.Errorf("secret %q reached the caller's context in full", secret)
		}
		for i := 0; i+8 <= len(secret); i++ {
			if window := secret[i : i+8]; strings.Contains(res.ForLLM, window) {
				t.Errorf("8-byte window %q of secret %q reached the caller's context", window, secret)
			}
		}
	}
	if n := len([]rune(row.Label)); n > labelMaxRunes {
		t.Errorf("emitted label is %d runes, exceeding %d", n, labelMaxRunes)
	}
	if n := len([]rune(row.NativeStatus)); n > nativeStatusMaxRunes {
		t.Errorf("emitted native_status is %d runes, exceeding %d", n, nativeStatusMaxRunes)
	}
	if row.NativeStatus == "" {
		t.Error("native_status is required on every row and must never be emptied by redaction")
	}
}

// TestListJobs_DescriptionContract asserts BOTH the mandated clauses and the
// size bound. A presence-only assertion passes no matter how large the string
// grows, and this text is a fixed per-request token cost for every agent that
// has the tool.
func TestListJobs_DescriptionContract(t *testing.T) {
	desc := (&ListJobsTool{}).Description()

	if n := len(desc); n > 900 {
		t.Errorf("Description() is %d characters, exceeding the 900-character bound", n)
	}
	for _, clause := range []struct{ name, needle string }{
		{"actionable=false is informational", "actionable=false"},
		{"action tools reject the id", "will not accept its id"},
		{"attention=caller", "attention=caller"},
		{"attention=elsewhere and do-not-intervene", "must not intervene"},
		{"attention=none on a blocked row", "attention=none on a blocked row"},
		{"id is meaningful only with its kind", "only paired with its kind"},
		{"read-only near-snapshot", "near-snapshot"},
		{"the bounds", "25 queued"},
		{"notes null convention", "notes is null"},
		{"counters omitted when zero", "omitted when zero"},
	} {
		if !strings.Contains(desc, clause.needle) {
			t.Errorf("Description() is missing the %s clause (%q)", clause.name, clause.needle)
		}
	}
	// Operator-facing material belongs in the runbook, not in a string sent on
	// every request to every agent.
	for _, forbidden := range []string{"sandbox.tool_policies", "runbook", "kill switch"} {
		if strings.Contains(strings.ToLower(desc), forbidden) {
			t.Errorf("Description() carries operator-facing material %q", forbidden)
		}
	}
}

// TestListJobs_ConcurrentCallsAreSafe. Run with -race.
func TestListJobs_ConcurrentCallsAreSafe(t *testing.T) {
	tool := newJobFixture().tool

	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(n int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 25; j++ {
				workspace := "ws1"
				if n%2 == 0 {
					workspace = ""
				}
				res := tool.Execute(jobCtx("mia", workspace), map[string]any{})
				if res.IsError {
					t.Errorf("concurrent call errored: %s", res.ForLLM)
					return
				}
			}
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
