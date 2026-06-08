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
)

const (
	compactLineThreshold = 100_000
	compactByteThreshold = int64(10 * 1024 * 1024) // 10 MB
	lruMaxSize           = 1000
)

// projectSessionLink is one line in project_session_links.jsonl.
type projectSessionLink struct {
	ProjectID string `json:"project_id"`
	SessionID string `json:"session_id"`
	CreatedAt string `json:"created_at"`
}

// linksFilePath returns the absolute path of the link file for the given home dir.
func linksFilePath(home string) string {
	return filepath.Join(home, "project_session_links.jsonl")
}

// linkFileMu serialises ALL writes to project_session_links.jsonl:
// the linker-hook append path, the cascade-delete rewrite path, and compaction.
// Must be held (write lock) for the full duration of any write or rewrite operation.
// RLock is sufficient for read-only access via readLinks.
var linkFileMu sync.RWMutex

// appendLink appends one (projectID, sessionID) entry to the link file.
// The file is opened with O_APPEND|O_CREATE so concurrent appenders on the same
// OS stay coherent; linkFileMu ensures our own process is serialised.
func appendLink(home, projectID, sessionID string) error {
	linkFileMu.Lock()
	defer linkFileMu.Unlock()

	path := linksFilePath(home)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	entry := projectSessionLink{
		ProjectID: projectID,
		SessionID: sessionID,
		CreatedAt: nowISO(),
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

// readLinks returns deduplicated links for a given projectID.
// TODO: consumed by the GET /api/v1/projects/{id}/sessions REST handler (Wave 2).
// Dedup key: (project_id, session_id) pair — keeps earliest entry (first seen).
// Returns nil when the file is absent or empty (not an error condition).
func readLinks(home, projectID string) []projectSessionLink {
	linkFileMu.RLock()
	defer linkFileMu.RUnlock()

	path := linksFilePath(home)
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	seen := make(map[string]struct{})
	var out []projectSessionLink
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024) // 256 KiB max line
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var link projectSessionLink
		if json.Unmarshal([]byte(line), &link) != nil {
			continue // skip malformed lines
		}
		if link.ProjectID != projectID {
			continue
		}
		key := link.ProjectID + ":" + link.SessionID
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, link)
	}
	return out
}

// removeLinksForProject rewrites the link file excluding all entries for projectID.
// Called during cascade delete (project.delete tool).
// A missing file is a no-op; write failures are logged at Warn level (best-effort).
func removeLinksForProject(home, projectID string) {
	linkFileMu.Lock()
	defer linkFileMu.Unlock()

	path := linksFilePath(home)
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("project_session_links: cannot read link file for rewrite",
				"error", err, "project_id", projectID)
		}
		return
	}

	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var link projectSessionLink
		if err := json.Unmarshal([]byte(line), &link); err != nil || link.ProjectID == projectID {
			continue // skip malformed and matching entries
		}
		kept = append(kept, line)
	}

	var content []byte
	if len(kept) > 0 {
		content = []byte(strings.Join(kept, "\n") + "\n")
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, 0600); err != nil {
		slog.Warn("project_session_links: failed to write temp file during rewrite",
			"error", err, "project_id", projectID)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		slog.Warn("project_session_links: failed to rename temp file during rewrite",
			"error", err, "project_id", projectID)
	}
}

// compactLinksIfNeeded triggers a background dedup-rewrite when the link file
// exceeds compactLineThreshold lines or compactByteThreshold bytes.
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

// compactLinks rewrites the link file with duplicate (project_id, session_id) pairs removed.
// Keeps the earliest entry for each pair. Holds linkFileMu (write lock) for the full operation.
func compactLinks(home string) {
	linkFileMu.Lock()
	defer linkFileMu.Unlock()

	path := linksFilePath(home)
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	seen := make(map[string]struct{})
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var link projectSessionLink
		if json.Unmarshal([]byte(line), &link) != nil {
			continue
		}
		key := link.ProjectID + ":" + link.SessionID
		if _, dup := seen[key]; !dup {
			seen[key] = struct{}{}
			kept = append(kept, line)
		}
	}

	var content []byte
	if len(kept) > 0 {
		content = []byte(strings.Join(kept, "\n") + "\n")
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, 0600); err != nil {
		slog.Warn("project_session_links: compaction write failed", "error", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		slog.Warn("project_session_links: compaction rename failed", "error", err)
	}
}

// ProjectSessionLinker is instantiated once at gateway start and wired into the
// agent loop as a ToolInterceptor adapter (in pkg/agent/loop.go, Wave 1c).
//
// Because pkg/agent imports pkg/sysagent/tools (cycle), this type does NOT
// implement agent.ToolInterceptor directly.  Instead it exposes LinkSession,
// which loop.go calls from a thin private adapter struct.
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

// lruCheck returns true if the (projectID, sessionID) pair is new and should be
// written; false if it has already been seen this instance's lifetime.
// When the set is full it is cleared (best-effort eviction, not true LRU).
func (l *ProjectSessionLinker) lruCheck(projectID, sessionID string) bool {
	key := projectID + ":" + sessionID
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

// LinkSession records that sessionID worked on projectID.
// Both arguments must be non-empty; if either is blank the call is a no-op.
// Duplicate (projectID, sessionID) pairs within the same instance are deduplicated
// by the LRU set and not re-written to disk.
// Write failures are logged at Warn level and do not propagate (best-effort).
func (l *ProjectSessionLinker) LinkSession(projectID, sessionID string) {
	if projectID == "" || sessionID == "" {
		return
	}
	if !l.lruCheck(projectID, sessionID) {
		return
	}
	if err := appendLink(l.home, projectID, sessionID); err != nil {
		slog.Warn("project_session_links: append failed",
			"error", err, "project_id", projectID, "session_id", sessionID)
	} else {
		compactLinksIfNeeded(l.home)
	}
}
