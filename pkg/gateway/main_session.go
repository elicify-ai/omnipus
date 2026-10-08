// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session-core U1 (FR-002, FR-003): main session identity over the gateway.
//
// The store owns WHAT a main is (pkg/session/main_session.go: the computed id
// and its get-or-create). This file owns WHO is entitled to one:
//
//   - an eligible native member of a workspace (a chat target: not a worker,
//     not a System Agent), and
//   - Admin, in the DEFAULT workspace only (Admin is deliberately never a
//     team member — see coreagent.ExcludedFromWorkspaceTeams — so the Admin
//     main is not created by a membership write at all).
//
// Nothing here mints a second identity, a hashed id or a member row: every
// path funnels into UnifiedStore.GetOrCreateMainSession under the pair's one
// computed id.
package gateway

import (
	"errors"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// errNoSessionStore is returned when the gateway has no shared session store
// wired. It is a server-side inconsistency (the store is constructed at boot),
// never a user input error.
var errNoSessionStore = errors.New("session store unavailable")

// mainSessionAccessor is the slice of *agent.AgentLoop this file needs:
// the live config (agent eligibility) and the shared session store (main
// get-or-create).
type mainSessionAccessor interface {
	GetConfig() *config.Config
	GetSessionStore() *session.UnifiedStore
}

// mainAgentEligible reports whether agentID is a kind of agent that can own a
// main at all, in workspace ws. Two rules, both from C-MAIN/FR-002:
//
//   - Admin owns a main in the DEFAULT workspace only. Admin sits on no team,
//     so this is the whole of Admin's rule.
//   - Every other agent must be a member (checked by mainSessionPairEligible)
//     AND a chat target: workers are delegation-only labor and System Agents
//     are out-of-turn internal-LLM agents, so neither gets a standing
//     conversation. An agent ID absent from the live config is not eligible —
//     a stale team entry never mints an identity.
func mainAgentEligible(cfg *config.Config, ws storedWorkspace, agentID string) bool {
	if cfg == nil || agentID == "" {
		return false
	}
	if agentID == string(coreagent.IDAdmin) {
		return ws.IsDefault
	}
	for _, ac := range cfg.Agents.List {
		if ac.ID == agentID {
			return ac.IsChatTarget()
		}
	}
	return false
}

// mainSessionPairEligible reports whether (ws, agentID) is a pair that owns a
// main: an eligible agent that is also a member of this workspace (Admin's
// exemption above aside).
func mainSessionPairEligible(cfg *config.Config, ws storedWorkspace, agentID string) bool {
	if !mainAgentEligible(cfg, ws, agentID) {
		return false
	}
	if agentID == string(coreagent.IDAdmin) {
		return true
	}
	for _, id := range ws.CoreTeam {
		if id == agentID {
			return true
		}
	}
	return false
}

// workspaceForMainSession loads the workspace a main id names. A pair whose
// workspace cannot be read has no eligibility information, so it is refused
// rather than guessed at.
func (a *restAPI) workspaceForMainSession(workspaceID string) (storedWorkspace, bool) {
	ws, err := readWorkspaceFile(a.homePath, workspaceID)
	if err != nil {
		return storedWorkspace{}, false
	}
	return ws, true
}

// ensureMainSession get-or-creates the main for a pair the CALLER has already
// established is eligible. It returns the computed id, or the refusal from the
// store (over-cap id, or a stored identity that does not match the pair — see
// UnifiedStore.GetOrCreateMainSession; nothing is written in either case).
func (a *restAPI) ensureMainSession(wsID, agentID string) (string, error) {
	sessionID, err := session.MainSessionID(wsID, agentID)
	if err != nil {
		return "", err
	}
	store := a.agentLoop.GetSessionStore()
	if store == nil {
		return "", errNoSessionStore
	}
	if _, err := store.GetOrCreateMainSession(wsID, agentID); err != nil {
		return "", err
	}
	return sessionID, nil
}

// mainSessionAddress is the wire projection of member_configs[agent].main_session_id:
// the pair's computed main id when that main actually RESOLVES to a matching
// stored identity, and "" otherwise (the field is then omitted).
//
// Resolving — never merely computing — is deliberate: a member whose stored
// main is corrupt or owned by someone else must not be advertised an address
// that does not work. The write path refuses to create a replacement in that
// case, and this projection refuses to pretend one exists.
func (a *restAPI) mainSessionAddress(ws storedWorkspace, agentID string) string {
	cfg := a.agentLoop.GetConfig()
	if !mainSessionPairEligible(cfg, ws, agentID) {
		return ""
	}
	sessionID, err := session.MainSessionID(ws.ID, agentID)
	if err != nil {
		return ""
	}
	store := a.agentLoop.GetSessionStore()
	if store == nil {
		return ""
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil {
		return ""
	}
	if meta.Type != session.SessionTypeMain || meta.AgentID != agentID || meta.WorkspaceID != ws.ID {
		return ""
	}
	return sessionID
}

// ensureMainsForTeam get-or-creates the main of every eligible member of ws's
// team. Best-effort per member: a pair whose stored identity is corrupt or
// mismatched is logged and skipped — the membership write does not carry that
// session file, so failing the whole workspace update over it would be the
// wrong surface to report it on. The refusal is served where it belongs, on
// the main's own lookup/attach (see getSession) and by the absence of a
// main_session_id on the wire.
//
// Over-cap pairs never reach here: prepareWorkspacePutMembers validates every
// prospective id up front and refuses the request with a visible 422.
func (a *restAPI) ensureMainsForTeam(ws storedWorkspace) {
	cfg := a.agentLoop.GetConfig()
	for _, agentID := range ws.CoreTeam {
		if !mainSessionPairEligible(cfg, ws, agentID) {
			continue
		}
		if _, err := a.ensureMainSession(ws.ID, agentID); err != nil {
			slog.Warn("rest: workspace team: main session not resolved",
				"workspace_id", ws.ID, "agent_id", agentID, "error", err)
		}
	}
}

// resolveMainSessionForRead is the lookup/attach gate for a session id that
// names a main pair. It returns true only when the pair is eligible AND its
// main resolves to a matching stored identity — creating it when it does not
// exist yet (BDD-01.1: a lookup is one of the paths that establishes the one
// main of an eligible pair).
//
// Every other outcome is a refusal, and a refusal NEVER creates anything:
//
//   - the pair is not eligible (non-member, worker, System Agent, Admin
//     outside the default workspace, unknown workspace) — nothing is minted,
//     so the caller's 404 is the truth rather than a gate over a new session;
//   - the stored identity is corrupt, unreadable or owned by someone else —
//     BDD-01.4's refusal, served here so a mismatched record can never be
//     handed back as the pair's valid main.
func (a *restAPI) resolveMainSessionForRead(id string) bool {
	wsID, agentID, ok := session.SplitMainSessionID(id)
	if !ok {
		return true // not a computed main id: the ordinary lookup path owns it
	}
	ws, ok := a.workspaceForMainSession(wsID)
	if !ok {
		return false
	}
	if !mainSessionPairEligible(a.agentLoop.GetConfig(), ws, agentID) {
		return false
	}
	if _, err := a.ensureMainSession(wsID, agentID); err != nil {
		slog.Warn("rest: main session lookup refused",
			"session_id", id, "workspace_id", wsID, "agent_id", agentID, "error", err)
		return false
	}
	return true
}

// mainSessionVisible reports whether a listed session should be surfaced.
// Every non-main row is visible. A main row is visible only while its pair is
// still entitled to it (FR-003): membership removal hides the main from the
// list immediately, while the stored identity itself is retained so a later
// re-add reveals the same id. A stored main whose record contradicts its own
// computed id is hidden too, never served as if it were valid.
func (a *restAPI) mainSessionVisible(m *session.UnifiedMeta) bool {
	if m == nil || m.Type != session.SessionTypeMain {
		return true
	}
	wsID, agentID, ok := session.SplitMainSessionID(m.ID)
	if !ok {
		return true
	}
	ws, ok := a.workspaceForMainSession(wsID)
	if !ok {
		return false
	}
	if !mainSessionPairEligible(a.agentLoop.GetConfig(), ws, agentID) {
		return false
	}
	return m.AgentID == agentID && m.WorkspaceID == wsID
}

// ensureDefaultWorkspaceAdminMain get-or-creates Admin's main in the default
// workspace. Admin has no membership event to hang it on, so this runs from
// the boot path that already ensures the default workspace, in BOTH branches —
// the one that creates the workspace and the one that finds it already there.
// An install with no default workspace (nothing to be Admin's main IN) is a
// no-op, never a guessed workspace.
func (a *restAPI) ensureDefaultWorkspaceAdminMain() {
	workspaces, err := listWorkspaceFiles(a.homePath)
	if err != nil {
		slog.Warn("rest: admin main: list workspaces failed", "error", err)
		return
	}
	for _, ws := range workspaces {
		if !ws.IsDefault {
			continue
		}
		if _, err := a.ensureMainSession(ws.ID, string(coreagent.IDAdmin)); err != nil {
			slog.Warn("rest: admin main: default-workspace main not resolved",
				"workspace_id", ws.ID, "error", err)
		}
		return
	}
}
