// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"bufio"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
)

const (
	compactByteThreshold = int64(10 * 1024 * 1024) // 10 MB
	lruMaxSize           = 1000
)

// ProjectSessionLink is one line in project_session_links.jsonl.
// Exported so that the REST gateway layer can call ReadLinks without
// duplicating the mutex or file-format logic.
// The WorkspaceID field is stored as "workspace_id" on disk.
type ProjectSessionLink struct {
	WorkspaceID string `json:"workspace_id"`
	SessionID   string `json:"session_id"`
	CreatedAt   string `json:"created_at"`
}

// decodeLink decodes a JSONL line into a ProjectSessionLink.
// Lines missing workspace_id or session_id are rejected (return false).
func decodeLink(line string) (ProjectSessionLink, bool) {
	var link ProjectSessionLink
	if json.Unmarshal([]byte(line), &link) != nil {
		return ProjectSessionLink{}, false
	}
	if link.WorkspaceID == "" || link.SessionID == "" {
		return ProjectSessionLink{}, false
	}
	return link, true
}

// linksFilePath returns the absolute path of the link file for the given home dir.
func linksFilePath(home string) string {
	return filepath.Join(home, "project_session_links.jsonl")
}

// linkFileMu serializes ALL writes to project_session_links.jsonl:
// the linker-hook append path, the cascade-delete rewrite path, and compaction.
// Must be held (write lock) for the full duration of any write or rewrite operation.
// RLock is sufficient for read-only access via ReadLinks.
var linkFileMu sync.RWMutex

// AppendLinkExported is the exported wrapper around appendLink for use by callers
// outside this package (e.g. pkg/gateway) that need to record a workspace↔session
// link without going through the ProjectSessionLinker's LRU dedup logic.
// Use this when the caller already knows the (workspaceID, sessionID) pair is new
// (e.g. just created a fresh session for a board task).
func AppendLinkExported(home, workspaceID, sessionID string) error {
	return appendLink(home, workspaceID, sessionID)
}

// appendLink appends one (workspaceID, sessionID) entry to the link file.
// The file is opened with O_APPEND|O_CREATE so concurrent appenders on the same
// OS stay coherent; linkFileMu ensures our own process is serialized.
func appendLink(home, workspaceID, sessionID string) error {
	linkFileMu.Lock()
	defer linkFileMu.Unlock()

	path := linksFilePath(home)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	entry := ProjectSessionLink{
		WorkspaceID: workspaceID,
		SessionID:   sessionID,
		CreatedAt:   nowISO(),
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

// ReadLinks returns deduplicated links for a given workspaceID.
// Exported for use by the REST gateway layer without duplicating mutex or I/O.
// Dedup key: (workspace_id, session_id) pair — keeps earliest entry (first seen).
// Returns nil when the file is absent or empty (not an error condition).
// Legacy lines with "project_id" are migrated transparently on read.
func ReadLinks(home, workspaceID string) []ProjectSessionLink {
	linkFileMu.RLock()
	defer linkFileMu.RUnlock()

	path := linksFilePath(home)
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	seen := make(map[string]struct{})
	var out []ProjectSessionLink
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024) // 256 KiB max line
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		link, ok := decodeLink(line)
		if !ok {
			continue
		}
		if link.WorkspaceID != workspaceID {
			continue
		}
		key := link.WorkspaceID + ":" + link.SessionID
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, link)
	}
	if err := scanner.Err(); err != nil {
		slog.Warn("project_session_links: ReadLinks scan error — partial result returned",
			"error", err, "workspace_id", workspaceID)
	}
	return out
}

// rewriteLinks rewrites the link file keeping only entries for which keep returns true.
// Holds linkFileMu (write lock) for the full operation — callers must NOT hold the lock.
func rewriteLinks(home string, keep func(ProjectSessionLink) bool) error {
	linkFileMu.Lock()
	defer linkFileMu.Unlock()

	path := linksFilePath(home)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		link, ok := decodeLink(line)
		if !ok {
			continue // drop malformed lines
		}
		if keep(link) {
			// Re-encode in the new format (workspace_id).
			reencoded, encErr := json.Marshal(link)
			if encErr != nil {
				continue
			}
			kept = append(kept, string(reencoded))
		}
	}

	var content []byte
	if len(kept) > 0 {
		content = []byte(strings.Join(kept, "\n") + "\n")
	}
	return fileutil.WriteFileAtomic(path, content, 0o600)
}

// RemoveLinksForProject rewrites the link file excluding all entries for workspaceID.
// Called during cascade delete (workspace.delete tool) and from
// pkg/gateway/rest_workspaces.go::handleWorkspaceDelete.
// A missing file is a no-op; write failures are logged at Warn level (best-effort).
func RemoveLinksForProject(home, workspaceID string) {
	if err := rewriteLinks(home, func(l ProjectSessionLink) bool {
		return l.WorkspaceID != workspaceID
	}); err != nil {
		slog.Warn("project_session_links: RemoveLinksForProject failed", "error", err, "workspace_id", workspaceID)
	}
}

// compactLinksIfNeeded triggers a background dedup-rewrite when the link file
// exceeds compactByteThreshold bytes.
// Runs in a goroutine so it never blocks the caller.
func compactLinksIfNeeded(home string) {
	go func() {
		path := linksFilePath(home)
		info, err := os.Stat(path)
		if err != nil || info.Size() < compactByteThreshold {
			return // file absent or below size threshold
		}
		compactLinks(home)
	}()
}

// compactLinks rewrites the link file with duplicate (workspace_id, session_id) pairs removed.
// Keeps the earliest entry for each pair.
func compactLinks(home string) {
	seen := make(map[string]struct{})
	if err := rewriteLinks(home, func(l ProjectSessionLink) bool {
		key := l.WorkspaceID + ":" + l.SessionID
		if _, dup := seen[key]; dup {
			return false
		}
		seen[key] = struct{}{}
		return true
	}); err != nil {
		slog.Warn("project_session_links: compaction write failed", "error", err)
	}
}

// ProjectSessionLinker is instantiated once at gateway start and records which
// sessions have worked on which workspaces. Because pkg/agent imports
// pkg/sysagent/tools, this type cannot implement agent.ToolInterceptor directly
// (that would create a circular import). Instead it exposes LinkSession, and a
// thin adapter in pkg/agent/project_linker_hook.go wraps it to satisfy the
// ToolInterceptor interface without introducing a cycle.
type ProjectSessionLinker struct {
	home   string
	lruMu  sync.Mutex
	lruSet map[string]struct{}
}

// NewProjectSessionLinker creates a linker that stores links under home.
func NewProjectSessionLinker(home string) *ProjectSessionLinker {
	return &ProjectSessionLinker{
		home:   home,
		lruSet: make(map[string]struct{}),
	}
}

// lruCheck returns true if the (workspaceID, sessionID) pair is new and should be
// written; false if it has already been seen this instance's lifetime.
// When the set is full it is cleared (best-effort eviction, not true LRU).
func (l *ProjectSessionLinker) lruCheck(workspaceID, sessionID string) bool {
	key := workspaceID + ":" + sessionID
	l.lruMu.Lock()
	defer l.lruMu.Unlock()
	if _, seen := l.lruSet[key]; seen {
		return false
	}
	if len(l.lruSet) >= lruMaxSize {
		l.lruSet = make(map[string]struct{})
	}
	l.lruSet[key] = struct{}{}
	return true
}

// LinkSession records that sessionID worked on workspaceID.
// Both arguments must be non-empty; if either is blank the call is a no-op.
// Duplicate (workspaceID, sessionID) pairs within the same instance are deduplicated
// by the LRU set and not re-written to disk.
// Write failures are logged at Warn level and do not propagate (best-effort).
func (l *ProjectSessionLinker) LinkSession(workspaceID, sessionID string) {
	if workspaceID == "" || sessionID == "" {
		return
	}
	if !l.lruCheck(workspaceID, sessionID) {
		return
	}
	if err := appendLink(l.home, workspaceID, sessionID); err != nil {
		slog.Warn("project_session_links: append failed",
			"error", err, "workspace_id", workspaceID, "session_id", sessionID)
	} else {
		compactLinksIfNeeded(l.home)
	}
}
