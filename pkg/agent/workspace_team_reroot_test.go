// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// workspace_team_reroot_test.go — drives runTurn end-to-end (via ProcessDirect)
// to prove the CoreTeam-membership-driven filesystem re-rooting in runTurn
// (pkg/agent/loop.go): an agent that belongs to a Workspace's core_team writes
// into that Workspace's own shared directory instead of its private per-agent
// directory, regardless of whether the turn carries a channel-bound
// workspace_id (ts.opts.WorkspaceID is empty here — ProcessDirect is not
// channel-bound — which is exactly the divergence case the re-rooting design
// is meant to cover: CoreTeam membership, not the turn-carried workspace_id,
// drives the re-root).
//
// Uses the ScenarioProvider harness (already bridged into runTurn by
// scenario_runturn_test.go) to script a single write_file tool call, avoiding
// any real LLM call — the effective root is asserted purely from where the
// file actually landed on disk.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestRunTurn_CoreTeamMember_WritesToWorkspaceSharedDir proves the operator
// requirement: an agent that belongs to a Workspace's core_team runs its
// turn rooted at that Workspace's shared directory, not its own agents/<id>/
// directory — even for a top-level (non-delegated) turn with no channel
// binding at all.
func TestRunTurn_CoreTeamMember_WritesToWorkspaceSharedDir(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpHome)

	agentWorkspaceDir := filepath.Join(tmpHome, "agents", "main")
	require.NoError(t, os.MkdirAll(agentWorkspaceDir, 0o755))

	// Persist a real workspace record whose core_team includes "main" — an
	// ordinary, explicitly-registered agent (below, cfg.Agents.List) with no
	// special meaning anymore. There is no implicit "main" sentinel to rely
	// on; this test names its own real agent "main" purely so it lines up
	// with the pre-existing agentWorkspaceDir/workspace fixture paths above.
	workspacesDir := filepath.Join(tmpHome, "workspaces")
	require.NoError(t, os.MkdirAll(workspacesDir, 0o755))
	wsJSON := `{"id":"team-ws","core_team":["main"]}`
	require.NoError(t, os.WriteFile(filepath.Join(workspacesDir, "team-ws.json"), []byte(wsJSON), 0o644))

	provider := testutil.NewScenario().
		WithToolCall("write_file", `{"path":"proof.txt","content":"hello-from-team-workspace","overwrite":true}`).
		WithText("done")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              agentWorkspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
				// Sandboxed (os.Root-relative) file resolution — required for a
				// relative write_file path to resolve against the workspace root
				// (fixed or re-rooted) instead of the process CWD.
				RestrictToWorkspace: true,
			},
			List: []config.AgentConfig{{ID: "main"}},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	// No-default-policy model (CLAUDE.md hard constraint 6): write_file needs
	// an explicit agent-level grant, or it fails closed to "deny" and the
	// re-rooting behavior under test never gets exercised.
	defaultAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"write_file": "allow"},
	})

	const sessionKey = "test-session-coreteam-reroot"
	ctx := context.Background()
	finalContent, err := al.ProcessDirect(ctx, "please write proof.txt for me", sessionKey)
	require.NoError(t, err, "ProcessDirect must succeed")
	assert.Equal(t, "done", finalContent)

	sharedFile := filepath.Join(workspacesDir, "team-ws", "work", "proof.txt")
	got, readErr := os.ReadFile(sharedFile)
	require.NoError(
		t,
		readErr,
		"write_file must have landed in the workspace's dedicated work/ directory (%s) — CoreTeam membership must re-root the turn's filesystem there",
		sharedFile,
	)
	assert.Equal(t, "hello-from-team-workspace", string(got))

	// The agent's own private directory must NOT have received the file.
	privateFile := filepath.Join(agentWorkspaceDir, "proof.txt")
	_, statErr := os.Stat(privateFile)
	assert.True(t, os.IsNotExist(statErr),
		"write_file must NOT land in the agent's own private directory (%s) when it is a CoreTeam member", privateFile)

	// The workspace root itself (one level up from work/) must NOT have
	// received the file either — proving the re-root target is the
	// dedicated work/ subdirectory, not the workspace's own directory (which
	// also holds AGENT.md and the shared memory room).
	workspaceRootFile := filepath.Join(workspacesDir, "team-ws", "proof.txt")
	_, rootStatErr := os.Stat(workspaceRootFile)
	assert.True(
		t,
		os.IsNotExist(rootStatErr),
		"write_file must NOT land directly in the workspace's own directory (%s) — only in its work/ subdirectory",
		workspaceRootFile,
	)
}

// TestRunTurn_WorkspacelessAgentRefused (ADR-046 P1, FR-007/008, US-2 AS-2 /
// US-3 AS-3) replaces the old TestRunTurn_NotCoreTeamMember_WritesToOwnDir:
// execution is now ALWAYS workspace-scoped, so an agent that belongs to no
// workspace's CoreTeam must have its turn REFUSED — no silent fallthrough to
// its own agent-home directory (the pre-ADR-046 behavior the old test
// pinned). This deliberately constructs the AgentLoop via NewAgentLoop
// directly, NOT mustNewAgentLoop — mustNewAgentLoop's harness seeding would
// otherwise make "main" a member of a workspace and defeat the very case
// under test.
func TestRunTurn_WorkspacelessAgentRefused(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpHome)

	agentWorkspaceDir := filepath.Join(tmpHome, "agents", "main")
	require.NoError(t, os.MkdirAll(agentWorkspaceDir, 0o755))

	// No workspaces/ directory at all — "main" is a member of nothing.
	provider := testutil.NewScenario().
		WithToolCall("write_file", `{"path":"proof.txt","content":"should-never-land","overwrite":true}`).
		WithText("done")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:                agentWorkspaceDir,
				DefaultModel:        config.DefaultModel{Model: "scripted-model"},
				MaxTokens:           4096,
				MaxToolIterations:   10,
				RestrictToWorkspace: true,
			},
			List: []config.AgentConfig{{ID: "main"}},
		},
	}

	msgBus := bus.NewMessageBus()
	al, err := NewAgentLoop(cfg, msgBus, provider)
	require.NoError(t, err, "NewAgentLoop must succeed")
	defer al.Close()
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	defaultAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"write_file": "allow"},
	})

	const sessionKey = "test-session-workspaceless-refused"
	ctx := context.Background()
	_, procErr := al.ProcessDirect(ctx, "please write proof.txt for me", sessionKey)
	require.Error(t, procErr, "a turn for an agent that is a member of no workspace must be refused")
	assert.True(t, errors.Is(procErr, ErrAgentNotWorkspaceMember),
		"refusal error must wrap ErrAgentNotWorkspaceMember, got: %v", procErr)

	// No file anywhere: neither the agent's own private directory...
	privateFile := filepath.Join(agentWorkspaceDir, "proof.txt")
	_, statErr := os.Stat(privateFile)
	assert.True(t, os.IsNotExist(statErr),
		"write_file must NOT land in the agent's own directory when the turn is refused (%s)", privateFile)

	// ...nor any workspace (there are none — the turn must never have reached
	// the tool-call stage at all).
	workspacesDir := filepath.Join(tmpHome, "workspaces")
	_, wsStatErr := os.Stat(workspacesDir)
	assert.True(t, os.IsNotExist(wsStatErr),
		"a refused turn must not create any workspace directory")
}

// TestRunTurn_WorkspacelessAgentRefused_ViaProcessMessage (ADR-046 P1,
// FR-007/008) is a SECOND, harness-independent proof of the same P0 property
// TestRunTurn_WorkspacelessAgentRefused pins, reached through a DIFFERENT
// entry point: al.processMessage (the channel/board-task-origin path — see
// TestRunTurn_MemberGetsWorkspaceWorkDir's sibling coverage of the MEMBER
// case via this same entry point), not al.ProcessDirect. This matters
// because the shared test harness (mustNewAgentLoop ->
// ensureTestWorkspaceMembership, test_helpers_test.go) auto-seeds workspace
// membership for essentially every agent id in this package's ~100 other
// tests — masking the FR-008 refusal gate everywhere except the one test
// that deliberately opts out of it. This test opts out too (NewAgentLoop
// directly, no workspaces/ directory at all) but drives the turn through a
// second, independent code path so the P0 "member of no workspace = refused"
// property isn't protected by exactly one entry point/test in the whole
// suite.
func TestRunTurn_WorkspacelessAgentRefused_ViaProcessMessage(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpHome)

	agentWorkspaceDir := filepath.Join(tmpHome, "agents", "main")
	require.NoError(t, os.MkdirAll(agentWorkspaceDir, 0o755))

	// No workspaces/ directory at all — "main" is a member of nothing.
	provider := testutil.NewScenario().
		WithToolCall("write_file", `{"path":"proof.txt","content":"should-never-land","overwrite":true}`).
		WithText("done")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:                agentWorkspaceDir,
				DefaultModel:        config.DefaultModel{Model: "scripted-model"},
				MaxTokens:           4096,
				MaxToolIterations:   10,
				RestrictToWorkspace: true,
			},
			List: []config.AgentConfig{{ID: "main"}},
		},
	}

	msgBus := bus.NewMessageBus()
	al, err := NewAgentLoop(cfg, msgBus, provider)
	require.NoError(t, err, "NewAgentLoop must succeed")
	defer al.Close()
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	defaultAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"write_file": "allow"},
	})

	ctx := context.Background()
	// Same entry point TestRunTurn_MemberGetsWorkspaceWorkDir uses for the
	// MEMBER case, but with no workspace at all — and, unlike that test, no
	// metadata workspace_id either, so the refusal isn't merely "explicit id
	// not found" but the identity-only FindForAgentPreferring path (mirrors
	// a plain channel message with no workspace binding).
	_, _, procErr := al.processMessage(ctx, bus.InboundMessage{
		Channel:    "cli",
		ChatID:     "direct",
		Content:    "please write proof.txt for me",
		SessionKey: "test-session-workspaceless-refused-via-process-message",
	})
	require.Error(t, procErr, "a turn for an agent that is a member of no workspace must be refused")
	assert.True(t, errors.Is(procErr, ErrAgentNotWorkspaceMember),
		"refusal error must wrap ErrAgentNotWorkspaceMember, got: %v", procErr)

	privateFile := filepath.Join(agentWorkspaceDir, "proof.txt")
	_, statErr := os.Stat(privateFile)
	assert.True(t, os.IsNotExist(statErr),
		"write_file must NOT land in the agent's own directory when the turn is refused (%s)", privateFile)

	workspacesDir := filepath.Join(tmpHome, "workspaces")
	_, wsStatErr := os.Stat(workspacesDir)
	assert.True(t, os.IsNotExist(wsStatErr),
		"a refused turn must not create any workspace directory")
}

// TestRunTurn_MemberGetsWorkspaceWorkDir (ADR-046 P1, FR-007, US-2 AS-1)
// proves the turn resolves its working dir from the EXPLICIT turn workspace
// (meta.WorkspaceID -> opts.WorkspaceID -> FindForAgentPreferring), not just
// identity-based CoreTeam derivation with no turn-carried workspace id (the
// sibling TestRunTurn_CoreTeamMember_WritesToWorkspaceSharedDir test above
// covers that case via ProcessDirect, which never threads a workspace id).
// Here the turn is driven through processMessage directly with
// msg.Metadata["workspace_id"] set — the same fallback path a board-task run
// uses (loop.go's processMessage, "Falls back to the inbound metadata key
// when present") — so ts.opts.WorkspaceID is non-empty for a member agent.
// TestRunTurn_WorkspacelessAgentRefused_EmitsTypedError pins the UAT defect
// the classifier-only fix left open: TranslateTurnError already mapped
// ErrAgentNotWorkspaceMember to agent_not_configured, but runTurn returned
// BEFORE registerActiveTurn, so no EventKindError was ever emitted and the
// user saw a turn that never started. The refusal must now be a real failed
// turn — typed error on the bus, catalogue copy, not silence.
func TestRunTurn_WorkspacelessAgentRefused_EmitsTypedError(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpHome)

	agentWorkspaceDir := filepath.Join(tmpHome, "agents", "main")
	require.NoError(t, os.MkdirAll(agentWorkspaceDir, 0o755))

	provider := testutil.NewScenario().WithText("should-never-be-called")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:                agentWorkspaceDir,
				DefaultModel:        config.DefaultModel{Model: "scripted-model"},
				MaxTokens:           4096,
				MaxToolIterations:   10,
				RestrictToWorkspace: true,
			},
			List: []config.AgentConfig{{ID: "main"}},
		},
	}

	msgBus := bus.NewMessageBus()
	al, err := NewAgentLoop(cfg, msgBus, provider)
	require.NoError(t, err, "NewAgentLoop must succeed")
	defer al.Close()

	sub := al.SubscribeEvents(32)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })

	_, procErr := al.ProcessDirect(context.Background(), "hello", "test-session-workspaceless-visible")
	require.Error(t, procErr, "a turn for an agent that is a member of no workspace must be refused")
	assert.True(t, errors.Is(procErr, ErrAgentNotWorkspaceMember),
		"refusal error must wrap ErrAgentNotWorkspaceMember, got: %v", procErr)

	events := drainEvents(sub.C)
	var errPayloads []ErrorPayload
	var sawTurnStart, sawTurnEndError bool
	for _, e := range events {
		switch e.Kind {
		case EventKindTurnStart:
			sawTurnStart = true
		case EventKindTurnEnd:
			if p, ok := e.Payload.(TurnEndPayload); ok && p.Status == TurnEndStatusError {
				sawTurnEndError = true
			}
		case EventKindError:
			if p, ok := e.Payload.(ErrorPayload); ok {
				errPayloads = append(errPayloads, p)
			}
		}
	}

	require.True(t, sawTurnStart,
		"a workspace refusal must start a real turn (EventKindTurnStart) so the SPA has something to attach the error to; events=%v",
		eventKinds(events))
	require.NotEmpty(t, errPayloads,
		"a workspace refusal must emit EventKindError — the classifier already knew the cause and the user still saw silence; events=%v",
		eventKinds(events))

	found := false
	for _, p := range errPayloads {
		if p.Code == string(CodeAgentNotConfigured) {
			found = true
			assert.Equal(t, UserMessageForCode(CodeAgentNotConfigured), p.Message,
				"the live error must carry the catalogue copy, not the raw sentinel")
			assert.Equal(t, "workspace", p.Stage)
			assert.NotEmpty(t, p.SessionID,
				"ErrorPayload.SessionID must be the routing session so a second tab/reload can matchesEvent")
		}
	}
	assert.True(t, found,
		"EventKindError must carry code agent_not_configured; payloads=%+v", errPayloads)
	assert.True(t, sawTurnEndError,
		"the turn must end as TurnEndStatusError so markTurnFailed fires and done cannot claim success")

	store := al.GetSessionStore()
	require.NotNil(t, store, "ProcessDirect must have a session store so the refusal can persist")
	metas, listErr := store.ListSessions()
	require.NoError(t, listErr)
	var persisted *session.TranscriptEntry
	for _, meta := range metas {
		entries, readErr := store.ReadTranscript(meta.ID)
		require.NoError(t, readErr)
		for i := range entries {
			if entries[i].Type == session.EntryTypeSystem && entries[i].Status == "error" {
				persisted = &entries[i]
				break
			}
		}
		if persisted != nil {
			break
		}
	}
	require.NotNil(t, persisted,
		"a live workspace refusal must persist ErrorCode — helper-only tests stay green if runTurn writes the catalogue sentence uncoded")
	assert.Equal(t, string(CodeAgentNotConfigured), persisted.ErrorCode,
		"reload looks up by ErrorCode; unknown would resurrect the UAT lie")
}

func eventKinds(events []Event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Kind.String())
	}
	return out
}

// Replay looks up the catalogue by ErrorCode (getLLMErrorDisplay →
// codeToMessage), not by the persisted Content. If the workspace refusal
// lands as unknown, a reload shows "we can't tell why" even though the
// live frame was right.
func TestAppendErrorTranscript_WorkspaceRefusal_StampsAgentNotConfigured(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)

	ts := &turnState{
		transcriptStore:     store,
		transcriptSessionID: meta.ID,
		agentID:             "main",
	}
	llm := LLMError{
		Code:      CodeAgentNotConfigured,
		Message:   UserMessageForCode(CodeAgentNotConfigured),
		Retryable: isRetryable(CodeAgentNotConfigured),
	}
	ts.appendClassifiedError(EventKindError.String(), "workspace", llm)
	catalogue := llm.Message

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var got *session.TranscriptEntry
	for i := range entries {
		if entries[i].Type == session.EntryTypeSystem && entries[i].Status == "error" {
			got = &entries[i]
			break
		}
	}
	require.NotNil(t, got, "workspace refusal must persist a system error entry for replay")
	assert.Equal(t, string(CodeAgentNotConfigured), got.ErrorCode,
		"replay stamps the bubble from ErrorCode; unknown would resurrect the UAT defect")
	assert.Equal(t, catalogue, got.Content,
		"persisted text must stay the catalogue sentence, not a re-classified unknown line")
	assert.Equal(t, isRetryable(CodeAgentNotConfigured), got.ErrorRetryable)
	assert.True(t, isTrustedInternalStage("workspace", EventKindError.String()),
		"workspace/error must be a trusted internal stage so a later classifier change cannot clobber the sentence")
}

// Uncoded workspace writes must NOT be restamped as membership. That lie
// is how a work-dir failure replayed as "add this agent to a workspace".
func TestAppendErrorTranscript_UncodedWorkspaceDoesNotStampMembership(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)

	ts := &turnState{
		transcriptStore:     store,
		transcriptSessionID: meta.ID,
		agentID:             "main",
	}
	catalogue := UserMessageForCode(CodeWorkspaceUnavailable)
	ts.appendErrorTranscript(EventKindError.String(), "workspace", catalogue)

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var got *session.TranscriptEntry
	for i := range entries {
		if entries[i].Type == session.EntryTypeSystem && entries[i].Status == "error" {
			got = &entries[i]
			break
		}
	}
	require.NotNil(t, got)
	assert.NotEqual(t, string(CodeAgentNotConfigured), got.ErrorCode,
		"an uncoded workspace write must not be restamped as membership")
	assert.Equal(t, string(CodeUnknown), got.ErrorCode,
		"a catalogue sentence has no classifier substring; uncoded write must stay unknown")
}

func TestAppendClassifiedError_KeepsCallerCodeThroughCatalogueSentence(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)

	ts := &turnState{
		transcriptStore:     store,
		transcriptSessionID: meta.ID,
		agentID:             "main",
	}
	llm := LLMError{
		Code:      CodeWorkspaceUnavailable,
		Message:   UserMessageForCode(CodeWorkspaceUnavailable),
		Retryable: isRetryable(CodeWorkspaceUnavailable),
	}
	ts.appendClassifiedError(EventKindError.String(), "workspace", llm)

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var got *session.TranscriptEntry
	for i := range entries {
		if entries[i].Type == session.EntryTypeSystem && entries[i].Status == "error" {
			got = &entries[i]
			break
		}
	}
	require.NotNil(t, got)
	assert.Equal(t, string(CodeWorkspaceUnavailable), got.ErrorCode,
		"replay looks up by ErrorCode; re-classifying the catalogue sentence would stamp unknown")
	assert.Equal(t, llm.Message, got.Content)
}

func TestAppendClassifiedError_ModelSwitchStampsModelUnavailable(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)

	ts := &turnState{
		transcriptStore:     store,
		transcriptSessionID: meta.ID,
		agentID:             "main",
	}
	llm := LLMError{
		Code:      CodeModelUnavailable,
		Message:   UserMessageForCode(CodeModelUnavailable),
		Retryable: isRetryable(CodeModelUnavailable),
	}
	ts.appendClassifiedError(EventKindError.String(), "model_switch", llm)

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var got *session.TranscriptEntry
	for i := range entries {
		if entries[i].Type == session.EntryTypeSystem && entries[i].Status == "error" {
			got = &entries[i]
			break
		}
	}
	require.NotNil(t, got)
	assert.Equal(t, string(CodeModelUnavailable), got.ErrorCode)
	assert.Equal(t, llm.Message, got.Content)
	assert.NotEqual(t, string(CodeUnknown), got.ErrorCode)
}

func TestAppendErrorTranscript_RateLimitDenial_StampsRateLimited(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)

	ts := &turnState{
		transcriptStore:     store,
		transcriptSessionID: meta.ID,
		agentID:             "main",
	}
	// Caller text without the historical "rate limit:" prefix — trusted
	// stage must still stamp CodeRateLimited, not unknown.
	ts.appendErrorTranscript(EventKindRateLimit.String(), "rate_limit", "session daily cost cap (retry shortly)")

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var got *session.TranscriptEntry
	for i := range entries {
		if entries[i].Type == session.EntryTypeSystem && entries[i].Status == "error" {
			got = &entries[i]
			break
		}
	}
	require.NotNil(t, got)
	assert.True(t, isTrustedInternalStage("rate_limit", EventKindRateLimit.String()),
		"rate_limit/rate_limit must be trusted; stage runTurn was the live-vs-reload landmine")
	assert.Equal(t, string(CodeRateLimited), got.ErrorCode,
		"replay looks up by ErrorCode; unknown would say we cannot tell why")
	assert.Equal(t, "session daily cost cap (retry shortly)", got.Content,
		"trusted stage must keep the caller text, not a reclassified unknown line")
}

func TestOutcomeRelabelApplies(t *testing.T) {
	assert.True(t, outcomeRelabelApplies("", CodeMediaUnsupported),
		"empty residual 4xx is the FR-017a inconclusive case")
	assert.True(t, outcomeRelabelApplies(CodeUnknown, CodeMediaUnsupported),
		"CodeUnknown is the FR-017a inconclusive case")
	assert.False(t, outcomeRelabelApplies(CodeRateLimited, CodeMediaUnsupported),
		"a later classified failure must keep its own code")
	assert.False(t, outcomeRelabelApplies(CodeUnknown, ""),
		"no stamp means the classifier verdict stands")
}

func TestAppendClassifiedError_OutcomeRelabelDoesNotClobberDistinctCode(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)

	ts := &turnState{
		transcriptStore:     store,
		transcriptSessionID: meta.ID,
		agentID:             "main",
	}
	ts.setOutcomeRelabel(CodeMediaUnsupported)
	llm := LLMError{
		Code:      CodeRateLimited,
		Message:   UserMessageForCode(CodeRateLimited),
		Retryable: isRetryable(CodeRateLimited),
	}
	ts.appendClassifiedError(EventKindError.String(), "hooks", llm)

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var got *session.TranscriptEntry
	for i := range entries {
		if entries[i].Type == session.EntryTypeSystem && entries[i].Status == "error" {
			got = &entries[i]
			break
		}
	}
	require.NotNil(t, got)
	assert.Equal(t, string(CodeRateLimited), got.ErrorCode,
		"a later classified failure must keep its own code after a successful media strip-retry")
	assert.Equal(t, llm.Message, got.Content)
	assert.NotEqual(t, string(CodeMediaUnsupported), got.ErrorCode,
		"reload must not say the model rejected an image when the later failure was a rate limit")
}

func TestAppendClassifiedError_OutcomeRelabelStillLabelsUnknown(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)

	ts := &turnState{
		transcriptStore:     store,
		transcriptSessionID: meta.ID,
		agentID:             "main",
	}
	ts.setOutcomeRelabel(CodeMediaUnsupported)
	llm := LLMError{
		Code:      CodeUnknown,
		Message:   UserMessageForCode(CodeUnknown),
		Retryable: isRetryable(CodeUnknown),
	}
	ts.appendClassifiedError(EventKindError.String(), "runTurn", llm)

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var got *session.TranscriptEntry
	for i := range entries {
		if entries[i].Type == session.EntryTypeSystem && entries[i].Status == "error" {
			got = &entries[i]
			break
		}
	}
	require.NotNil(t, got)
	assert.Equal(t, string(CodeMediaUnsupported), got.ErrorCode,
		"FR-017a must still label an inconclusive residual 4xx as media after a successful strip-retry")
	assert.Equal(t, defaultUserMessage(CodeMediaUnsupported), got.Content)
}

func TestRunTurn_MemberWorkDirUnavailable_EmitsAndPersistsWorkspaceUnavailable(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpHome)

	agentWorkspaceDir := filepath.Join(tmpHome, "agents", "main")
	require.NoError(t, os.MkdirAll(agentWorkspaceDir, 0o755))

	workspacesDir := filepath.Join(tmpHome, "workspaces")
	wsDir := filepath.Join(workspacesDir, "blocked-ws")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	wsJSON := `{"id":"blocked-ws","core_team":["main"]}`
	require.NoError(t, os.WriteFile(filepath.Join(workspacesDir, "blocked-ws.json"), []byte(wsJSON), 0o644))
	// Make work/ a file so EnsureWorkDir's MkdirAll fails. The agent IS a
	// member — this must not restamp as "add this agent to a workspace".
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "work"), []byte("not-a-dir"), 0o644))

	provider := testutil.NewScenario().WithText("should-never-be-called")
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:                agentWorkspaceDir,
				DefaultModel:        config.DefaultModel{Model: "scripted-model"},
				MaxTokens:           4096,
				MaxToolIterations:   10,
				RestrictToWorkspace: true,
			},
			List: []config.AgentConfig{{ID: "main"}},
		},
	}
	msgBus := bus.NewMessageBus()
	al, err := NewAgentLoop(cfg, msgBus, provider)
	require.NoError(t, err)
	defer al.Close()

	sub := al.SubscribeEvents(32)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })

	_, procErr := al.ProcessDirect(context.Background(), "hello", "test-session-workdir-blocked")
	require.Error(t, procErr, "a member whose work dir cannot be opened must be refused")
	assert.True(t, errors.Is(procErr, ErrWorkspaceWorkDirUnavailable),
		"refusal must wrap ErrWorkspaceWorkDirUnavailable, not membership; got: %v", procErr)
	assert.False(t, errors.Is(procErr, ErrAgentNotWorkspaceMember),
		"a member with a broken work dir must not be told they are not on the team")

	events := drainEvents(sub.C)
	var errPayloads []ErrorPayload
	for _, e := range events {
		if e.Kind == EventKindError {
			if p, ok := e.Payload.(ErrorPayload); ok {
				errPayloads = append(errPayloads, p)
			}
		}
	}
	require.NotEmpty(t, errPayloads, "live frame must carry the work-dir failure; events=%v", eventKinds(events))
	found := false
	for _, p := range errPayloads {
		if p.Code == string(CodeWorkspaceUnavailable) {
			found = true
			assert.Equal(t, UserMessageForCode(CodeWorkspaceUnavailable), p.Message)
			assert.Equal(t, "workspace", p.Stage)
			assert.NotEqual(t, string(CodeAgentNotConfigured), p.Code)
		}
	}
	assert.True(t, found, "live EventKindError must be workspace_unavailable; payloads=%+v", errPayloads)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	metas, listErr := store.ListSessions()
	require.NoError(t, listErr)
	var persisted *session.TranscriptEntry
	for _, meta := range metas {
		entries, readErr := store.ReadTranscript(meta.ID)
		require.NoError(t, readErr)
		for i := range entries {
			if entries[i].Type == session.EntryTypeSystem && entries[i].Status == "error" {
				persisted = &entries[i]
				break
			}
		}
		if persisted != nil {
			break
		}
	}
	require.NotNil(t, persisted, "work-dir failure must persist for reload")
	assert.Equal(t, string(CodeWorkspaceUnavailable), persisted.ErrorCode,
		"reload must not say add-this-agent-to-a-workspace for a disk/path failure")
}

func TestRunTurn_MemberGetsWorkspaceWorkDir(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpHome)

	agentWorkspaceDir := filepath.Join(tmpHome, "agents", "main")
	require.NoError(t, os.MkdirAll(agentWorkspaceDir, 0o755))

	workspacesDir := filepath.Join(tmpHome, "workspaces")
	require.NoError(t, os.MkdirAll(workspacesDir, 0o755))
	wsJSON := `{"id":"explicit-ws","core_team":["main"]}`
	require.NoError(t, os.WriteFile(filepath.Join(workspacesDir, "explicit-ws.json"), []byte(wsJSON), 0o644))

	provider := testutil.NewScenario().
		WithToolCall("write_file", `{"path":"explicit.txt","content":"explicit-workspace-write","overwrite":true}`).
		WithText("done")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:                agentWorkspaceDir,
				DefaultModel:        config.DefaultModel{Model: "scripted-model"},
				MaxTokens:           4096,
				MaxToolIterations:   10,
				RestrictToWorkspace: true,
			},
			List: []config.AgentConfig{{ID: "main"}},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	defaultAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"write_file": "allow"},
	})

	ctx := context.Background()
	finalContent, _, procErr := al.processMessage(ctx, bus.InboundMessage{
		Channel:    "cli",
		ChatID:     "direct",
		Content:    "please write explicit.txt for me",
		SessionKey: "test-session-explicit-workspace",
		Metadata:   map[string]string{"workspace_id": "explicit-ws"},
	})
	require.NoError(t, procErr, "processMessage must succeed")
	assert.Equal(t, "done", finalContent)

	sharedFile := filepath.Join(workspacesDir, "explicit-ws", "work", "explicit.txt")
	got, readErr := os.ReadFile(sharedFile)
	require.NoError(t, readErr,
		"write_file must land in the EXPLICIT turn workspace's work/ dir (%s)", sharedFile)
	assert.Equal(t, "explicit-workspace-write", string(got))
}

// TestRunTurn_CoreTeamMember_CannotEscapeWorkToWorkspaceRoot proves the
// security property motivating the work/ subdirectory design: re-rooting a
// CoreTeam member's file tools to workspaces/<id>/work/ (not
// workspaces/<id>/ itself) means AGENT.md and the shared memory room, one
// level up, are structurally unreachable — a relative "../" escape attempt is
// rejected by the os.Root confinement itself, not by a guard that has to
// separately recognize the workspaces/<id>/ layout (pkg/tools/metadata_guard.go's
// app-level guard only recognizes agents/<id>/AGENT.md and does NOT match
// workspaces/<id>/AGENT.md — this test does not rely on that guard at all).
func TestRunTurn_CoreTeamMember_CannotEscapeWorkToWorkspaceRoot(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpHome)

	agentWorkspaceDir := filepath.Join(tmpHome, "agents", "main")
	require.NoError(t, os.MkdirAll(agentWorkspaceDir, 0o755))

	workspacesDir := filepath.Join(tmpHome, "workspaces")
	wsDir := filepath.Join(workspacesDir, "team-ws")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	wsJSON := `{"id":"team-ws","core_team":["main"]}`
	require.NoError(t, os.WriteFile(filepath.Join(workspacesDir, "team-ws.json"), []byte(wsJSON), 0o644))

	// A real AGENT.md at the workspace root, with known content, that the
	// re-rooted turn must never be able to touch.
	agentMdPath := filepath.Join(wsDir, "AGENT.md")
	require.NoError(t, os.WriteFile(agentMdPath, []byte("original project instructions"), 0o600))

	provider := testutil.NewScenario().
		WithToolCall("write_file", `{"path":"../AGENT.md","content":"pwned","overwrite":true}`).
		WithText("done")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:                agentWorkspaceDir,
				DefaultModel:        config.DefaultModel{Model: "scripted-model"},
				MaxTokens:           4096,
				MaxToolIterations:   10,
				RestrictToWorkspace: true,
			},
			List: []config.AgentConfig{{ID: "main"}},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	const sessionKey = "test-session-coreteam-escape-attempt"
	ctx := context.Background()
	finalContent, err := al.ProcessDirect(ctx, "please write ../AGENT.md for me", sessionKey)
	require.NoError(
		t,
		err,
		"ProcessDirect must succeed (the tool call itself is expected to be rejected, not the turn)",
	)
	assert.Equal(t, "done", finalContent)

	// AGENT.md must be completely untouched.
	got, readErr := os.ReadFile(agentMdPath)
	require.NoError(t, readErr, "AGENT.md must still exist")
	assert.Equal(
		t,
		"original project instructions",
		string(got),
		"a CoreTeam member's write_file must NOT be able to reach workspaces/<id>/AGENT.md via a relative escape from work/",
	)
}
