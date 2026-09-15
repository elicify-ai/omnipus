// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// TestGrepTool_InvalidMountRecordNeverBecomesASearchRoot pins the boundary
// property grep depends on but does not itself enforce: an entry in a
// hand-edited or partially-migrated mount record that Mount.Validate rejects
// must never become a live search root, and must never appear as the Name
// prefix on a reported hit.
//
// grep reads mounts through workspace.LoadMounts, and loadMountStore runs
// Mount.Validate over every entry before returning any of them (dropping the
// failures with a WARN) — so the property holds at the loader, one layer
// below this tool, for grep exactly as it does for the resolver's own
// workspace.AllowedMountRoots. This test asserts the OUTCOME at grep's own
// surface rather than the mechanism, so it keeps holding if either layer is
// rearranged, and starts failing the moment an invalid entry can reach a
// search root by any route.
func TestGrepTool_InvalidMountRecordNeverBecomesASearchRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	const wsID, agentID = "ws-mv", "agent-mv"
	work := seedGrepWorkspace(t, home, wsID, agentID)

	// One legitimate mount (the control: valid entries must still work) and
	// four entries covering every shape Mount.Validate refuses.
	valid := t.TempDir()
	mustWriteFile(t, filepath.Join(valid, "ok.txt"), "mount-token-valid\n")

	badNameDir := t.TempDir()
	mustWriteFile(t, filepath.Join(badNameDir, "leak.txt"), "mount-token-badname\n")
	badSepDir := t.TempDir()
	mustWriteFile(t, filepath.Join(badSepDir, "leak.txt"), "mount-token-separator\n")
	badDotDotDir := t.TempDir()
	mustWriteFile(t, filepath.Join(badDotDotDir, "leak.txt"), "mount-token-dotdot\n")
	uncleanDir := t.TempDir()
	mustWriteFile(t, filepath.Join(uncleanDir, "leak.txt"), "mount-token-unclean\n")

	record := map[string]any{
		"workspace_id": wsID,
		"mounts": []map[string]string{
			{"name": "good", "host_path": valid},
			// Refused by ValidateMountName: empty.
			{"name": "", "host_path": badNameDir},
			// Refused by ValidateMountName: contains a path separator.
			{"name": "nested/name", "host_path": badSepDir},
			// Refused by ValidateMountName: contains "..".
			{"name": "..", "host_path": badDotDotDir},
			// Refused by Mount.Validate: host_path is not in cleaned form.
			{"name": "unclean", "host_path": uncleanDir + "/./"},
		},
	}
	storePath, err := workspace.MountStorePath(home, wsID)
	if err != nil {
		t.Fatalf("mount store path: %v", err)
	}
	if mkErr := os.MkdirAll(filepath.Dir(storePath), 0o700); mkErr != nil {
		t.Fatalf("mkdir mount store: %v", mkErr)
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	if wErr := os.WriteFile(storePath, data, 0o600); wErr != nil {
		t.Fatalf("write hand-edited mount record: %v", wErr)
	}

	// The loader's own verdict, asserted here so a failure downstream is
	// unambiguous about which layer changed.
	loaded, ok := workspace.LoadMounts(home, wsID)
	if !ok {
		t.Fatalf("hand-edited record must still load (invalid ENTRIES are dropped, not the file)")
	}
	if len(loaded) != 1 || loaded[0].Name != "good" {
		t.Fatalf("loader must drop every invalid entry and keep only \"good\", got %+v", loaded)
	}

	// The two mount readers must agree, entry for entry. grep needs each
	// mount's NAME (it prefixes every reported hit with it), which
	// AllowedMountRoots does not carry — so grep reads workspace.LoadMounts
	// and the resolver reads workspace.AllowedMountRoots. That is only safe
	// while the two see the SAME set, which they do because
	// AllowedMountRoots is itself LoadMounts plus a re-run of the validation
	// LoadMounts already performed. Asserting the equality here means a
	// future divergence — a rule added to one reader and not the other —
	// fails a test instead of quietly giving grep a wider set of roots than
	// read_file.
	granted := workspace.AllowedMountRoots(home, wsID)
	if len(granted) != len(loaded) {
		t.Fatalf("mount readers disagree: LoadMounts=%d entries, AllowedMountRoots=%d entries", len(loaded), len(granted))
	}
	for i, m := range loaded {
		if granted[i] != m.HostPath {
			t.Fatalf("mount readers disagree at %d: LoadMounts=%q AllowedMountRoots=%q", i, m.HostPath, granted[i])
		}
	}

	tool := NewGrepTool(work, true)
	ctx := WithTurnWorkspaceDir(WithAgentID(context.Background(), agentID), work)

	t.Run("no invalid entry contributes content", func(t *testing.T) {
		res := tool.Execute(ctx, map[string]any{"pattern": "mount-token-"})
		if res.IsError {
			t.Fatalf("grep must run: %s", res.ForLLM)
		}
		for _, leak := range []string{
			"mount-token-badname", "mount-token-separator", "mount-token-dotdot", "mount-token-unclean",
		} {
			if strings.Contains(res.ForLLM, leak) {
				t.Errorf("an invalid mount entry became a search root and served %q:\n%s", leak, res.ForLLM)
			}
		}
		if !strings.Contains(res.ForLLM, "good/ok.txt:1:") {
			t.Fatalf("the VALID mount must still be searched (control case):\n%s", res.ForLLM)
		}
	})

	t.Run("no invalid entry is addressable by name via `path`", func(t *testing.T) {
		for _, scope := range []string{"nested/name", "unclean", ".."} {
			res := tool.Execute(ctx, map[string]any{"pattern": "mount-token-", "path": scope})
			if !res.IsError && !strings.Contains(res.ForLLM, "0 match(es)") {
				t.Errorf("path=%q reached an invalid mount entry:\n%s", scope, res.ForLLM)
			}
		}
	})
}
