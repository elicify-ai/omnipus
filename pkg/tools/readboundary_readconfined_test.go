// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// #920 RED pack — US-4 (the Judge's review turns). Tests 15-18 of
// read-boundary-consistency-spec.md.
//
// Oracles: DS-3 (read-confined matrix), decisions D6 and D10 (read
// confinement = workspace plus its mounts, for all three reading tools, and
// only for the Judge's review turn), FR-012..FR-015, MV-7 ("read-confined"
// in every confinement refusal).
package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const rbJudgeAgentID = "judge"

// judgeCtx is a Judge review turn over workspace W: re-rooted to <WS>,
// carrying W's id (so W's mounts apply) and the engine-set read-confined
// fact (tools.WithReadConfined — set in production only by
// pkg/agent/verifier_adjudication.go::dispatchTurn).
func (f *rbFixture) judgeCtx(t *testing.T) context.Context {
	t.Helper()
	ctx := WithAgentID(context.Background(), rbJudgeAgentID)
	ctx = WithWorkspaceID(ctx, rbWorkspaceID)
	ctx = WithTurnWorkspaceDir(ctx, f.ws)
	ctx = WithVerifierAdjudicationID(ctx, "adj-920")
	return WithReadConfined(ctx, true)
}

// TestResolvePath_ReadConfinedMountMatrix is test 15: DS-3 rows 1-10 for
// grep, read_file and list_directory in one Judge review turn. Admitted rows
// must return the content; refused rows must say "read-confined" (MV-7) or
// be refused as a carve-out. grep's refusals must each write one
// path.access_denied row with the DS-3 reason (S-4.3).
//
// Traces: S-4.1, S-4.2, S-4.3, S-4.7; FR-012, FR-020.
func TestResolvePath_ReadConfinedMountMatrix(t *testing.T) {
	f := newRBFixture(t)
	rbWrite(t, filepath.Join(f.home, "master.key"), "MASTER-KEY-MATERIAL\n")
	rbSymlink(t, filepath.Join(f.ext, "ext"), filepath.Join(f.ws, "link-out"))
	rbSymlink(t, filepath.Join(f.mnt, "src"), filepath.Join(f.ws, "to-mount"))
	jctx := f.judgeCtx(t)

	relToExt, err := filepath.Rel(f.ws, f.ext)
	if err != nil {
		t.Fatal(err)
	}
	relToMnt, err := filepath.Rel(f.ws, f.mnt)
	if err != nil {
		t.Fatal(err)
	}

	type call struct {
		tool string
		args map[string]any
	}
	type row struct {
		ds3     string
		calls   []call
		admit   bool
		marker  string // admitted: text that must appear
		reason  string // refused: DS-3 audit reason for grep
		confine bool   // refused: text must contain "read-confined"
	}
	g := func(path string) call {
		args := map[string]any{"pattern": "needle"}
		if path != "" {
			args["path"] = path
		}
		return call{"grep", args}
	}
	r := func(path string) call { return call{"read_file", map[string]any{"path": path}} }
	l := func(path string) call { return call{"list_directory", map[string]any{"path": path}} }

	rows := []row{
		{ds3: "row 1 default area", calls: []call{g(""), l(".")}, admit: true, marker: "notes"},
		{ds3: "row 2 workspace file", calls: []call{r("notes/a.md"), l("notes"), g("notes")}, admit: true, marker: "a.md"},
		{ds3: "row 3 mount shorthand", calls: []call{g(rbMountName + "/src")}, admit: true, marker: rbMountName + "/src/b.md"},
		{ds3: "row 4 absolute into a mount", calls: []call{
			r(filepath.Join(f.mnt, "src", "b.md")), l(filepath.Join(f.mnt, "src")), g(filepath.Join(f.mnt, "src")),
		}, admit: true, marker: "b.md"},
		{ds3: "row 5 outside workspace and mounts", calls: []call{
			r(filepath.Join(f.ext, "ext", "notes.txt")), l(f.ext), g(f.ext),
		}, reason: ReasonOutsideWorkspace, confine: true},
		{ds3: "row 6 dotdot escape", calls: []call{
			r(filepath.Join(relToExt, "ext", "notes.txt")), l(relToExt), g(relToExt),
		}, reason: ReasonOutsideWorkspace, confine: true},
		{ds3: "row 7 dotdot landing in a mount", calls: []call{
			r(filepath.Join(relToMnt, "src", "b.md")), l(filepath.Join(relToMnt, "src")), g(filepath.Join(relToMnt, "src")),
		}, admit: true, marker: "b.md"},
		{ds3: "row 8 symlink out of the workspace", calls: []call{
			r("link-out/notes.txt"), l("link-out"), g("link-out"),
		}, reason: ReasonOutsideWorkspace, confine: true},
		{ds3: "row 9 symlink from the workspace into a mount", calls: []call{
			r("to-mount/b.md"), l("to-mount"), g("to-mount"),
		}, admit: true, marker: "b.md"},
		{ds3: "row 10 secret set", calls: []call{
			r(filepath.Join(f.home, "master.key")), l(filepath.Join(f.home, "master.key")), g(filepath.Join(f.home, "master.key")),
		}, reason: ReasonCarveOut},
	}

	var wantGrepReasons []string
	for _, rw := range rows {
		for _, c := range rw.calls {
			name := rw.ds3 + " " + c.tool
			t.Run(name, func(t *testing.T) {
				var res *ToolResult
				switch c.tool {
				case "grep":
					res = f.grep.Execute(jctx, c.args)
				case "read_file":
					res = f.read.Execute(jctx, c.args)
				default:
					res = f.list.Execute(jctx, c.args)
				}
				if rw.admit {
					if res.IsError {
						t.Fatalf("DS-3 %s: %s(%v) must be admitted in a Judge review turn, got: %s", rw.ds3, c.tool, c.args, res.ForLLM)
					}
					marker := rw.marker
					if c.tool == "read_file" {
						marker = "needle" // read_file returns content, not names
					}
					if !strings.Contains(res.ForLLM, marker) {
						t.Fatalf("DS-3 %s: %s(%v) result lacks %q:\n%s", rw.ds3, c.tool, c.args, marker, res.ForLLM)
					}
					return
				}
				if !res.IsError {
					t.Fatalf("DS-3 %s: %s(%v) must be refused, got success:\n%s", rw.ds3, c.tool, c.args, res.ForLLM)
				}
				if rw.confine && !strings.Contains(res.ForLLM, "read-confined") {
					t.Errorf("MV-7: %s refusal for DS-3 %s must contain \"read-confined\", got: %s", c.tool, rw.ds3, res.ForLLM)
				}
				for _, leak := range []string{"needle outside", "MASTER-KEY-MATERIAL"} {
					if strings.Contains(res.ForLLM, leak) {
						t.Errorf("DS-3 %s: %s refusal leaked %q", rw.ds3, c.tool, leak)
					}
				}
			})
			if c.tool == "grep" && !rw.admit {
				wantGrepReasons = append(wantGrepReasons, rw.reason)
			}
		}
	}

	t.Run("S-4.3 each grep refusal writes one path.access_denied row with the DS-3 reason", func(t *testing.T) {
		denials := rbRowsFor(f.rows(t), rbAccessDeniedEvent, "grep")
		got := make([]string, len(denials))
		for i, d := range denials {
			got[i] = d.detail("reason")
		}
		if strings.Join(got, ",") != strings.Join(wantGrepReasons, ",") {
			t.Fatalf("grep path.access_denied reasons = %q, want %q in call order (DS-3 rows 5, 6, 8, 10)", got, wantGrepReasons)
		}
		for _, d := range denials {
			if d.AgentID != rbJudgeAgentID {
				t.Errorf("grep denial agent_id = %q, want %q", d.AgentID, rbJudgeAgentID)
			}
		}
	})
}

// TestResolvePath_ReadConfinedIgnoresAllowPatterns is test 16 (S-4.4, DS-3
// row 11, SL-1): an operator AllowReadPaths pattern covering session
// transcripts never reopens them in a Judge review turn. Passes on today's
// code BY DESIGN — JUDGE-FR-060 already holds; it is the guard that the
// GREEN mount exception must not consult AllowedRoots grown by
// ResolvePathAllowingPatterns (ADR-081 D6 correction 1).
//
// Traces: S-4.4; FR-013.
func TestResolvePath_ReadConfinedIgnoresAllowPatterns(t *testing.T) {
	f := newRBFixture(t)
	transcript := filepath.Join(f.home, "sessions", "s1.jsonl")
	rbWrite(t, transcript, "{\"content\":\"TRANSCRIPT-BODY\"}\n")
	pattern := regexp.MustCompile("^" + regexp.QuoteMeta(filepath.Join(f.home, "sessions")) + string(filepath.Separator) + ".*")
	tool := NewReadFileTool(f.ws, true, MaxReadFileSize, []*regexp.Regexp{pattern})

	if res := tool.Execute(f.ctx, map[string]any{"path": transcript}); res.IsError {
		t.Fatalf("control: an ordinary turn reads the transcript, got: %s", res.ForLLM)
	}
	res := tool.Execute(f.judgeCtx(t), map[string]any{"path": transcript})
	if !res.IsError || strings.Contains(res.ForLLM, "TRANSCRIPT-BODY") {
		t.Fatalf("S-4.4: an allow-path pattern must not reopen a transcript in a Judge review turn; IsError=%v: %s", res.IsError, res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "read-confined") {
		t.Errorf("S-4.4 / MV-7: refusal must say read-confined, got: %s", res.ForLLM)
	}
}

// TestResolvePath_ReadConfinedSendStaysConfined is test 17 (S-4.5, DS-3 row
// 12, SL-3): a send-operation resolution of a mount file stays refused in a
// read-confined policy, while the read operation on the same file is
// admitted (the discriminating control).
//
// Traces: S-4.5; FR-015, FR-012.
func TestResolvePath_ReadConfinedSendStaysConfined(t *testing.T) {
	f := newRBFixture(t)
	jctx := f.judgeCtx(t)
	policy, err := ResolveTurnFSPolicy(jctx, f.ws, true)
	if err != nil {
		t.Fatalf("ResolveTurnFSPolicy: %v", err)
	}
	if !policy.ReadConfined {
		t.Fatal("precondition: the Judge review-turn policy must be read-confined")
	}
	mountFile := filepath.Join(f.mnt, "src", "b.md")

	h, err := ResolvePath(jctx, policy, "send_file", "", FSOpSend, mountFile)
	if err == nil {
		h.Close()
		t.Fatalf("S-4.5: FSOpSend of a mount file must stay refused in a read-confined turn")
	}
	if !errors.Is(err, ErrOutsideScope) || !strings.Contains(err.Error(), "read-confined") {
		t.Fatalf("S-4.5: want ErrOutsideScope naming read-confined, got %v", err)
	}

	h, err = ResolvePath(jctx, policy, "read_file", "", FSOpRead, mountFile)
	if err != nil {
		t.Fatalf("FR-012 control: FSOpRead of the same mount file must be admitted, got %v", err)
	}
	h.Close()
}

// TestResolvePath_ReadConfinedMountAncestorSwap is test 18 (S-4.6, DS-3 row
// 13, SL-2): the Judge resolves <MNT>/src/b.md for reading; <MNT>/src is
// then replaced by a symlink to <EXT>/ext holding its own b.md; reading
// through the resolved handle fails and never returns <EXT>'s bytes.
//
// Traces: S-4.6; FR-014.
func TestResolvePath_ReadConfinedMountAncestorSwap(t *testing.T) {
	f := newRBFixture(t)
	rbWrite(t, filepath.Join(f.ext, "ext", "b.md"), "OUTSIDE-SWAPPED-CONTENT\n")
	jctx := f.judgeCtx(t)
	policy, err := ResolveTurnFSPolicy(jctx, f.ws, true)
	if err != nil {
		t.Fatalf("ResolveTurnFSPolicy: %v", err)
	}

	h, err := ResolvePath(jctx, policy, "read_file", "", FSOpRead, filepath.Join(f.mnt, "src", "b.md"))
	if err != nil {
		t.Fatalf("S-4.6 precondition (FR-012): the Judge must be able to resolve a mount file for reading, got %v", err)
	}
	defer h.Close()

	src := filepath.Join(f.mnt, "src")
	if err := os.RemoveAll(src); err != nil {
		t.Fatalf("remove %q: %v", src, err)
	}
	rbSymlink(t, filepath.Join(f.ext, "ext"), src)

	data, readErr := h.ReadFile()
	if strings.Contains(string(data), "OUTSIDE-SWAPPED-CONTENT") {
		t.Fatalf("S-4.6: the read followed a swapped ancestor out of the mount and returned <EXT>'s content")
	}
	if readErr == nil {
		t.Fatalf("S-4.6: the read through a swapped ancestor must fail, got %d bytes and no error", len(data))
	}
}

// TestReadBoundary_GrepAbsoluteUnderMountAncestorSwap is A1 from the Opus
// security-lead review: a Judge grep with an ABSOLUTE path under a mount
// must NOT follow an ancestor directory swapped for a symlink to a folder
// outside the mount. read_file is already protected by the newMountRootHandle
// anchor at the mount's HostPath (TestResolvePath_ReadConfinedMountAncestorSwap,
// S-4.6); grep's absoluteGrepRoot today calls os.OpenRoot(parent) with a
// plain path open, which follows the swap — the same TOCTOU class
// resolvepath.go's package doc claims to close for write/serve. A Judge
// review turn is the only agent that resolves an absolute path outside the
// workdir under a mount, so this is the only call site the read-confined
// posture can leak through.
//
// ORACLE (S-4.6 / FR-014, restated for grep): the result must not contain
// the outside folder's content. The drive:
//
//  1. <MNT>/src is a real directory at grep-call time; <MNT>/src/b.md holds
//     "needle" (so the grep's resolved realpath is admitted by ResolvePath
//     while <MNT>/src still exists — the seam must fire AFTER admission).
//  2. <EXT>/ext/b.md holds a distinct sentinel "OUTSIDE-NEEDLE" — different
//     content from the mount's needle, so a leak is unambiguous.
//  3. installGrepScopeAncestorSwapHook swaps <MNT>/src for a symlink to
//     <EXT>/ext between absoluteGrepRoot's os.Stat and its os.OpenRoot.
//  4. pre-fix: os.OpenRoot(<MNT>/src) follows the symlink and opens
//     <EXT>/ext as container; resolveScopedRoot walks inside <EXT>/ext and
//     finds OUTSIDE-NEEDLE. GREEN: absoluteGrepRoot detects realAbs is
//     inside a mount, anchors os.OpenRoot at the mount's HostPath, and
//     the os.Root's escape protection keeps the walk inside <MNT> (the
//     open of "src" via os.Root(<MNT>) fails because the symlink points
//     outside the bound root, surfaced as lost root).
func TestReadBoundary_GrepAbsoluteUnderMountAncestorSwap(t *testing.T) {
	f := newRBFixture(t)
	// Sentinel inside the mount: admitted by ResolvePath at grep-call time.
	rbWrite(t, filepath.Join(f.mnt, "src", "b.md"), "needle\n")
	// Distinct sentinel outside: a leak surfaces this content.
	rbWrite(t, filepath.Join(f.ext, "ext", "b.md"), "OUTSIDE-NEEDLE\n")
	jctx := f.judgeCtx(t)

	installGrepScopeAncestorSwapHook(t,
		filepath.Join(f.mnt, "src"),
		filepath.Join(f.ext, "ext"),
	)

	res := f.grep.Execute(jctx, map[string]any{
		"pattern": "OUTSIDE-NEEDLE",
		"path":    filepath.Join(f.mnt, "src", "b.md"),
	})
	if !res.IsError && strings.Contains(res.ForLLM, "OUTSIDE-NEEDLE") {
		t.Fatalf("A1: read-confined grep followed a swapped ancestor out of the mount and returned <EXT>'s content:\n%s", res.ForLLM)
	}
}
