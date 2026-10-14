//go:build windows

// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// defaultPathextExtensions is the standard Windows executable-extension set,
// used when PATHEXT is unset or blank (matching how CreateProcess/LookPath
// resolve a bare command name).
func defaultPathextExtensions() []string {
	return []string{".COM", ".EXE", ".BAT", ".CMD"}
}

// currentUserCanExecute answers whether Windows would execute path: Windows
// keeps no execute mode bit to consult, so executability is by extension —
// the file's extension must be one PATHEXT admits, compared case-insensitively
// (issue #1274 fix round 1, CHECK F01). An existing plain .txt file is data,
// not a program, and is refused even though LookPath would find it.
func currentUserCanExecute(path string) error {
	ext := filepath.Ext(path)
	allowed := pathextExtensions()
	for _, candidate := range allowed {
		if ext != "" && strings.EqualFold(ext, candidate) {
			return nil
		}
	}
	return fmt.Errorf("extension %q is not in PATHEXT (%s)", ext, strings.Join(allowed, ";"))
}

// pathextExtensions parses %PATHEXT%, skipping empty segments and supplying
// missing leading dots. The matcher compares extensions case-insensitively.
// An unset or blank list falls back to the standard set.
func pathextExtensions() []string {
	raw := os.Getenv("PATHEXT")
	if strings.TrimSpace(raw) == "" {
		return defaultPathextExtensions()
	}
	out := make([]string, 0, 8)
	for _, e := range strings.Split(raw, ";") {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return defaultPathextExtensions()
	}
	return out
}
