// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Deletion guard (founder ruling, session-core U4): the SessionMessage kind
// `decision_request` has no producer — helpers report through message_parent's
// kinds — and was deleted outright from the contract, the generated Go/TS
// artifacts and every code path. This guard fails if any of them comes back.

package session

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRootForDeletionGuard(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

// bannedKindSpellings are the two spellings the deleted kind could return as:
// the wire value and the generated/Go identifier stem.
var bannedKindSpellings = []string{"decision" + "_request", "Decision" + "Request"}

func TestDecisionRequestKindStaysDeleted(t *testing.T) {
	root := repoRootForDeletionGuard(t)

	// Positive control for the instrument: the scan really reads these trees
	// (a kind that is present must be found), so an empty result means absent.
	probe := filepath.Join(root, "contracts", "components", "schemas", "SessionMessageProgress.yaml")
	if data, err := os.ReadFile(probe); err != nil || !strings.Contains(string(data), "progress") {
		t.Fatalf("instrument check failed: cannot read a known contract file %s: %v", probe, err)
	}

	if _, err := os.Stat(filepath.Join(root, "contracts", "components", "schemas", "SessionMessage"+bannedKindSpellings[1]+".yaml")); err == nil {
		t.Fatal("the deleted SessionMessage decision-request schema file is back under contracts/components/schemas")
	}

	scanRoots := []string{
		"contracts",
		filepath.Join("pkg", "api", "generated"),
		filepath.Join("pkg", "gateway", "inboundschemas"),
		filepath.Join("src", "lib", "api", "generated"),
		filepath.Join("pkg", "agent"),
		filepath.Join("pkg", "tools"),
		filepath.Join("pkg", "bus"),
		filepath.Join("pkg", "session"),
		"scripts",
	}
	for _, rel := range scanRoots {
		walkErr := filepath.WalkDir(filepath.Join(root, rel), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			// This guard names the spellings by necessity; the RED pack's
			// comment also records the (now moot) Q4 scope note.
			if strings.HasSuffix(path, "decision_request_deleted_test.go") ||
				strings.HasSuffix(path, "sessioncore_u4_report_class_test.go") {
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			for _, banned := range bannedKindSpellings {
				if strings.Contains(string(data), banned) {
					t.Errorf("%s mentions the deleted SessionMessage kind (%q): remove it — the kind was deleted by founder ruling", path, banned)
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("scan %s: %v", rel, walkErr)
		}
	}
}
