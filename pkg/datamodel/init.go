// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package datamodel provides directory initialization and entity schema types
// for the Omnipus file-based data model per Appendix E of the BRD.
package datamodel

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// dirEntry describes one directory in the ~/.omnipus/ tree.
type dirEntry struct {
	path string
	perm os.FileMode
}

// omnipusDirs lists all directories that must exist under the home directory.
// Matches the layout in Appendix E §E.3.
var omnipusDirs = []dirEntry{
	{"agents", 0o700},
	{"workspaces", 0o700},
	{"sessions", 0o700},
	{"tasks", 0o700},
	{"pins", 0o700},
	{"channels", 0o700},
	{"skills", 0o700},
	{"backups", 0o700},
	{"system", 0o700},
	{"logs", 0o700},
}

// defaultConfig is written to config.json on first run when none exists.
// It is intentionally minimal — no agents configured, no providers.
var defaultConfig = map[string]any{
	"version": 1,
	"agents": map[string]any{
		"defaults": map[string]any{},
		"list":     []any{},
	},
	"providers": []any{},
	"channels":  map[string]any{},
	"gateway": map[string]any{
		"host": "localhost",
		"port": 5000,
	},
	"storage": map[string]any{
		"retention": map[string]any{
			"session_days":            90,
			"archive_before_delete":   true,
			"keep_compaction_summary": true,
		},
	},
}

// Init bootstraps the ~/.omnipus/ directory tree per Appendix E §E.3.
//
// It is idempotent: existing files and directories are never overwritten.
// Returns an error if the home directory is not writable or if any required
// sub-directory cannot be created.
//
// Implements US-1 acceptance criteria.
func Init(home string) error {
	// Verify home directory is reachable and writable.
	if info, err := os.Stat(home); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("datamodel: %q exists but is not a directory", home)
		}
		// Quick write-access probe.
		probe := filepath.Join(home, ".write_probe")
		if f, err := os.OpenFile(probe, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600); err != nil {
			return fmt.Errorf("datamodel: home directory %q is not writable: %w", home, err)
		} else {
			// Surface the Close error so a partial-write doesn't go silent.
			// closing a freshly-truncated empty file should always succeed —
			// any failure here means something is fundamentally wrong with
			// the filesystem (quota, disk full mid-fsync, etc.).
			if closeErr := f.Close(); closeErr != nil {
				return fmt.Errorf("datamodel: write-probe close failed for %q: %w", home, closeErr)
			}
			_ = os.Remove(probe)
		}
	}

	// Create home with restricted permissions (0700).
	if err := os.MkdirAll(home, 0o700); err != nil {
		return fmt.Errorf("datamodel: create home %q: %w", home, err)
	}
	if err := os.Chmod(home, 0o700); err != nil {
		return fmt.Errorf("datamodel: chmod home %q: %w", home, err)
	}

	// Create sub-directories.
	for _, d := range omnipusDirs {
		full := filepath.Join(home, d.path)
		if err := os.MkdirAll(full, d.perm); err != nil {
			return fmt.Errorf("datamodel: create dir %q: %w", full, err)
		}
	}

	// Write default config.json if it does not exist.
	configPath := filepath.Join(home, "config.json")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		data, err := json.MarshalIndent(defaultConfig, "", "  ")
		if err != nil {
			return fmt.Errorf("datamodel: marshal default config: %w", err)
		}
		if err := fileutil.WriteFileAtomic(configPath, data, 0o600); err != nil {
			return fmt.Errorf("datamodel: write default config: %w", err)
		}
		slog.Info("datamodel: first-run setup complete — default config written",
			"path", configPath,
			"note", "no agents configured; edit config.json to add providers and agents",
		)
	}

	// Write system/state.json if it does not exist.
	statePath := filepath.Join(home, "system", "state.json")
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		state := map[string]any{
			"version":    1,
			"created_at": time.Now().UTC().Format(time.RFC3339),
			"onboarding": map[string]any{"completed": false},
		}
		data, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return fmt.Errorf("datamodel: marshal state: %w", err)
		}
		if err := fileutil.WriteFileAtomic(statePath, data, 0o600); err != nil {
			return fmt.Errorf("datamodel: write state: %w", err)
		}
	}

	slog.Debug("datamodel: home directory initialized", "home", home)
	return nil
}

// InitAgentWorkspace creates the workspace directory tree for an agent.
//
// Layout per Appendix E §E.3 (agent subtree):
//
//	~/.omnipus/agents/<id>/
//	├── sessions/
//	├── memory/
//	│   └── daily/
//	└── skills/
//
// Implements US-7 acceptance criteria.
func InitAgentWorkspace(home, agentID string) error {
	if agentID == "" {
		return fmt.Errorf("datamodel: agentID must not be empty")
	}
	base := filepath.Join(home, "agents", agentID)

	subdirs := []string{
		"sessions",
		"memory",
		filepath.Join("memory", "daily"),
		"skills",
	}
	for _, sub := range subdirs {
		dir := filepath.Join(base, sub)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("datamodel: create workspace dir %q: %w", dir, err)
		}
	}

	slog.Debug("datamodel: agent workspace initialized", "agent_id", agentID, "path", base)
	return nil
}

// AgentWorkspacePath returns the canonical workspace path for agentID.
func AgentWorkspacePath(home, agentID string) string {
	return filepath.Join(home, "agents", agentID)
}
