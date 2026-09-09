package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/elicify-ai/omnipus/pkg/entity"
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

// errMailboxEntryMalformed is returned by mailboxAgentEntryToNested when an
// agent's raw mailboxes entry mixes legacy-flat and nested-shape values.
// Callers add the offending agent ID when surfacing this to the caller (HTTP
// 500) — see setAgentMailbox / deleteAgentMailbox / removeMailboxesForWorkspace.
var errMailboxEntryMalformed = errors.New("mailboxes entry is malformed (mixed legacy/nested shape)")

// mailboxAgentEntryToNested normalizes raw (m["mailboxes"][agentID], decoded
// from config.json into map[string]any) into the nested workspace-keyed shape.
// Raw-map read-modify-write cycles (setAgentMailbox, deleteAgentMailbox) bypass
// config.MailboxesConfig.UnmarshalJSON, which performs this same legacy-flat →
// nested migration on read — this keeps direct config.json writers consistent
// with it so a still-on-disk legacy entry is correctly folded into (or removed
// from) the nested shape instead of silently surviving untouched.
//
// Shape detection is STRICT, mirroring config.MailboxesConfig.UnmarshalJSON:
// an entry is legacy-flat iff ALL its inner values are non-objects (the
// mailbox's own scalar fields, e.g. "enabled": true, "workspace_id": "ws1"),
// nested iff ALL its inner values are mailbox objects. A MIX of both within
// one entry is malformed and returns errMailboxEntryMalformed rather than
// being silently misclassified — the previous any-one-non-object heuristic
// could decode object-valued nested keys against MailboxConfig's scalar
// fields, produce an empty WorkspaceID, and silently drop the entire roster.
//
// On success, returns a new, non-nil, mutable map; absent/empty input yields
// an empty map with a nil error.
func mailboxAgentEntryToNested(raw any) (map[string]any, error) {
	asMap, ok := raw.(map[string]any)
	if !ok || len(asMap) == 0 {
		return map[string]any{}, nil
	}
	allObjects := true
	allScalars := true
	for _, v := range asMap {
		if _, isObj := v.(map[string]any); isObj {
			allScalars = false
		} else {
			allObjects = false
		}
	}
	switch {
	case allObjects:
		out := make(map[string]any, len(asMap))
		for k, v := range asMap {
			out[k] = v
		}
		return out, nil
	case allScalars:
		out := map[string]any{}
		if wsID, _ := asMap["workspace_id"].(string); wsID != "" {
			out[wsID] = asMap
		} else {
			slog.Warn("config: dropping legacy mailbox without workspace_id during merge (unreachable)")
		}
		return out, nil
	default:
		return nil, errMailboxEntryMalformed
	}
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

	jsonOK(w, mailboxToWire(agentID, workspaceID, mb, configured))
}

// setAgentMailbox handles PUT /api/v1/agents/{id}/mailboxes/{workspaceId}.
// The workspace is addressed entirely by the path — the request body no
// longer carries a workspace_id field (path is authoritative, 2026-07-03).
func (a *restAPI) setAgentMailbox(w http.ResponseWriter, r *http.Request, agentID, workspaceID string) {
	if !a.agentExists(agentID) {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", agentID))
		return
	}

	// Verify the workspace exists before saving — a mailbox addressed by a
	// nonexistent workspace ID would be permanently unreachable (no
	// workspace-scoped surface would ever list it) and would silently drift
	// from the workspace-delete cascade (removeMailboxesForWorkspace), which
	// only cleans up pairs whose workspace it can see got deleted.
	ws, ok := a.loadWorkspace(w, workspaceID)
	if !ok {
		return
	}

	// ADR-033 (operator-decided 2026-07-03): the owning agent MUST be a
	// core_team member of the target workspace, aligning mailbox ownership
	// with ADR-029's channel-binding rule (FR-006) — the mailbox surfaces its
	// unhandled mail as Board tasks in this workspace, so its owner must be a
	// member there. Workers are likewise excluded (FR-008 parity): the panel
	// never offers them, and a delegation-only worker has no inbox-working
	// heartbeat persona. Both reject with 422, same messages as the channel
	// routing gate.
	cfg := a.agentLoop.GetConfig()
	var owningAgent *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == agentID {
			ac := cfg.Agents.List[i]
			owningAgent = &ac
			break
		}
	}
	if owningAgent != nil && owningAgent.IsWorker() {
		jsonErr(w, http.StatusUnprocessableEntity,
			"workers cannot own a mailbox")
		return
	}
	inTeam := false
	for _, memberID := range ws.CoreTeam {
		if memberID == agentID {
			inTeam = true
			break
		}
	}
	if !inTeam {
		jsonErr(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("agent %q is not a member of workspace %q", agentID, workspaceID))
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
		byWorkspace, err := mailboxAgentEntryToNested(mailboxes[agentID])
		if err != nil {
			return err
		}

		existing, _ := byWorkspace[workspaceID].(map[string]any)
		if existing == nil {
			existing = map[string]any{}
		}

		// Capture the mailbox's enabled state as it stood BEFORE this save —
		// a fresh/absent entry (first-time configure) type-asserts to the
		// false zero value, matching "not previously enabled". This is the
		// disabled→enabled transition signal grantEmailToolAllows's call
		// below needs; it must be read before existing["enabled"] is
		// overwritten with the request's new value a few lines down.
		wasEnabled, _ := existing["enabled"].(bool)

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
		// deny-by-default builtin allowlist that predates the mailbox (every
		// agent except the Assistant seed) would otherwise get the tools
		// registered but policy-hidden — a silently dead mailbox. Fill in any
		// email tool that is missing or explicitly "deny" (the ubiquitous,
		// non-deliberate baseline every such agent inherits from the seed
		// under the mandatory-coverage model — CLAUDE.md hard constraint 6,
		// there is no default_policy field to distinguish "unset" from "seed
		// default" any more); an entry already "allow" or "ask" is left
		// untouched (see grantEmailToolAllows's doc comment for why "ask" is
		// the one value that can only reflect genuine operator intent).
		//
		// Gated on the ACTUAL disabled→enabled transition (req.Enabled &&
		// !wasEnabled), not merely "the saved value is enabled": this handler
		// runs on EVERY mailbox save, including edits to an already-enabled
		// mailbox (rotate a password, change the IMAP host, …). Before this
		// fix, any such edit re-ran the grant unconditionally whenever
		// req.Enabled was true — and since a seed "deny" and an operator's
		// later, deliberate "deny" (set via the Tool Policies UI to lock an
		// email tool back down after first enabling the mailbox) are the same
		// literal string in the data model, that meant an unrelated edit to
		// an already-enabled mailbox would silently RE-GRANT "allow" to a
		// tool the operator had just locked down — a privilege-widening
		// regression, the mirror image of the dead-mailbox bug this grant
		// exists to fix (found live, silent-failure-hunter, 2026-07-06).
		if req.Enabled && !wasEnabled {
			grantEmailToolAllows(a.homePath, agentID)
		}
		return nil
	}); err != nil {
		if errors.Is(err, errMailboxEntryMalformed) {
			msg := fmt.Sprintf("mailboxes entry for agent %q is malformed (mixed legacy/nested shape)", agentID)
			slog.Error(
				"rest: configure mailbox: malformed entry",
				"agent_id",
				agentID,
				"workspace_id",
				workspaceID,
				"error",
				err,
			)
			jsonErr(w, http.StatusInternalServerError, msg)
			return
		}
		slog.Error("rest: configure mailbox", "agent_id", agentID, "workspace_id", workspaceID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}

	// Re-wire the agent's tools so the email tools are (de)registered to match
	// the new mailbox state. triggerReloadAndWait (not a bare TriggerReload) —
	// mirrors createAgent/updateAgent/updateAgentTools: registerEmailToolsForAgent
	// (pkg/agent/email_tools.go) only runs during NewAgentRegistry's per-instance
	// construction, itself only reached via the async
	// TriggerReload → executeReload → ReloadProviderAndConfig pipeline — a bare
	// TriggerReload enqueues that work and returns immediately, so a client that
	// enables a mailbox and immediately asks the agent to send an email could
	// hit a "tool not registered" gap for as long as that goroutine takes to
	// run. triggerReloadAndWait absorbs ErrReloadNotConfigured (the normal
	// no-op path in unit tests without the full reload pipeline wired)
	// internally, so a non-nil error here is always a genuine reload failure.
	if confirmed, err := a.triggerReloadAndWaitOutcome(); err != nil {
		slog.Error(
			"rest: mailbox configure reload failed",
			"agent_id",
			agentID,
			"workspace_id",
			workspaceID,
			"error",
			err,
		)
		jsonErr(w, http.StatusServiceUnavailable,
			"config saved but in-memory reload failed; restart the gateway or retry")
		return
	} else if !confirmed {
		slog.Warn(
			"rest: mailbox configure reload did not confirm within the poll window; "+
				"email tools may not yet reflect the new mailbox state",
			"agent_id", agentID,
			"workspace_id", workspaceID,
		)
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
	jsonOK(w, mailboxToWire(agentID, workspaceID, out, configured))
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
		normalized, err := mailboxAgentEntryToNested(mailboxes[agentID])
		if err != nil {
			return err
		}
		delete(normalized, workspaceID)
		if len(normalized) == 0 {
			delete(mailboxes, agentID)
		} else {
			mailboxes[agentID] = normalized
		}
		return nil
	}); err != nil {
		if errors.Is(err, errMailboxEntryMalformed) {
			msg := fmt.Sprintf("mailboxes entry for agent %q is malformed (mixed legacy/nested shape)", agentID)
			slog.Error(
				"rest: delete mailbox: malformed entry",
				"agent_id",
				agentID,
				"workspace_id",
				workspaceID,
				"error",
				err,
			)
			jsonErr(w, http.StatusInternalServerError, msg)
			return
		}
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

	// triggerReloadAndWait (not a bare TriggerReload) — see setAgentMailbox's
	// doc comment: registerEmailToolsForAgent only deregisters the removed
	// mailbox's email tools during the async registry rebuild, so waiting here
	// closes the same race in the other direction (a deleted mailbox's email
	// tools staying live on the running instance). Best-effort: deletion has
	// already durably persisted, so a genuine reload failure is logged, not
	// surfaced as an error response (mirrors deleteAgent).
	if confirmed, err := a.triggerReloadAndWaitOutcome(); err != nil {
		slog.Error(
			"rest: mailbox delete reload failed",
			"agent_id",
			agentID,
			"workspace_id",
			workspaceID,
			"error",
			err,
		)
	} else if !confirmed {
		slog.Warn(
			"rest: mailbox delete reload did not confirm within the poll window; "+
				"deleted mailbox's email tools may still be live on the running instance",
			"agent_id", agentID,
			"workspace_id", workspaceID,
		)
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
					slog.Error(
						"rest: mailbox credential check",
						"agent_id",
						agentID,
						"workspace_id",
						wsID,
						"error",
						err,
					)
					jsonErr(w, http.StatusInternalServerError,
						"credential store unavailable — unlock it (set OMNIPUS_MASTER_KEY) and retry")
					return
				}
				configured = ok
			}
			items = append(items, mailboxToWire(agentID, wsID, mb, configured))
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

// grantEmailToolAllows fills in "allow" for the email tools in the agent's
// builtin tool policy when a mailbox is enabled, treating a missing entry or
// an explicit "deny" as fill-eligible, but never touching a tool already
// "allow" or "ask". ADR-054 D2/§11 checklist item 5: agents are per-entity
// records under entities/agents/<id>.json, not config.json's agents.list —
// this grants via the agent store instead of splicing the raw config map
// that setAgentMailbox's safeUpdateConfigJSON closure operates on (that
// closure's `m` is the MAILBOX config, a setting per ADR-054 R2 — this
// function no longer touches it at all).
//
// Historical context: before the DefaultPolicy/default_policy fallback was
// removed (CLAUDE.md hard constraint 6), an agent's builtin.policies map was
// typically SPARSE — a missing entry meant "fall back to the agent's
// default_policy field," so a deny-by-default agent's email tools were
// implicitly denied simply by being absent from the map. This function used
// to check that default_policy field and only fill genuinely-missing
// entries. Now every agent's policies map is fully enumerated (seeded
// explicitly allow/deny per tool via coreagent.denyAllThenOverride; the
// two-layer model under ADR-077 lets an unmentioned tool ride the reconciled
// global ceiling instead of a per-agent deny backfill) —
// there is no default_policy field any more, so a deny-by-default agent's
// email tools are no longer "missing," they carry an EXPLICIT "deny" entry
// inherited from the seed. Checking builtin["default_policy"] (as this
// function used to) is therefore always empty/absent, always compares
// unequal to "deny", and the function ALWAYS silently no-op'd — meaning
// enabling a mailbox for any agent whose email tools are seed-deny (every
// agent except Mia) left those tools permanently denied with zero
// operator-visible signal (found live, three independent reviewers,
// 2026-07-06).
//
// Fixed to operate directly on the real builtin["policies"] map: an entry
// currently "deny" (the ubiquitous, non-deliberate baseline inherited from
// the seed) is functionally equivalent to the old "missing" case and is
// fill-eligible. An entry already "allow" needs no change. An entry "ask" is
// preserved untouched: no core-agent seed (pkg/coreagent/core.go) ever
// assigns "ask" to an email tool — only "allow" (Mia) or the
// denyAllThenOverride baseline "deny" — so a persisted "ask" can only have
// come from a deliberate operator action via the Tool Policies UI/API, which
// this function must never override.
func grantEmailToolAllows(homePath, agentID string) {
	var granted []string
	_, err := agentstore.New(homePath).Update(agentID, func(ag *config.AgentConfig) error {
		if ag.Tools == nil {
			ag.Tools = &config.AgentToolsCfg{}
		}
		if ag.Tools.Builtin.Policies == nil {
			ag.Tools.Builtin.Policies = map[string]config.ToolPolicy{}
		}
		for _, name := range emailToolNames {
			current := ag.Tools.Builtin.Policies[name]
			if current == config.ToolPolicyAsk || current == config.ToolPolicyAllow {
				continue // already permits, or a deliberate operator choice — leave it
			}
			ag.Tools.Builtin.Policies[name] = config.ToolPolicyAllow
			granted = append(granted, name)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			// The agentExists gate (called earlier in setAgentMailbox) already
			// proved this agent is registered — reaching here means the agent
			// store diverged from that check (e.g. concurrent delete). The
			// mailbox will be saved Active but the email tools stay
			// policy-hidden with no operator-visible signal, so this is an
			// error, not a benign no-op.
			slog.Error(
				"mailbox: agent not found in agent store — cannot grant email tool allows (mailbox will save Active but tools stay policy-hidden)",
				"agent_id", agentID,
			)
			return
		}
		slog.Error("mailbox: could not grant email tool allows", "agent_id", agentID, "error", err)
		return
	}
	if len(granted) > 0 {
		slog.Info("mailbox: granted email tool allows (deny/missing email-tool policy, mailbox enabled)",
			"agent_id", agentID, "tools", strings.Join(granted, ","))
	}
}

// mailboxToWire converts a stored MailboxConfig to the Mailbox wire type. The
// password is never included — only the resolved `configured` flag.
//
// agentID and workspaceID are the AUTHORITATIVE (agent, workspace) pair key —
// the caller's map keys, not fields read off mb. mb.WorkspaceID is a mutable
// mirror written back into config.json for human readability and is NOT
// guaranteed to match the key it is nested under (e.g. a hand-edited
// config.json, or a caller that forgot to keep the mirror in sync); trusting
// it here would let the wire response report a mailbox under the wrong
// workspace. gen.Mailbox.WorkspaceId is a required (non-pointer) string, so
// this also always serializes a value — never the omitted/null shape.
func mailboxToWire(agentID, workspaceID string, mb config.MailboxConfig, configured bool) gen.Mailbox {
	out := gen.Mailbox{
		AgentId:     agentID,
		WorkspaceId: workspaceID,
		Enabled:     mb.Enabled,
		Configured:  configured,
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
