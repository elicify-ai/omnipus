package testutil

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

type fixtureT interface {
	Helper()
	Fatalf(format string, args ...any)
	Cleanup(func())
}

// TreeNode identifies one real session in a DelegationTree.
type TreeNode struct {
	Name        string
	SessionID   string
	AgentID     string
	WorkspaceID string
	Generation  int
}

// Tree is a real store-backed root and its steered descendants.
type Tree struct {
	Root  TreeNode
	A     TreeNode
	B     TreeNode
	C     TreeNode
	Nodes []TreeNode

	mu           sync.Mutex
	t            fixtureT
	deps         steer.Deps
	sessionDir   string
	lifecycleDir string
	crashed      bool
}

// DelegationTree builds root -> A -> B (-> C) with one distinct agent
// profile per session. A non-nil launcher owns child creation; a nil launcher
// uses the same persisted record shape directly for CP-0 callers.
func DelegationTree(t fixtureT, deps steer.Deps, depth int) *Tree {
	t.Helper()
	if deps.SessionStore == nil || deps.LifecycleStore == nil {
		t.Fatalf("DelegationTree: SessionStore and LifecycleStore are required")
	}
	if depth < 1 || depth > 3 {
		t.Fatalf("DelegationTree: depth = %d, want 1..3", depth)
	}

	tree := &Tree{
		t:            t,
		deps:         deps,
		sessionDir:   deps.SessionStore.BaseDir(),
		lifecycleDir: deps.LifecycleStore.Dir(),
	}
	tree.Root = tree.createRoot()
	tree.Nodes = append(tree.Nodes, tree.Root)
	parent := tree.Root
	for level := 1; level <= depth; level++ {
		node := tree.createChild(parent, level, depth)
		tree.Nodes = append(tree.Nodes, node)
		parent = node
	}
	tree.assignNamedNodes()
	t.Cleanup(func() {
		tree.mu.Lock()
		defer tree.mu.Unlock()
		if !tree.crashed && tree.deps.SessionStore != nil {
			_ = tree.deps.SessionStore.Close()
			tree.crashed = true
		}
	})
	return tree
}

func (tree *Tree) createRoot() TreeNode {
	const (
		agentID     = "adr091-fixture-agent-root"
		workspaceID = "adr091-fixture-workspace"
	)
	meta, err := tree.deps.SessionStore.NewSession(session.SessionTypeChat, "webchat", agentID)
	if err != nil {
		tree.t.Fatalf("DelegationTree: create root session: %v", err)
	}
	title, owner := "ADR-091 fixture root", "adr091-fixture-owner"
	if err := tree.deps.SessionStore.SetMeta(meta.ID, session.MetaPatch{
		Title: &title, Owner: &owner, WorkspaceID: ptr(workspaceID),
	}); err != nil {
		tree.t.Fatalf("DelegationTree: stamp root metadata: %v", err)
	}
	root := TreeNode{Name: "R", SessionID: meta.ID, AgentID: agentID, WorkspaceID: workspaceID, Generation: 1}
	if tree.deps.Launcher == nil {
		tree.persistRoot(root)
	}
	return root
}

func (tree *Tree) persistRoot(root TreeNode) {
	rec := &session.LifecycleRecord{
		SessionID: root.SessionID, Generation: root.Generation, State: session.LifecycleRunning,
		Origin:         &session.Origin{Kind: session.OriginKindChat},
		OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID:    root.WorkspaceID, AgentID: root.AgentID,
	}
	if err := tree.deps.LifecycleStore.Persist(rec); err != nil {
		tree.t.Fatalf("DelegationTree: persist ordinary_root: %v", err)
	}
}

func (tree *Tree) createChild(parent TreeNode, level, depth int) TreeNode {
	name := string(rune('A' + level - 1))
	agentID := "adr091-fixture-agent-" + strings.ToLower(name)
	label := "ADR-091 fixture " + name
	origin := steer.Origin{Kind: steer.OriginKindDelegate, CallID: "adr091-call-" + strings.ToLower(name)}
	if tree.deps.Launcher != nil {
		result, err := tree.deps.Launcher.Launch(context.Background(), steer.LaunchRequest{
			SteeringSessionID: parent.SessionID,
			TargetAgentID:     agentID,
			Label:             label,
			Task:              "Build fixture node " + name,
			Origin:            origin,
			ToolExclusions:    []string{"switch_agent"},
		})
		if err != nil {
			tree.t.Fatalf("DelegationTree: launch %s: %v", name, err)
		}
		if _, err := tree.deps.Launcher.Dispatch(context.Background(), result.SessionID, result.Generation); err != nil {
			tree.t.Fatalf("DelegationTree: dispatch %s: %v", name, err)
		}
		return TreeNode{
			Name: name, SessionID: result.SessionID, AgentID: agentID,
			WorkspaceID: tree.Root.WorkspaceID, Generation: result.Generation,
		}
	}
	return tree.createChildDirect(parent, name, agentID, label, origin, depth-level)
}

func (tree *Tree) createChildDirect(parent TreeNode, name, agentID, label string, origin steer.Origin, remainingDepth int) TreeNode {
	meta, err := tree.deps.SessionStore.NewSession(session.SessionTypeDelegate, "webchat", agentID)
	if err != nil {
		tree.t.Fatalf("DelegationTree: create %s session: %v", name, err)
	}
	owner := "adr091-fixture-owner"
	if err := tree.deps.SessionStore.SetMeta(meta.ID, session.MetaPatch{
		Title: &label, Owner: &owner, WorkspaceID: &tree.Root.WorkspaceID,
		ParentSessionID: &parent.SessionID,
	}); err != nil {
		tree.t.Fatalf("DelegationTree: stamp %s metadata: %v", name, err)
	}
	node := TreeNode{
		Name: name, SessionID: meta.ID, AgentID: agentID,
		WorkspaceID: tree.Root.WorkspaceID, Generation: 1,
	}
	rec := &session.LifecycleRecord{
		SessionID: node.SessionID, Generation: 1, State: session.LifecycleRunning,
		Origin: &origin,
		SteeredBy: &session.SteeredBy{
			SteeringSessionID: parent.SessionID,
			RootSessionID:     tree.Root.SessionID,
			ReportingTarget: session.ReportingTarget{
				SessionID: parent.SessionID, Channel: "webchat", ChatID: parent.SessionID,
			},
			Authorization: session.Authorization{
				Mode: session.AuthorizationModeDirect, RemainingDepth: remainingDepth,
			},
			ToolExclusions: []string{"switch_agent"},
		},
		OwnerScopeKind: session.OwnerScopeParentSession,
		OwnerScopeID:   parent.SessionID, WorkspaceID: node.WorkspaceID,
		AgentID: node.AgentID, ParentAgentID: parent.AgentID,
	}
	if err := tree.deps.LifecycleStore.Persist(rec); err != nil {
		tree.t.Fatalf("DelegationTree: persist %s lifecycle: %v", name, err)
	}
	return node
}

func (tree *Tree) assignNamedNodes() {
	for _, node := range tree.Nodes {
		switch node.Name {
		case "A":
			tree.A = node
		case "B":
			tree.B = node
		case "C":
			tree.C = node
		}
	}
}

// Deps returns the fixture's current dependencies, including reopened stores.
func (tree *Tree) Deps() steer.Deps {
	tree.mu.Lock()
	defer tree.mu.Unlock()
	return tree.deps
}

// Reenter asks upward delivery to hand a child's completion to sessionID.
func (tree *Tree) Reenter(sessionID string) (steer.Delivery, error) {
	child, ok := tree.directChild(sessionID)
	if !ok {
		return steer.Delivery{}, fmt.Errorf("DelegationTree: session %q has no child", sessionID)
	}
	return tree.deliverHandback(child, sessionID, child.Generation)
}

// QueueWake exercises the same delivery operation with the supplied recipient generation.
func (tree *Tree) QueueWake(sessionID string, generation int) (steer.Delivery, error) {
	child, ok := tree.directChild(sessionID)
	if !ok {
		return steer.Delivery{}, fmt.Errorf("DelegationTree: session %q has no child", sessionID)
	}
	return tree.deliverHandback(child, sessionID, generation)
}

func (tree *Tree) deliverHandback(child TreeNode, parentID string, generation int) (steer.Delivery, error) {
	if tree.deps.Deliverer == nil {
		return steer.Delivery{}, errors.New("DelegationTree: Deliverer is not configured")
	}
	parent := parentID
	gen := generation
	var message generated.SessionMessage
	if err := message.FromSessionMessageHandback(generated.SessionMessageHandback{
		MessageId: child.SessionID + ":" + fmt.Sprint(generation) + ":final",
		SessionId: child.SessionID, ParentSessionId: &parent,
		CreatedAt: time.Now().UTC(), Generation: &gen, Depth: 1,
		Direction:   generated.SessionMessageHandbackDirection("child_to_parent"),
		Mode:        generated.SessionMessageHandbackMode("final"),
		ResultSoFar: "fixture handback", Artifacts: []string{}, OpenQuestions: []string{},
		SenderIdentity: child.AgentID, UntrustedOrigin: true,
	}); err != nil {
		return steer.Delivery{}, fmt.Errorf("DelegationTree: encode handback: %w", err)
	}
	return tree.deps.Deliverer.Deliver(context.Background(), steer.UpwardEvent{
		ChildSessionID: child.SessionID, Outcome: steer.OutcomeFinalAnswer, Message: message,
	})
}

// Stop maps directly to Canceller.CancelSubtree.
func (tree *Tree) Stop(sessionID string) (steer.CancelReport, error) {
	if tree.deps.Canceller == nil {
		return steer.CancelReport{}, errors.New("DelegationTree: Canceller is not configured")
	}
	return tree.deps.Canceller.CancelSubtree(context.Background(), sessionID, steer.Principal{
		Kind: steer.PrincipalKindHuman, ID: "adr091-fixture-operator",
	})
}

// Revive maps directly to Canceller.Revive.
func (tree *Tree) Revive(sessionID string) (int, error) {
	if tree.deps.Canceller == nil {
		return 0, errors.New("DelegationTree: Canceller is not configured")
	}
	return tree.deps.Canceller.Revive(context.Background(), sessionID, steer.Principal{
		Kind: steer.PrincipalKindHuman, ID: "adr091-fixture-operator",
	})
}

// Crash closes the real session store without running BootHook.
func (tree *Tree) Crash() error {
	tree.mu.Lock()
	defer tree.mu.Unlock()
	if tree.crashed {
		return nil
	}
	tree.crashed = true
	return tree.deps.SessionStore.Close()
}

// Reboot reopens both stores and runs the injected production boot hook.
func (tree *Tree) Reboot(ctx context.Context) error {
	tree.mu.Lock()
	if !tree.crashed {
		tree.mu.Unlock()
		return errors.New("DelegationTree: Reboot requires Crash first")
	}
	sessions, err := session.NewUnifiedStore(tree.sessionDir)
	if err != nil {
		tree.mu.Unlock()
		return fmt.Errorf("DelegationTree: reopen session store: %w", err)
	}
	tree.deps.SessionStore = sessions
	tree.deps.LifecycleStore = session.NewLifecycleStore(tree.lifecycleDir)
	tree.crashed = false
	bootHook := tree.deps.BootHook
	tree.mu.Unlock()
	if bootHook != nil {
		if err := bootHook(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (tree *Tree) directChild(sessionID string) (TreeNode, bool) {
	for i := range tree.Nodes[:len(tree.Nodes)-1] {
		if tree.Nodes[i].SessionID == sessionID {
			return tree.Nodes[i+1], true
		}
	}
	return TreeNode{}, false
}

func ptr[T any](value T) *T { return &value }
