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
func buildMailboxes(cfg *config.Config, store *credentials.Store) []email.Mailbox {
	if cfg == nil || len(cfg.Mailboxes) == 0 || store == nil {
		return nil
	}
	out := make([]email.Mailbox, 0, len(cfg.Mailboxes))
	for agentID, mb := range cfg.Mailboxes {
		if !mb.Enabled || strings.TrimSpace(mb.PasswordRef) == "" || mb.WorkspaceID == "" {
			continue
		}
		password, err := store.Get(mb.PasswordRef)
		if err != nil || password == "" {
			slog.Warn("mailbox drain: password did not resolve — skipping mailbox",
				"agent_id", agentID, "password_ref", mb.PasswordRef, "error", err)
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
				"agent_id", agentID, "error", err)
			continue
		}
		out = append(out, email.Mailbox{
			AgentID:     agentID,
			WorkspaceID: mb.WorkspaceID,
			Transport:   client,
		})
	}
	return out
}

// Mailbox account REST handlers (M11). Email is modeled as a TOOL surface, not a
// conversational channel: a mailbox is owned by exactly one agent (the map key in
// config.Mailboxes) and surfaces in exactly one workspace (per-(agent, workspace)
// cap-1). The mailbox password is routed into the encrypted credential store and
// persisted only as a reference — never inline in config.json (the SEC-23 pattern
// reused from channel secrets).

// mailboxCredKey is the credential-store key for an agent's mailbox password.
// The format is opaque to readers; the email tools resolve the secret via the
// config password_ref, never by reconstructing this key.
func mailboxCredKey(agentID string) string {
	return "mailbox_" + agentID + "_password"
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

// getAgentMailbox handles GET /api/v1/agents/{id}/mailbox.
func (a *restAPI) getAgentMailbox(w http.ResponseWriter, agentID string) {
	if !a.agentExists(agentID) {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", agentID))
		return
	}
	cfg := a.agentLoop.GetConfig()
	mb, ok := cfg.Mailboxes[agentID]
	if !ok {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("no mailbox configured for agent %q", agentID))
		return
	}

	configured := false
	if strings.TrimSpace(mb.PasswordRef) != "" {
		ok, err := a.credentialRefResolves(mb.PasswordRef)
		if err != nil {
			slog.Error("rest: mailbox credential check", "agent_id", agentID, "error", err)
			jsonErr(w, http.StatusInternalServerError,
				"credential store unavailable — unlock it (set OMNIPUS_MASTER_KEY) and retry")
			return
		}
		configured = ok
	}

	jsonOK(w, mailboxToWire(agentID, mb, configured))
}

// setAgentMailbox handles PUT /api/v1/agents/{id}/mailbox.
func (a *restAPI) setAgentMailbox(w http.ResponseWriter, r *http.Request, agentID string) {
	if !a.agentExists(agentID) {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", agentID))
		return
	}

	var req gen.MailboxConfigureRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "MailboxConfigureRequest", &req, validateEnabled) {
		return
	}
	if strings.TrimSpace(req.WorkspaceId) == "" {
		jsonErr(w, http.StatusBadRequest, "workspace_id is required")
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

	// Cap-1 (per-(agent, workspace)): reject if another enabled agent already owns
	// a mailbox in this workspace. Pre-check against the live config; the on-disk
	// write below re-validates the merged map so a concurrent write cannot slip
	// past this gate.
	if req.Enabled {
		cfg := a.agentLoop.GetConfig()
		for otherID, other := range cfg.Mailboxes {
			if otherID == agentID {
				continue
			}
			if other.Enabled && other.WorkspaceID == req.WorkspaceId {
				jsonErr(w, http.StatusUnprocessableEntity,
					fmt.Sprintf("cap-1: workspace %q already has a mailbox (agent %q)", req.WorkspaceId, otherID))
				return
			}
		}
	}

	refName := mailboxCredKey(agentID)
	passwordProvided := req.Password != nil
	clearPassword := passwordProvided && strings.TrimSpace(*req.Password) == ""

	// Route the password into the credential store BEFORE the config write so the
	// stored mailbox can authenticate the moment its ref is persisted.
	if passwordProvided && !clearPassword {
		if _, err := a.storeCredential(refName, *req.Password); err != nil {
			slog.Error("rest: store mailbox credential", "agent_id", agentID, "error", err)
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
		existing, _ := mailboxes[agentID].(map[string]any)
		if existing == nil {
			existing = map[string]any{}
		}

		existing["enabled"] = req.Enabled
		existing["workspace_id"] = req.WorkspaceId
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

		mailboxes[agentID] = existing

		// Re-validate cap-1 over the merged on-disk map (concurrent-write guard).
		if err := validateMailboxesMapCap1(mailboxes); err != nil {
			return err
		}
		return nil
	}); err != nil {
		if errors.Is(err, config.ErrMailboxesCap1Violated) {
			slog.Warn("rest: configure mailbox rejected by cap-1", "agent_id", agentID, "error", err)
			jsonErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		slog.Error("rest: configure mailbox", "agent_id", agentID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}

	// Re-wire the agent's tools so the email tools are (de)registered to match the
	// new mailbox state. Reload is cheap and idempotent; ErrReloadNotConfigured is
	// the normal no-op path in unit tests without the full reload pipeline.
	if err := a.agentLoop.TriggerReload(); err != nil {
		if !errors.Is(err, agent.ErrReloadNotConfigured) {
			slog.Error("rest: mailbox configure reload failed", "agent_id", agentID, "error", err)
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
		WorkspaceID: req.WorkspaceId,
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

// deleteAgentMailbox handles DELETE /api/v1/agents/{id}/mailbox.
func (a *restAPI) deleteAgentMailbox(w http.ResponseWriter, agentID string) {
	if !a.agentExists(agentID) {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", agentID))
		return
	}
	cfg := a.agentLoop.GetConfig()
	if _, ok := cfg.Mailboxes[agentID]; !ok {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("no mailbox configured for agent %q", agentID))
		return
	}

	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		mailboxes, _ := m["mailboxes"].(map[string]any)
		if mailboxes == nil {
			return nil
		}
		delete(mailboxes, agentID)
		return nil
	}); err != nil {
		slog.Error("rest: delete mailbox", "agent_id", agentID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}

	// Config is durable now — delete the stored password (a missing entry is not
	// an error). Log on failure: the ref is already gone, only an orphaned blob
	// could remain.
	if err := a.removeStoredCredential(mailboxCredKey(agentID)); err != nil {
		slog.Error("rest: delete mailbox credential", "agent_id", agentID, "error", err)
	}

	if err := a.agentLoop.TriggerReload(); err != nil {
		if !errors.Is(err, agent.ErrReloadNotConfigured) {
			slog.Error("rest: mailbox delete reload failed", "agent_id", agentID, "error", err)
		}
	}

	jsonOK(w, gen.OperationResult{Success: true})
}

// listMailboxes handles GET /api/v1/mailboxes (M11). Returns every configured
// mailbox (cap-1 per workspace in 0.1.0, so typically zero or one). An empty
// list means "no mailbox configured" — unlike the per-agent GET, this endpoint
// never 404s, so the SPA can render mailbox status without probing each
// agent's mailbox endpoint (each probe 404 lands in the browser console and
// trips the e2e zero-console-errors gate).
func (a *restAPI) listMailboxes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	cfg := a.agentLoop.GetConfig()

	// Deterministic order: Go map iteration is randomized.
	agentIDs := make([]string, 0, len(cfg.Mailboxes))
	for agentID := range cfg.Mailboxes {
		agentIDs = append(agentIDs, agentID)
	}
	sort.Strings(agentIDs)

	items := make([]gen.Mailbox, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		mb := cfg.Mailboxes[agentID]
		configured := false
		if strings.TrimSpace(mb.PasswordRef) != "" {
			ok, err := a.credentialRefResolves(mb.PasswordRef)
			if err != nil {
				slog.Error("rest: mailbox credential check", "agent_id", agentID, "error", err)
				jsonErr(w, http.StatusInternalServerError,
					"credential store unavailable — unlock it (set OMNIPUS_MASTER_KEY) and retry")
				return
			}
			configured = ok
		}
		items = append(items, mailboxToWire(agentID, mb, configured))
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

// validateMailboxesMapCap1 enforces cap-1 over a raw config.json mailboxes map
// (the shape inside safeUpdateConfigJSON). It mirrors config.ValidateMailboxesCap1
// but operates on map[string]any so it can run inside the config mutation closure
// before the write commits.
func validateMailboxesMapCap1(mailboxes map[string]any) error {
	seen := make(map[string]string, len(mailboxes))
	for agentID, raw := range mailboxes {
		mb, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		enabled, _ := mb["enabled"].(bool)
		ws, _ := mb["workspace_id"].(string)
		if !enabled || ws == "" {
			continue
		}
		if other, dup := seen[ws]; dup {
			return fmt.Errorf("%w: workspace %q is already used by agent %q (rejecting agent %q)",
				config.ErrMailboxesCap1Violated, ws, other, agentID)
		}
		seen[ws] = agentID
	}
	return nil
}
