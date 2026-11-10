// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// IsolatedHome is a throwaway user home a test binary runs under, so that no test
// can resolve, read or write the developer's real ~/.omnipus. A test that falls
// back to "$HOME/.omnipus" lands in Dir instead, and Leaks reports it.
type IsolatedHome struct {
	// Dir is the temp directory HOME (USERPROFILE on Windows) now points at.
	Dir string
	// OmnipusDir is Dir/.omnipus: the path every "$HOME/.omnipus" fallback resolves
	// to while the binary runs.
	OmnipusDir string
}

// IsolateProcessHome points HOME (and USERPROFILE) at a fresh temp directory for
// the rest of the process. Call it first in TestMain, before m.Run().
//
// Why a guard and not a snapshot of the real home: the developer's real home
// belongs to a live instance that changes independently, so a before/after
// comparison would fail for reasons unrelated to the tests and would read real
// data. Redirecting the home means the tests never touch it at all, and anything
// they create under Dir/.omnipus is by construction a test that did not isolate
// itself (OMNIPUS_HOME unset, or a config built before the isolation).
//
// GOMODCACHE and GOCACHE default to paths under $HOME, so they are pinned to their
// current values first; otherwise a child `go` command would see an empty cache.
func IsolateProcessHome() (*IsolatedHome, error) {
	for _, key := range []string{"GOMODCACHE", "GOCACHE"} {
		if os.Getenv(key) != "" {
			continue
		}
		out, err := exec.Command("go", "env", key).Output()
		if err != nil {
			continue
		}
		if v := strings.TrimSpace(string(out)); v != "" {
			if err := os.Setenv(key, v); err != nil {
				return nil, fmt.Errorf("pin %s: %w", key, err)
			}
		}
	}
	dir, err := os.MkdirTemp("", "omnipus-test-home-*")
	if err != nil {
		return nil, fmt.Errorf("create the isolated home: %w", err)
	}
	if err := os.Setenv("HOME", dir); err != nil {
		return nil, fmt.Errorf("set HOME: %w", err)
	}
	if runtime.GOOS == "windows" {
		if err := os.Setenv("USERPROFILE", dir); err != nil {
			return nil, fmt.Errorf("set USERPROFILE: %w", err)
		}
	}
	return &IsolatedHome{Dir: dir, OmnipusDir: filepath.Join(dir, ".omnipus")}, nil
}

// Leaks lists what the run created under the isolated "$HOME/.omnipus", relative
// to it, in sorted order. Empty means every test kept to its own temp home.
func (h *IsolatedHome) Leaks() []string {
	var out []string
	_ = filepath.Walk(h.OmnipusDir, func(path string, info os.FileInfo, err error) error {
		//nolint:nilerr // an unreadable entry must not abort the sweep: the assertion is
		// "nothing was created here", and aborting would hide the rest.
		if err != nil || info == nil || path == h.OmnipusDir {
			return nil
		}
		if rel, relErr := filepath.Rel(h.OmnipusDir, path); relErr == nil {
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// Remove deletes the isolated home. Call it after the guard has run.
func (h *IsolatedHome) Remove() { _ = os.RemoveAll(h.Dir) }
