package tools

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// ---------------------------------------------------------------------------
// ADR-091 FR-C-010: label_contains and the displayed label read the launch
// title from the lifecycle record. They never reopen the live delegate index.
//
// Repro (live UAT, 2026-08-03): delegate(run, async=true,
// label="UAT_LABEL_TEST_PROBE", ...) dispatched successfully, then
// list_jobs(label_contains="uat_label") returned {"rows":[],"notes":null} —
// the dispatch was never found by its own label, because
// pkg/tools/list_jobs_sources.go's subagentLabel unconditionally returned the
// delegated agent's display name (e.g. "Worker") with no reference at all to
// the caller's `label` argument.
// ---------------------------------------------------------------------------

// fakeLabelResolver mirrors JobLabelResolver exactly (a batch accessor over a
// static map), the same fake-per-real-store-filter-semantics discipline this
// file's other fakes already follow: a session id absent from the map is a
// "no custom label was set" miss, never a defect.
type fakeLabelResolver struct {
	labels map[string]string
	calls  int
}

func (f *fakeLabelResolver) ResolvableLabels(ids []string) map[string]string {
	f.calls++
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		if v, ok := f.labels[id]; ok {
			out[id] = v
		}
	}
	return out
}

// TestListJobs_LabelContainsMatchesCustomDelegateLabel is the RED/GREEN test
// for the defect itself: dispatched with a custom label, the row MUST be
// findable by that label — and, per Binding Rule 4, a non-matching term MUST
// still return zero rows so this is not a filter that has stopped filtering.
func TestListJobs_LabelContainsMatchesCustomDelegateLabel(t *testing.T) {
	lifecycles := &fakeJobLifecycleStore{records: []session.LifecycleRecord{
		// The labeled dispatch — the exact UAT repro shape: a running
		// subagent delegated to "worker", tagged with a caller-chosen label
		// at dispatch time.
		{SessionID: "ses-labeled", WorkspaceID: "ws1", AgentID: "worker", Title: "UAT_LABEL_TEST_PROBE",
			ParentAgentID: "mia", State: session.LifecycleRunning},
		// A second, UNLABELED dispatch to the SAME agent — present so a test
		// that accidentally matched on agent id/name alone would return BOTH
		// rows instead of exactly one.
		{SessionID: "ses-unlabeled", WorkspaceID: "ws1", AgentID: "worker",
			ParentAgentID: "mia", State: session.LifecycleRunning},
	}}
	tool := NewListJobsTool(nil, nil, lifecycles)
	tool.SetAgentNamer(func() JobAgentNamer { return fakeAgentNamer{"worker": "Worker"} })
	liveLabels := &fakeLabelResolver{labels: map[string]string{
		"ses-labeled": "UAT_LABEL_TEST_PROBE",
	}}
	tool.SetLabelResolver(func() JobLabelResolver {
		return liveLabels
	})

	// Positive lower bound: the label the caller actually set MUST find its
	// row, case-insensitively, substring-matched — exactly the UAT repro
	// (label_contains="uat_label").
	matched := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{
		"label_contains": "uat_label",
	}))
	if len(matched.Rows) != 1 || matched.Rows[0].ID != "ses-labeled" {
		t.Fatalf("custom label must match its own row exactly, got %v", rowIDs(matched.Rows))
	}
	if matched.Rows[0].Label != "UAT_LABEL_TEST_PROBE" {
		t.Errorf("displayed label = %q, want durable lifecycle title", matched.Rows[0].Label)
	}
	if liveLabels.calls != 0 {
		t.Errorf("live delegate label resolver called %d times, want 0", liveLabels.calls)
	}

	// Negative assertion, paired per Binding Rule 4: a term that matches
	// NEITHER row's custom label nor its agent name must return zero rows —
	// proving the filter still excludes, not just includes.
	unmatched := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{
		"label_contains": "no-such-label-anywhere",
	}))
	if len(unmatched.Rows) != 0 {
		t.Fatalf("a non-matching label must return zero rows, got %v", rowIDs(unmatched.Rows))
	}
}

func TestListJobs_OneRowPerSession_AfterRestart(t *testing.T) {
	dir := t.TempDir()
	store := session.NewLifecycleStore(dir)
	records := []*session.LifecycleRecord{
		{
			SessionID: "task-session", Generation: 1, State: session.LifecycleRunning,
			WorkspaceID: "ws1", AgentID: "worker", ParentAgentID: "mia", Title: "Task-backed worker",
			OwnerScopeKind: session.OwnerScopeParentSession,
			Origin:         &session.Origin{Kind: session.OriginKindTask, TaskID: "task-1"},
		},
		{
			SessionID: "delegate-session", Generation: 1, State: session.LifecycleRunning,
			WorkspaceID: "ws1", AgentID: "worker", ParentAgentID: "mia", Title: "Durable delegate title",
			OwnerScopeKind: session.OwnerScopeParentSession,
			Origin:         &session.Origin{Kind: session.OriginKindDelegate, CallID: "call-1"},
		},
	}
	for _, rec := range records {
		if err := store.Persist(rec); err != nil {
			t.Fatalf("Persist(%s): %v", rec.SessionID, err)
		}
	}

	// Reopen the store to prove the row comes from the record, not any live
	// delegate state. Ceiling 1 also proves task-origin exclusion happens
	// before the result limit.
	reopened := session.NewLifecycleStore(dir)
	result := collectSubagentRows(reopened, "mia", "ws1", newRedactor(nil), 1, nil, nil, nil)
	if result.err != nil {
		t.Fatalf("collectSubagentRows: %v", result.err)
	}
	if len(result.rows) != 1 {
		t.Fatalf("rows = %d, want one delegate-only row", len(result.rows))
	}
	row := result.rows[0]
	if row.ID != "delegate-session" || row.Label != "Durable delegate title" || !row.Actionable {
		t.Fatalf("row = %+v, want actionable delegate row labelled from lifecycle title", row)
	}
}

// TestListJobs_LabelContainsFallsBackToAgentNameWhenNoCustomLabel locks in the
// deliberate fallback decision: label_contains matching a subagent row's
// agent display name must keep working for the (very common) delegation made
// with no `label` argument at all — this is not incidental, it is what keeps
// every un-labeled subagent row findable exactly as it was before this fix.
func TestListJobs_LabelContainsFallsBackToAgentNameWhenNoCustomLabel(t *testing.T) {
	lifecycles := &fakeJobLifecycleStore{records: []session.LifecycleRecord{
		{SessionID: "ses-ray", WorkspaceID: "ws1", AgentID: "ray",
			ParentAgentID: "mia", State: session.LifecycleRunning},
	}}
	tool := NewListJobsTool(nil, nil, lifecycles)
	tool.SetAgentNamer(func() JobAgentNamer { return fakeAgentNamer{"ray": "Ray"} })
	// A resolver IS wired, but it has no entry at all for this session id —
	// the ordinary "no label was set for this dispatch" case, not a defect.
	tool.SetLabelResolver(func() JobLabelResolver { return &fakeLabelResolver{labels: map[string]string{}} })

	// Positive lower bound: falls back to the agent display name.
	byName := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{
		"label_contains": "ray",
	}))
	if len(byName.Rows) != 1 || byName.Rows[0].ID != "ses-ray" {
		t.Fatalf("with no custom label, agent name must still match, got %v", rowIDs(byName.Rows))
	}

	// Negative assertion, paired: an unrelated term must still exclude it.
	byOther := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{
		"label_contains": "unrelated-term",
	}))
	if len(byOther.Rows) != 0 {
		t.Fatalf("a non-matching term must return zero rows, got %v", rowIDs(byOther.Rows))
	}
}

// TestListJobs_LabelContainsWithNoResolverWiredIsUnchanged proves this fix is
// inert — never a behavior CHANGE — for the production wiring state that
// exists today: nothing yet calls SetLabelResolver (that requires
// pkg/tools/delegate.go to implement JobLabelResolver and pkg/agent/loop.go
// to wire it, both out of this fix's file scope — see the causal-chain
// report). Without it, matching must be byte-identical to pre-fix behavior:
// agent name only.
func TestListJobs_LabelContainsWithNoResolverWiredIsUnchanged(t *testing.T) {
	lifecycles := &fakeJobLifecycleStore{records: []session.LifecycleRecord{
		{SessionID: "ses-ava", WorkspaceID: "ws1", AgentID: "ava",
			ParentAgentID: "mia", State: session.LifecycleRunning},
	}}
	tool := NewListJobsTool(nil, nil, lifecycles)
	tool.SetAgentNamer(func() JobAgentNamer { return fakeAgentNamer{"ava": "Ava"} })
	// SetLabelResolver deliberately NOT called.

	byName := decodeRoster(t, tool.Execute(jobCtx("mia", "ws1"), map[string]any{
		"label_contains": "ava",
	}))
	if len(byName.Rows) != 1 || byName.Rows[0].ID != "ses-ava" {
		t.Fatalf("with no resolver wired, agent name must still match, got %v", rowIDs(byName.Rows))
	}
}
