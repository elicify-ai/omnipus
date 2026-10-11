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
	"fmt"
	"log/slog"
	"net/http"

	"github.com/elicify-ai/omnipus/pkg/api/generated"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// errNoSessionStore is returned when the gateway has no shared session store
// wired. It is a server-side inconsistency (the store is constructed at boot),
// never a user input error.
var errNoSessionStore = errors.New("session store unavailable")

// errMainChatNotPrepared is the fixed client-facing text when an entitled pair's
// main could not be created or trusted. It is deliberately free of paths, file
// names, session or identity ids and stored owner/workspace values; the real
// cause is in the server log at error level.
var errMainChatNotPrepared = errors.New(
	"This agent's main chat could not be prepared because session storage failed. " +
		"Check disk space and permissions, then retry. Details are in the server log.")

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
// rather than guessed at. A workspace that does not exist is the ordinary "not
// eligible"; a read FAILURE is logged (final-gate M8) so a corrupt or unreadable
// workspace file is never indistinguishable from a missing one.
func (a *restAPI) workspaceForMainSession(workspaceID string) (storedWorkspace, bool) {
	ws, found, err := loadWorkspaceForMain(a.homePath, workspaceID)
	if err != nil {
		slog.Error("rest: main eligibility: the workspace could not be read; its mains are hidden until it can be",
			"workspace_id", workspaceID, "error", err)
		return storedWorkspace{}, false
	}
	return ws, found
}

// loadWorkspaceForMain separates the three outcomes eligibility needs: found,
// not found (nil error), and a read failure (non-nil error) - the same split
// address_deps.go::PairEligible and the task resolver already make.
func loadWorkspaceForMain(home, workspaceID string) (ws storedWorkspace, found bool, err error) {
	ws, err = readWorkspaceFile(home, workspaceID)
	if err != nil {
		if errors.Is(err, errWorkspaceNotFound) {
			return storedWorkspace{}, false, nil
		}
		return storedWorkspace{}, false, err
	}
	return ws, true, nil
}

// mainSessionGate decides whether a session id may be served as a main right
// now (final-gate M4). isMain is false for any id that is not a computed main
// id (the caller's ordinary lookup owns it). For a main id, eligible is the same
// answer the session list gives (mainSessionPairEligible over the current
// workspace record); err is non-nil only when the workspace record could not be
// READ, which is neither "eligible" nor "not a member". It creates nothing.
func mainSessionGate(home string, cfg *config.Config, id string) (isMain, eligible bool, err error) {
	wsID, agentID, ok := session.SplitMainSessionID(id)
	if !ok {
		return false, true, nil
	}
	ws, found, err := loadWorkspaceForMain(home, wsID)
	if err != nil {
		return true, false, err
	}
	if !found {
		return true, false, nil
	}
	return true, mainSessionPairEligible(cfg, ws, agentID), nil
}

// mainSessionAllowed is the WebSocket door of mainSessionGate: false (after
// sending the client one error frame) when the id names a main whose pair is no
// longer entitled to it, or whose workspace cannot be read. It creates nothing.
func (h *WSHandler) mainSessionAllowed(wc *wsConn, id string) bool {
	// h.home is set at gateway boot; a handler built without it (tests) resolves
	// the same OMNIPUS_HOME the rest of the gateway does - never the working directory.
	home := h.home
	if home == "" {
		home = config.OmnipusHomeDir()
	}
	isMain, eligible, err := mainSessionGate(home, h.agentLoop.GetConfig(), id)
	if err != nil {
		slog.Error("ws: main eligibility: the workspace could not be read", "session_id", id, "error", err)
		sid := id
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type: string(generated.WsFrameTypeError), SessionId: &sid, Message: errMainChatNotPrepared.Error(),
		})
		return false
	}
	if isMain && !eligible {
		sid := id
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type: string(generated.WsFrameTypeError), SessionId: &sid, Message: "session not found",
		})
		return false
	}
	return true
}

// gateMainRead is mainSessionGate for the REST read/write doors that must not
// serve a hidden main. It writes the response and returns false when the caller
// must stop: 404 for a main whose pair is no longer entitled to it (the same
// answer as a session that does not exist), a fixed 500 when the workspace
// record cannot be read.
func (a *restAPI) gateMainRead(w http.ResponseWriter, id string) bool {
	isMain, eligible, err := mainSessionGate(a.homePath, a.agentLoop.GetConfig(), id)
	if err != nil {
		slog.Error("rest: main eligibility: the workspace could not be read", "session_id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, errMainChatNotPrepared.Error())
		return false
	}
	if isMain && !eligible {
		jsonErr(w, http.StatusNotFound, "session not found")
		return false
	}
	return true
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
// team. Best-effort per member: one pair whose stored identity is corrupt or
// mismatched, or whose storage write fails, does not stop the others — the
// membership write does not carry that session file, so failing the whole
// workspace update over it would be the wrong surface to report it on. Every
// per-pair failure is RETURNED (joined, each naming the workspace and agent) so
// the caller logs it at error level; none is swallowed here. The user-facing
// refusal is served where it belongs, on the main's own lookup/attach (see
// getSession, which re-attempts creation and reports the real cause) and by the
// absence of a main_session_id on the wire.
//
// Over-cap pairs never reach here: prepareWorkspacePutMembers validates every
// prospective id up front and refuses the request with a visible 422.
func (a *restAPI) ensureMainsForTeam(ws storedWorkspace) error {
	cfg := a.agentLoop.GetConfig()
	var errs []error
	for _, agentID := range ws.CoreTeam {
		if !mainSessionPairEligible(cfg, ws, agentID) {
			continue
		}
		if _, err := a.ensureMainSession(ws.ID, agentID); err != nil {
			errs = append(errs, fmt.Errorf("workspace %s agent %s: %w", ws.ID, agentID, err))
		}
	}
	return errors.Join(errs...)
}

// resolveMainSessionForRead is the lookup gate that also ESTABLISHES a main for a
// session id that names an entitled pair (getSession). The read-only door gate
// every other path uses is mainSessionGate (REST: gateMainRead, WebSocket:
// mainSessionAllowed); this one creates, so it is deliberately separate. It returns true only when the pair is eligible AND its
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
//
// The error return separates "this pair has no main" (false, nil: the caller's
// 404 is the truth) from "this pair is entitled to a main but it could not be
// created or trusted" (false, errMainChatNotPrepared): a fixed, actionable
// message (the real cause is logged), so a retry shows something useful instead
// of "session not found". It is re-attempted on every call, so a transient
// storage failure at boot heals on the next open.
func (a *restAPI) resolveMainSessionForRead(id string) (bool, error) {
	wsID, agentID, ok := session.SplitMainSessionID(id)
	if !ok {
		return true, nil // not a computed main id: the ordinary lookup path owns it
	}
	ws, found, wsErr := loadWorkspaceForMain(a.homePath, wsID)
	if wsErr != nil {
		// BDD-01.4: unreadable metadata is a visible error, not "no such chat".
		slog.Error("rest: main session could not be resolved: the workspace could not be read",
			"session_id", id, "workspace_id", wsID, "error", wsErr)
		return false, errMainChatNotPrepared
	}
	if !found {
		return false, nil
	}
	if !mainSessionPairEligible(a.agentLoop.GetConfig(), ws, agentID) {
		return false, nil
	}
	if _, err := a.ensureMainSession(wsID, agentID); err != nil {
		// The full contextual error stays in the server log; the client gets a
		// fixed, actionable message that carries no path, file name, id or
		// stored identity value.
		slog.Error("rest: main session could not be prepared on open",
			"session_id", id, "workspace_id", wsID, "agent_id", agentID, "error", err)
		return false, errMainChatNotPrepared
	}
	return true, nil
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

// ensureWorkspaceMains provisions the main of every eligible member of one
// workspace's team - the provisioning a workspace save runs - for the writers
// that change membership without a save (the Admin create_agent tool, final-gate
// H2). A workspace that cannot be read is an error, never a silent no-op.
func (a *restAPI) ensureWorkspaceMains(workspaceID string) error {
	ws, err := readWorkspaceFile(a.homePath, workspaceID)
	if err != nil {
		return fmt.Errorf("workspace %s: %w", workspaceID, err)
	}
	return a.ensureMainsForTeam(ws)
}

// ensureBootMains eagerly establishes every main that FR-002 says exists
// without any later event: Admin's main in the default workspace, and one main
// per eligible member of EVERY existing workspace team. It runs from the boot
// path after the default workspace is ensured, so a fresh install's seeded
// team owns its mains at first boot — a same-team PUT is a no-op and never the
// trigger. Admin has no membership to hang a main on, so it is handled here
// explicitly. Best-effort and idempotent: ensureMainSession reuses an existing
// main, and an unresolvable pair is logged and skipped. An install with no
// default workspace gets no Admin main — never a guessed workspace.
func (a *restAPI) ensureBootMains() {
	workspaces, err := listWorkspaceFiles(a.homePath)
	if err != nil {
		slog.Warn("rest: boot mains: list workspaces failed", "error", err)
		return
	}
	for _, ws := range workspaces {
		if ws.IsDefault {
			if _, err := a.ensureMainSession(ws.ID, string(coreagent.IDAdmin)); err != nil {
				slog.Error("rest: boot mains: Admin's main was not created; it is re-attempted when the chat is opened",
					"workspace_id", ws.ID, "agent_id", string(coreagent.IDAdmin), "error", err)
			}
		}
		if err := a.ensureMainsForTeam(ws); err != nil {
			slog.Error("rest: boot mains: some team mains were not created; each is re-attempted when its chat is opened",
				"workspace_id", ws.ID, "error", err)
		}
	}
}
