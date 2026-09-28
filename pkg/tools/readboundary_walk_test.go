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
)

// TestReadBoundary_WalkWithholdsCarveOuts is test 7: greping the Omnipus
// home itself is admitted (DS-1 row 13, D10 reach), session files outside
// the secret set are searchable, and every protected entry met during the
// walk is withheld by name and content.
//
// Traces: S-2.1; FR-005.
func TestReadBoundary_WalkWithholdsCarveOuts(t *testing.T) {
	f := newRBFixture(t)
	rbWrite(t, filepath.Join(f.home, "master.key"), "needle-secret-master\n")
	rbWrite(t, filepath.Join(f.home, "config.json"), "{\"k\": \"needle-secret-config\"}\n")
	rbWrite(t, filepath.Join(f.home, "agents", "other", "x.md"), "needle-secret-other-agent\n")
	rbWrite(t, filepath.Join(f.home, "sessions", "s1.jsonl"), "{\"content\": \"needle in a session\"}\n")

	res := f.grepCall("needle", rbStr(f.home))
	if res.IsError {
		t.Fatalf("grep path=$OMNIPUS_HOME: DS-1 row 13 says admitted, got error: %s", res.ForLLM)
	}
	hits := rbHitPaths(res.ForLLM)
	wantSession := rbSlash(filepath.Join(f.home, "sessions", "s1.jsonl"))
	found := false
	for _, h := range hits {
		if h == wantSession {
			found = true
		}
		for _, banned := range []string{"master.key", "config.json", "/agents/other"} {
			if strings.Contains(h, banned) {
				t.Errorf("S-2.1: protected entry surfaced as a match: %q", h)
			}
		}
	}
	if !found {
		t.Errorf("S-2.1: %q (absolute) must be in the result; got %q", wantSession, hits)
	}
	if strings.Contains(res.ForLLM, "needle-secret-") {
		t.Errorf("S-2.1: protected content leaked into the result:\n%s", res.ForLLM)
	}
}

// TestReadBoundary_SkillsRegistryGate is test 8: registry skill instruction
// files never match (name or content), their helpers do, a path naming one
// is refused like read_file refuses it, and project-shelf instruction files
// stay searchable exactly as read_file reads them.
//
// Deviation from S-2.4's wording, stated: S-2.4 says "greps with no
// `path`". A project shelf lives under a DOT folder (`.omnipus/skills` or
// `.claude/skills`, resolvepath.go::skillsGateProjectSubdirs) and grep's
// engine prunes hidden entries during every walk (filegrep's IncludeHidden
// is never set by the tool), so a no-`path` search can never reach it on
// any code, before or after #920. The subtests below keep S-2.4's oracle —
// "the match is returned, exactly as read_file would read it" — by naming
// the shelf folder as `path`, by shorthand and by absolute
// path. The wording conflict is reported as a finding.
//
// Traces: S-2.2, S-2.3, S-2.4; FR-006.
func TestReadBoundary_SkillsRegistryGate(t *testing.T) {
	seedRegistry := func(t *testing.T, f *rbFixture) (skills, demo string) {
		t.Helper()
		skills = filepath.Join(f.home, "skills")
		demo = filepath.Join(skills, "demo")
		for _, leaf := range []string{"SKILL.md", "AGENT.md", "AGENTS.md"} {
			rbWrite(t, filepath.Join(demo, leaf), "needle-instruction-"+leaf+"\n")
		}
		rbWrite(t, filepath.Join(demo, "scripts", "needle-helper.py"), "print('needle-helper-content')\n")
		return skills, demo
	}

	t.Run("S-2.2 registry instruction files withheld, helper matches by name and content", func(t *testing.T) {
		f := newRBFixture(t)
		skills, demo := seedRegistry(t, f)
		res := f.grepCall("needle", rbStr(skills))
		if res.IsError {
			t.Fatalf("grep path=$OMNIPUS_HOME/skills: DS-1 row 15 says admitted, got error: %s", res.ForLLM)
		}
		helper := rbSlash(filepath.Join(demo, "scripts", "needle-helper.py"))
		if !strings.Contains(res.ForLLM, helper+"  (name match)") {
			t.Errorf("S-2.2: the helper must match by NAME; want line %q in:\n%s", helper+"  (name match)", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, helper+":1:") {
			t.Errorf("S-2.2: the helper must match by CONTENT; want %q in:\n%s", helper+":1:", res.ForLLM)
		}
		for _, h := range rbHitPaths(res.ForLLM) {
			for _, leaf := range []string{"SKILL.md", "AGENT.md", "AGENTS.md"} {
				if strings.HasSuffix(h, "/"+leaf) {
					t.Errorf("S-2.2: registry instruction file %q matched", h)
				}
			}
		}
		if strings.Contains(res.ForLLM, "needle-instruction-") {
			t.Errorf("S-2.2: registry instruction content leaked:\n%s", res.ForLLM)
		}
		// Name probe: a pattern that matches only the instruction file
		// NAMES must not surface them either ("by name", S-2.2).
		byName := f.grepCall("SKILL", rbStr(skills))
		if byName.IsError {
			t.Fatalf("name probe grep failed: %s", byName.ForLLM)
		}
		for _, h := range rbHitPaths(byName.ForLLM) {
			if strings.HasSuffix(h, "/SKILL.md") {
				t.Errorf("S-2.2: registry SKILL.md surfaced by name: %q", h)
			}
		}
	})

	t.Run("S-2.3 a path naming a registry instruction file is refused like read_file", func(t *testing.T) {
		f := newRBFixture(t)
		_, demo := seedRegistry(t, f)
		skillMD := filepath.Join(demo, "SKILL.md")
		res := f.grepCall("needle", rbStr(skillMD))
		if !res.IsError {
			t.Fatalf("S-2.3: grep path=%q must be refused, got success:\n%s", skillMD, res.ForLLM)
		}
		if strings.Contains(res.ForLLM, "needle-instruction-") {
			t.Fatalf("S-2.3: refusal leaked the instruction content: %s", res.ForLLM)
		}
		if rr := f.read.Execute(f.ctx, map[string]any{"path": skillMD}); !rr.IsError {
			t.Fatalf("S-2.3 parity: read_file of the registry SKILL.md must be refused, got: %s", rr.ForLLM)
		}
		// "Refused like read_file" (S-2.3) — by the same decision, not by a
		// grep-only lexical rule: grep's refusal row carries the reason
		// read_file records for the same path (FR-020, MV-2).
		rows := f.rows(t)
		g := rbRowsFor(rows, rbAccessDeniedEvent, "grep")
		r := rbRowsFor(rows, rbAccessDeniedEvent, "read_file")
		if len(g) != 1 || len(r) != 1 {
			t.Fatalf("S-2.3: want one path.access_denied row each for grep and read_file, got grep=%d read_file=%d: %+v", len(g), len(r), rows)
		}
		if g[0].detail("reason") != r[0].detail("reason") {
			t.Errorf("S-2.3 / MV-2: grep reason %q != read_file reason %q for the same registry SKILL.md", g[0].detail("reason"), r[0].detail("reason"))
		}
	})

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
