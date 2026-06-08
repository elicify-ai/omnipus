// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// projectSessionLink records that a session used a project during its execution.
// Stored as ~/.omnipus/project_links/<projectID>/<sessionID>.json.
type projectSessionLink struct {
	SessionID string `json:"session_id"`
	CreatedAt string `json:"created_at"`
}

func projectLinksDir(home string) string { return filepath.Join(home, "project_links") }

// addLinkForProject records a session↔project link.
// Idempotent: a second call for the same pair is a no-op.
func addLinkForProject(home, projectID, sessionID string) error {
	if err := validateID(projectID); err != nil {
		return err
	}
	if err := validateID(sessionID); err != nil {
		return err
	}
	dir := filepath.Join(projectLinksDir(home), projectID)
	link := projectSessionLink{
		SessionID: sessionID,
		CreatedAt: nowISO(),
	}
	return writeEntity(dir, sessionID, link)
}

// listLinksForProject returns all session links recorded for the given project.
// Returns an empty slice (not an error) if the project has no links yet.
func listLinksForProject(home, projectID string) ([]projectSessionLink, error) {
	if err := validateID(projectID); err != nil {
		return nil, err
	}
	dir := filepath.Join(projectLinksDir(home), projectID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var links []projectSessionLink
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			slog.Warn("sysagent: skipping unreadable project link file", "file", e.Name(), "error", err)
			continue
		}
		var l projectSessionLink
		if err := json.Unmarshal(data, &l); err != nil {
			slog.Warn("sysagent: skipping corrupt project link file", "file", e.Name(), "error", err)
			continue
		}
		links = append(links, l)
	}
	return links, nil
}

// removeLinksForProject deletes the entire project link directory for projectID.
// Called during cascade-delete so no orphaned link records remain.
// Errors are logged but not returned — the caller continues regardless.
func removeLinksForProject(home, projectID string) {
	if err := validateID(projectID); err != nil {
		slog.Warn("sysagent: removeLinksForProject: invalid project id", "id", projectID, "error", err)
		return
	}
	dir := filepath.Join(projectLinksDir(home), projectID)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		slog.Warn("sysagent: removeLinksForProject: failed to remove link dir",
			"project_id", projectID, "dir", dir, "error", err)
	}
}
