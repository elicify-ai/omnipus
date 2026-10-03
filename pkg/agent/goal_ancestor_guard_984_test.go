package agent

import (
	"testing"

	agenttestutil "github.com/elicify-ai/omnipus/pkg/agent/testutil"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// goalAncestorGuardHarness is a real store-backed root chat -> child ->
// grandchild tree. Every node owns a distinct ACTIVE session goal with its
// own id, so each assertion can prove the ORIGINAL goal of every node —
// the stopped node, its descendants and its ancestors alike — is still the
// one active record after a Stop, not a replacement or a terminal sibling.
type goalAncestorGuardHarness struct {
	lifecycle *session.LifecycleStore
	sessions  *session.UnifiedStore
	tree      *agenttestutil.Tree
	goalIDs   map[string]string
}

func newGoalAncestorGuardHarness(t *testing.T) *goalAncestorGuardHarness {
	t.Helper()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lifecycle)
	wireSteerCompletionDeps(t, al)

	tree := agenttestutil.DelegationTree(t, steer.Deps{
		LifecycleStore: lifecycle,
		SessionStore:   al.GetSessionStore(),
		Canceller:      al.steerCanceller(),
	}, 2)

	goalIDs := make(map[string]string, len(tree.Nodes))
	for _, node := range tree.Nodes {
		goalIDs[node.SessionID] = activateTestGoalRecord(t, node.SessionID, "finish work for "+node.Name)
	}
	h := &goalAncestorGuardHarness{
		lifecycle: lifecycle,
		sessions:  al.GetSessionStore(),
		tree:      tree,
		goalIDs:   goalIDs,
	}
	for _, node := range tree.Nodes {
		h.requireGoalActive(t, node)
	}
	return h
}

func (h *goalAncestorGuardHarness) requireGoalActive(t *testing.T, node agenttestutil.TreeNode) {
	t.Helper()
	rec, err := resolveGoalRecordStore().Get(h.goalIDs[node.SessionID])
	if err != nil {
		t.Fatalf("load %s goal: %v", node.Name, err)
	}
	if rec.OwnerKind != generated.GoalOwnerKindSession || rec.OwnerID != node.SessionID {
		t.Fatalf("%s goal owner = %s/%s, want session/%s", node.Name, rec.OwnerKind, rec.OwnerID, node.SessionID)
	}
	if rec.State != generated.GoalStateActive {
		t.Fatalf("%s goal state = %q, want active", node.Name, rec.State)
	}
}

// requireNoGoalOutcome fails when node's transcript carries any goal
// outcome entry: a Stop must never emit one. The frozen D2/D6 Goal row keeps
// GoalOutcome.ending=stopped_by_user for an explicit /goal clear or an
// authorized clear_goal ONLY — never for stopping a session. The positive
// control that a deliberate clear does emit exactly one such entry is
// TestGoalOutcome_UserClear_StoppedByUserWithTheRealRounds (goal_outcome_test.go),
// which reads the same store and entry subtype.
func (h *goalAncestorGuardHarness) requireNoGoalOutcome(t *testing.T, node agenttestutil.TreeNode) {
	t.Helper()
	entries, err := h.sessions.ReadTranscript(node.SessionID)
	if err != nil {
		t.Fatalf("read %s transcript: %v", node.Name, err)
	}
	for _, e := range entries {
		if e.SystemSubtype == session.SystemSubtypeGoalOutcome {
			t.Fatalf("%s transcript carries goal outcome entry %q after Stop; a Stop never ends a session goal", node.Name, e.ID)
		}
	}
}

func requireCancelReachedExactly(t *testing.T, report steer.CancelReport, nodes ...agenttestutil.TreeNode) {
	t.Helper()
	want := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		want[node.SessionID] = struct{}{}
	}
	if len(report.Reached) != len(want) {
		t.Fatalf("cancel reached %v, want exactly %d session(s)", report.Reached, len(want))
	}
	for _, id := range report.Reached {
		if _, ok := want[id]; !ok {
			t.Fatalf("cancel reached unexpected session %q; reached=%v", id, report.Reached)
		}
	}
	if len(report.Unreachable) != 0 || len(report.SkippedTerminal) != 0 || len(report.SkippedNewerGeneration) != 0 {
		t.Fatalf("cancel report has unexpected exclusions: %+v", report)
	}
}

// TestGoalAncestorGuard984_StopAllPreservesAllSessionGoals pins the frozen
// D2/D6 Goal row: a Stop or Stop all — of a session itself, of a descendant,
// or of the root ancestor — ends NO session-owned goal. Every goal stays
// active under its original id and session ownership; only an explicit
// /goal clear or an authorized clear_goal ends one (stopped_by_user), and
// natural met/exhaustion adjudication is independent of Stop.
func TestGoalAncestorGuard984_StopAllPreservesAllSessionGoals(t *testing.T) {
	t.Run("grandchild Stop keeps its own and every ancestor goal active", func(t *testing.T) {
		h := newGoalAncestorGuardHarness(t)
		report, err := h.tree.Stop(h.tree.B.SessionID)
		if err != nil {
			t.Fatalf("cancel grandchild: %v", err)
		}
		requireCancelReachedExactly(t, report, h.tree.B)
		h.requireGoalActive(t, h.tree.B)
		h.requireGoalActive(t, h.tree.A)
		h.requireGoalActive(t, h.tree.Root)
		h.requireNoGoalOutcome(t, h.tree.B)
		h.requireNoGoalOutcome(t, h.tree.A)
		h.requireNoGoalOutcome(t, h.tree.Root)
	})

	t.Run("child Stop all keeps subtree and root goals active", func(t *testing.T) {
		h := newGoalAncestorGuardHarness(t)
		report, err := h.tree.Stop(h.tree.A.SessionID)
		if err != nil {
			t.Fatalf("cancel child: %v", err)
		}
		requireCancelReachedExactly(t, report, h.tree.A, h.tree.B)
		h.requireGoalActive(t, h.tree.A)
		h.requireGoalActive(t, h.tree.B)
		h.requireGoalActive(t, h.tree.Root)
		h.requireNoGoalOutcome(t, h.tree.A)
		h.requireNoGoalOutcome(t, h.tree.B)
		h.requireNoGoalOutcome(t, h.tree.Root)
	})

	t.Run("root chat Stop keeps every session goal active", func(t *testing.T) {
		h := newGoalAncestorGuardHarness(t)
		report, err := h.tree.Stop(h.tree.Root.SessionID)
		if err != nil {
			t.Fatalf("Stop root chat: %v", err)
		}
		requireCancelReachedExactly(t, report, h.tree.Root, h.tree.A, h.tree.B)

		root, err := h.lifecycle.Load(h.tree.Root.SessionID)
		if err != nil {
			t.Fatalf("load root lifecycle after Stop: %v", err)
		}
		if root.Terminal() || !root.Stopped() {
			t.Fatalf("root lifecycle after Stop = state %q, stopped=%v; want non-terminal with current Stop marker", root.State, root.Stopped())
		}
		h.requireGoalActive(t, h.tree.Root)
		h.requireGoalActive(t, h.tree.A)
		h.requireGoalActive(t, h.tree.B)
		h.requireNoGoalOutcome(t, h.tree.Root)
		h.requireNoGoalOutcome(t, h.tree.A)
		h.requireNoGoalOutcome(t, h.tree.B)
	})
}
