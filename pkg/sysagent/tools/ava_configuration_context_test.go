// Omnipus — System Agent Tool Tests: Ava configuration-write contexts (ADR-090)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/entity"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

// ADR-090 (adr-090-agent-configuration-and-skills-spec.md, "Delegation and
// confirmation") removed Ava-specific hardcoded write restrictions based on
// delegation depth, unattended status, or missing user-session identity: once
// the user's approval is conveyed, Ava applies the proposal in whatever run
// she is in, subject to ordinary permissions, revision checks and
// protected-field rules. Confirmation stays conversational (optionally
// relayed through Jim via message_parent) — there is deliberately NO approval
// token or session-type mutation gate at the tool layer, so none is asserted
// here. These tests pin the write side of that contract for the agent and
// workspace CRUD tools: every execution context performs the real persisted
// write, and the ordinary guards (protected fields, revision staleness,
// destructive confirm) still refuse with zero writes.

// avaWriteModes mirrors the session-context vocabulary of
// skill_ava_guard_adr090_test.go so the skill half and the agent/workspace
// half of the ADR-090 guard removal stay covered by the same matrix.
var avaWriteModes = []string{"attended", "delegated", "unattended", "missing-session"}

// avaWriteCtx builds the acting context for an Ava configuration write.
// "attended" is the owner-session control; the other three are the contexts
// the removed ValidateConfigurationWriteContext gate used to refuse.
func avaWriteCtx(mode string) context.Context {
	ctx := tools.WithAgentID(context.Background(), "ava")
	if mode != "missing-session" {
		ctx = tools.WithTranscriptSessionID(ctx, "owner-session")
	}
	if mode == "delegated" {
		ctx = tools.WithDelegationDepth(ctx, 1)
	}
	if mode == "unattended" {
		ctx = tools.WithAutoDenyAsk(ctx, true)
	}
	return ctx
}

// avaSeedCustomAgent creates a custom teammate through the REAL create_agent
// path (Ava's US-2 workflow) and returns its id. Update/delete scenarios use
// this as their persisted target so the whole lifecycle runs through
// production code against real storage.
func avaSeedCustomAgent(t *testing.T, ctx context.Context, deps *systools.Deps) string {
	t.Helper()
	result := systools.NewAgentCreateTool(deps).Execute(ctx, map[string]any{
		"name":        "Field Analyst",
		"description": "Summarizes field reports",
		"soul":        "You summarize field reports for the team.",
		"model":       "test/model",
		"color":       "#22C55E",
		"icon":        "robot",
	})
	require.False(t, result.IsError, result.ForLLM)
	id, _ := parseSuccess(t, result.ForLLM)["id"].(string)
	require.Equal(t, "field-analyst", id)
	return id
}

// TestADR090_AvaAgentWritesDoNotRequireOwnerSession proves create_agent,
// update_agent and delete_agent persist real agent-store writes from Ava in
// every execution context — attended (control), delegated, unattended, and
// missing-session.
func TestADR090_AvaAgentWritesDoNotRequireOwnerSession(t *testing.T) {
	for _, operation := range []string{"create", "update", "delete"} {
		for _, mode := range avaWriteModes {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				deps, _ := newTestDepsWithHome(t)
				ctx := avaWriteCtx(mode)
				switch operation {
				case "create":
					avaAgentCreatePersists(t, ctx, deps)
				case "update":
					avaAgentUpdatePersists(t, ctx, deps)
				case "delete":
					avaAgentDeletePersists(t, ctx, deps)
				}
			})
		}
	}
}

// avaAgentCreatePersists proves a create_agent call from Ava persists the
// entity record and SOUL.md regardless of execution context. Expected values
// derive from the tool's published contract: the slug id, the response's
// status field (metadata_only with no workspace turn context), and the soul
// argument written verbatim to SOUL.md.
func avaAgentCreatePersists(t *testing.T, ctx context.Context, deps *systools.Deps) {
	t.Helper()
	const soul = "You summarize field reports for the team."
	result := systools.NewAgentCreateTool(deps).Execute(ctx, map[string]any{
		"name":        "Field Analyst",
		"description": "Summarizes field reports",
		"soul":        soul,
		"model":       "test/model",
		"color":       "#22C55E",
		"icon":        "robot",
	})
	require.False(t, result.IsError, result.ForLLM)
	body := parseSuccess(t, result.ForLLM)
	require.Equal(t, "field-analyst", body["id"])
	require.Equal(t, "metadata_only", body["status"],
		"no workspace turn context is set, so the agent must be metadata-only")

	store := agentstore.New(deps.Home)
	persisted, err := store.Get("field-analyst")
	require.NoError(t, err, "create must persist the entity record")
	require.Equal(t, "Field Analyst", persisted.Name)
	require.Equal(t, "Summarizes field reports", persisted.Description)
	require.Equal(t, "#22C55E", persisted.Color)
	require.Equal(t, "robot", persisted.Icon)
	require.NotNil(t, persisted.Model)
	require.Equal(t, "test/model", persisted.Model.Primary)

	state, err := store.ReadState("field-analyst")
	require.NoError(t, err)
	require.NoError(t, agentstore.ValidateRevision(state.Revision),
		"persisted revision must be the 64-lowercase-hex store revision")

	soulOnDisk, err := os.ReadFile(filepath.Join(deps.Home, "agents", "field-analyst", "SOUL.md"))
	require.NoError(t, err, "create must write SOUL.md")
	require.Equal(t, soul, string(soulOnDisk))
}

// avaAgentUpdatePersists proves an update_agent call from Ava persists the
// supplied fields, preserves omitted ones, and advances the revision — the
// contract the tool Description states ("Only provided fields are changed;
// omitted fields are left as-is") plus the store's revision semantics.
func avaAgentUpdatePersists(t *testing.T, ctx context.Context, deps *systools.Deps) {
	t.Helper()
	id := avaSeedCustomAgent(t, ctx, deps)
	before := currentAgentRevision(t, deps, id)
	result := systools.NewAgentUpdateTool(deps).Execute(ctx, map[string]any{
		"id":          id,
		"revision":    before,
		"description": "Summarizes and routes field reports",
		"model":       "openrouter/z-ai/glm-5.3-flash",
	})
	require.False(t, result.IsError, result.ForLLM)
	body := parseSuccess(t, result.ForLLM)
	require.Equal(t, []any{"description", "model"}, body["changed_fields"])

	persisted, err := agentstore.New(deps.Home).Get(id)
	require.NoError(t, err)
	require.Equal(t, "Summarizes and routes field reports", persisted.Description)
	require.NotNil(t, persisted.Model)
	require.Equal(t, "openrouter/z-ai/glm-5.3-flash", persisted.Model.Primary)
	require.Equal(t, "Field Analyst", persisted.Name, "omitted field must be preserved")
	require.Equal(t, "#22C55E", persisted.Color, "omitted field must be preserved")

	after := currentAgentRevision(t, deps, id)
	require.NotEqual(t, before, after, "a persisted update must advance the revision")
	require.Equal(t, after, body["revision"], "the response revision must match the persisted revision")
	require.NoError(t, agentstore.ValidateRevision(after))
}

// avaAgentDeletePersists proves a delete_agent call from Ava removes both the
// entity record and the SOUL.md the create path wrote (DeleteState's
// documented scope: "removes only the entity and applicable SOUL bytes owned
// by this resource").
func avaAgentDeletePersists(t *testing.T, ctx context.Context, deps *systools.Deps) {
	t.Helper()
	id := avaSeedCustomAgent(t, ctx, deps)
	result := systools.NewAgentDeleteTool(deps).Execute(ctx, map[string]any{
		"id":       id,
		"confirm":  true,
		"revision": currentAgentRevision(t, deps, id),
	})
	require.False(t, result.IsError, result.ForLLM)
	require.Equal(t, true, parseSuccess(t, result.ForLLM)["deleted"])

	_, err := agentstore.New(deps.Home).Get(id)
	require.ErrorIs(t, err, entity.ErrNotFound, "the entity record must be gone")
	require.NoFileExists(t, filepath.Join(deps.Home, "entities", "agents", id+".json"))
	require.NoFileExists(t, filepath.Join(deps.Home, "agents", id, "SOUL.md"),
		"the SOUL.md owned by the deleted agent must be removed with the record")
}

// TestADR090_AvaWorkspaceWritesDoNotRequireOwnerSession proves
// create_workspace, update_workspace and delete_workspace persist real
// workspace-record writes from Ava in every execution context.
func TestADR090_AvaWorkspaceWritesDoNotRequireOwnerSession(t *testing.T) {
	for _, operation := range []string{"create", "update", "delete"} {
		for _, mode := range avaWriteModes {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				deps, home := newTestDepsWithHome(t)
				ctx := avaWriteCtx(mode)
				switch operation {
				case "create":
					avaWorkspaceCreatePersists(t, ctx, deps)
				case "update":
					avaWorkspaceUpdatePersists(t, ctx, deps, home)
				case "delete":
					avaWorkspaceDeletePersists(t, ctx, deps, home)
				}
			})
		}
	}
}

// avaWorkspaceCreatePersists proves a create_workspace call from Ava persists
// the workspace record with the supplied name/description and status active
// (the tool's published create contract) and a store-valid revision.
func avaWorkspaceCreatePersists(t *testing.T, ctx context.Context, deps *systools.Deps) {
	t.Helper()
	result := systools.NewWorkspaceCreateTool(deps).Execute(ctx, map[string]any{
		"name":        "Launch Room",
		"description": "Coordination for the launch",
	})
	require.False(t, result.IsError, result.ForLLM)
	body := parseSuccess(t, result.ForLLM)
	id, _ := body["id"].(string)
	require.NotEmpty(t, id)
	require.Equal(t, "Launch Room", body["name"])

	state, err := workspacepkg.ReadState(deps.Home, id)
	require.NoError(t, err, "create must persist the workspace record")
	require.Equal(t, "Launch Room", state.Workspace.Name)
	require.Equal(t, "Coordination for the launch", state.Workspace.Description)
	require.Equal(t, "active", state.Workspace.Status)
	require.NoError(t, workspacepkg.ValidateRevision(state.Revision),
		"persisted revision must be the 64-lowercase-hex store revision")
	require.FileExists(t, filepath.Join(deps.Home, "workspaces", id+".json"))
}

// avaWorkspaceUpdatePersists proves an update_workspace call from Ava
// persists the supplied fields (name, pinned, core_team), preserves the
// omitted description, and advances the revision.
func avaWorkspaceUpdatePersists(t *testing.T, ctx context.Context, deps *systools.Deps, home string) {
	t.Helper()
	avaSeedCustomAgent(t, ctx, deps)
	created := systools.NewWorkspaceCreateTool(deps).Execute(ctx, map[string]any{
		"name":        "Launch Room",
		"description": "Coordination for the launch",
	})
	require.False(t, created.IsError, created.ForLLM)
	id := workspaceID(t, created.ForLLM)
	before := currentWorkspaceRevision(t, home, id)

	result := systools.NewWorkspaceUpdateTool(deps).Execute(ctx, map[string]any{
		"id":        id,
		"revision":  before,
		"name":      "Launch Room West",
		"pinned":    true,
		"core_team": []any{"field-analyst"},
	})
	require.False(t, result.IsError, result.ForLLM)

	state, err := workspacepkg.ReadState(home, id)
	require.NoError(t, err)
	require.Equal(t, "Launch Room West", state.Workspace.Name)
	require.Equal(t, true, state.Workspace.Pinned)
	require.Equal(t, []string{"field-analyst"}, state.Workspace.CoreTeam)
	require.Equal(t, "Coordination for the launch", state.Workspace.Description,
		"omitted field must be preserved")

	after := currentWorkspaceRevision(t, home, id)
	require.NotEqual(t, before, after, "a persisted update must advance the revision")
	require.NoError(t, workspacepkg.ValidateRevision(after))
}

// avaWorkspaceDeletePersists proves a delete_workspace call from Ava removes
// the authoritative workspace record from disk.
func avaWorkspaceDeletePersists(t *testing.T, ctx context.Context, deps *systools.Deps, home string) {
	t.Helper()
	created := systools.NewWorkspaceCreateTool(deps).Execute(ctx, map[string]any{
		"name": "Launch Room",
	})
	require.False(t, created.IsError, created.ForLLM)
	id := workspaceID(t, created.ForLLM)

	result := systools.NewWorkspaceDeleteTool(deps).Execute(ctx, map[string]any{
		"id":       id,
		"revision": currentWorkspaceRevision(t, home, id),
		"confirm":  true,
	})
	require.False(t, result.IsError, result.ForLLM)
	body := parseSuccess(t, result.ForLLM)
	require.Equal(t, true, body["deleted"])
	require.Equal(t, float64(0), body["tasks_deleted"])
	require.NoFileExists(t, filepath.Join(home, "workspaces", id+".json"),
		"the authoritative workspace record must be gone")
}

// TestADR090_AvaConfigurationWriteGuardsStillHold pins the ORDINARY guards
// the spec keeps after the session-type gate removal: protected fields on
// ordinary built-ins, revision staleness, and the destructive confirm flag.
// Run in the delegated context — the context where the removed gate used to
// refuse everything — to prove these refusals are the field/revision/confirm
// rules themselves, not a resurrected context gate. The editable-field
// subtest on the SAME record proves the protection is field-scoped, never a
// blanket deny that would amount to a new approval gate.
func TestADR090_AvaConfigurationWriteGuardsStillHold(t *testing.T) {
	ctx := avaWriteCtx("delegated")
	t.Run("protected fields on an ordinary builtin refuse with zero writes", func(t *testing.T) {
		avaGuardProtectedFieldsRefuseWithZeroWrites(t, ctx)
	})
	t.Run("editable field on the same builtin still succeeds", func(t *testing.T) {
		avaGuardEditableFieldOnBuiltinStillSucceeds(t, ctx)
	})
	t.Run("stale agent revision refuses with zero writes", func(t *testing.T) {
		avaGuardStaleAgentRevisionRefuses(t, ctx)
	})
	t.Run("stale workspace revision refuses with zero writes", func(t *testing.T) {
		avaGuardStaleWorkspaceRevisionRefuses(t, ctx)
	})
	t.Run("delete_agent without confirm refuses and the agent survives", func(t *testing.T) {
		avaGuardDeleteWithoutConfirmRefuses(t, ctx)
	})
}

// avaSeedAvaRecord seeds Ava's own ordinary-builtin entity record (with a
// soul) so a guards subtest can target the exact roster identity the
// field-protection matrix keys off.
func avaSeedAvaRecord(t *testing.T, deps *systools.Deps, soul string) {
	t.Helper()
	_, err := agentstore.New(deps.Home).CreateState("ava", &config.AgentConfig{
		ID:    "ava",
		Name:  "Ava",
		Model: &config.AgentModelConfig{Primary: "test/model"},
	}, soul)
	require.NoError(t, err)
}

// avaGuardProtectedFieldsRefuseWithZeroWrites proves name and soul stay
// protected on Ava's own record and that the refused request writes nothing
// — the spec's "A forbidden-field request must produce zero record or
// instruction-file writes."
func avaGuardProtectedFieldsRefuseWithZeroWrites(t *testing.T, ctx context.Context) {
	t.Helper()
	deps, home := newTestDepsWithHome(t)
	const seededSoul = "Ava's seeded soul."
	avaSeedAvaRecord(t, deps, seededSoul)
	entityPath := filepath.Join(home, "entities", "agents", "ava.json")
	soulPath := filepath.Join(home, "agents", "ava", "SOUL.md")
	entityBefore, err := os.ReadFile(entityPath)
	require.NoError(t, err)
	revisionBefore := currentAgentRevision(t, deps, "ava")

	result := systools.NewAgentUpdateTool(deps).Execute(ctx, map[string]any{
		"id":       "ava",
		"revision": revisionBefore,
		"name":     "Eve",
		"soul":     "Overridden instructions.",
	})
	require.True(t, result.IsError, "a protected-field request must be refused")
	errBlock, _ := parseError(t, result.ForLLM)["error"].(map[string]any)
	require.Equal(t, "PROTECTED_FIELD", errBlock["code"])
	require.Contains(t, errBlock["message"], "name")
	require.Contains(t, errBlock["message"], "soul")

	entityAfter, err := os.ReadFile(entityPath)
	require.NoError(t, err)
	require.Equal(t, string(entityBefore), string(entityAfter),
		"a refused field write must leave the entity record byte-identical")
	soulAfter, err := os.ReadFile(soulPath)
	require.NoError(t, err)
	require.Equal(t, seededSoul, string(soulAfter),
		"a refused field write must leave SOUL.md byte-identical")
	require.Equal(t, revisionBefore, currentAgentRevision(t, deps, "ava"),
		"a refused field write must not advance the revision")
}

// avaGuardEditableFieldOnBuiltinStillSucceeds proves the field protection is
// field-scoped: on the SAME record that just refused name+soul, a model
// update succeeds. A regression that turned the ordinary-builtin protection
// into a blanket deny (in effect, a new approval gate) fails here.
func avaGuardEditableFieldOnBuiltinStillSucceeds(t *testing.T, ctx context.Context) {
	t.Helper()
	deps, _ := newTestDepsWithHome(t)
	avaSeedAvaRecord(t, deps, "Ava's seeded soul.")

	result := systools.NewAgentUpdateTool(deps).Execute(ctx, map[string]any{
		"id":       "ava",
		"revision": currentAgentRevision(t, deps, "ava"),
		"model":    "openrouter/z-ai/glm-5.3-flash",
	})
	require.False(t, result.IsError, "model is an editable field for an ordinary builtin: %s", result.ForLLM)
	persisted, err := agentstore.New(deps.Home).Get("ava")
	require.NoError(t, err)
	require.NotNil(t, persisted.Model)
	require.Equal(t, "openrouter/z-ai/glm-5.3-flash", persisted.Model.Primary)
	require.Equal(t, "Ava", persisted.Name, "the protected name must be untouched by a model-only update")
}

// avaGuardStaleAgentRevisionRefuses proves the ordinary revision check
// survives the guard removal: a well-formed but stale revision is refused
// with zero writes.
func avaGuardStaleAgentRevisionRefuses(t *testing.T, ctx context.Context) {
	t.Helper()
	deps, _ := newTestDepsWithHome(t)
	id := avaSeedCustomAgent(t, ctx, deps)
	entityPath := filepath.Join(deps.Home, "entities", "agents", id+".json")
	before, err := os.ReadFile(entityPath)
	require.NoError(t, err)

	result := systools.NewAgentUpdateTool(deps).Execute(ctx, map[string]any{
		"id":          id,
		"revision":    strings.Repeat("0", 64),
		"description": "must not persist",
	})
	require.True(t, result.IsError, "a stale revision must be refused")
	// Characterization pin: update_agent's wired conflict code (the spec
	// mandates the refusal and zero writes; the exact code string is current
	// wiring, not spec prose).
	errBlock, _ := parseError(t, result.ForLLM)["error"].(map[string]any)
	require.Equal(t, "CONFLICT", errBlock["code"])
	after, err := os.ReadFile(entityPath)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "a refused update must leave the record byte-identical")
}

// avaGuardStaleWorkspaceRevisionRefuses proves the workspace revision check
// survives the guard removal: a stale revision refuses before any record or
// delegation-store write.
func avaGuardStaleWorkspaceRevisionRefuses(t *testing.T, ctx context.Context) {
	t.Helper()
	deps, home := newTestDepsWithHome(t)
	avaSeedCustomAgent(t, ctx, deps)
	created := systools.NewWorkspaceCreateTool(deps).Execute(ctx, map[string]any{"name": "Launch Room"})
	require.False(t, created.IsError, created.ForLLM)
	id := workspaceID(t, created.ForLLM)
	wsPath := filepath.Join(home, "workspaces", id+".json")
	before, err := os.ReadFile(wsPath)
	require.NoError(t, err)
	edgesBefore, storeExisted := workspacepkg.LoadDelegation(home, id)

	result := systools.NewWorkspaceUpdateTool(deps).Execute(ctx, map[string]any{
		"id":        id,
		"revision":  strings.Repeat("0", 64),
		"name":      "Must not persist",
		"core_team": []any{"field-analyst"},
	})
	require.True(t, result.IsError, "a stale revision must be refused")
	// Characterization pin: update_workspace's wired conflict code.
	errBlock, _ := parseError(t, result.ForLLM)["error"].(map[string]any)
	require.Equal(t, "REVISION_CONFLICT", errBlock["code"])
	after, err := os.ReadFile(wsPath)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "a refused update must leave the record byte-identical")
	edges, ok := workspacepkg.LoadDelegation(home, id)
	require.Equal(t, storeExisted, ok, "a refused update must preserve delegation-store existence")
	require.Equal(t, edgesBefore, edges, "a refused update must not touch the delegation store")
}

// avaGuardDeleteWithoutConfirmRefuses proves the destructive confirm flag is
// still required for delete_agent and that an unconfirmed delete leaves the
// agent intact.
func avaGuardDeleteWithoutConfirmRefuses(t *testing.T, ctx context.Context) {
	t.Helper()
	deps, _ := newTestDepsWithHome(t)
	id := avaSeedCustomAgent(t, ctx, deps)

	result := systools.NewAgentDeleteTool(deps).Execute(ctx, map[string]any{
		"id":       id,
		"confirm":  false,
		"revision": currentAgentRevision(t, deps, id),
	})
	require.True(t, result.IsError, "an unconfirmed delete must be refused")
	errBlock, _ := parseError(t, result.ForLLM)["error"].(map[string]any)
	require.Equal(t, "CONFIRMATION_REQUIRED", errBlock["code"])
	_, err := agentstore.New(deps.Home).Get(id)
	require.NoError(t, err, "the agent must survive an unconfirmed delete")
}
