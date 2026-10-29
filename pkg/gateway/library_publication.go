// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// library_publication.go: the one shared publication path for chat files
// (#1449 FR-077, D25, D26). A chat file exists in two representations: the
// chat copy in the workspace media library (workspaces/<id>/media/, the
// media://workspace/<id>/<uuid> reference chats show) and the Library file
// under the workspace's .library folder (what agents read and the Library
// panel lists). Person uploads and agent-returned group files are both
// published here, and the chat copy's media entry durably records its
// Library file's path (library_path), so one explicit Library deletion can
// remove both.

package gateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	wkspace "github.com/elicify-ai/omnipus/pkg/workspace"
)

// errLibraryNameTaken: the exact Library file name holds a different file.
var errLibraryNameTaken = errors.New("library file name is taken by another file")

// writeLibraryFile writes data as a Library file of workspaceID under
// .library and returns its workspace-relative path (".library/<name>").
//
// With dedupe, a taken name gets a " (N)" suffix (a person's upload). Without
// it the name is exact and create-or-adopt (an answer file under its name
// allocated at acceptance, FR-078): an existing file with the same SHA-256 is
// adopted; one with other bytes is errLibraryNameTaken. A partly written file
// is removed before an error returns.
func writeLibraryFile(home, workspaceID, name string, data []byte, dedupe bool) (string, error) {
	sanitized, err := agent.SanitizeUploadFilename(name)
	if err != nil {
		return "", fmt.Errorf("sanitize filename: %w", err)
	}
	workDir, err := wkspace.SafeWorkDir(home, workspaceID)
	if err != nil {
		return "", fmt.Errorf("resolve workspace work dir: %w", err)
	}
	libraryDir := filepath.Join(workDir, agent.LibraryDirName())
	if mkErr := os.MkdirAll(libraryDir, 0o700); mkErr != nil {
		return "", fmt.Errorf("create workspace library dir: %w", mkErr)
	}
	ext := filepath.Ext(sanitized)
	base := strings.TrimSuffix(sanitized, ext)
	const maxDedupAttempts = 1000
	destName := sanitized
	for attempt := 1; ; attempt++ {
		destPath := filepath.Join(libraryDir, destName)
		f, openErr := os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if openErr == nil {
			if err := writeAndClose(f, destPath, data); err != nil {
				return "", err
			}
			return agent.LibraryDirName() + "/" + destName, nil
		}
		if !errors.Is(openErr, os.ErrExist) {
			return "", fmt.Errorf("create workspace library file: %w", openErr)
		}
		if !dedupe {
			existing, readErr := os.ReadFile(destPath)
			if readErr == nil && bytes.Equal(sha(existing), sha(data)) {
				return agent.LibraryDirName() + "/" + destName, nil // adopt
			}
			return "", errLibraryNameTaken
		}
		if attempt > maxDedupAttempts {
			return "", fmt.Errorf("too many filename collisions for %q in workspace library", sanitized)
		}
		destName = fmt.Sprintf("%s (%d)%s", base, attempt, ext)
	}
}

func writeAndClose(f *os.File, path string, data []byte) error {
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			logger.WarnCF("rest", "library publication: cleanup partial file failed",
				map[string]any{"path": path, "error": rmErr.Error()})
		}
		return fmt.Errorf("write workspace library file: %w", err)
	}
	return nil
}

func sha(b []byte) []byte {
	s := sha256.Sum256(b)
	return s[:]
}

func shaHex(b []byte) string { return hex.EncodeToString(sha(b)) }

// associateLibraryFile records the chat copy's Library file path and the
// in-memory lookup the transcript uses. A failure is returned: without the
// durable association a later Library deletion could not remove the chat
// copy (D26).
func associateLibraryFile(lib *library.Library, ref, mediaID, relPath string) error {
	if err := lib.SetLibraryPath(mediaID, relPath); err != nil {
		return fmt.Errorf("record the library file of %s: %w", mediaID, err)
	}
	agent.RecordUploadWorkPath(ref, relPath)
	return nil
}

// removeLibraryFile removes a Library file by its workspace-relative path
// (best-effort cleanup of a publication that is rolled back).
func removeLibraryFile(home, workspaceID, relPath string) {
	workDir, err := wkspace.SafeWorkDir(home, workspaceID)
	if err != nil || relPath == "" || strings.Contains(relPath, "..") {
		return
	}
	path := filepath.Join(workDir, filepath.FromSlash(relPath))
	if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		logger.WarnCF("rest", "library publication: rollback of library file failed",
			map[string]any{"path": path, "error": rmErr.Error()})
	}
}
