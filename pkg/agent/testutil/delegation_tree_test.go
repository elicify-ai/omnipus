package testutil

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestDelegationTree_RebootHookCanReadReopenedDeps(t *testing.T) {
	home := t.TempDir()
	sessions, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "lifecycle"))
	var tree *Tree
	bootCalled := false
	tree = DelegationTree(t, steer.Deps{
		LifecycleStore: lifecycle,
		SessionStore:   sessions,
		BootHook: func(context.Context) error {
			bootCalled = true
			deps := tree.Deps()
			if deps.SessionStore == nil || deps.LifecycleStore == nil {
				t.Fatal("boot hook received incomplete reopened dependencies")
			}
			return nil
		},
	}, 1)

	if err := tree.Crash(); err != nil {
		t.Fatalf("Crash: %v", err)
	}
	if err := tree.Reboot(context.Background()); err != nil {
		t.Fatalf("Reboot: %v", err)
	}
	if !bootCalled {
		t.Fatal("Reboot did not invoke BootHook")
	}
}

func TestDelegationTree_BuildsValidEdges_AndRootRecord(t *testing.T) {
	home := t.TempDir()
	sessions, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "lifecycle"))
	tree := DelegationTree(t, steer.Deps{
		LifecycleStore: lifecycle,
		SessionStore:   sessions,
	}, 3)

	if got := len(tree.Nodes); got != 4 {
		t.Fatalf("len(tree.Nodes) = %d, want root plus three descendants", got)
	}
	wantAgents := map[string]bool{}
	for _, node := range tree.Nodes {
		if node.SessionID == "" || node.AgentID == "" {
			t.Fatalf("node %+v has empty identity", node)
		}
		if wantAgents[node.AgentID] {
			t.Fatalf("agent profile %q reused; every fixture node must be distinct", node.AgentID)
		}
		wantAgents[node.AgentID] = true
	}

	root, err := lifecycle.Load(tree.Root.SessionID)
	if err != nil {
		t.Fatalf("load root lifecycle: %v", err)
	}
	if root.SteeredBy != nil || root.Origin == nil || root.Origin.Kind != steer.OriginKindChat {
		t.Fatalf("root record = %+v, want ordinary_root origin=chat", root)
	}

	for i, node := range tree.Nodes[1:] {
		rec, loadErr := lifecycle.Load(node.SessionID)
		if loadErr != nil {
			t.Fatalf("load node %s lifecycle: %v", node.Name, loadErr)
		}
		parent := tree.Nodes[i]
		if rec.SteeredBy == nil {
			t.Fatalf("node %s has no steered-by edge", node.Name)
		}
		if rec.SteeredBy.SteeringSessionID != parent.SessionID {
			t.Fatalf("node %s parent = %q, want %q", node.Name, rec.SteeredBy.SteeringSessionID, parent.SessionID)
		}
		if rec.SteeredBy.RootSessionID != tree.Root.SessionID {
			t.Fatalf("node %s root = %q, want %q", node.Name, rec.SteeredBy.RootSessionID, tree.Root.SessionID)
		}
		meta, metaErr := sessions.GetMeta(node.SessionID)
		if metaErr != nil {
			t.Fatalf("load node %s meta: %v", node.Name, metaErr)
		}
		if meta.ParentSessionID != parent.SessionID || meta.Title == "" {
			t.Fatalf("node %s meta = %+v, want direct parent and non-empty title", node.Name, meta)
		}
	}

	if err := tree.Crash(); err != nil {
		t.Fatalf("crash sessions: %v", err)
	}
	reopened, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("reopen sessions: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	for _, node := range tree.Nodes {
		if _, err := reopened.GetMeta(node.SessionID); err != nil {
			t.Fatalf("session %s missing after reopen: %v", node.Name, err)
		}
	}

	if _, err := lifecycle.Load("missing"); !errors.Is(err, session.ErrLifecycleNotFound) {
		t.Fatalf("fixture lifecycle control: missing load error = %v", err)
	}
}
