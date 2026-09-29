// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// #920 RED pack — test 24 of read-boundary-consistency-spec.md: the tool
// descriptions agents read meet FR-024's text requirements (DR-G1..DR-G8 for
// grep, DR-R1..DR-R3 for read_file and list_directory).
//
// The spec names requirements, not wording, so each check is the loosest
// marker that can only be satisfied by text stating the requirement:
// prometheus-prompt-engineer keeps full freedom of phrasing. Banned phrases
// are only the OLD confinement claims (dispatcher ruling 2026-09-26: DR-G4
// itself requires "never reachable" for protected, other-agent and
// other-workspace files, so that phrase is required, not banned).
package tools

import (
	"fmt"
	"strings"
	"testing"
	"unicode"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/filegrep"
)

func rbHasEmoji(s string) bool {
	for _, r := range s {
		if r >= 0x1F000 || (r >= 0x2600 && r <= 0x27BF) || r == 0xFE0F {
			return true
		}
		if unicode.Is(unicode.So, r) {
			return true
		}
	}
	return false
}

func rbGrepPathParamDescription(t *testing.T, g *GrepTool) string {
	t.Helper()
	props, _ := g.Parameters()["properties"].(map[string]any)
	p, _ := props["path"].(map[string]any)
	d, _ := p["description"].(string)
	if d == "" {
		t.Fatal("precondition: grep's `path` parameter must carry a description")
	}
	return d
}

// TestGrepDescription_Requirements is test 24 for grep.
//
// Traces: S-6.1; FR-024 (DR-G1..DR-G8).
func TestGrepDescription_Requirements(t *testing.T) {
	g := NewGrepTool("", false)
	desc := g.Description()
	lower := strings.ToLower(desc)
	pathDesc := strings.ToLower(rbGrepPathParamDescription(t, g))

	t.Run("DR-G1 no claim that search is confined to the workspace and mounts", func(t *testing.T) {
		for _, banned := range []string{
			"never anywhere else",
			"one subdirectory of your workspace",
			"only your workspace",
			"no workspace_id argument, so another",
		} {
			if strings.Contains(lower, banned) {
				t.Errorf("DR-G1: description still contains the superseded confinement claim %q", banned)
			}
			if strings.Contains(pathDesc, banned) {
				t.Errorf("DR-G1: `path` parameter description still contains the superseded confinement claim %q", banned)
			}
		}
	})

	t.Run("DR-G2 default area is the workspace plus its mounted folders", func(t *testing.T) {
		if !strings.Contains(lower, "workspace") || !strings.Contains(lower, "mount") {
			t.Errorf("DR-G2: description must name the workspace and its mounted folders as the default area:\n%s", desc)
		}
	})

	t.Run("DR-G3 path may be relative, a mount name, or absolute, reaching what read_file reads", func(t *testing.T) {
		for _, need := range []string{"absolute", "read_file"} {
			if !strings.Contains(lower, need) {
				t.Errorf("DR-G3: description must say `path` may be absolute and reaches what read_file can read; missing %q:\n%s", need, desc)
			}
		}
	})

	t.Run("DR-G4 protected, other agents' and other workspaces' files are never reachable", func(t *testing.T) {
		if !strings.Contains(lower, "never reachable") {
			t.Errorf("DR-G4: description must state these files are never reachable:\n%s", desc)
		}
		if !strings.Contains(lower, "protected") {
			t.Errorf("DR-G4: description must name Omnipus's protected files:\n%s", desc)
		}
	})

	t.Run("DR-G5 match-path rule: workspace-relative inside, absolute elsewhere", func(t *testing.T) {
		if !strings.Contains(lower, "relative") || !strings.Contains(lower, "absolute") {
			t.Errorf("DR-G5: description must state the relative/absolute match-path rule:\n%s", desc)
		}
	})

	t.Run("DR-G6 symlinks met while searching are not followed", func(t *testing.T) {
		if !strings.Contains(lower, "symlink") && !strings.Contains(lower, "symbolic link") {
			t.Errorf("DR-G6: description must say symlinks met while searching are not followed:\n%s", desc)
		}
	})

	t.Run("DR-G7 every limit is rendered from the constants", func(t *testing.T) {
		for _, n := range []string{
			fmt.Sprintf("%d MiB", filegrep.PerFileContentCap>>20),
			fmt.Sprintf("%d", filegrep.DefaultMaxMatches),
			fmt.Sprintf("%d", filegrep.DefaultMatchesPerFile),
			fmt.Sprintf("%d", config.DefaultBuiltinSuccessCap),
		} {
			if !strings.Contains(desc, n) {
				t.Errorf("DR-G7: description must render the limit %q from its constant:\n%s", n, desc)
			}
		}
	})

	t.Run("DR-G8 no approval, Auto-approve or policy mechanics", func(t *testing.T) {
		for _, banned := range []string{"approv", "auto-approve", "policy"} {
			if strings.Contains(lower, banned) {
				t.Errorf("DR-G8: description must not mention %q", banned)
			}
		}
	})

	t.Run("plain text, no emoji", func(t *testing.T) {
		if rbHasEmoji(desc) {
			t.Error("FR-024: description must contain no emoji")
		}
	})
}

// TestReadListDescription_Requirements is test 24 for read_file and
// list_directory. Passes on today's text BY DESIGN: the spec lists both as
// "reviewed / rewritten", and today's text already meets DR-R1..DR-R3; this
// pins that a rewrite keeps meeting them.
//
// Traces: S-6.1; FR-024 (DR-R1..DR-R3).
func TestReadListDescription_Requirements(t *testing.T) {
	for _, tl := range []Tool{NewReadFileTool("", false, 0), NewListDirTool("", false)} {
		t.Run(tl.Name(), func(t *testing.T) {
			desc := tl.Description()
			lower := strings.ToLower(desc)
			if strings.TrimSpace(desc) == "" {
				t.Fatal("DR-R2: description must not be empty")
			}
			for _, banned := range []string{
				"only inside the workspace", "only within the workspace", "limited to the workspace",
				"outside the workspace asks", "outside the workspace, it asks",
			} {
				if strings.Contains(lower, banned) {
					t.Errorf("DR-R1: %s description implies reads are limited to the workspace or ask outside it (%q)", tl.Name(), banned)
				}
			}
			for _, banned := range []string{"approv", "auto-approve", "policy"} {
				if strings.Contains(lower, banned) {
					t.Errorf("DR-R3: %s description must not mention %q", tl.Name(), banned)
				}
			}
			if rbHasEmoji(desc) {
				t.Errorf("FR-024: %s description must contain no emoji", tl.Name())
			}
		})
	}
}
