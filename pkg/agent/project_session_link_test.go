// Omnipus — Agent Loop integration tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Session auto-link integration tests (Tests 34–36).
//
// These exercise the project-session linker AFTER-TOOL hook end-to-end through
// runAgentLoop, driven by a scripted ScenarioProvider (no LLM call is made).
// They verify the hook WIRING — that when a real system.task.create /
// system.task.update tool call carries a non-empty project_id, a link entry is
// appended to ~/.omnipus/project_session_links.jsonl, and that no link is
// written when project_id is absent.
//
// Traces to: project-task-management-level1-spec.md
//   - Test 34 (line 926): TestAgentLoop_TaskCreate_WithProjectID_LinksSession
//   - Test 35 (line 927): TestAgentLoop_TaskUpdate_WithProjectID_LinksSession
//   - Test 36 (line 928): TestAgentLoop_TaskCreate_NoProjectID_NoLink
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
// projects/ and tasks/.
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
	// linker uses, so project validation in task.create/update reads the project
	// files we write, and the resulting tool name (system.task.create /
	// system.task.update) is exactly what the linker's AfterTool hook keys on.
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

// writeProjectFile writes a minimal valid project JSON to <home>/projects/<id>.json
// so that task.create / task.update project_id validation (readProjectFromDisk)
// succeeds. Returns the project id.
func writeProjectFile(t *testing.T, home, name string) string {
	t.Helper()
	id := ulid.Make().String()
	dir := filepath.Join(home, "projects")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	// Field names mirror the systools.project struct JSON tags.
	proj := map[string]any{
		"id":         id,
		"name":       name,
		"status":     "active",
		"created_at": "2026-06-08T00:00:00Z",
		"updated_at": "2026-06-08T00:00:00Z",
	}
	data, err := json.MarshalIndent(proj, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, id+".json"), data, 0o600))
	return id
}

// writeTaskFile writes a minimal valid GTD task JSON to <home>/tasks/<id>.json
// so that task.update (which reads the existing task first) succeeds. Returns
// the task id.
func writeTaskFile(t *testing.T, home, name, projectID string) string {
	t.Helper()
	id := ulid.Make().String()
	dir := filepath.Join(home, "tasks")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	task := map[string]any{
		"id":         id,
		"name":       name,
		"status":     "inbox",
		"project_id": projectID,
		"created_at": "2026-06-08T00:00:00Z",
		"updated_at": "2026-06-08T00:00:00Z",
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

// TestAgentLoop_TaskCreate_WithProjectID_LinksSession (Test 34).
//
// BDD: Given session S is active and project P exists,
//
//	When an agent executes system.task.create with project_id "P" during S,
//	Then a link entry {project_id:"P", session_id:"S", created_at:…} is
//	appended to project_session_links.jsonl.
//
// Traces to: project-task-management-level1-spec.md line 926 (Test 34);
//
//	User Story 4, Acceptance Scenario 1 (line 182);
//	BDD "Agent task.create with project_id writes link entry" (line 624).
func TestAgentLoop_TaskCreate_WithProjectID_LinksSession(t *testing.T) {
	provider := testutil.NewScenario()
	env := newLinkTestEnv(t, provider)

	projectID := writeProjectFile(t, env.home, "website-api")
	const sessionID = "session_TASKCREATE_34"

	// Precondition: no link exists yet for this project.
	require.Empty(t, systools.ReadLinks(env.home, projectID),
		"precondition: no link should exist before the tool call")

	// Step 1: scripted tool call to system.task.create with a real project_id.
	// Step 2: scripted text so the turn completes after the tool result.
	provider.
		WithToolCall("system.task.create", `{"name":"fix login","project_id":"`+projectID+`"}`).
		WithText("Created the task.")

	runOneToolTurn(t, env, sessionID)

	// Assert: exactly one link entry for (projectID, sessionID) was written.
	links := systools.ReadLinks(env.home, projectID)
	require.Len(t, links, 1,
		"CRITICAL: system.task.create with project_id must append exactly one link entry "+
			"(linker AfterTool hook did not fire or did not write)")
	assert.Equal(t, projectID, links[0].ProjectID,
		"link entry project_id must match the project_id passed to system.task.create")
	assert.Equal(t, sessionID, links[0].SessionID,
		"link entry session_id must be the transcript session id of the turn")
	assert.NotEmpty(t, links[0].CreatedAt,
		"link entry must carry a non-empty created_at timestamp")

	// Differentiation: a DIFFERENT project id must have no link — proves the
	// session_id/project_id are real and not hardcoded.
	otherProject := writeProjectFile(t, env.home, "unrelated")
	assert.Empty(t, systools.ReadLinks(env.home, otherProject),
		"a project that was never named in a tool call must have zero links")
}

// TestAgentLoop_TaskUpdate_WithProjectID_LinksSession (Test 35).
//
// BDD: Given session S already linked to project A,
//
//	When the agent executes system.task.update on a task in project B,
//	Then a new link entry for (B, S) is appended — (A, S) is NOT removed
//	(many-to-many accumulate).
//
// Traces to: project-task-management-level1-spec.md line 927 (Test 35);
//
//	User Story 4, Acceptance Scenario 2 (line 183).
func TestAgentLoop_TaskUpdate_WithProjectID_LinksSession(t *testing.T) {
	provider := testutil.NewScenario()
	env := newLinkTestEnv(t, provider)

	projectA := writeProjectFile(t, env.home, "project-a")
	projectB := writeProjectFile(t, env.home, "project-b")
	// A task that already lives in project B; update will re-assert project_id B.
	taskID := writeTaskFile(t, env.home, "wire the form", projectB)
	const sessionID = "session_TASKUPDATE_35"

	// Turn 1: task.create in project A — establishes the (A, S) link.
	// Turn 2: task.update in project B — must ADD (B, S) without removing (A, S).
	provider.
		// turn 1 (create in A)
		WithToolCall("system.task.create", `{"name":"seed","project_id":"`+projectA+`"}`).
		WithText("Linked to project A.").
		// turn 2 (update in B)
		WithToolCall("system.task.update", `{"id":"`+taskID+`","status":"active","project_id":"`+projectB+`"}`).
		WithText("Updated and linked to project B.")

	runOneToolTurn(t, env, sessionID) // create in A
	runOneToolTurn(t, env, sessionID) // update in B

	// Assert (A, S) still present (accumulate, not replace).
	linksA := systools.ReadLinks(env.home, projectA)
	require.Len(t, linksA, 1, "the original (A, S) link must survive a later update on project B")
	assert.Equal(t, sessionID, linksA[0].SessionID, "(A, S) link session must be unchanged")

	// Assert (B, S) was added by the update.
	linksB := systools.ReadLinks(env.home, projectB)
	require.Len(t, linksB, 1,
		"CRITICAL: system.task.update with project_id must append a (B, S) link entry")
	assert.Equal(t, projectB, linksB[0].ProjectID, "new link project_id must be project B")
	assert.Equal(t, sessionID, linksB[0].SessionID, "new link session must be the same session S")
}

// TestAgentLoop_TaskCreate_NoProjectID_NoLink (Test 36).
//
// BDD: Given session S is active,
//
//	When the agent executes system.task.create WITHOUT a project_id,
//	Then no new line is appended to project_session_links.jsonl.
//
// Traces to: project-task-management-level1-spec.md line 928 (Test 36);
//
//	User Story 4, Acceptance Scenario 3 (line 184);
//	BDD "task.create without project_id writes no link entry" (line 644).
func TestAgentLoop_TaskCreate_NoProjectID_NoLink(t *testing.T) {
	provider := testutil.NewScenario()
	env := newLinkTestEnv(t, provider)

	const sessionID = "session_NOLINK_36"
	linkFile := filepath.Join(env.home, "project_session_links.jsonl")

	// Precondition: link file does not exist yet.
	_, statErr := os.Stat(linkFile)
	require.True(t, os.IsNotExist(statErr),
		"precondition: link file must not exist before the tool call")

	// Step 1: scripted task.create with NO project_id.
	// Step 2: scripted text so the turn completes.
	provider.
		WithToolCall("system.task.create", `{"name":"write docs"}`).
		WithText("Created unassigned task.")

	runOneToolTurn(t, env, sessionID)

	// Assert: no link file was created (nothing to write means no append).
	_, statErr = os.Stat(linkFile)
	assert.True(t, os.IsNotExist(statErr),
		"CRITICAL: a task.create without project_id must NOT create the link file")

	// Differentiation: prove the negative is meaningful — a real project that was
	// never referenced has zero links even though the turn ran a real tool.
	someProject := writeProjectFile(t, env.home, "never-referenced")
	assert.Empty(t, systools.ReadLinks(env.home, someProject),
		"no project should have any links after a no-project_id task.create")
}
