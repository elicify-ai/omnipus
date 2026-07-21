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
//  1. $OMNIPUS_HOME (the user's explicit override — trusted verbatim)
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
		return override
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
