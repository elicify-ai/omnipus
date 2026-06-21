// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"os"
	"path/filepath"

	"github.com/dapicom-ai/omnipus/pkg"
	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// OmnipusHomeDir resolves the base directory for all Omnipus-owned data.
// Every subsystem that needs to compute an Omnipus path MUST go through this
// helper — never read HOME / UserHomeDir ad hoc and never join ".omnipus"
// inline. Doing so splits the installation across two directories whenever
// OMNIPUS_HOME is set and one code path skips the env check.
//
// Resolution order:
//  1. $OMNIPUS_HOME (the user's explicit override — always resolved to an
//     absolute path so runtime files never end up inside the source tree
//     when the process CWD is the repo root)
//  2. $HOME/.omnipus (the conventional fallback)
//  3. A user-private temp directory (0700, randomly-suffixed) if HOME is
//     unreadable — keeps the install working without silently sharing data
//     with other users on the box.
//
// Intentionally not memoised so tests can override OMNIPUS_HOME mid-process.
// The os.Getenv + filepath.Join cost is irrelevant outside tight loops, and
// any subsystem that needs a stable value should capture it at init.
func OmnipusHomeDir() string {
	if override := os.Getenv(EnvHome); override != "" {
		// Resolve relative paths against the process CWD. The previous
		// "trusted verbatim" behavior landed runtime files (cost.json,
		// state.json) inside the source tree when the gateway was started
		// from the repo root with `OMNIPUS_HOME=pkg/gateway` (or any
		// relative override). Normalising to an absolute path keeps the
		// home directory stable across CWD changes and the cost-tracker
		// pointing at the right location. A warning is logged so a future
		// operator who set a relative value by accident notices the
		// resolution and can switch to an absolute path.
		if !filepath.IsAbs(override) {
			abs, absErr := filepath.Abs(override)
			if absErr == nil {
				logger.WarnCF("config", "OMNIPUS_HOME was relative; resolved against CWD",
					map[string]any{"original": override, "resolved": abs})
				return filepath.Clean(abs)
			}
			logger.WarnCF(
				"config",
				"OMNIPUS_HOME is relative and could not be resolved to an absolute path; using verbatim",
				map[string]any{"original": override, "error": absErr.Error()},
			)
			return filepath.Clean(override)
		}
		return filepath.Clean(override)
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		tempDir, mkErr := os.MkdirTemp(os.TempDir(), "omnipus-")
		if mkErr != nil {
			logger.ErrorCF(
				"config",
				"UserHomeDir failed and could not create secure temp dir; data isolation not guaranteed",
				map[string]any{"error": err.Error(), "mkdir_error": mkErr.Error()},
			)
			return os.TempDir()
		}
		// MkdirTemp creates with 0700 on Unix; re-assert in case of platform variance.
		if chErr := os.Chmod(tempDir, 0o700); chErr != nil {
			logger.WarnCF("config", "Could not set 0700 on temp home dir",
				map[string]any{"path": tempDir, "error": chErr.Error()})
		}
		logger.WarnCF("config", "UserHomeDir failed; falling back to user-private temp directory",
			map[string]any{"error": err.Error(), "fallback": tempDir})
		return tempDir
	}
	return filepath.Join(userHome, pkg.DefaultOmnipusHome)
}
