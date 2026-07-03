package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/dapicom-ai/omnipus/pkg/agent"
	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
	"github.com/dapicom-ai/omnipus/pkg/email"
)

// buildMailboxes resolves the set of enabled, password-resolvable mailboxes from
// cfg + the credential store into live email transports. It is the side-effect-
// free source for the M11 mailbox-drain service: an enabled mailbox whose
// password does not resolve (locked store, missing secret) is skipped with a WARN
// rather than failing the whole drain. The returned slice is empty when no
// mailbox is configured.
//
// cfg.Mailboxes is agent ID → workspace ID → mailbox (pair-addressed 2026-07-03):
// the same agent may own a different mailbox in each workspace it belongs to.
func buildMailboxes(cfg *config.Config, store *credentials.Store) []email.Mailbox {
	if cfg == nil || len(cfg.Mailboxes) == 0 || store == nil {
		return nil
	}
	var out []email.Mailbox
	for agentID, byWorkspace := range cfg.Mailboxes {
		for workspaceID, mb := range byWorkspace {
			if !mb.Enabled || strings.TrimSpace(mb.PasswordRef) == "" || workspaceID == "" {
				continue
			}
			password, err := store.Get(mb.PasswordRef)
			if err != nil || password == "" {
				slog.Warn("mailbox drain: password did not resolve — skipping mailbox",
					"agent_id", agentID, "workspace_id", workspaceID, "password_ref", mb.PasswordRef, "error", err)
				continue
			}
			client, err := email.NewClient(email.Account{
				IMAPHost: mb.IMAPHost,
				IMAPPort: mb.IMAPPort,
				SMTPHost: mb.SMTPHost,
				SMTPPort: mb.SMTPPort,
				Username: mb.Username,
				Password: password,
			})
			if err != nil {
				slog.Warn("mailbox drain: transport construction failed — skipping mailbox",
					"agent_id", agentID, "workspace_id", workspaceID, "error", err)
				continue
			}
			out = append(out, email.Mailbox{
				AgentID:     agentID,
				WorkspaceID: workspaceID,
				Transport:   client,
			})
		}
	}
	return out
}

// Mailbox account REST handlers (M11). Email is modeled as a TOOL surface, not a
// conversational channel: a mailbox belongs to exactly one (agent, workspace)
// pair — config.Mailboxes[agentID][workspaceID] — and several agents (or the
// same agent in different workspaces) may each have their own mailbox (the
// 0.1.0 cap-1 rule was lifted 2026-07-03, then pair-addressing landed the same
// day so one agent can hold a distinct mailbox per workspace). The mailbox
// password is routed into the encrypted credential store and persisted only as
// a reference — never inline in config.json (the SEC-23 pattern reused from
// channel secrets).

// mailboxCredKey is the credential-store key for one (agent, workspace) pair's
// mailbox password. The format is opaque to readers; the email tools resolve
// the secret via the config password_ref, never by reconstructing this key.
// Per-pair keying (2026-07-03) is required because pair-addressing lets one
// agent hold a distinct mailbox in each workspace — a per-agent-only key would
// let a second workspace's mailbox clobber the first's stored password.
func mailboxCredKey(agentID, workspaceID string) string {
	return "mailbox_" + agentID + "_" + workspaceID + "_password"
}

// legacyMailboxCredKey is the pre-pair-addressing (0.1.0) credential-store key
// for an agent's single mailbox password. New writes never use it, but delete
// still attempts to clean it up so installs migrated from the flat
// one-mailbox-per-agent shape don't orphan the blob (a missing entry is not an
// error).
func legacyMailboxCredKey(agentID string) string {
	return "mailbox_" + agentID + "_password"
}

// mailboxAgentEntryToNested normalizes raw (m["mailboxes"][agentID], decoded
// from config.json into map[string]any) into the nested workspace-keyed shape.
// Raw-map read-modify-write cycles (setAgentMailbox, deleteAgentMailbox) bypass
// config.MailboxesConfig.UnmarshalJSON, which performs this same legacy-flat →
// nested migration on read — this keeps direct config.json writers consistent
// with it so a still-on-disk legacy entry is correctly folded into (or removed
// from) the nested shape instead of silently surviving untouched.
//
// A legacy entry is detected by probing its values: the nested shape's values
// are always mailbox objects, while a legacy flat entry's values are the
// mailbox's own scalar fields (e.g. "enabled": true, "workspace_id": "ws1").
// Returns a new, non-nil, mutable map; absent/malformed/empty input yields an
// empty map.
func mailboxAgentEntryToNested(raw any) map[string]any {
	asMap, ok := raw.(map[string]any)
	if !ok || len(asMap) == 0 {
		return map[string]any{}
	}
	legacy := false
	for _, v := range asMap {
		if _, isObj := v.(map[string]any); !isObj {
			legacy = true
			break
		}
	}
	if !legacy {
		out := make(map[string]any, len(asMap))
		for k, v := range asMap {
			out[k] = v
		}
		return out
	}
	out := map[string]any{}
	if wsID, _ := asMap["workspace_id"].(string); wsID != "" {
		out[wsID] = asMap
	} else {
		slog.Warn("config: dropping legacy mailbox without workspace_id during merge (unreachable)")
	}
	return out
}

// agentExists reports whether an agent with the given ID is registered.
func (a *restAPI) agentExists(agentID string) bool {
	if reg := a.agentLoop.GetRegistry(); reg != nil {
		if _, ok := reg.GetAgent(agentID); ok {
			return true
		}
	}
	// Fall back to the config list (covers worker/disabled agents not in the live
	// registry but still legitimately configurable).
	cfg := a.agentLoop.GetConfig()
	for _, ac := range cfg.Agents.List {
		if ac.ID == agentID {
			return true
		}
	}
	return false
}

// getAgentMailbox handles GET /api/v1/agents/{id}/mailboxes/{workspaceId}.
func (a *restAPI) getAgentMailbox(w http.ResponseWriter, agentID, workspaceID string) {
	if !a.agentExists(agentID) {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", agentID))
		return
	}
	cfg := a.agentLoop.GetConfig()
	byWorkspace, ok := cfg.Mailboxes[agentID]
	if !ok {
		jsonErr(w, http.StatusNotFound,
			fmt.Sprintf("no mailbox configured for agent %q in workspace %q", agentID, workspaceID))
		return
	}
	mb, ok := byWorkspace[workspaceID]
	if !ok {
		jsonErr(w, http.StatusNotFound,
			fmt.Sprintf("no mailbox configured for agent %q in workspace %q", agentID, workspaceID))
		return
	}

	configured := false
	if strings.TrimSpace(mb.PasswordRef) != "" {
		ok, err := a.credentialRefResolves(mb.PasswordRef)
		if err != nil {
			slog.Error("rest: mailbox credential check", "agent_id", agentID, "workspace_id", workspaceID, "error", err)
			jsonErr(w, http.StatusInternalServerError,
				"credential store unavailable — unlock it (set OMNIPUS_MASTER_KEY) and retry")
			return
		}
		configured = ok
	}

	jsonOK(w, mailboxToWire(agentID, mb, configured))
}

// setAgentMailbox handles PUT /api/v1/agents/{id}/mailboxes/{workspaceId}.
// The workspace is addressed entirely by the path — the request body no
// longer carries a workspace_id field (path is authoritative, 2026-07-03).
func (a *restAPI) setAgentMailbox(w http.ResponseWriter, r *http.Request, agentID, workspaceID string) {
	if !a.agentExists(agentID) {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", agentID))
		return
	}

	var req gen.MailboxConfigureRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "MailboxConfigureRequest", &req, validateEnabled) {
		return
	}
	if strings.TrimSpace(req.ImapHost) == "" {
		jsonErr(w, http.StatusBadRequest, "imap_host is required")
		return
	}
	if strings.TrimSpace(req.SmtpHost) == "" {
		jsonErr(w, http.StatusBadRequest, "smtp_host is required")
		return
	}
	if strings.TrimSpace(req.Username) == "" {
		jsonErr(w, http.StatusBadRequest, "username is required")
		return
	}

	// NOTE: the 0.1.0 cap-1-per-workspace rule was removed 2026-07-03
	// (operator-approved): every agent may own a mailbox, several per workspace.
	// Pair-addressing (same day) further lifted "one mailbox per agent" — an
	// agent may hold a distinct mailbox in each workspace it belongs to.

	refName := mailboxCredKey(agentID, workspaceID)
	passwordProvided := req.Password != nil
	clearPassword := passwordProvided && strings.TrimSpace(*req.Password) == ""

	// Route the password into the credential store BEFORE the config write so the
	// stored mailbox can authenticate the moment its ref is persisted.
	if passwordProvided && !clearPassword {
		if _, err := a.storeCredential(refName, *req.Password); err != nil {
			slog.Error("rest: store mailbox credential", "agent_id", agentID, "workspace_id", workspaceID, "error", err)
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not store mailbox password: %v", err))
			return
		}
	}

	var persistedRef string
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		mailboxes, _ := m["mailboxes"].(map[string]any)
		if mailboxes == nil {
			mailboxes = map[string]any{}
			m["mailboxes"] = mailboxes
		}

		// Normalize the agent's entry into the nested workspace-keyed shape,
		// folding in (or discarding, if unaddressable) any still-on-disk
		// legacy flat entry from a pre-pair-addressing install.
		byWorkspace := mailboxAgentEntryToNested(mailboxes[agentID])

		existing, _ := byWorkspace[workspaceID].(map[string]any)
		if existing == nil {
			existing = map[string]any{}
		}

		existing["enabled"] = req.Enabled
		existing["workspace_id"] = workspaceID
		existing["imap_host"] = req.ImapHost
		existing["smtp_host"] = req.SmtpHost
		existing["username"] = req.Username
		if req.ImapPort != nil {
			existing["imap_port"] = *req.ImapPort
		}
		if req.SmtpPort != nil {
			existing["smtp_port"] = *req.SmtpPort
		}

		// Password handling: stored → set ref; cleared → drop ref; omitted → keep.
		switch {
		case clearPassword:
			existing["password_ref"] = ""
		case passwordProvided:
			existing["password_ref"] = refName
		}
		if pr, _ := existing["password_ref"].(string); pr != "" {
			persistedRef = pr
		}
		// Never persist a plaintext password key.
		delete(existing, "password")

		byWorkspace[workspaceID] = existing
		mailboxes[agentID] = byWorkspace

		// Tool-policy grant: enabling a mailbox is the operator's explicit
		// opt-in to the email tools for this agent (the wire contract's
		// `enabled` literally means "register the email tools"). Agents with a
		// deny-by-default builtin allowlist that predates the mailbox (only the
		// Assistant seed carries the five email allows) would otherwise get the
		// tools registered but policy-hidden — a silently dead mailbox. Fill in
		// ONLY missing entries: an explicit operator-set allow/ask/deny is
		// intent and stays untouched.
		if req.Enabled {
			grantEmailToolAllows(m, agentID)
		}
		return nil
	}); err != nil {
		slog.Error("rest: configure mailbox", "agent_id", agentID, "workspace_id", workspaceID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}

	// Re-wire the agent's tools so the email tools are (de)registered to match the
	// new mailbox state. Reload is cheap and idempotent; ErrReloadNotConfigured is
	// the normal no-op path in unit tests without the full reload pipeline.
	if err := a.agentLoop.TriggerReload(); err != nil {
		if !errors.Is(err, agent.ErrReloadNotConfigured) {
			slog.Error("rest: mailbox configure reload failed", "agent_id", agentID, "workspace_id", workspaceID, "error", err)
			jsonErr(w, http.StatusServiceUnavailable,
				"config saved but in-memory reload failed; restart the gateway or retry")
			return
		}
	}

	configured := strings.TrimSpace(persistedRef) != ""
	if configured {
		ok, err := a.credentialRefResolves(persistedRef)
		if err == nil {
			configured = ok
		}
	}

	out := config.MailboxConfig{
		Enabled:     req.Enabled,
		WorkspaceID: workspaceID,
		IMAPHost:    req.ImapHost,
		SMTPHost:    req.SmtpHost,
		Username:    req.Username,
		PasswordRef: persistedRef,
	}
	if req.ImapPort != nil {
		out.IMAPPort = *req.ImapPort
	}
	if req.SmtpPort != nil {
		out.SMTPPort = *req.SmtpPort
	}
	jsonOK(w, mailboxToWire(agentID, out, configured))
}

// deleteAgentMailbox handles DELETE /api/v1/agents/{id}/mailboxes/{workspaceId}.
// Only the addressed (agent, workspace) pair is removed — mailboxes the agent
// holds in OTHER workspaces are untouched.
func (a *restAPI) deleteAgentMailbox(w http.ResponseWriter, agentID, workspaceID string) {
	if !a.agentExists(agentID) {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", agentID))
		return
	}
	cfg := a.agentLoop.GetConfig()
	byWorkspace, ok := cfg.Mailboxes[agentID]
	if !ok {
		jsonErr(w, http.StatusNotFound,
			fmt.Sprintf("no mailbox configured for agent %q in workspace %q", agentID, workspaceID))
		return
	}
	if _, ok := byWorkspace[workspaceID]; !ok {
		jsonErr(w, http.StatusNotFound,
			fmt.Sprintf("no mailbox configured for agent %q in workspace %q", agentID, workspaceID))
		return
	}

	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		mailboxes, _ := m["mailboxes"].(map[string]any)
		if mailboxes == nil {
			return nil
		}
		normalized := mailboxAgentEntryToNested(mailboxes[agentID])
		delete(normalized, workspaceID)
		if len(normalized) == 0 {
			delete(mailboxes, agentID)
		} else {
			mailboxes[agentID] = normalized
		}
		return nil
	}); err != nil {
		slog.Error("rest: delete mailbox", "agent_id", agentID, "workspace_id", workspaceID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}

	// Config is durable now — delete the stored password (a missing entry is not
	// an error). Log on failure: the ref is already gone, only an orphaned blob
	// could remain. Delete BOTH the per-pair key (the only one new writes ever
	// use) and the legacy per-agent key, so installs migrated from the
	// pre-pair-addressing flat shape don't orphan their blob either.
	if err := a.removeStoredCredential(mailboxCredKey(agentID, workspaceID)); err != nil {
		slog.Error("rest: delete mailbox credential", "agent_id", agentID, "workspace_id", workspaceID, "error", err)
	}
	if err := a.removeStoredCredential(legacyMailboxCredKey(agentID)); err != nil {
		slog.Error("rest: delete legacy mailbox credential", "agent_id", agentID, "error", err)
	}

	if err := a.agentLoop.TriggerReload(); err != nil {
		if !errors.Is(err, agent.ErrReloadNotConfigured) {
			slog.Error("rest: mailbox delete reload failed", "agent_id", agentID, "workspace_id", workspaceID, "error", err)
		}
	}

	jsonOK(w, gen.OperationResult{Success: true})
}

// listMailboxes handles GET /api/v1/mailboxes (M11). Returns every configured
// mailbox — one entry per (agent, workspace) pair; an agent may appear more
// than once if it holds a mailbox in several workspaces. An empty list means
// "no mailbox configured" — unlike the per-pair GET, this endpoint never
// 404s, so the SPA can render mailbox status without probing each agent's
// mailbox endpoint (each probe 404 lands in the browser console and trips the
// e2e zero-console-errors gate).
func (a *restAPI) listMailboxes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	cfg := a.agentLoop.GetConfig()

	// Deterministic order: Go map iteration is randomized. Sort the outer
	// (agent) keys, then the inner (workspace) keys per agent.
	agentIDs := make([]string, 0, len(cfg.Mailboxes))
	for agentID := range cfg.Mailboxes {
		agentIDs = append(agentIDs, agentID)
	}
	sort.Strings(agentIDs)

	items := make([]gen.Mailbox, 0, len(cfg.Mailboxes))
	for _, agentID := range agentIDs {
		byWorkspace := cfg.Mailboxes[agentID]
		workspaceIDs := make([]string, 0, len(byWorkspace))
		for wsID := range byWorkspace {
			workspaceIDs = append(workspaceIDs, wsID)
		}
		sort.Strings(workspaceIDs)

		for _, wsID := range workspaceIDs {
			mb := byWorkspace[wsID]
			configured := false
			if strings.TrimSpace(mb.PasswordRef) != "" {
				ok, err := a.credentialRefResolves(mb.PasswordRef)
				if err != nil {
					slog.Error("rest: mailbox credential check", "agent_id", agentID, "workspace_id", wsID, "error", err)
					jsonErr(w, http.StatusInternalServerError,
						"credential store unavailable — unlock it (set OMNIPUS_MASTER_KEY) and retry")
					return
				}
				configured = ok
			}
			items = append(items, mailboxToWire(agentID, mb, configured))
		}
	}

	// Round-trip []gen.Mailbox into the generated list type (whose element is an
	// anonymous inline struct, structurally identical to gen.Mailbox) — same
	// idiom as handleListSchedules; no hand-written wire struct (constraint #8).
	buf, err := json.Marshal(map[string][]gen.Mailbox{"mailboxes": items})
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	var out gen.MailboxListResponse
	if err := json.Unmarshal(buf, &out); err != nil {
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	jsonOK(w, out)
}

// emailToolNames are the five M11 email tools gated by mailbox ownership.
var emailToolNames = []string{"read_inbox", "search_email", "read_message", "send_email", "reply"}

// grantEmailToolAllows fills in missing "allow" entries for the email tools in
// the agent's builtin tool policy, but ONLY when that policy is
// deny-by-default (under default-allow a missing entry already permits the
// tool) and ONLY for names with no existing entry (an explicit operator
// allow/ask/deny is intent and is never overridden). Operates on the raw
// config map inside a safeUpdateConfigJSON write.
func grantEmailToolAllows(m map[string]any, agentID string) {
	agents, _ := m["agents"].(map[string]any)
	list, _ := agents["list"].([]any)
	for _, entry := range list {
		ag, _ := entry.(map[string]any)
		if ag == nil || ag["id"] != agentID {
			continue
		}
		toolsCfg, _ := ag["tools"].(map[string]any)
		if toolsCfg == nil {
			return // no per-agent tool config → defaults apply (allow)
		}
		builtin, _ := toolsCfg["builtin"].(map[string]any)
		if builtin == nil {
			return
		}
		if dp, _ := builtin["default_policy"].(string); dp != "deny" {
			return // default-allow: missing entries already permit the tools
		}
		policies, _ := builtin["policies"].(map[string]any)
		if policies == nil {
			policies = map[string]any{}
			builtin["policies"] = policies
		}
		granted := make([]string, 0, len(emailToolNames))
		for _, name := range emailToolNames {
			if _, exists := policies[name]; !exists {
				policies[name] = "allow"
				granted = append(granted, name)
			}
		}
		if len(granted) > 0 {
			slog.Info("mailbox: granted email tool allows (deny-default agent, mailbox enabled)",
				"agent_id", agentID, "tools", strings.Join(granted, ","))
		}
		return
	}
}

// mailboxToWire converts a stored MailboxConfig to the Mailbox wire type. The
// password is never included — only the resolved `configured` flag.
func mailboxToWire(agentID string, mb config.MailboxConfig, configured bool) gen.Mailbox {
	out := gen.Mailbox{
		AgentId:    agentID,
		Enabled:    mb.Enabled,
		Configured: configured,
	}
	if mb.WorkspaceID != "" {
		ws := mb.WorkspaceID
		out.WorkspaceId = &ws
	}
	if mb.IMAPHost != "" {
		v := mb.IMAPHost
		out.ImapHost = &v
	}
	if mb.IMAPPort != 0 {
		v := mb.IMAPPort
		out.ImapPort = &v
	}
	if mb.SMTPHost != "" {
		v := mb.SMTPHost
		out.SmtpHost = &v
	}
	if mb.SMTPPort != 0 {
		v := mb.SMTPPort
		out.SmtpPort = &v
	}
	if mb.Username != "" {
		v := mb.Username
		out.Username = &v
	}
	return out
}
