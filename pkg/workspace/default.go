// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package workspace provides a minimal, dependency-free reader for the
// on-disk workspace records under $OMNIPUS_HOME/workspaces/<id>.json. It exists
// so non-gateway packages (pkg/tools, pkg/agent) can resolve the real default
// workspace ID — the one flagged is_default — without importing pkg/gateway.
//
// The gateway (pkg/gateway/rest_workspaces.go) owns the authoritative CRUD +
// wire mapping; this package only reads the two fields the task subsystem needs
// (id, is_default), so it deliberately does NOT mirror the full storedWorkspace
// struct.
package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNoDefault is returned by ResolveDefaultID when no workspace on disk is
// flagged is_default (e.g. the default-workspace seed has not run yet). Callers
// MUST treat this as a hard error and reject the operation rather than inventing
// a literal id — landing a task in a non-existent workspace makes it invisible.
var ErrNoDefault = errors.New("no default workspace found")

// record is the minimal subset of the on-disk workspace JSON this package reads.
type record struct {
	ID        string `json:"id"`
	IsDefault bool   `json:"is_default,omitempty"`
}

// dirFor returns the workspaces directory under the given OMNIPUS_HOME root.
func dirFor(home string) string { return filepath.Join(home, "workspaces") }

// ResolveDefaultID returns the ID of the workspace flagged is_default under
// home (~/.omnipus). It returns ErrNoDefault when no such workspace exists, and
// a wrapped I/O error when the directory cannot be read. Malformed files are
// skipped. It does NOT cache — the default is resolved fresh each call so a
// just-seeded default is observed.
func ResolveDefaultID(home string) (string, error) {
	dir := dirFor(home)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNoDefault
		}
		return "", fmt.Errorf("workspace: list %q: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			continue
		}
		var w record
		if jerr := json.Unmarshal(data, &w); jerr != nil {
			continue
		}
		if w.IsDefault {
			id := w.ID
			if id == "" {
				id = strings.TrimSuffix(e.Name(), ".json")
			}
			return id, nil
		}
	}
	return "", ErrNoDefault
}

// Exists reports whether a workspace file with the given id exists under home.
// Returns false for an empty id or any read/stat error.
func Exists(home, id string) bool {
	if !safeID(id) {
		return false
	}
	_, err := os.Stat(filepath.Join(dirFor(home), id+".json"))
	return err == nil
}

// ListWorkspaceIDs returns every workspace id recorded under home, sorted for
// a deterministic enumeration order.
//
// It exists so a caller that must visit "every workspace" — the knowledge
// indexer's boot sweep is the first one (docs/internal/design/
// knowledge-tools-remediation.md, ruling R2) — has a single, minimal answer
// to draw from, rather than each caller inventing its own directory walk
// against workspaces/*.json the way pkg/gateway's listWorkspaceFiles and this
// file's own ResolveDefaultID both already do. It reads only the filename
// stem, not the record contents, so a malformed workspace file still yields
// its id here (unlike ResolveDefaultID, which must parse the file to find the
// is_default flag and therefore skips what it cannot parse) — an id this
// package cannot vouch for is still one a caller may need to know exists, so
// it can decide for itself, rather than one this function should hide.
//
// Returns (nil, nil) for a home with no workspaces directory yet, matching
// ResolveDefaultID's and listWorkspaceFiles' own "not an error" reading of
// os.ErrNotExist.
func ListWorkspaceIDs(home string) ([]string, error) {
	dir := dirFor(home)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("workspace: list %q: %w", dir, err)
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(e.Name(), ".json"))
	}
	sort.Strings(ids)
	return ids, nil
}
