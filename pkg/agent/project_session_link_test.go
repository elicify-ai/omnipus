// Omnipus — Agent Loop integration tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Session auto-link integration tests (Tests 34–36).
//
// These exercise the workspace-session linker AFTER-TOOL hook end-to-end through
// runAgentLoop, driven by a scripted ScenarioProvider (no LLM call is made).
// They verify the hook WIRING — that when a real create_task_in_workspace /
// update_task_in_workspace tool call carries a non-empty workspace_id, a link entry is
// appended to ~/.omnipus/project_session_links.jsonl, and that no link is
// written when workspace_id is absent.
//
// Traces to: project-task-management-level1-spec.md (FR-1.9 rename: project→workspace)
//   - Test 34 (line 926): TestAgentLoop_TaskCreate_WithWorkspaceID_LinksSession
//   - Test 35 (line 927): TestAgentLoop_TaskUpdate_WithWorkspaceID_LinksSession
//   - Test 36 (line 928): TestAgentLoop_TaskCreate_NoWorkspaceID_NoLink
//   - BDD "Feature: Session Auto-Link" (spec lines 622–660)
//   - User Story 4, Acceptance Scenarios 1, 2, 3 (spec lines 182–184)

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/agent/testutil"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// linkTestEnv is the wired-up agent loop plus the home dir under which the
// linker writes project_session_links.jsonl and the task tools read/write
// workspaces/ and tasks/.
type linkTestEnv struct {
	al   *AgentLoop
	home string // == filepath.Dir(workspace); linker + task tools share this root
}

// newLinkTestEnv builds an AgentLoop whose default agent has the real
// system.task.create / system.task.update tools registered (allow policy), so a
// scripted tool call drives a genuine tool execution and the auto-link
// AfterTool hook fires on a real result. The linker is auto-mounted by
// NewAgentLoop and keyed to filepath.Dir(workspace) == home.
func newLinkTestEnv(t *testing.T, provider *testutil.ScenarioProvider) *linkTestEnv {
	t.Helper()

	home := t.TempDir()
	workspaceDir := filepath.Join(home, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspaceDir,
				ModelName:         "scripted-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(func() { al.Close() })

	// Register the real GTD task tools with a Deps rooted at the same home the
	// linker uses, so workspace validation in task.create/update reads the workspace
	// files we write, and the resulting tool name (create_task_in_workspace /
	// update_task_in_workspace) is exactly what the linker's AfterTool hook keys on.
	deps := &systools.Deps{Home: home}
	al.RegisterTool(systools.NewTaskCreateTool(deps))
	al.RegisterTool(systools.NewTaskUpdateTool(deps))

	// Allow the ScopeCore task tools on every agent. The task tools declare
	// RequiresAdminAsk()==true, so the admin-ask fence (FR-061) escalates them to
	// "ask" UNLESS the policy is marked IsCoreAgent. The default "main" agent is a
	// core agent, so IsCoreAgent:true is the correct, faithful policy here — it
	// matches what the gateway stores for core agents and lets the tool execute so
	// the AfterTool linker hook actually fires.
	for _, agentID := range al.GetRegistry().ListAgentIDs() {
		ag, ok := al.GetRegistry().GetAgent(agentID)
		if !ok {
			continue
		}
		ag.StoreToolPolicy(&tools.ToolPolicyCfg{DefaultPolicy: "allow", IsCoreAgent: true})
	}

	return &linkTestEnv{al: al, home: home}
}

// writeWorkspaceFile writes a minimal valid workspace JSON to <home>/workspaces/<id>.json
// so that task.create / task.update workspace_id validation (readWorkspaceFromDisk)
// succeeds. Returns the workspace id.
func writeWorkspaceFile(t *testing.T, home, name string) string {
	t.Helper()
	id := ulid.Make().String()
	dir := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	// Field names mirror the systools.workspace struct JSON tags.
	ws := map[string]any{
		"id":         id,
		"name":       name,
		"status":     "active",
		"created_at": "2026-06-08T00:00:00Z",
		"updated_at": "2026-06-08T00:00:00Z",
	}
	data, err := json.MarshalIndent(ws, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, id+".json"), data, 0o600))
	return id
}

// writeTaskFile writes a minimal valid GTD task JSON to <home>/tasks/<id>.json
// so that task.update (which reads the existing task first) succeeds. Returns
// the task id.
func writeTaskFile(t *testing.T, home, name, workspaceID string) string {
	t.Helper()
	id := ulid.Make().String()
	dir := filepath.Join(home, "tasks")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	task := map[string]any{
		"id":           id,
		"name":         name,
		"status":       "inbox",
		"workspace_id": workspaceID,
		"created_at":   "2026-06-08T00:00:00Z",
		"updated_at":   "2026-06-08T00:00:00Z",
	}
	data, err := json.MarshalIndent(task, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, id+".json"), data, 0o600))
	return id
}

// runOneToolTurn drives runAgentLoop once. The ScenarioProvider must be scripted
// with a tool call followed by a text response so the loop completes the turn.
// The transcript session ID is the value the linker records as session_id.
func runOneToolTurn(t *testing.T, env *linkTestEnv, sessionID string) {
	t.Helper()
	agent := env.al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent, "default agent must exist after boot")

	_, err := env.al.runAgentLoop(context.Background(), agent, processOptions{
		SessionKey:          "session-link-test",
		Channel:             "cli",
		ChatID:              "direct",
		UserMessage:         "do the thing",
		DefaultResponse:     defaultResponse,
		EnableSummary:       false,
		SendResponse:        false,
		TranscriptSessionID: sessionID, // becomes ctx transcript session id -> linker session_id
	})
	require.NoError(t, err, "runAgentLoop must complete without error")
}

// TestAgentLoop_TaskCreate_WithWorkspaceID_LinksSession (Test 34).
//
// BDD: Given session S is active and workspace W exists,
//
//	When an agent executes system.task.create with workspace_id "W" during S,
//	Then a link entry {workspace_id:"W", session_id:"S", created_at:…} is
//	appended to project_session_links.jsonl.
//
// Traces to: project-task-management-level1-spec.md line 926 (Test 34);
//
//	User Story 4, Acceptance Scenario 1 (line 182);
//	BDD "Agent task.create with workspace_id writes link entry" (line 624).
func TestAgentLoop_TaskCreate_WithWorkspaceID_LinksSession(t *testing.T) {
	provider := testutil.NewScenario()
	env := newLinkTestEnv(t, provider)

	workspaceID := writeWorkspaceFile(t, env.home, "website-api")
	const sessionID = "session_TASKCREATE_34"

	// Precondition: no link exists yet for this workspace.
	require.Empty(t, systools.ReadLinks(env.home, workspaceID),
		"precondition: no link should exist before the tool call")

	// Step 1: scripted tool call to create_task_in_workspace with a real workspace_id.
	// Step 2: scripted text so the turn completes after the tool result.
	provider.
		WithToolCall("create_task_in_workspace", `{"name":"fix login","workspace_id":"`+workspaceID+`"}`).
		WithText("Created the task.")

	runOneToolTurn(t, env, sessionID)

	// Assert: exactly one link entry for (workspaceID, sessionID) was written.
	links := systools.ReadLinks(env.home, workspaceID)
	require.Len(t, links, 1,
		"CRITICAL: create_task_in_workspace with workspace_id must append exactly one link entry "+
			"(linker AfterTool hook did not fire or did not write)")
	assert.Equal(t, workspaceID, links[0].WorkspaceID,
		"link entry workspace_id must match the workspace_id passed to create_task_in_workspace")
	assert.Equal(t, sessionID, links[0].SessionID,
		"link entry session_id must be the transcript session id of the turn")
	assert.NotEmpty(t, links[0].CreatedAt,
		"link entry must carry a non-empty created_at timestamp")

	// Differentiation: a DIFFERENT workspace id must have no link — proves the
	// session_id/workspace_id are real and not hardcoded.
	otherWorkspace := writeWorkspaceFile(t, env.home, "unrelated")
	assert.Empty(t, systools.ReadLinks(env.home, otherWorkspace),
		"a workspace that was never named in a tool call must have zero links")
}

// TestAgentLoop_TaskUpdate_WithWorkspaceID_LinksSession (Test 35).
//
// BDD: Given session S already linked to workspace A,
//
//	When the agent executes system.task.update on a task in workspace B,
//	Then a new link entry for (B, S) is appended — (A, S) is NOT removed
//	(many-to-many accumulate).
//
// Traces to: project-task-management-level1-spec.md line 927 (Test 35);
//
//	User Story 4, Acceptance Scenario 2 (line 183).
func TestAgentLoop_TaskUpdate_WithWorkspaceID_LinksSession(t *testing.T) {
	provider := testutil.NewScenario()
	env := newLinkTestEnv(t, provider)

	workspaceA := writeWorkspaceFile(t, env.home, "workspace-a")
	workspaceB := writeWorkspaceFile(t, env.home, "workspace-b")
	// A task that already lives in workspace B; update will re-assert workspace_id B.
	taskID := writeTaskFile(t, env.home, "wire the form", workspaceB)
	const sessionID = "session_TASKUPDATE_35"

	// Turn 1: task.create in workspace A — establishes the (A, S) link.
	// Turn 2: task.update in workspace B — must ADD (B, S) without removing (A, S).
	provider.
		// turn 1 (create in A)
		WithToolCall("create_task_in_workspace", `{"name":"seed","workspace_id":"`+workspaceA+`"}`).
		WithText("Linked to workspace A.").
		// turn 2 (update in B)
		WithToolCall("update_task_in_workspace", `{"id":"`+taskID+`","status":"active","workspace_id":"`+workspaceB+`"}`).
		WithText("Updated and linked to workspace B.")

	runOneToolTurn(t, env, sessionID) // create in A
	runOneToolTurn(t, env, sessionID) // update in B

	// Assert (A, S) still present (accumulate, not replace).
	linksA := systools.ReadLinks(env.home, workspaceA)
	require.Len(t, linksA, 1, "the original (A, S) link must survive a later update on workspace B")
	assert.Equal(t, sessionID, linksA[0].SessionID, "(A, S) link session must be unchanged")

	// Assert (B, S) was added by the update.
	linksB := systools.ReadLinks(env.home, workspaceB)
	require.Len(t, linksB, 1,
		"CRITICAL: update_task_in_workspace with workspace_id must append a (B, S) link entry")
	assert.Equal(t, workspaceB, linksB[0].WorkspaceID, "new link workspace_id must be workspace B")
	assert.Equal(t, sessionID, linksB[0].SessionID, "new link session must be the same session S")
}

// TestAgentLoop_TaskCreate_NoWorkspaceID_NoLink (Test 36).
//
// BDD: Given session S is active,
//
//	When the agent executes system.task.create WITHOUT a workspace_id,
//	Then no new line is appended to project_session_links.jsonl.
//
// Traces to: project-task-management-level1-spec.md line 928 (Test 36);
//
//	User Story 4, Acceptance Scenario 3 (line 184);
//	BDD "task.create without workspace_id writes no link entry" (line 644).
func TestAgentLoop_TaskCreate_NoWorkspaceID_NoLink(t *testing.T) {
	provider := testutil.NewScenario()
	env := newLinkTestEnv(t, provider)

	const sessionID = "session_NOLINK_36"
	linkFile := filepath.Join(env.home, "project_session_links.jsonl")

	// Precondition: link file does not exist yet.
	_, statErr := os.Stat(linkFile)
	require.True(t, os.IsNotExist(statErr),
		"precondition: link file must not exist before the tool call")

	// Step 1: scripted create_task_in_workspace with NO workspace_id.
	// Step 2: scripted text so the turn completes.
	provider.
		WithToolCall("create_task_in_workspace", `{"name":"write docs"}`).
		WithText("Created unassigned task.")

	runOneToolTurn(t, env, sessionID)

	// Assert: no link file was created (nothing to write means no append).
	_, statErr = os.Stat(linkFile)
	assert.True(t, os.IsNotExist(statErr),
		"CRITICAL: a create_task_in_workspace without workspace_id must NOT create the link file")

	// Differentiation: prove the negative is meaningful — a real workspace that was
	// never referenced has zero links even though the turn ran a real tool.
	someWorkspace := writeWorkspaceFile(t, env.home, "never-referenced")
	assert.Empty(t, systools.ReadLinks(env.home, someWorkspace),
		"no workspace should have any links after a no-workspace_id task.create")
}
