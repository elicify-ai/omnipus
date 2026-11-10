// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// #920 RED pack — US-3 unit level. Tests 11 and 12 of
// read-boundary-consistency-spec.md, plus DS-2 R1 at the pin level.
//
// Oracles: decision D8 (a pin of class "runs" skips the mid-call re-check;
// the zero-value class stays fail-closed; RUNS-IF pins unchanged), D3 and
// MV-5 (read_file, list_directory, grep classified "runs"; the write/send
// tools stay "runs_if_args"), DS-2 rows R1, R4, R5 and 13.
//
// Built only on symbols that exist today: the D8 class is carried from the
// verdict into the pin by AutoPinForVerdict, so the tests below never name
// the new AutoPin.Class field and keep compiling before GREEN.
package tools

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// rbPNG writes a 1x1 PNG.
func rbPNG(t *testing.T, p string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write png: %v", err)
	}
}

// TestAutoPin_RunsClassSkipsRecheck is test 11 (S-3.4, DS-2 R4/R5, SL-7).
//
// Traces: S-3.4; FR-018.
func TestAutoPin_RunsClassSkipsRecheck(t *testing.T) {
	f := newRBFixture(t)
	policy, err := ResolveTurnFSPolicy(f.ctx, f.ws, true)
	if err != nil {
		t.Fatalf("ResolveTurnFSPolicy: %v", err)
	}
	outsideFile := filepath.Join(f.ext, "ext", "notes.txt")
	insideFile := filepath.Join(f.ws, "notes", "a.md")
	read := fspolicy.PathGrantAccessRead

	runsVerdict := AutoVerdict{Run: true, Class: AutoVerdictClassRuns, Reason: "read_file runs under Auto"}
	runsPin := AutoPinForVerdict("read_file", runsVerdict)

	t.Run("R4 a runs-class pin with no paths passes the re-check outside the workspace", func(t *testing.T) {
		ctx := WithAutoApproved(f.ctx, runsPin)
		if err := RecheckAutoPin(ctx, "read_file", policy, outsideFile, read); err != nil {
			t.Fatalf("D8: a pin of class %q must skip the re-check; got %v", AutoVerdictClassRuns, err)
		}
	})

	t.Run("R4 through the tool: read_file under a runs-class pin returns the content", func(t *testing.T) {
		ctx := WithAutoApproved(f.ctx, runsPin)
		res := f.read.Execute(ctx, map[string]any{"path": outsideFile})
		if res.IsError {
			t.Fatalf("D8: read_file under a runs-class pin must not be refused as moved; got %s (err %v)", res.ForLLM, res.Err)
		}
		if !strings.Contains(res.ForLLM, "needle outside") {
			t.Fatalf("read_file under a runs-class pin did not return the content:\n%s", res.ForLLM)
		}
		lr := f.list.Execute(WithAutoApproved(f.ctx, AutoPinForVerdict("list_directory", runsVerdict)),
			map[string]any{"path": filepath.Join(f.ext, "ext")})
		if lr.IsError || !strings.Contains(lr.ForLLM, "notes.txt") {
			t.Fatalf("D8: list_directory under a runs-class pin must list <EXT>/ext; IsError=%v: %s", lr.IsError, lr.ForLLM)
		}
	})

	t.Run("DS-2 row 13 image inspection passes both authorisation steps under a runs-class pin", func(t *testing.T) {
		pic := filepath.Join(f.ext, "pic.png")
		rbPNG(t, pic)
		ctx := WithAutoApproved(f.ctx, runsPin)
		res := f.read.Execute(ctx, map[string]any{"path": pic})
		if res.IsError {
			t.Fatalf("S-3.3: image inspection outside the workspace under a runs-class pin failed: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "[image: ") || len(res.InspectionImages) != 1 {
			t.Fatalf("S-3.3: want one inspection image and an image marker, got %d images:\n%s", len(res.InspectionImages), res.ForLLM)
		}
		if rerr := res.InspectionImages[0].Reauthorize(ctx); rerr != nil {
			t.Fatalf("S-3.3: the second authorisation step must also pass, got %v", rerr)
		}
	})

	t.Run("R5 a pin with no class and no paths still fails the re-check (fail-closed)", func(t *testing.T) {
		ctx := WithAutoApproved(f.ctx, AutoPin{Tool: "read_file"})
		err := RecheckAutoPin(ctx, "read_file", policy, outsideFile, read)
		if !errors.Is(err, ErrAutoPinMoved) || !errors.Is(err, ErrOutsideScope) {
			t.Fatalf("SL-7: an empty-class pin must still be re-checked and refused with ErrAutoPinMoved+ErrOutsideScope; got %v", err)
		}
		// Inside the workspace too: no paths pinned means no access pinned.
		if err := RecheckAutoPin(ctx, "read_file", policy, insideFile, read); !errors.Is(err, ErrAutoPinMoved) {
			t.Fatalf("SL-7: an empty-class, pathless pin must fail even inside the workspace; got %v", err)
		}
	})

	t.Run("RUNS-IF pins re-check exactly as before", func(t *testing.T) {
		insideWrite := filepath.Join(f.ws, "out.md")
		v := AutoVerdict{
			Run: true, Class: AutoVerdictClassRunsIfArgs, Reason: "inside",
			Paths: []PinnedPath{{Real: insideWrite, Access: fspolicy.PathGrantAccessWrite}},
		}
		ctx := WithAutoApproved(f.ctx, AutoPinForVerdict("write_file", v))
		if err := RecheckAutoPin(ctx, "write_file", policy, insideWrite, fspolicy.PathGrantAccessWrite); err != nil {
			t.Fatalf("a RUNS-IF pin must still pass inside the workspace; got %v", err)
		}
		err := RecheckAutoPin(ctx, "write_file", policy, filepath.Join(f.ext, "x.md"), fspolicy.PathGrantAccessWrite)
		if !errors.Is(err, ErrAutoPinMoved) {
			t.Fatalf("a RUNS-IF pin must still refuse a path outside the workspace; got %v", err)
		}
	})

	t.Run("a runs-class pin never reopens the secret set", func(t *testing.T) {
		secret := filepath.Join(f.home, "master.key")
		rbWrite(t, secret, "MASTER-KEY-MATERIAL\n")
		res := f.read.Execute(WithAutoApproved(f.ctx, runsPin), map[string]any{"path": secret})
		if !res.IsError || strings.Contains(res.ForLLM, "MASTER-KEY-MATERIAL") {
			t.Fatalf("DS-2 row 8: master.key must stay refused under a runs-class pin; IsError=%v: %s", res.IsError, res.ForLLM)
		}
	})
}

// TestAutoPin_WritePinStillCatchesSwap is DS-2 R1 (S-3.5) at the pin level:
// write_file approved by Auto for <WS>/out/x.txt, then <WS>/out swapped for
// a symlink to <EXT> before the write — refused as moved, nothing written.
// It is a RUNS-IF regression pin and passes on today's code BY DESIGN. The
// swap cannot be placed deterministically between the loop's decision and
// its dispatch without a loop seam, so this row is driven through the real
// pin API (WithAutoApproved + AutoPinForVerdict) that the loop itself calls
// (pkg/agent/loop_run_turn_tools.go).
//
// Traces: S-3.5; FR-018.
func TestAutoPin_WritePinStillCatchesSwap(t *testing.T) {
	f := newRBFixture(t)
	outDir := filepath.Join(f.ws, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tool := NewWriteFileTool(f.ws, true)
	v := ClassifyAutoApprove(f.ctx, "write_file", tool, map[string]any{"path": "out/x.txt", "content": "x"})
	if !v.Run || v.Class != AutoVerdictClassRunsIfArgs {
		t.Fatalf("precondition: write_file inside the workspace must be RUNS-IF approved, got %+v", v)
	}
	if err := os.Remove(outDir); err != nil {
		t.Fatal(err)
	}
	rbSymlink(t, f.ext, outDir)

	res := tool.Execute(WithAutoApproved(f.ctx, AutoPinForVerdict("write_file", v)),
		map[string]any{"path": "out/x.txt", "content": "PWNED"})
	if !res.IsError {
		t.Fatalf("S-3.5: the swapped write must be refused, got success: %s", res.ForLLM)
	}
	if !errors.Is(res.Err, ErrOutsideScope) {
		t.Errorf("S-3.5: refusal must be an outside-scope refusal, got err %v", res.Err)
	}
	if _, err := os.Stat(filepath.Join(f.ext, "x.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("S-3.5: nothing may be written under <EXT>; stat err = %v", err)
	}
}

// TestAutoApproveClasses_ReadToolsRun is test 12 (MV-5, S-3.1
// classification): the three reading tools are class "runs"; the write and
// send tools keep "runs_if_args"; ClassifyAutoApprove runs read_file
// outside the workspace with class "runs".
//
// Traces: S-3.1; FR-017.
func TestAutoApproveClasses_ReadToolsRun(t *testing.T) {
	for _, name := range []string{"read_file", "list_directory", "grep"} {
		if got := AutoApproveClassOf(name); got != AutoRuns {
			t.Errorf("AutoApproveClassOf(%q) = %s, want runs (D3, MV-5)", name, got)
		}
	}
	for _, name := range []string{"write_file", "edit_file", "append_file", "send_file", "browser_screenshot"} {
		if got := AutoApproveClassOf(name); got != AutoRunsIfArgs {
			t.Errorf("AutoApproveClassOf(%q) = %s, want runs_if_args (unchanged, MV-5)", name, got)
		}
	}

	f := newRBFixture(t)
	outside := filepath.Join(f.ext, "ext", "notes.txt")
	for _, tc := range []struct {
		name string
		tool Tool
		args map[string]any
	}{
		{"read_file", f.read, map[string]any{"path": outside}},
		{"list_directory", f.list, map[string]any{"path": filepath.Dir(outside)}},
	} {
		v := ClassifyAutoApprove(f.ctx, tc.name, tc.tool, tc.args)
		if !v.Run || v.Class != AutoVerdictClassRuns {
			t.Errorf("ClassifyAutoApprove(%s, outside the workspace) = %+v, want Run=true Class=%q (DS-2 row 7)", tc.name, v, AutoVerdictClassRuns)
		}
	}
}
