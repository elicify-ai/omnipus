// Omnipus — ADR-067 FR-052/FR-053: the workspace a knowledge call belongs to.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
package knowledge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// ---------------------------------------------------------------------------
// The turn that carries no workspace id
// ---------------------------------------------------------------------------
//
// Two ordinary kinds of turn never set tools.WithWorkspaceID:
//
//   - a CLI / ProcessDirect turn (`omnipus <agent> "..."`) — ProcessDirect
//     builds a bus.InboundMessage with no workspace at all;
//   - a scheduled task or heartbeat turn.
//
// That is deliberate on the agent side (pkg/agent/workspace_reroot.go re-roots
// the WORK DIRECTORY into the agent's workspace and explicitly does not touch
// workspace_id or memory-room routing). The consequence for knowledge was a
// silent empty answer: ResolveScope(home, "") is an empty scope, and the agent
// was told "No knowledge base is available in this workspace" while the
// operator's mount sat right there, granted, on the workspace the same turn had
// just been re-rooted into.
//
// pkg/tools/resolvepath.go already carries a twenty-line comment and a fix for
// the identical failure on the filesystem-mount side. These tests pin the
// knowledge equivalent.

// stFixture builds a home with one workspace whose core team contains agentID,
// and one knowledge base mounted into it holding one findable note.
func stFixture(t *testing.T, agentID, phrase string) (home, wsID, root string) {
	t.Helper()
	home = a4Home(t)
	wsID = "stws-" + agentID
	now := time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{
		ID: wsID, Name: wsID, Status: "active",
		CoreTeam:  []string{agentID},
		CreatedAt: now, UpdatedAt: now,
	}))
	root = a4Vault(t, filepath.Join(t.TempDir(), "KB"), "KB")
	a4Mount(t, home, wsID, "kb", root)

	require.NoError(t, os.MkdirAll(filepath.Join(root, "notes"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "notes", "found.md"),
		[]byte("# Found\n\n"+phrase+"\n"), 0o600))
	return home, wsID, root
}

// TestKnowledgeSearch_FindsTheAgentsOwnWorkspaceOnACLITurn is the repair.
//
// The turn carries an AGENT but no workspace id — exactly what ProcessDirect
// and a heartbeat build. The search must still address the knowledge base
// mounted into that agent's workspace.
//
// DIES ON: reverting either Execute to
// `ResolveScope(t.deps.Home, tools.ToolWorkspaceID(ctx))`, and on
// TurnWorkspaceID dropping its FindForAgentPreferring fallback.
func TestKnowledgeSearch_FindsTheAgentsOwnWorkspaceOnACLITurn(t *testing.T) {
	const agentID = "ava"
	const phrase = "stegosaurus"
	home, _, _ := stFixture(t, agentID, phrase)

	// The context an agent-loop turn installs MINUS the workspace id.
	ctx := tools.WithAgentID(context.Background(), agentID)
	require.Empty(t, tools.ToolWorkspaceID(ctx),
		"the fixture only means anything if the turn genuinely carries no workspace id")

	tool := stTool(t, home, "knowledge_search")
	res := tool.Execute(ctx, map[string]any{"query": phrase})
	require.False(t, res.IsError, res.ForLLM)

	var out struct {
		ResultCount        int      `json:"result_count"`
		Notes              []string `json:"notes"`
		CollectionsInScope []string `json:"collections_in_scope"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &out), "payload: %s", res.ForLLM)

	assert.NotEmptyf(t, out.CollectionsInScope,
		"a CLI or scheduled turn resolved an EMPTY knowledge scope. The agent is told %q "+
			"while the operator's mount is granted on the workspace this very turn is "+
			"rooted in — the same silent-empty defect pkg/tools/resolvepath.go already "+
			"repairs for filesystem mounts (FR-052/FR-053)", out.Notes)
	assert.Contains(t, out.CollectionsInScope, "KB")
}

// TestKnowledgeTurnScope_NeverWidensBeyondTheAgentsOwnWorkspace is the
// security half, and it is the reason the fallback is a lookup rather than a
// default.
//
// Three properties, each of which a careless "just pick a workspace" fallback
// would break:
//
//  1. A turn that NAMES a workspace resolves exactly that workspace. The
//     fallback is consulted only on the empty path, so there is no route by
//     which workspace A's turn reaches workspace B.
//  2. An agent that belongs to no workspace still resolves nothing.
//  3. No agent id at all still resolves nothing — an anonymous turn must not
//     inherit someone else's grants.
func TestKnowledgeTurnScope_NeverWidensBeyondTheAgentsOwnWorkspace(t *testing.T) {
	const agentID = "ava"
	home, wsID, _ := stFixture(t, agentID, "stegosaurus")

	t.Run("an explicit workspace id is used verbatim", func(t *testing.T) {
		ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), agentID), wsID)
		assert.Equal(t, wsID, TurnWorkspaceID(ctx, home))
	})

	t.Run("a DIFFERENT explicit workspace id is NOT replaced by the agent's own", func(t *testing.T) {
		// The whole point: a turn that names another workspace must resolve
		// THAT one (and then find nothing in it), never be silently redirected
		// to the one the agent happens to belong to.
		other := "stws-someone-else"
		ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), agentID), other)
		assert.Equal(t, other, TurnWorkspaceID(ctx, home),
			"an explicitly named workspace must never be swapped for the agent's own")
		scope, _ := ResolveTurnScope(ctx, home)
		assert.Empty(t, scope.Collections(),
			"a workspace the agent is not on grants nothing (FR-052)")
	})

	t.Run("an agent on no workspace resolves nothing", func(t *testing.T) {
		ctx := tools.WithAgentID(context.Background(), "an-agent-on-no-workspace")
		assert.Empty(t, TurnWorkspaceID(ctx, home))
		scope, _ := ResolveTurnScope(ctx, home)
		assert.Empty(t, scope.Collections())
	})

	t.Run("no agent id resolves nothing", func(t *testing.T) {
		assert.Empty(t, TurnWorkspaceID(context.Background(), home),
			"an anonymous turn must not inherit any agent's grants")
	})

	t.Run("an empty home resolves nothing", func(t *testing.T) {
		ctx := tools.WithAgentID(context.Background(), agentID)
		assert.Empty(t, TurnWorkspaceID(ctx, ""))
	})
}

// stTool finds one retrieval tool by name for a given home.
func stTool(t *testing.T, home, name string) tools.Tool {
	t.Helper()
	for _, tool := range RetrievalTools(ToolDeps{Home: home}) {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("tool %q is not registered by RetrievalTools", name)
	return nil
}
