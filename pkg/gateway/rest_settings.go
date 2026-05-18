//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
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

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
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
		jsonOK(w, []json.RawMessage{})
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
	jsonOK(w, entries)
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
	case r.Method == http.MethodPost && sub == "":
		a.setCredential(w, r)
	case r.Method == http.MethodDelete && sub != "":
		a.deleteCredential(w, sub)
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

// setCredential adds or updates an encrypted credential.
func (a *restAPI) setCredential(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
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
	w.WriteHeader(http.StatusCreated)
	jsonOK(w, map[string]string{"key": req.Key})
}

// deleteCredential removes a credential by key.
func (a *restAPI) deleteCredential(w http.ResponseWriter, key string) {
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
	jsonOK(w, map[string]string{"status": "removed", "key": key})
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
	type backupEntry struct {
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
	var req struct {
		Filename string `json:"filename"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
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
	resp := map[string]any{"status": "cleared", "count": totalRemoved}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}
	jsonOK(w, resp)
}

// HandleAbout handles GET /api/v1/about.
// Returns version, go_version, os, arch, uptime, pid, and preview listener fields.
//
// Preview fields added per FR-009:
//   - preview_port: effective preview listener port (int)
//   - preview_listener_enabled: whether the preview listener is running (bool)
//   - warmup_timeout_seconds: dev-server warmup timeout from config (int)
//   - preview_origin: omitted when not set in config (string, omitempty)
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
		// FR-009: preview listener fields for SPA iframe URL construction.
		PreviewPort:            int(cfg.Gateway.PreviewPort),
		PreviewListenerEnabled: cfg.Gateway.IsPreviewListenerEnabled(),
		WarmupTimeoutSeconds:   int(cfg.Tools.RunInWorkspace.WarmupTimeoutSeconds),
		// F-8: signals to the SPA that frame-ancestors is '*' (degraded T-04 defense).
		FrameAncestorsFallback: frameAncestorsFallback,
	}
	// preview_origin is optional — only include when the operator set it.
	if cfg.Gateway.PreviewOrigin != "" {
		resp.PreviewOrigin = &cfg.Gateway.PreviewOrigin
	}
	jsonOK(w, resp)
}
