// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// #920 RED pack — US-1 (search reach equals read reach). Tests 1-6 and 19 of
// read-boundary-consistency-spec.md's Test Implementation Order.
//
// Oracles: DS-1 (parity matrix), DS-5 (unchanged limits), decisions D1 (one
// read decision for grep, read_file and list_directory), D5 (default area
// unchanged) and D7 (limits unchanged). No expected value below was read off
// the current code's output.
package tools

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rbReadParity runs the read-side tool DS-1 pairs with a grep row:
// read_file for a file, list_directory for a folder.
func (f *rbFixture) rbReadParity(t *testing.T, path string, isDir bool) *ToolResult {
	t.Helper()
	if isDir {
		return f.list.Execute(f.ctx, map[string]any{"path": path})
	}
	return f.read.Execute(f.ctx, map[string]any{"path": path})
}

// TestReadBoundary_ParityMatrix is test 1: DS-1's admitted rows (1-8, 22)
// for one ordinary (not read-confined) agent. For each row grep must admit
// the path with exactly the DS-1 match-path form, and the read-side tool
// must admit the same path (except the grep-only shorthand, row 3).
//
// Traces: S-1.1, S-1.2, S-1.3, S-1.7, S-1.8, S-1.12; FR-001, FR-002, FR-004.
func TestReadBoundary_ParityMatrix(t *testing.T) {
	f := newRBFixture(t)

	relToExt, err := filepath.Rel(f.ws, f.ext)
	if err != nil {
		t.Fatalf("filepath.Rel(ws, ext): %v", err)
	}
	relToMnt, err := filepath.Rel(f.ws, f.mnt)
	if err != nil {
		t.Fatalf("filepath.Rel(ws, mnt): %v", err)
	}
	if !strings.HasPrefix(filepath.ToSlash(relToExt), "../") || !strings.HasPrefix(filepath.ToSlash(relToMnt), "../") {
		t.Fatalf("fixture precondition: <EXT> and <MNT> must be reachable from <WS> only through \"..\" (got %q, %q)", relToExt, relToMnt)
	}

	extHit := rbSlash(filepath.Join(f.ext, "ext", "notes.txt"))
	mntHit := rbSlash(filepath.Join(f.mnt, "src", "b.md"))

	rows := []struct {
		row       string
		path      string
		wantHits  []string
		readIsDir bool
		grepOnly  bool // DS-1 row 3: the shorthand has no read-side parity
	}{
		{"DS-1 row 1 workspace-relative", "notes", []string{"notes/a.md"}, true, false},
		{"DS-1 row 2 absolute inside workspace", filepath.Join(f.ws, "notes"), []string{"notes/a.md"}, true, false},
		{"DS-1 row 3 mount-name shorthand", rbMountName + "/src", []string{rbMountName + "/src/b.md"}, true, true},
		{"DS-1 row 4 absolute in a mount", filepath.Join(f.mnt, "src"), []string{mntHit}, true, false},
		{"DS-1 row 5 absolute outside everything", f.ext, []string{extHit}, true, false},
		{"DS-1 row 6 dotdot escape outside", relToExt, []string{extHit}, true, false},
		{"DS-1 row 7 dotdot staying inside", "notes/../notes", []string{"notes/a.md"}, true, false},
		{"DS-1 row 8 dotdot landing in a mount", filepath.Join(relToMnt, "src"), []string{mntHit}, true, false},
		{"DS-1 row 22 single file", filepath.Join(f.ext, "ext", "notes.txt"), []string{extHit}, false, false},
	}

	for _, r := range rows {
		t.Run(r.row, func(t *testing.T) {
			res := f.grepCall("needle", rbStr(r.path))
			if res.IsError {
				t.Fatalf("grep path=%q: DS-1 says admitted, got error: %s", r.path, res.ForLLM)
			}
			got := rbHitPaths(res.ForLLM)
			if !rbSameSet(got, r.wantHits) {
				t.Fatalf("grep path=%q: match paths = %q, DS-1 match-path form wants exactly %q\nfull result:\n%s",
					r.path, got, r.wantHits, res.ForLLM)
			}
			if r.grepOnly {
				return
			}
			if rr := f.rbReadParity(t, r.path, r.readIsDir); rr.IsError {
				t.Fatalf("read-side parity for path=%q: DS-1 says admitted, got error: %s", r.path, rr.ForLLM)
			}
		})
	}

	// S-1.2's second half and FR-004: an absolute match path is accepted by
	// read_file unchanged. (Mount-name-form hits are grep-only — FR-004 as
	// corrected at b03486b — so none is round-tripped here.)
	t.Run("S-1.2 an absolute match path reads back unchanged", func(t *testing.T) {
		res := f.read.Execute(f.ctx, map[string]any{"path": extHit})
		if res.IsError {
			t.Fatalf("read_file(%q) of a grep match path failed: %s", extHit, res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "needle outside") {
			t.Fatalf("read_file(%q) did not return the file's content; got:\n%s", extHit, res.ForLLM)
		}
	})
}

// TestReadBoundary_GrepRefusesProtected is test 2: DS-1 rows 9-12 plus
// S-1.4's config.json example. grep is refused with the reason read_file
// records for the same path (carve_out), writes exactly one
// path.access_denied row with tool "grep", and writes no path.search_roots
// row (S-5.3). Each example runs in its own fixture so the audit rows
// belong to that one call.
//
// Traces: S-1.4, S-5.3; FR-005, FR-020, MV-2.
func TestReadBoundary_GrepRefusesProtected(t *testing.T) {
	cases := []struct {
		name  string
		seed  func(t *testing.T, f *rbFixture) (path string, isDir bool)
		ds1   string
		probe string
	}{
		{
			name: "master.key", ds1: "row 9",
			seed: func(t *testing.T, f *rbFixture) (string, bool) {
				p := filepath.Join(f.home, "master.key")
				rbWrite(t, p, "needle-in-master-key\n")
				return p, false
			},
		},
		{
			name: "config.json", ds1: "S-1.4 example",
			seed: func(t *testing.T, f *rbFixture) (string, bool) {
				p := filepath.Join(f.home, "config.json")
				rbWrite(t, p, "{\"needle\": \"needle-in-config\"}\n")
				return p, false
			},
		},
		{
			name: "backups", ds1: "row 10",
			seed: func(t *testing.T, f *rbFixture) (string, bool) {
				p := filepath.Join(f.home, "backups")
				rbWrite(t, filepath.Join(p, "config.json.bak"), "needle-in-backup\n")
				return p, true
			},
		},
		{
			name: "another agent's home", ds1: "row 11",
			seed: func(t *testing.T, f *rbFixture) (string, bool) {
				p := filepath.Join(f.home, "agents", "other-agent")
				rbWrite(t, filepath.Join(p, "notes.md"), "needle-in-other-agent\n")
				return p, true
			},
		},
		{
			name: "another workspace", ds1: "row 12",
			seed: func(t *testing.T, f *rbFixture) (string, bool) {
				other := seedGrepWorkspace(t, f.home, "ws-rb-other", "agent-rb-other")
				rbWrite(t, filepath.Join(other, "x.md"), "needle-in-other-workspace\n")
				return rbRealpath(t, other), true
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.ds1+" "+tc.name, func(t *testing.T) {
			f := newRBFixture(t)
			path, isDir := tc.seed(t, f)

			res := f.grepCall("needle", rbStr(path))
			if !res.IsError {
				t.Fatalf("grep path=%q: DS-1 %s says refused(carve_out), got success:\n%s", path, tc.ds1, res.ForLLM)
			}
			if hits := rbHitPaths(res.ForLLM); len(hits) != 0 {
				t.Fatalf("grep path=%q returned match paths %q from a refused call", path, hits)
			}
			if strings.Contains(res.ForLLM, "needle-in-") {
				t.Fatalf("grep path=%q leaked protected content in its refusal:\n%s", path, res.ForLLM)
			}
			rr := f.rbReadParity(t, path, isDir)
			if !rr.IsError {
				t.Fatalf("read-side parity: DS-1 %s says refused(carve_out) for path=%q, got success:\n%s", tc.ds1, path, rr.ForLLM)
			}

			rows := f.rows(t)
			denials := rbRowsFor(rows, rbAccessDeniedEvent, "grep")
			if len(denials) != 1 {
				t.Fatalf("path.access_denied rows with tool=grep = %d, want exactly 1 (FR-020, S-5.3); all rows: %+v", len(denials), rows)
			}
			if got := denials[0].detail("reason"); got != ReasonCarveOut {
				t.Errorf("grep denial reason = %q, want %q (DS-1 reason column, MV-2)", got, ReasonCarveOut)
			}
			if denials[0].Decision != "deny" {
				t.Errorf("grep denial decision = %q, want \"deny\"", denials[0].Decision)
			}
			readTool := "read_file"
			if isDir {
				readTool = "list_directory"
			}
			readDenials := rbRowsFor(rows, rbAccessDeniedEvent, readTool)
			if len(readDenials) != 1 || readDenials[0].detail("reason") != ReasonCarveOut {
				t.Errorf("MV-2 parity: %s must record exactly one %s row with reason %q for the same path; got %+v",
					readTool, rbAccessDeniedEvent, ReasonCarveOut, readDenials)
			}
			if roots := rbRowsFor(rows, rbSearchRootsEvent, ""); len(roots) != 0 {
				t.Errorf("a refused grep wrote %d path.search_roots rows, want 0 (S-5.3, FR-021)", len(roots))
			}
		})
	}
}

// TestReadBoundary_DefaultAreaAndShorthand is test 3: D5 and FR-002 are
// unchanged, and `path: ""` behaves exactly like an omitted `path`
// (S-1.13, DS-1 row 24), audit rows included.
//
// The first two subtests are the spec's regression pins for D5 ("unchanged")
// and FR-002 ("as before"); the third pins that grep writes exactly one
// path.search_roots row per call, omitted and empty path alike.
//
// Traces: S-1.5, S-1.6, S-1.13; FR-002, FR-003, FR-021.
func TestReadBoundary_DefaultAreaAndShorthand(t *testing.T) {
	wantDefault := []string{"notes/a.md", rbMountName + "/src/b.md"}

	t.Run("S-1.5 no path searches the workspace and every mount (D5 regression pin)", func(t *testing.T) {
		f := newRBFixture(t)
		res := f.grepCall("needle", nil)
		if res.IsError {
			t.Fatalf("grep with no path failed: %s", res.ForLLM)
		}
		if got := rbHitPaths(res.ForLLM); !rbSameSet(got, wantDefault) {
			t.Fatalf("match paths = %q, S-1.5 wants exactly %q", got, wantDefault)
		}
	})

	t.Run("S-1.6 mount-name shorthand keeps working (FR-002 regression pin)", func(t *testing.T) {
		f := newRBFixture(t)
		res := f.grepCall("needle", rbStr(rbMountName+"/src"))
		if res.IsError {
			t.Fatalf("grep path=docs-mount/src failed: %s", res.ForLLM)
		}
		want := []string{rbMountName + "/src/b.md"}
		if got := rbHitPaths(res.ForLLM); !rbSameSet(got, want) {
			t.Fatalf("match paths = %q, S-1.6 wants exactly %q", got, want)
		}
	})

	t.Run("S-1.13 empty path equals omitted path, one roots row each", func(t *testing.T) {
		f := newRBFixture(t)
		omitted := f.grepCall("needle", nil)
		empty := f.grepCall("needle", rbStr(""))
		if omitted.IsError || empty.IsError {
			t.Fatalf("both calls must succeed: omitted IsError=%v (%s), empty IsError=%v (%s)",
				omitted.IsError, omitted.ForLLM, empty.IsError, empty.ForLLM)
		}
		if omitted.ForLLM != empty.ForLLM {
			t.Fatalf("S-1.13: same matches, same order, same stats line required.\nomitted:\n%s\nempty:\n%s", omitted.ForLLM, empty.ForLLM)
		}
		if got := rbHitPaths(empty.ForLLM); !rbSameSet(got, wantDefault) {
			t.Fatalf("match paths = %q, want exactly %q", got, wantDefault)
		}

		rows := f.rows(t)
		roots := rbRowsFor(rows, rbSearchRootsEvent, "")
		if len(roots) != 2 {
			t.Fatalf("path.search_roots rows = %d, want 2 (one per call, S-1.13, FR-021); all rows: %+v", len(roots), rows)
		}
		wantRoots := []string{f.ws, f.mnt}
		for i, row := range roots {
			got, ok := rbStringSlice(row.Details["roots"])
			if !ok || len(got) != 2 || got[0] != wantRoots[0] || got[1] != wantRoots[1] {
				t.Errorf("row %d roots = %#v, want %q in walk order (MV-3)", i, row.Details["roots"], wantRoots)
			}
			pathArg, present := row.Details["path_arg"]
			if !present || pathArg != "" {
				t.Errorf("row %d path_arg = %#v (present=%v), want \"\" (S-1.13)", i, pathArg, present)
			}
			if row.Tool != "grep" || row.Decision != "allow" {
				t.Errorf("row %d tool/decision = %q/%q, want grep/allow (MV-3)", i, row.Tool, row.Decision)
			}
		}
		if denials := rbRowsFor(rows, rbAccessDeniedEvent, ""); len(denials) != 0 {
			t.Errorf("S-1.13: neither call may write path.access_denied, got %+v", denials)
		}
	})
}

// TestReadBoundary_MissingPathIsError is test 4: a nonexistent absolute
// location is an error naming it — never "0 matches" — and writes neither a
// path.search_roots nor a path.access_denied row (DS-1 row 21, FR-010).
//
// Traces: S-1.9; FR-010, FR-021.
func TestReadBoundary_MissingPathIsError(t *testing.T) {
	f := newRBFixture(t)
	missing := filepath.Join(f.ext, "does-not-exist")

	res := f.grepCall("needle", rbStr(missing))
	if !res.IsError {
		t.Fatalf("grep path=%q: S-1.9 requires an error, got success:\n%s", missing, res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, missing) && !strings.Contains(res.ForLLM, rbSlash(missing)) {
		t.Fatalf("S-1.9: the error must name %q, got: %s", missing, res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "0 match(es)") {
		t.Fatalf("S-1.9: a missing path must never read as a zero-match success: %s", res.ForLLM)
	}
	if rr := f.read.Execute(f.ctx, map[string]any{"path": missing}); !rr.IsError {
		t.Fatalf("DS-1 row 21 parity: read_file of a missing path must error, got: %s", rr.ForLLM)
	}

	rows := f.rows(t)
	if roots := rbRowsFor(rows, rbSearchRootsEvent, ""); len(roots) != 0 {
		t.Errorf("S-1.9: no path.search_roots row may be written, got %d", len(roots))
	}
	if denials := rbRowsFor(rows, rbAccessDeniedEvent, "grep"); len(denials) != 0 {
		t.Errorf("a not-found path is not a refusal: want no grep path.access_denied row, got %+v", denials)
	}
}

// TestReadBoundary_VolumeRootBounded is test 5: `/` is admitted (DS-1 row
// 23) and the walk stops at the UNCHANGED limits, saying so (US-1 AS-8, D7).
//
// Deviation from the spec text, stated: S-1.10 says "limits injected low
// (files visited = 50)", but neither the spec nor the code names a seam
// that lets a test inject grep's file limit (GrepTool.Execute sets only
// Matches/MatchesPerFile). This test therefore runs with the shipped DS-5
// limits and accepts either of the two DS-5 limits a root walk can hit
// first — max_files (50,000) or deadline (10 s) — which is exactly US-1
// AS-8's oracle ("stops at the unchanged search limits and says it stopped
// early"). The missing seam is reported to the dispatcher as a finding.
//
// A 60 s watchdog turns a hung walk (a blocking read of a special file
// under /proc, say) into a loud failure instead of a hung test binary.
//
// Traces: S-1.10; FR-011, MV-4.
func TestReadBoundary_VolumeRootBounded(t *testing.T) {
	f := newRBFixture(t)
	root := string(filepath.Separator)
	if vol := filepath.VolumeName(f.ext); vol != "" {
		root = vol + string(filepath.Separator)
	}
	// A protected file of THIS install, carrying the search term in its name
	// and content; it must never surface, wherever the walk goes.
	rbWrite(t, filepath.Join(f.home, "master.key"), "needle-volume-root-secret\n")

	done := make(chan *ToolResult, 1)
	go func() { done <- f.grepCall("needle", rbStr(root)) }()
	var res *ToolResult
	select {
	case res = <-done:
	case <-time.After(60 * time.Second):
		t.Fatalf("grep path=%q did not return within 60 s; D7's 10 s deadline must bound it", root)
	}

	if res.IsError {
		t.Fatalf("grep path=%q: DS-1 row 23 says admitted, got error: %s", root, res.ForLLM)
	}
	reason := rbTruncation(res.ForLLM)
	if reason != "max_files" && reason != "deadline" {
		t.Fatalf("grep path=%q: truncation reason = %q, want max_files or deadline (DS-5 limits 4 and 3):\n%.2000s", root, reason, res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "needle-volume-root-secret") {
		t.Fatal("S-1.10: a protected Omnipus file's content appeared in a volume-root search")
	}
	for _, p := range rbHitPaths(res.ForLLM) {
		if strings.HasSuffix(p, rbSlash(filepath.Join(f.home, "master.key"))) {
			t.Fatalf("S-1.10: protected file %q appeared as a match", p)
		}
	}
}

// TestReadBoundary_NULRefusedAndAudited is test 6: a NUL byte in `path` is
// refused by grep's own pre-check and — new — audited as path_invalid
// (Ambiguity Warning 5, DS-1 row 20).
//
// Traces: S-1.11; FR-001 step 1, FR-020.
func TestReadBoundary_NULRefusedAndAudited(t *testing.T) {
	f := newRBFixture(t)
	res := f.grepCall("needle", rbStr("notes\x00a"))
	if !res.IsError {
		t.Fatalf("S-1.11: a NUL-bearing path must be refused, got success:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "NUL") {
		t.Errorf("S-1.11: the refusal must name the NUL byte, got: %s", res.ForLLM)
	}
	rows := f.rows(t)
	denials := rbRowsFor(rows, rbAccessDeniedEvent, "grep")
	if len(denials) != 1 {
		t.Fatalf("path.access_denied rows with tool=grep = %d, want exactly 1 (S-1.11); all rows: %+v", len(denials), rows)
	}
	if got := denials[0].detail("reason"); got != ReasonPathInvalid {
		t.Errorf("reason = %q, want %q (DS-1 row 20)", got, ReasonPathInvalid)
	}
	if roots := rbRowsFor(rows, rbSearchRootsEvent, ""); len(roots) != 0 {
		t.Errorf("a refused grep wrote %d path.search_roots rows, want 0", len(roots))
	}
}

// TestReadBoundary_UnconfinedTurnUnaffected is test 19 (S-4.8): read
// confinement applies only to the Judge's review turns; an ordinary agent
// reads <EXT>. Passes on today's code BY DESIGN — it is the spec's
// regression pin that D6 did not leak into ordinary turns.
//
// Traces: S-4.8; FR-016.
func TestReadBoundary_UnconfinedTurnUnaffected(t *testing.T) {
	f := newRBFixture(t)
	if ReadConfined(f.ctx) {
		t.Fatal("fixture precondition: an ordinary turn context must not be read-confined")
	}
	target := filepath.Join(f.ext, "ext", "notes.txt")
	res := f.read.Execute(f.ctx, map[string]any{"path": target})
	if res.IsError {
		t.Fatalf("S-4.8: an ordinary agent's read_file(%q) must succeed, got: %s", target, res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "needle outside") {
		t.Fatalf("S-4.8: read_file(%q) did not return the file's content:\n%s", target, res.ForLLM)
	}
}
