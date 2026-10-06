package agent

import (
	"testing"

	agenttestutil "github.com/elicify-ai/omnipus/pkg/agent/testutil"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// goalAncestorGuardHarness is a real store-backed root chat -> child ->
// grandchild tree. Every node owns a distinct ACTIVE session goal, so each
// assertion can distinguish the cancelled subtree from either ancestor.
type goalAncestorGuardHarness struct {
	lifecycle *session.LifecycleStore
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
	h := &goalAncestorGuardHarness{lifecycle: lifecycle, tree: tree, goalIDs: goalIDs}
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

// TestGoalAncestorGuard984_CancelNeverEndsAncestorGoal preserves the ancestor
// guard and also pins the superseding own/subtree-goal rule. Frozen
// ADR-20260928 D7 (F0929-6): "Stop all does **not** end any goal — not the
// cascaded sessions' own, and (unchanged from the draft) never an ancestor's
// either." D6: "Every session-owned active goal stays active across stop,
// timeout, question expiry, restart, `done` and `failed`." These exact ACTIVE
// assertions replace only the withdrawn terminal-goal oracle; reached-set and
// goal ownership checks remain unchanged.
func TestGoalAncestorGuard984_CancelNeverEndsAncestorGoal(t *testing.T) {
	t.Run("grandchild cancel preserves parent and root goals", func(t *testing.T) {
		h := newGoalAncestorGuardHarness(t)
		report, err := h.tree.Stop(h.tree.B.SessionID)
		if err != nil {
			t.Fatalf("cancel grandchild: %v", err)
		}
		requireCancelReachedExactly(t, report, h.tree.B)
		h.requireGoalActive(t, h.tree.B)
		h.requireGoalActive(t, h.tree.A)
		h.requireGoalActive(t, h.tree.Root)
	})

	t.Run("child cancel preserves subtree and root goals", func(t *testing.T) {
		h := newGoalAncestorGuardHarness(t)
		report, err := h.tree.Stop(h.tree.A.SessionID)
		if err != nil {
			t.Fatalf("cancel child: %v", err)
		}
		requireCancelReachedExactly(t, report, h.tree.A, h.tree.B)
		h.requireGoalActive(t, h.tree.A)
		h.requireGoalActive(t, h.tree.B)
		h.requireGoalActive(t, h.tree.Root)
	})

	t.Run("root chat stop all preserves every goal", func(t *testing.T) {
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
	})
}
