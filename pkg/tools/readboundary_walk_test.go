// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// #920 RED pack — US-2 (a wider search never shows what reading would
// refuse). Tests 7-10 of read-boundary-consistency-spec.md.
//
// Oracles: S-2.1..S-2.7, DS-1 rows 13-19, FR-005..FR-008, ADR-072 D10.3,
// decision D11 (grep stricter on names than list_directory).
package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadBoundary_WalkWithholdsCarveOuts preserves the wide-walk oracle.
// L1-AMENDMENTS-20261013.md §2 D1/D2 and §2.4 supersede DS-1 row 13:
// the home root and sessions are now protected; ordinary external files stay
// searchable and no protected names/content may surface during a wider walk.
func TestReadBoundary_WalkWithholdsCarveOuts(t *testing.T) {
	f := newRBFixture(t)
	rbWrite(t, filepath.Join(f.home, "master.key"), "needle-secret-master\n")
	rbWrite(t, filepath.Join(f.home, "config.json"), "{\"k\": \"needle-secret-config\"}\n")
	rbWrite(t, filepath.Join(f.home, "agents", "other", "x.md"), "needle-secret-other-agent\n")
	rbWrite(t, filepath.Join(f.home, "sessions", "s1.jsonl"), "{\"content\": \"needle-secret-session\"}\n")

	requireInternalFileRefusal(t, f.grepCall("needle", rbStr(f.home)), "grep")
	res := f.grepCall("needle", rbStr(filepath.Dir(f.home)))
	if res.IsError {
		t.Fatalf("wide external search must run: %s", res.ForLLM)
	}
	hits := rbHitPaths(res.ForLLM)
	wantExternal := rbSlash(filepath.Join(f.ext, "ext", "notes.txt"))
	found := false
	for _, h := range hits {
		if h == wantExternal {
			found = true
		}
		for _, banned := range []string{"master.key", "config.json", "/agents/other", "/sessions/"} {
			if strings.Contains(h, banned) {
				t.Errorf("protected entry surfaced as a match: %q", h)
			}
		}
	}
	if !found {
		t.Errorf("external positive control %q must match; got %q", wantExternal, hits)
	}
	if strings.Contains(res.ForLLM, "needle-secret-") {
		t.Errorf("protected content leaked into the result:\n%s", res.ForLLM)
	}
}

// TestReadBoundary_SkillsRegistryGate retains #920's read/search/audit parity
// and project-shelf controls. r15 §6.1 and S-2.1–4 supersede its old leaf-only
// gate: registry/control roots are denied to everyone; an ungranted non-editor
// cannot search any part of a skill; a grant or editor permission admits the
// whole ordinary folder, including instructions and helper names/content.
// Project shelves are still probed explicitly because hidden shelf directories
// are not descended by a default grep walk (#920 S-2.4's recorded limitation).
func TestReadBoundary_SkillsRegistryGate(t *testing.T) {
	for _, editor := range []bool{false, true} {
		label := "non-editor"
		if editor {
			label = "editor"
		}
		t.Run("r15 registry and control roots refused/"+label, func(t *testing.T) {
			f, root, _ := newRBSkillsFixture(t, true, editor)
			for _, target := range []string{root, filepath.Join(root, ".staging"), filepath.Join(root, ".swap"), filepath.Join(root, "invalid.name")} {
				res := f.grepCall("needle", rbStr(target))
				require.True(t, res.IsError, "registry/control search must be refused: %s", res.ForLLM)
				assert.Contains(t, res.ForLLM, ErrCarveOut.Error(), "not a missing-file or directory error")
				assert.NotContains(t, res.ForLLM, "needle-instruction-")
				read := f.read.Execute(f.ctx, map[string]any{"path": target})
				require.True(t, read.IsError, "registry/control read must be refused: %s", read.ForLLM)
				assert.Contains(t, read.ForLLM, ErrCarveOut.Error())
			}
		})
	}

	for _, authority := range []struct {
		name    string
		granted bool
		editor  bool
	}{
		{"granted non-editor", true, false},
		{"ungranted editor", false, true},
		{"granted editor", true, true},
	} {
		t.Run("r15 whole authorized folder/"+authority.name, func(t *testing.T) {
			f, _, demo := newRBSkillsFixture(t, authority.granted, authority.editor)
			res := f.grepCall("needle", rbStr(demo))
			require.False(t, res.IsError, "authorized folder must really be searched: %s", res.ForLLM)
			want := []string{
				rbSlash(filepath.Join(demo, "SKILL.md")), rbSlash(filepath.Join(demo, "AGENT.md")),
				rbSlash(filepath.Join(demo, "AGENTS.md")), rbSlash(filepath.Join(demo, "scripts", "needle-helper.py")),
			}
			assert.ElementsMatch(t, want, rbHitPaths(res.ForLLM), "the whole folder, not helpers alone, is searchable")
			helper := rbSlash(filepath.Join(demo, "scripts", "needle-helper.py"))
			assert.Contains(t, res.ForLLM, helper+"  (name match)", "helper name matching is retained")
			assert.Contains(t, res.ForLLM, helper+":1:", "helper content matching is retained")
			for _, leaf := range []string{"SKILL.md", "AGENT.md", "AGENTS.md"} {
				path := rbSlash(filepath.Join(demo, leaf))
				assert.Contains(t, res.ForLLM, "needle-instruction-"+leaf)
				byName := f.grepCall(leaf, rbStr(demo))
				require.False(t, byName.IsError, "authorized name search: %s", byName.ForLLM)
				assert.Equal(t, []string{path}, rbHitPaths(byName.ForLLM), "only the requested instruction filename matches")
				assert.Contains(t, byName.ForLLM, path+"  (name match)")
				read := f.read.Execute(f.ctx, map[string]any{"path": filepath.Join(demo, leaf)})
				require.False(t, read.IsError, "authorized grep/read parity: %s", read.ForLLM)
				assert.Contains(t, read.ForLLM, "needle-instruction-"+leaf)
			}
			read := f.read.Execute(f.ctx, map[string]any{"path": filepath.Join(demo, "scripts", "needle-helper.py")})
			require.False(t, read.IsError, "authorized helper read: %s", read.ForLLM)
			assert.Contains(t, read.ForLLM, "print('needle-helper-content')")
		})
	}

	for _, leaf := range []string{"SKILL.md", "AGENT.md", "AGENTS.md", "scripts/needle-helper.py"} {
		t.Run("r15 ungranted whole-folder refusal and audit parity/"+leaf, func(t *testing.T) {
			f, _, demo := newRBSkillsFixture(t, false, false)
			target := filepath.Join(demo, filepath.FromSlash(leaf))
			res := f.grepCall("needle", rbStr(target))
			require.True(t, res.IsError, "ungranted instruction/helper search must be refused: %s", res.ForLLM)
			assert.Contains(t, res.ForLLM, ErrCarveOut.Error())
			assert.NotContains(t, res.ForLLM, "needle-instruction-")
			assert.NotContains(t, res.ForLLM, "needle-helper-content")
			read := f.read.Execute(f.ctx, map[string]any{"path": target})
			require.True(t, read.IsError, "ungranted grep/read parity: %s", read.ForLLM)
			assert.Contains(t, read.ForLLM, ErrCarveOut.Error())
			assert.NotContains(t, read.ForLLM, "needle-instruction-")
			assert.NotContains(t, read.ForLLM, "needle-helper-content")
			rows := f.rows(t)
			g := rbRowsFor(rows, rbAccessDeniedEvent, "grep")
			r := rbRowsFor(rows, rbAccessDeniedEvent, "read_file")
			require.Len(t, g, 1, "one real grep path.access_denied row")
			require.Len(t, r, 1, "one real read_file path.access_denied row")
			require.NotEmpty(t, g[0].detail("reason"), "audit parity cannot be two absent reasons")
			assert.Equal(t, r[0].detail("reason"), g[0].detail("reason"), "same path, same canonical read decision")
		})
	}

	t.Run("S-2.4 project-shelf instruction file stays searchable", func(t *testing.T) {
		f := newRBFixture(t)
		shelfRel := filepath.Join(".omnipus", "skills", "proj")
		rbWrite(t, filepath.Join(f.mnt, shelfRel, "SKILL.md"), "needle-project-skill\n")

		viaShorthand := f.grepCall("needle", rbStr(rbMountName+"/"+filepath.ToSlash(shelfRel)))
		if viaShorthand.IsError {
			t.Fatalf("grep of the project shelf by mount shorthand failed: %s", viaShorthand.ForLLM)
		}
		wantShort := rbMountName + "/" + filepath.ToSlash(shelfRel) + "/SKILL.md"
		if got := rbHitPaths(viaShorthand.ForLLM); !rbSameSet(got, []string{wantShort}) {
			t.Errorf("S-2.4 (shorthand): match paths = %q, want exactly %q", got, []string{wantShort})
		}

		absShelf := filepath.Join(f.mnt, shelfRel)
		viaAbs := f.grepCall("needle", rbStr(absShelf))
		if viaAbs.IsError {
			t.Fatalf("S-2.4 (absolute, DS-1 row 16): grep path=%q must be admitted, got error: %s", absShelf, viaAbs.ForLLM)
		}
		wantAbs := rbSlash(filepath.Join(absShelf, "SKILL.md"))
		if got := rbHitPaths(viaAbs.ForLLM); !rbSameSet(got, []string{wantAbs}) {
			t.Errorf("S-2.4 (absolute): match paths = %q, want exactly %q", got, []string{wantAbs})
		}
		if rr := f.read.Execute(f.ctx, map[string]any{"path": filepath.Join(absShelf, "SKILL.md")}); rr.IsError ||
			!strings.Contains(rr.ForLLM, "needle-project-skill") {
			t.Fatalf("S-2.4 parity: read_file of the project-shelf SKILL.md must succeed, got IsError=%v: %s", rr.IsError, rr.ForLLM)
		}
	})
}

// TestReadBoundary_MetadataGuardInWalk is test 9: agent metadata files
// inside a searched root never produce a match, by name or content, and
// read_file of each is refused (D11: list_directory is deliberately left
// unchanged, so it is not asserted here). Five examples, each in its own
// fixture so a case-insensitive filesystem cannot merge SOUL.md and soul.md.
//
// Traces: S-2.5; FR-007.
func TestReadBoundary_MetadataGuardInWalk(t *testing.T) {
	examples := []struct {
		meta        string
		namePattern string // matches the file NAME only (smart case)
	}{
		{"SOUL.md", "SOUL"},
		{"HEARTBEAT.md", "HEARTBEAT"},
		{"AGENT.md", "AGENT"},
		{"MEMORY.md", "MEMORY"},
		{"soul.md", "soul"},
	}
	for _, ex := range examples {
		t.Run(ex.meta, func(t *testing.T) {
			f := newRBFixture(t)
			rel := "agents/a1/" + ex.meta
			rbWrite(t, filepath.Join(f.ws, "agents", "a1", ex.meta), "needle-metadata-content\n")

			for _, pattern := range []string{"needle", ex.namePattern} {
				res := f.grepCall(pattern, nil)
				if res.IsError {
					t.Fatalf("grep %q with no path failed: %s", pattern, res.ForLLM)
				}
				for _, h := range rbHitPaths(res.ForLLM) {
					if strings.EqualFold(h, rel) {
						t.Errorf("S-2.5: metadata file %q matched pattern %q", h, pattern)
					}
				}
				if strings.Contains(res.ForLLM, "needle-metadata-content") {
					t.Errorf("S-2.5: metadata content leaked for pattern %q:\n%s", pattern, res.ForLLM)
				}
			}
			if rr := f.read.Execute(f.ctx, map[string]any{"path": rel}); !rr.IsError {
				t.Fatalf("S-2.5: read_file(%q) must be refused, got: %s", rel, rr.ForLLM)
			}
		})
	}
}

// rbSymlink creates a symlink or skips where the platform refuses one
// (precedent: auto_approve_test.go::swapToSymlink).
func rbSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
}

// TestReadBoundary_WalkSymlinksNotFollowed is test 10a (S-2.6): symlinks met
// during a walk are never followed, and each link may still match by name.
// Passes on today's code BY DESIGN — "unchanged" per DS-1 row 19; it is the
// agent-side regression pin that replaces TestFileGrep_SymlinkConfinement's
// agent-side role.
//
// Traces: S-2.6; FR-008.
func TestReadBoundary_WalkSymlinksNotFollowed(t *testing.T) {
	f := newRBFixture(t)
	rbSymlink(t, filepath.Join(f.ext, "ext"), filepath.Join(f.ws, "link-out"))
	rbSymlink(t, filepath.Join(f.ws, "notes"), filepath.Join(f.ws, "link-in"))

	res := f.grepCall("needle", nil)
	if res.IsError {
		t.Fatalf("grep with no path failed: %s", res.ForLLM)
	}
	want := []string{"notes/a.md", rbMountName + "/src/b.md"}
	if got := rbHitPaths(res.ForLLM); !rbSameSet(got, want) {
		t.Fatalf("S-2.6: match paths = %q, want exactly %q (nothing behind link-out or link-in)", got, want)
	}

	byName := f.grepCall("link", nil)
	if byName.IsError {
		t.Fatalf("grep \"link\" failed: %s", byName.ForLLM)
	}
	for _, name := range []string{"link-out", "link-in"} {
		if !strings.Contains(byName.ForLLM, name+"  (name match)") {
			t.Errorf("S-2.6: %q must be returned as a name match; got:\n%s", name, byName.ForLLM)
		}
	}
}

// TestReadBoundary_SymlinkPathResolvesLikeRead is test 10b (S-2.7): a
// symlink named as `path` resolves to its target location and is judged
// like read_file/list_directory judge it; the hit is absolute because the
// resolved location lies outside the workspace (DS-1 row 18).
//
// Traces: S-2.7; FR-001, FR-008.
func TestReadBoundary_SymlinkPathResolvesLikeRead(t *testing.T) {
	f := newRBFixture(t)
	rbSymlink(t, filepath.Join(f.ext, "ext"), filepath.Join(f.ws, "link-out"))

	res := f.grepCall("needle", rbStr("link-out"))
	if res.IsError {
		t.Fatalf("S-2.7: grep path=link-out must search the link's target, got error: %s", res.ForLLM)
	}
	want := []string{rbSlash(filepath.Join(f.ext, "ext", "notes.txt"))}
	if got := rbHitPaths(res.ForLLM); !rbSameSet(got, want) {
		t.Fatalf("S-2.7: match paths = %q, want exactly %q (absolute target location)", got, want)
	}
	lr := f.list.Execute(f.ctx, map[string]any{"path": "link-out"})
	if lr.IsError || !strings.Contains(lr.ForLLM, "notes.txt") {
		t.Fatalf("S-2.7 parity: list_directory(link-out) must list notes.txt, got IsError=%v: %s", lr.IsError, lr.ForLLM)
	}
}
