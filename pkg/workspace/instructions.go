// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package workspace

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// ErrInvalidWorkspaceID is returned when a workspace id could escape the
// workspaces/ root (contains a separator or "..", or is empty). Callers that
// build a filesystem path from an id MUST treat this as a hard stop.
var ErrInvalidWorkspaceID = errors.New("workspace: invalid id")

// ErrInstructionsTooLarge is returned by WriteInstructions when content exceeds
// maxInstructionsBytes. Surfaced as a 400 at the REST layer via errors.Is.
var ErrInstructionsTooLarge = errors.New("workspace: instructions too large")

// instructionsFileName is the per-workspace AGENT.md that holds the workspace's
// Project Instructions — a single instruction block injected as a per-turn
// context layer for every agent acting in the workspace. It lives in the
// workspace's own directory (workspaces/<id>/AGENT.md), alongside the shared
// memory room (workspaces/<id>/.omnipus/).
const instructionsFileName = "AGENT.md"

// maxInstructionsBytes is the upper bound enforced at the storage layer. It is
// a BYTE count (matching len(content)); the WorkspaceInstructionsRequest
// contract caps at 262144 codepoints, so for multibyte UTF-8 the storage layer
// is the tighter bound. A write above this is rejected (ErrInstructionsTooLarge),
// never truncated.
const maxInstructionsBytes = 262144

// safeID rejects ids that could escape the workspaces/ root via traversal or
// separators. The single source of truth for workspace-id path safety; Exists
// and SafeWorkspaceDir both route through it.
func safeID(id string) bool {
	return id != "" && !strings.ContainsAny(id, "/\\") && !strings.Contains(id, "..")
}

// WorkspaceDir returns the per-workspace directory (workspaces/<id>/) under home.
// This is distinct from the flat workspaces/<id>.json metadata record. It does
// NOT validate id — use SafeWorkspaceDir when the id is externally influenced
// and will be used to build a filesystem root.
func WorkspaceDir(home, id string) string {
	return filepath.Join(dirFor(home), id)
}

// SafeWorkspaceDir returns the per-workspace directory after validating that id
// cannot escape the workspaces/ root. It is the ONLY sanctioned way to turn an
// externally-influenced workspace id into a filesystem root (e.g. the per-turn
// re-rooting in the agent loop). Returns ErrInvalidWorkspaceID for an unsafe id.
func SafeWorkspaceDir(home, id string) (string, error) {
	if !safeID(id) {
		return "", fmt.Errorf("%w: %q", ErrInvalidWorkspaceID, id)
	}
	return WorkspaceDir(home, id), nil
}

// instructionsPath returns the absolute path of a workspace's AGENT.md.
func instructionsPath(home, id string) string {
	return filepath.Join(WorkspaceDir(home, id), instructionsFileName)
}

// ReadInstructions returns the content of the workspace's AGENT.md (Project
// Instructions). It returns "" with a nil error when the file does not exist —
// an absent instructions file is a valid empty state, not an error. A read error
// other than not-exist is returned. The id is validated against traversal.
//
// The read is bounded to maxInstructionsBytes (the same cap enforced at write
// time). Files that exceed the cap are truncated to that byte limit and a WARN
// is logged; the content is never silently injected unbounded into every turn's
// system context.
func ReadInstructions(home, id string) (string, error) {
	if !safeID(id) {
		return "", fmt.Errorf("%w: %q", ErrInvalidWorkspaceID, id)
	}
	path := instructionsPath(home, id)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("workspace: read instructions %q: %w", id, err)
	}
	defer f.Close()

	// Read at most maxInstructionsBytes+1 so we can detect whether the file
	// exceeds the cap without reading the whole thing.
	lr := io.LimitReader(f, int64(maxInstructionsBytes)+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		return "", fmt.Errorf("workspace: read instructions %q: %w", id, err)
	}
	if len(data) > maxInstructionsBytes {
		logger.WarnCF("workspace", "instructions file exceeds size cap; truncating to maxInstructionsBytes",
			map[string]any{
				"workspace_id": id,
				"max_bytes":    maxInstructionsBytes,
				"hint":         "file may have been written outside Omnipus; use PUT /api/v1/workspaces/{id}/instructions to update it",
			})
		data = data[:maxInstructionsBytes]
	}
	return string(data), nil
}

// WriteInstructions replaces the workspace's AGENT.md with content. An empty
// content removes the file (the canonical "cleared" state) rather than leaving a
// zero-byte file. The per-workspace directory is created if absent. Content
// above maxInstructionsBytes is rejected. The id is validated against traversal.
func WriteInstructions(home, id, content string) error {
	if !safeID(id) {
		return fmt.Errorf("%w: %q", ErrInvalidWorkspaceID, id)
	}
	if len(content) > maxInstructionsBytes {
		return fmt.Errorf("%w: %d > %d bytes", ErrInstructionsTooLarge, len(content), maxInstructionsBytes)
	}
	path := instructionsPath(home, id)
	if strings.TrimSpace(content) == "" {
		// Clear: remove the file. Absent file == empty instructions.
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("workspace: clear instructions %q: %w", id, err)
		}
		return nil
	}
	if err := os.MkdirAll(WorkspaceDir(home, id), 0o755); err != nil {
		return fmt.Errorf("workspace: ensure dir %q: %w", id, err)
	}
	if err := fileutil.WriteFileAtomic(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("workspace: write instructions %q: %w", id, err)
	}
	return nil
}
