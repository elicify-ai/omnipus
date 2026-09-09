// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
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
	if os.IsNotExist(err) {
		// AuditLogResponse envelope: no entries, chain not checkable.
		jsonOK(w, map[string]any{"entries": []json.RawMessage{}, "chain_status": "unknown"})
		return
	}
	if err != nil {
		slog.Error("rest: open audit log", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read audit log: %v", err))
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
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read audit log: %v", err))
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

	// G4: verify the HMAC tamper-evident chain (v0.2 #155) so the UI can surface
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
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not list credentials: %v", err))
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
		slog.Error("rest: set credential", "key", req.Key, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save credential: %v", err))
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
		slog.Error("rest: delete credential", "key", key, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not delete credential: %v", err))
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
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("rotation failed: %v", err))
		return
	}
	slog.Info("rest: credential vault rotated")
	jsonOK(w, map[string]string{"status": "rotated"})
}

// HandleCreateBackup handles POST /api/v1/backup.
// Creates a tar.gz of ~/.omnipus/ excluding logs and backups directories.
func (a *restAPI) HandleCreateBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	backupsDir := filepath.Join(a.homePath, "backups")
	if err := os.MkdirAll(backupsDir, 0o700); err != nil {
		slog.Error("rest: create backups dir", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not create backups directory: %v", err))
		return
	}
	timestamp := time.Now().UTC().Format("20060102T150405Z")
	filename := fmt.Sprintf("backup-%s.tar.gz", timestamp)
	destPath := filepath.Join(backupsDir, filename)

	if err := createTarGz(a.homePath, destPath); err != nil {
		slog.Error("rest: create backup", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not create backup: %v", err))
		return
	}
	info, err := os.Stat(destPath)
	if err != nil {
		slog.Error("rest: stat backup file", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not stat backup: %v", err))
		return
	}
	jsonOK(w, gen.BackupCreateResponse{
		Path:      destPath,
		SizeBytes: info.Size(),
		CreatedAt: info.ModTime().UTC(),
	})
}

// createTarGz archives srcDir into destPath (tar.gz), excluding "logs" and "backups"
// top-level subdirectories. The archive is written atomically via a temp file + rename.
func createTarGz(srcDir, destPath string) error {
	tmpFile, err := os.CreateTemp(filepath.Dir(destPath), ".backup-*.tar.gz")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		os.Remove(tmpPath) // no-op after successful rename
	}()

	gz := gzip.NewWriter(tmpFile)
	tw := tar.NewWriter(gz)

	walkErr := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(srcDir, path)
		if relErr != nil {
			return fmt.Errorf("compute relative path for %q: %w", path, relErr)
		}
		if rel == "." {
			return nil // skip the root itself
		}
		// Skip logs and backups to prevent log noise and recursive inclusion.
		topLevel := strings.SplitN(rel, string(filepath.Separator), 2)[0]
		if topLevel == "logs" || topLevel == "backups" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		hdr := &tar.Header{
			Name:    filepath.ToSlash(rel),
			Size:    info.Size(),
			Mode:    int64(info.Mode()),
			ModTime: info.ModTime(),
		}
		if writeErr := tw.WriteHeader(hdr); writeErr != nil {
			return fmt.Errorf("write tar header for %q: %w", rel, writeErr)
		}
		src, openErr := os.Open(path)
		if openErr != nil {
			return fmt.Errorf("open %q: %w", path, openErr)
		}
		_, copyErr := io.Copy(tw, src)
		src.Close()
		if copyErr != nil {
			return fmt.Errorf("archive %q: %w", rel, copyErr)
		}
		return nil
	})
	if walkErr != nil {
		return fmt.Errorf("walk source directory: %w", walkErr)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar writer: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("close gzip writer: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("flush temp file: %w", err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("rename temp to dest: %w", err)
	}
	return nil
}

// HandleListBackups handles GET /api/v1/backups.
// Lists all .tar.gz files in ~/.omnipus/backups/.
func (a *restAPI) HandleListBackups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	backupsDir := filepath.Join(a.homePath, "backups")
	entries, err := os.ReadDir(backupsDir)
	if err != nil {
		if os.IsNotExist(err) {
			jsonOK(w, []any{})
			return
		}
		slog.Error("rest: list backups", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not list backups: %v", err))
		return
	}
	type backupEntry struct { // not-wire-format: response-only local type used in jsonOK; oapi-codegen inlines the shape; no gen.BackupEntry Go type exists
		Filename  string `json:"filename"`
		SizeBytes int64  `json:"size_bytes"`
		CreatedAt string `json:"created_at"`
	}
	backups := make([]backupEntry, 0)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar.gz") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			slog.Warn("rest: stat backup entry", "name", e.Name(), "error", err)
			continue
		}
		backups = append(backups, backupEntry{
			Filename:  e.Name(),
			SizeBytes: info.Size(),
			CreatedAt: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	jsonOK(w, backups)
}

// HandleRestore handles POST /api/v1/restore.
// Extracts a backup tar.gz over ~/.omnipus/, skipping config.json to preserve settings.
func (a *restAPI) HandleRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req gen.RestoreBackupRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "RestoreBackupRequest", &req, validateEnabled) {
		return
	}
	if req.Filename == "" {
		jsonErr(w, http.StatusUnprocessableEntity, "filename is required")
		return
	}
	// Reject any path separators or traversal sequences.
	if strings.ContainsAny(req.Filename, "/\\") || strings.Contains(req.Filename, "..") {
		jsonErr(w, http.StatusBadRequest, "invalid filename")
		return
	}
	if !strings.HasSuffix(req.Filename, ".tar.gz") {
		jsonErr(w, http.StatusBadRequest, "filename must end with .tar.gz")
		return
	}
	backupPath := filepath.Join(a.homePath, "backups", req.Filename)
	if _, err := os.Stat(backupPath); err != nil {
		if os.IsNotExist(err) {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("backup %q not found", req.Filename))
			return
		}
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not access backup: %v", err))
		return
	}
	if err := extractTarGz(backupPath, a.homePath); err != nil {
		slog.Error("rest: restore backup", "filename", req.Filename, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not restore backup: %v", err))
		return
	}
	jsonOK(w, map[string]string{"status": "restored", "filename": req.Filename})
}

// maxRestoreFileSize is the maximum size of a single file extracted from a backup.
// Defense-in-depth against tampered archives with decompression bombs.
const maxRestoreFileSize = 256 * 1024 * 1024 // 256 MB

// extractTarGz extracts archivePath into destDir, skipping config.json and
// rejecting any entries with unsafe paths (absolute or traversal).
func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}
		clean := filepath.Clean(hdr.Name)
		// Reject absolute paths and parent-directory traversal.
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			slog.Warn("rest: restore: skipping unsafe tar entry", "name", hdr.Name)
			continue
		}
		// Preserve current config.json.
		if clean == "config.json" {
			continue
		}
		destPath := filepath.Join(destDir, clean)
		// Defense-in-depth: ensure the resolved path is still under destDir.
		if !strings.HasPrefix(destPath, filepath.Clean(destDir)+string(os.PathSeparator)) {
			slog.Warn("rest: restore: skipping tar entry escaping destination", "name", hdr.Name)
			continue
		}
		if mkdirErr := os.MkdirAll(filepath.Dir(destPath), 0o700); mkdirErr != nil {
			return fmt.Errorf("mkdir for %q: %w", clean, mkdirErr)
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(hdr.Mode)&0o700)
		if err != nil {
			return fmt.Errorf("create %q: %w", clean, err)
		}
		// Limit extraction size per file to defend against decompression bombs.
		written, copyErr := io.Copy(out, io.LimitReader(tr, maxRestoreFileSize+1))
		out.Close()
		if copyErr != nil {
			return fmt.Errorf("write %q: %w", clean, copyErr)
		}
		if written > maxRestoreFileSize {
			os.Remove(destPath)
			return fmt.Errorf("file %q exceeds maximum restore size (%d bytes)", clean, maxRestoreFileSize)
		}
	}
	return nil
}

// HandleClearSessions handles DELETE /api/v1/sessions/all.
// HandleClearSessions removes all session directories from all agent stores.
func (a *restAPI) HandleClearSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	totalRemoved := 0
	var warnings []string
	for _, id := range a.agentLoop.GetRegistry().ListAgentIDs() {
		store := a.agentLoop.GetAgentStore(id)
		if store == nil {
			continue
		}
		n, err := store.ClearAll()
		if err != nil {
			slog.Error("rest: clear sessions for agent", "agent_id", id, "error", err)
			warnings = append(warnings, fmt.Sprintf("agent %q: %v", id, err))
		}
		totalRemoved += n
	}
	resp := gen.ClearAllSessionsResponse{
		Status: gen.ClearAllSessionsResponseStatusCleared,
		Count:  totalRemoved,
	}
	if len(warnings) > 0 {
		resp.Warnings = &warnings
	}
	jsonOK(w, resp)
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
