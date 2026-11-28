// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/programs"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// startTime records when the gateway process started, used by GET /api/v1/about.
var startTime = time.Now()

// HandleAuditLog handles GET /api/v1/audit-log.
// Returns the last 100 audit log entries in reverse-chronological order.
func (a *restAPI) HandleAuditLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// Read from system/ directory to match the audit logger's documented path
	// (~/.omnipus/system/audit.jsonl per audit package).
	auditPath := filepath.Join(a.homePath, "system", "audit.jsonl")
	f, err := os.Open(auditPath)
	if errors.Is(err, os.ErrNotExist) {
		// AuditLogResponse envelope: no entries, chain not checkable.
		jsonOK(w, map[string]any{"entries": []json.RawMessage{}, "chain_status": "unknown"})
		return
	}
	if err != nil {
		slog.Error("rest: open audit log", "error", err)
		jsonServerFailure(w, http.StatusInternalServerError, "could not read audit log", err)
		return
	}
	defer f.Close()

	var entries []json.RawMessage
	scanner := bufio.NewScanner(f)
	// Limit scanner buffer to 1MiB per line and 64KiB initial buffer to prevent
	// unbounded memory usage on large or malformed audit log files.
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		entries = append(entries, json.RawMessage(line))
	}
	if err := scanner.Err(); err != nil {
		slog.Error("rest: scan audit log", "error", err)
		jsonServerFailure(w, http.StatusInternalServerError, "could not read audit log", err)
		return
	}

	// Reverse for reverse-chronological order.
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	if len(entries) > 100 {
		entries = entries[:100]
	}
	if entries == nil {
		entries = []json.RawMessage{}
	}

	// G4: verify the HMAC tamper-evident chain (#155) so the UI can surface
	// integrity. chain_status is "unknown" when there is no logger/chain key to
	// verify with (e.g. audit logging disabled).
	chainStatus := "unknown"
	var chainBrokenIndex *int
	if logger := a.agentLoop.AuditLogger(); logger != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if res, verr := logger.Verify(ctx); verr != nil {
			slog.Warn("rest: audit chain verify failed", "error", verr)
		} else if res != nil {
			if res.Valid {
				chainStatus = "valid"
			} else {
				chainStatus = "broken"
				idx := res.BrokenAt
				chainBrokenIndex = &idx
			}
		}
	}

	resp := map[string]any{
		"entries":      entries,
		"chain_status": chainStatus,
	}
	if chainBrokenIndex != nil {
		resp["chain_broken_index"] = *chainBrokenIndex
	}
	jsonOK(w, resp)
}

// credentialsStorePath returns the path to the encrypted credentials file.
func (a *restAPI) credentialsStorePath() string {
	return filepath.Join(a.homePath, "credentials.json")
}

// HandleCredentials handles GET/POST /api/v1/credentials and DELETE /api/v1/credentials/{key}.
func (a *restAPI) HandleCredentials(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	sub := strings.TrimPrefix(path, "/api/v1/credentials")
	sub = strings.TrimPrefix(sub, "/")

	switch {
	case r.Method == http.MethodGet && sub == "":
		a.listCredentials(w)
	case r.Method == http.MethodPost && sub == "rotate":
		a.rotateCredentials(w, r)
	case r.Method == http.MethodPost && sub == "":
		a.setCredential(w, r)
	case r.Method == http.MethodDelete && sub != "":
		a.deleteCredential(w, r, sub)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// listCredentials lists all credential key names without values.
func (a *restAPI) listCredentials(w http.ResponseWriter) {
	store := a.credStore
	if store == nil {
		store = credentials.NewStore(a.credentialsStorePath())
	}
	keys, err := store.List()
	if err != nil {
		slog.Error("rest: list credentials", "error", err)
		jsonServerFailure(w, http.StatusInternalServerError, "could not list credentials", err)
		return
	}
	if keys == nil {
		keys = []string{}
	}
	jsonOK(w, keys)
}

// setCredential adds or updates an encrypted credential. Credential-vault writes
// are the highest-blast-radius excluded mutation (they store API keys, channel
// tokens), so this handler requires a re-auth consent token (FR-12.2, ADR-022).
func (a *restAPI) setCredential(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig)
	if !ok || user == nil {
		jsonErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if !a.requireReAuth(w, r, user.Username) {
		return
	}
	var req gen.CredentialSetRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "CredentialSetRequest", &req, validateEnabled) {
		return
	}
	if req.Key == "" {
		jsonErr(w, http.StatusUnprocessableEntity, "key is required")
		return
	}
	store := a.credStore
	if store == nil || store.IsLocked() {
		// Fall back to creating and unlocking a store if no shared store is available
		store = credentials.NewStore(a.credentialsStorePath())
		if err := credentials.Unlock(store); err != nil {
			slog.Warn("rest: credential store locked for set", "error", err)
			jsonErr(
				w,
				http.StatusServiceUnavailable,
				"credential store is locked — set OMNIPUS_MASTER_KEY or OMNIPUS_KEY_FILE",
			)
			return
		}
	}
	if err := store.Set(req.Key, req.Value); err != nil {
		if errors.Is(err, credentials.ErrProgramOwned) {
			jsonErr(w, http.StatusConflict, programs.ProgramOwnedMessage(a.agentLoop.GetConfig(), req.Key))
			return
		}
		slog.Error("rest: set credential", "key", req.Key, "error", err)
		jsonServerFailure(w, http.StatusInternalServerError, "could not save credential", err)
		return
	}
	// Trigger reload AND WAIT for it (triggerReloadAndWaitOutcome, not a bare
	// TriggerReload — mirrors HandleProviders' PUT branch and
	// createAgent/updateAgent/deleteAgent). A credential value only becomes
	// LIVE for a provider once executeReload's credentials.InjectFromConfig
	// step re-runs os.Setenv(ref, newValue) — see ModelConfig.APIKey(), which
	// reads the ref straight out of the process environment set at
	// boot/reload. Without this call, a credential write here sits correctly
	// encrypted in the store but the OLD value stays live in-process
	// (whatever the last boot/reload injected) until some unrelated config
	// write happens to trigger a reload — a stale-credential window that was
	// previously invisible to the caller (UAT batch3 finding #6: a stale
	// OPENROUTER_API_KEY value survived a `credentials set` and required an
	// explicit `/reload` to take effect). A reload failure does NOT undo the
	// store write above (the new value really is saved), so this still
	// returns 201 — mirroring the provider-update pattern of reporting the
	// persisted fact and warning separately about live application.
	confirmed, reloadErr := a.triggerReloadAndWaitOutcome()
	if reloadErr != nil {
		slog.Error("config reload after credential set failed", "key", req.Key, "error", reloadErr)
	} else if !confirmed {
		slog.Warn("rest: reload after credential set did not confirm within the poll window; "+
			"providers/channels using this credential may still be served the stale value", "key", req.Key)
	}
	w.WriteHeader(http.StatusCreated)
	resp := map[string]any{"key": req.Key}
	if reloadErr != nil {
		resp["reload_warning"] = fmt.Sprintf(
			"credential %q was saved but the live reload failed (%s); it will apply after the next "+
				"config reload or gateway restart", req.Key, reloadErr.Error())
	} else if !confirmed {
		resp["reload_warning"] = fmt.Sprintf(
			"credential %q was saved but the live reload did not confirm in time; it may not be live yet",
			req.Key)
	}
	jsonOK(w, resp)
}

// deleteCredential removes a credential by key. Like setCredential, it requires
// a re-auth consent token (FR-12.2, ADR-022) — deleting a stored secret is as
// disruptive as writing one (it can revoke a channel or provider mid-session).
func (a *restAPI) deleteCredential(w http.ResponseWriter, r *http.Request, key string) {
	user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig)
	if !ok || user == nil {
		jsonErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if !a.requireReAuth(w, r, user.Username) {
		return
	}
	if err := validateEntityID(key); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid credential key")
		return
	}
	store := a.credStore
	if store == nil || store.IsLocked() {
		store = credentials.NewStore(a.credentialsStorePath())
		if err := credentials.Unlock(store); err != nil {
			slog.Warn("rest: credential store locked for delete", "error", err)
			jsonErr(
				w,
				http.StatusServiceUnavailable,
				"credential store is locked — set OMNIPUS_MASTER_KEY or OMNIPUS_KEY_FILE",
			)
			return
		}
	}
	if err := store.Delete(key); err != nil {
		var notFound *credentials.NotFoundError
		if errors.As(err, &notFound) {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("credential %q not found", key))
			return
		}
		if errors.Is(err, credentials.ErrProgramOwned) {
			jsonErr(w, http.StatusConflict, programs.ProgramOwnedMessage(a.agentLoop.GetConfig(), key))
			return
		}
		slog.Error("rest: delete credential", "key", key, "error", err)
		jsonServerFailure(w, http.StatusInternalServerError, "could not delete credential", err)
		return
	}
	// Trigger reload AND WAIT for it — same reasoning as setCredential above.
	// Symmetric behavior: if this ref is still wired into an ENABLED channel's
	// config, executeReload's escalation check rejects the reload (rollback,
	// nothing degrades silently); if it is only a provider's key, the reload
	// continues with that provider's row degraded. Either way the deletion
	// itself already committed to the store, so this is reported via
	// reload_warning rather than undone.
	confirmed, reloadErr := a.triggerReloadAndWaitOutcome()
	resp := map[string]string{"status": "removed", "key": key}
	if reloadErr != nil {
		slog.Error("config reload after credential delete failed", "key", key, "error", reloadErr)
		resp["reload_warning"] = fmt.Sprintf(
			"credential %q was deleted but the live reload failed (%s); dependent providers/channels "+
				"may still be degraded until the next config reload or gateway restart", key, reloadErr.Error())
	} else if !confirmed {
		slog.Warn("rest: reload after credential delete did not confirm within the poll window; "+
			"providers/channels using this credential may still be served the stale value", "key", key)
		resp["reload_warning"] = fmt.Sprintf(
			"credential %q was deleted but the live reload did not confirm in time; it may not be live yet", key)
	}
	jsonOK(w, resp)
}

// rotateCredentials handles POST /api/v1/credentials/rotate (G5). It re-encrypts
// the whole vault under a new passphrase-derived key. Like set/delete, it requires
// a re-auth consent token (FR-12.2, ADR-022) — rotation is a high-blast-radius
// security operation. No restart needed: the store updates its in-memory key.
func (a *restAPI) rotateCredentials(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig)
	if !ok || user == nil {
		jsonErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if !a.requireReAuth(w, r, user.Username) {
		return
	}
	var req gen.CredentialRotateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "CredentialRotateRequest", &req, validateEnabled) {
		return
	}
	if strings.TrimSpace(req.NewPassphrase) == "" {
		jsonErr(w, http.StatusBadRequest, "new_passphrase is required and must not be empty")
		return
	}
	store := a.credStore
	if store == nil || store.IsLocked() {
		store = credentials.NewStore(a.credentialsStorePath())
		if err := credentials.Unlock(store); err != nil {
			slog.Warn("rest: credential store locked for rotate", "error", err)
			jsonErr(
				w,
				http.StatusServiceUnavailable,
				"credential store is locked — set OMNIPUS_MASTER_KEY or OMNIPUS_KEY_FILE",
			)
			return
		}
	}
	if err := store.RotateWithPassphrase(req.NewPassphrase); err != nil {
		slog.Error("rest: rotate credentials", "error", err)
		jsonServerFailure(w, http.StatusInternalServerError, "rotation failed", err)
		return
	}
	slog.Info("rest: credential vault rotated")
	jsonOK(w, map[string]string{"status": "rotated"})
}

// clearSessionsWarning is the fixed per-agent warning for a clear that failed
// partway: the cause (paths, OS error) is in the error log, never here.
func clearSessionsWarning(agentID string) string {
	return fmt.Sprintf("agent %q: sessions could not be fully cleared; see the server log", agentID)
}

// HandleClearSessions handles DELETE /api/v1/sessions/all.
//
// Founder ruling (S2 #6): "a main chat can only be deleted when the main agent
// is deleted" - so this NEVER deletes a main; its history is kept, and so are the
// helper sessions that belong to a main. Every other chat is deleted one by one
// through the same path a single delete uses: the session's tree is Stopped first
// (a chat that will not stop is kept and reported, nothing half-deleted), then
// removed together with its helper descendants' uploads, retained external-CLI
// drivers and in-memory media refs. The response counts what was removed and
// reports, by fixed text, every chat that was kept or could not even be read.
func (a *restAPI) HandleClearSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store := a.agentLoop.GetSessionStore()
	if store == nil {
		jsonErr(w, http.StatusInternalServerError, "session store unavailable")
		return
	}
	metas, err := store.ListSessions()
	if err != nil {
		jsonServerFailure(w, http.StatusInternalServerError, "could not list sessions", err)
		return
	}
	// A chat whose metadata cannot be read is invisible to ListSessions; it is
	// neither deleted nor counted as kept unless asked for here.
	unreadable, err := store.UnreadableSessionIDs()
	if err != nil {
		jsonServerFailure(w, http.StatusInternalServerError, "could not list sessions", err)
		return
	}
	keptWithMain, err := a.sessionsBelongingToMains(metas)
	if err != nil {
		// Fail closed (S2-02): with an incomplete helper walk some chats that
		// belong to a main cannot be told apart from ordinary ones, so nothing
		// is deleted and the person is not told "cleared".
		jsonServerFailure(w, http.StatusInternalServerError, "could not determine which chats belong to a main chat; nothing was deleted. Check the server log for the damaged session record, repair or remove it, then retry", err)
		return
	}
	removed, kept, mains := 0, len(unreadable), 0
	for _, id := range unreadable {
		slog.Warn("rest: clear sessions: a chat's metadata is unreadable; it is kept", "session_id", id)
	}
	for _, m := range metas {
		if m == nil {
			continue
		}
		// A main is never deleted here, and neither is a helper session that
		// belongs to one: it is part of that main's history.
		if m.Type == session.SessionTypeMain {
			mains++
			continue
		}
		if _, isMainsHelper := keptWithMain[m.ID]; isMainsHelper {
			continue
		}
		if summary := a.stopBeforeDelete(r, m.ID); summary != "" {
			slog.Warn("rest: clear sessions: Stop incomplete; this chat is kept",
				"session_id", m.ID, "summary", summary)
			kept++
			continue
		}
		if err := a.removeStoppedSession(store, m.ID); err != nil {
			slog.Error("rest: clear sessions: could not delete a chat; it is kept",
				"session_id", m.ID, "error", err)
			kept++
			continue
		}
		removed++
	}
	resp := gen.ClearAllSessionsResponse{
		Status: gen.ClearAllSessionsResponseStatusCleared,
		Count:  removed,
	}
	var warnings []string
	if kept > 0 {
		warnings = append(warnings, clearSessionsKeptWarning(kept))
	}
	if mains > 0 {
		warnings = append(warnings, clearSessionsMainsWarning(mains))
	}
	if len(warnings) > 0 {
		resp.Warnings = &warnings
	}
	jsonOK(w, resp)
}

// sessionsBelongingToMains returns the ids of every listed session that is a
// helper of a main chat: a descendant by the durable lifecycle edge, or by the
// session's own parent link. They are kept with their main. A descendant walk
// that could not be completed is returned as an error (never just logged): the
// caller must not delete anything on a truncated view.
func (a *restAPI) sessionsBelongingToMains(metas []*session.UnifiedMeta) (map[string]struct{}, error) {
	byID := make(map[string]*session.UnifiedMeta, len(metas))
	for _, m := range metas {
		if m != nil {
			byID[m.ID] = m
		}
	}
	out := make(map[string]struct{})
	var walkErrs []error
	for _, m := range metas {
		if m == nil || m.Type != session.SessionTypeMain {
			continue
		}
		ids, walkErr := agent.CollectDescendantSessionIDs(a.agentLoop.GetSessionLifecycleStore(), m.ID)
		if walkErr != nil {
			walkErrs = append(walkErrs, walkErr)
		}
		for _, id := range ids {
			out[id] = struct{}{}
		}
	}
	for _, m := range metas {
		if m == nil || m.Type == session.SessionTypeMain {
			continue
		}
		seen := map[string]bool{m.ID: true}
		for cur := m; cur != nil && cur.ParentSessionID != ""; {
			parent := byID[cur.ParentSessionID]
			if parent == nil || seen[parent.ID] {
				break
			}
			if parent.Type == session.SessionTypeMain {
				out[m.ID] = struct{}{}
				break
			}
			seen[parent.ID] = true
			cur = parent
		}
	}
	return out, errors.Join(walkErrs...)
}

// clearSessionsKeptWarning is the fixed warning for chats delete-all could not
// remove (a Stop that did not complete, a storage failure, or metadata that could
// not be read). It names no session id, path or cause; those are in the server log.
func clearSessionsKeptWarning(n int) string {
	return fmt.Sprintf("%d chat(s) could not be deleted and were kept (a running turn did not stop, storage failed, or the chat could not be read); see the server log", n)
}

// clearSessionsMainsWarning is the fixed notice that main chats are never
// deleted by delete-all: a main goes only with its agent.
func clearSessionsMainsWarning(n int) string {
	return fmt.Sprintf("%d main chat(s) were kept: a main chat is deleted only together with its agent", n)
}

// HandleAbout handles GET /api/v1/about.
// Returns version, go_version, os, arch, uptime, pid, and preview fields.
//
// Preview fields per FR-015 (preview-on-main-listener, US-8): the separate
// preview listener/port/origin are retired — `/preview/` is always served on
// the main gateway listener. The SPA only needs to know whether preview is
// currently enabled:
//   - preview_enabled: whether preview is enabled (bool, cfg.IsPreviewEnabled())
//   - warmup_timeout_seconds: dev-server warmup timeout from config (int)
//   - device_pairing_enabled: dark-launched device-pairing feature flag (bool)
func (a *restAPI) HandleAbout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	cfg := a.agentLoop.GetConfig()
	uptime := time.Since(startTime).Round(time.Second)
	// F-8: determine whether frame-ancestors is in fallback ('*') mode.
	// This happens when host=0.0.0.0/[::] AND public_url is not set, meaning
	// strict embedding control (T-04) is degraded. The SPA can show a banner.
	mainOrigin := resolveMainOrigin(cfg)
	frameAncestorsFallback := mainOrigin == ""
	resp := gen.AboutResponse{
		Version:       Version,
		GoVersion:     runtime.Version(),
		Os:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		Uptime:        uptime.String(),
		UptimeSeconds: int(uptime.Seconds()),
		Pid:           os.Getpid(),
		// FR-015: preview is served on the main listener; the SPA only needs
		// to know whether it is currently enabled (no port/origin to report).
		PreviewEnabled:       cfg.IsPreviewEnabled(),
		WarmupTimeoutSeconds: int(cfg.Tools.RunInWorkspace.WarmupTimeoutSeconds),
		// F-8: signals to the SPA that frame-ancestors is '*' (degraded T-04 defense).
		FrameAncestorsFallback: frameAncestorsFallback,
		// Dark-launched device-pairing feature — see Sandbox.Experimental.DevicePairingEnabled.
		DevicePairingEnabled: cfg.Sandbox.Experimental.DevicePairingEnabled,
	}
	jsonOK(w, resp)
}
