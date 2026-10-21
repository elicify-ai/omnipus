// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session-core U1 (FR-002, FR-003): main session identity over the gateway.
//
// The store owns WHAT a main is (pkg/session/main_session.go: the computed id
// and its get-or-create). This file owns WHO is entitled to one:
//
//   - an eligible native member of a workspace (a chat target: not a worker,
//     not a System Agent). Omnipus is an ordinary member of the default
//     workspace only (omnipus-system-agent FR-003), so its main is the
//     default workspace's ordinary member main.
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
	"strings"
	"syscall"

	"github.com/elicify-ai/omnipus/pkg/api/generated"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"
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
// main at all (C-MAIN/FR-002): a member (checked by mainSessionPairEligible)
// AND a chat target — workers are delegation-only labor and System Agents are
// out-of-turn internal-LLM agents, so neither gets a standing conversation. An
// agent ID absent from the live config is not eligible — a stale team entry
// never mints an identity.
func mainAgentEligible(cfg *config.Config, _ storedWorkspace, agentID string) bool {
	if cfg == nil || agentID == "" {
		return false
	}
	for _, ac := range cfg.Agents.List {
		if ac.ID == agentID {
			return ac.IsChatTarget()
		}
	}
	return false
}

// mainSessionPairEligible reports whether (ws, agentID) is a pair that owns a
// main: an eligible agent that is also a member of this workspace.
func mainSessionPairEligible(cfg *config.Config, ws storedWorkspace, agentID string) bool {
	if !mainAgentEligible(cfg, ws, agentID) {
		return false
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

// omnipusStandaloneRefusal is the fixed client text for a request to create a
// chat with Omnipus outside the default workspace.
const omnipusStandaloneRefusal = "Omnipus's chats live in the default workspace; open Omnipus's main chat there."

// agentMustBeNamed is the visible refusal for a new chat (REST create or a
// WebSocket frame without session_id) that names no agent
// (omnipus-system-agent FR-122, §6.4): there is no default agent.
const agentMustBeNamed = "an agent must be named"

// sessionOwnerConflictRefusal is the visible refusal for a message that names
// an agent other than the existing session's canonical owner (spec §6.4,
// CHECK F02): the session keeps its owner, and the conflict is refused,
// naming both agents.
func sessionOwnerConflictRefusal(owner, named string) string {
	return fmt.Sprintf("this chat belongs to %s; a message naming %s cannot run in it", owner, named)
}

// foldSystemAgentID returns the system agent's canonical id for any letter-case
// spelling of it, and id unchanged otherwise (CHECK F01). The system agent's
// guards — one conversation (FR-005), its placement (FR-003) — compare against
// the canonical id, while the registry also answers to a case alias, so an
// alias must become the canonical id at the door. Only the system agent is
// folded: an agent imported with a mixed-case id (OpenClaw migration stores
// the source id as-is) keeps its exact spelling, and every other id keeps its
// existing handling.
func foldSystemAgentID(id string) string {
	if strings.EqualFold(id, string(coreagent.IDOmnipus)) {
		return string(coreagent.IDOmnipus)
	}
	return id
}

// sessionOwnerMissingRefusal is the visible refusal for a message to an
// existing session whose owner agent no longer exists (§6.4): the session
// never falls back to another agent.
func sessionOwnerMissingRefusal(owner string) string {
	return fmt.Sprintf("the session's owner agent %s no longer exists", owner)
}

// omnipusSecondConversationRefusal enforces FR-005: Omnipus has exactly one
// conversation, its main chat, so any request to mint another chat with it is
// refused with that main's id (never a dead end). The id is the existing main
// in store when there is one, else the main the default workspace will hold.
// Returns ("", "", nil) for any other agent, and when Omnipus has no main and
// there is no default workspace yet (omnipusChatRefusal answers that case). A
// failure to read the sessions or the workspaces is returned as the error.
func omnipusSecondConversationRefusal(store *session.UnifiedStore, home, agentID string) (string, string, error) {
	if agentID != string(coreagent.IDOmnipus) {
		return "", "", nil
	}
	mainID := ""
	if store != nil {
		mains, err := store.ListSessionsFiltered(func(m *session.UnifiedMeta) bool {
			return m.Type == session.SessionTypeMain && m.AgentID == agentID
		})
		if err != nil {
			return "", "", err
		}
		if len(mains) > 0 {
			mainID = mains[0].ID
		}
	}
	if mainID == "" {
		def, err := workspace.ResolveDefaultID(home)
		if err != nil {
			if errors.Is(err, workspace.ErrNoDefault) {
				return "", "", nil
			}
			return "", "", err
		}
		if def == "" {
			return "", "", nil
		}
		if mainID, err = session.MainSessionID(def, agentID); err != nil {
			return "", "", err
		}
	}
	return fmt.Sprintf("Omnipus has one conversation; continue it in its main chat (session %s)", mainID), mainID, nil
}

// omnipusChatRefusal enforces "one main per agent per workspace" for the
// system agent at every door that mints or re-binds a chat session (POST
// /sessions, the first WebSocket message, and a later frame naming another
// workspace): Omnipus belongs to the default workspace only
// (omnipus-system-agent FR-003), so a chat with it is accepted only inside the
// DEFAULT workspace; anything else (no workspace, another workspace, no
// default yet) returns the refusal text. Other agents return "". home is the
// OMNIPUS_HOME the workspaces live under.
//
// A failure to READ the workspaces (anything but "there is no default yet") is a
// server fault, returned as the error and never as a refusal: the caller answers
// it as a 500 with the fixed server-failure text, not as the user-facing 400.
func omnipusChatRefusal(home, agentID, workspaceID string) (string, error) {
	if agentID != string(coreagent.IDOmnipus) {
		return "", nil
	}
	def, err := workspace.ResolveDefaultID(home)
	if err != nil {
		if errors.Is(err, workspace.ErrNoDefault) {
			return omnipusStandaloneRefusal, nil
		}
		return "", err
	}
	if def == "" || strings.TrimSpace(workspaceID) != def {
		return omnipusStandaloneRefusal, nil
	}
	return "", nil
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

// mainSessionAddress resolves the read-only member main projection without
// creating anything. Only a matching stored identity gets an address; an
// eligible member's failure is preserved for the safe error projection.
// Ineligible members have neither an address nor a preparation error.
func (a *restAPI) mainSessionAddress(ws storedWorkspace, agentID string) (string, error) {
	cfg := a.agentLoop.GetConfig()
	if !mainSessionPairEligible(cfg, ws, agentID) {
		return "", nil
	}
	sessionID, err := session.MainSessionID(ws.ID, agentID)
	if err != nil {
		return "", err
	}
	store := a.agentLoop.GetSessionStore()
	if store == nil {
		return "", errNoSessionStore
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil {
		return "", err
	}
	if meta.Type != session.SessionTypeMain || meta.AgentID != agentID || meta.WorkspaceID != ws.ID {
		return "", errMainChatNotPrepared
	}
	return sessionID, nil
}

// mainSessionErrorMessage is display-only, never raw error text. Classify the
// structured cause so no path, filename or stored identity can reach the UI.
func mainSessionErrorMessage(err error) string {
	if errors.Is(err, syscall.ENOTDIR) {
		return "The main chat could not be prepared because session storage has a file where a folder is required. Ask the server operator to fix it, then retry."
	}
	return errMainChatNotPrepared.Error()
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
// without any later event: one main per eligible member of EVERY existing
// workspace team — Omnipus's included, as an ordinary member of the default
// workspace. It runs from the boot path after the default workspace is
// ensured, so a fresh install's seeded team owns its mains at first boot — a
// same-team PUT is a no-op and never the trigger. Best-effort and idempotent:
// ensureMainSession reuses an existing main, and an unresolvable pair is
// logged and skipped.
func (a *restAPI) ensureBootMains() {
	workspaces, err := listWorkspaceFiles(a.homePath)
	if err != nil {
		slog.Warn("rest: boot mains: list workspaces failed", "error", err)
		return
	}
	for _, ws := range workspaces {
		if err := a.ensureMainsForTeam(ws); err != nil {
			slog.Error("rest: boot mains: some team mains were not created; each is re-attempted when its chat is opened",
				"workspace_id", ws.ID, "error", err)
		}
	}
}
