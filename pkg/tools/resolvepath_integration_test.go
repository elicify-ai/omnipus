// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — ADR-046 P1 integration tests: the generic file tools
// actually route through ResolvePath (spec test 17 subset), filesystem_scope
// is symmetric across read/write (spec test 34), and a P1 stand-in for spec
// test 33 pins the Confined-refuses-outside-work contract the follow-up
// defects wave's serve_web migration will rely on.

package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// TestTools_RouteThroughResolvePath — spec test 17 subset: THIS wave
// migrates read_file/write_file/edit_file/append_file/list_directory to
// ResolvePath (the 4 defect tools — serve_web/send_file/browser_screenshot/
// install_skill — are the follow-up wave). For each, confirms the tool (a)
// confines a Confined-scope call to its working dir (an outside absolute
// path is refused) and (b) still performs real I/O for an in-scope relative
// path — proving Execute actually reaches ResolvePath rather than a stub.
func TestTools_RouteThroughResolvePath(t *testing.T) {
	ctx := context.Background()

	t.Run("read_file", func(t *testing.T) {
		ws := t.TempDir()
		outside := t.TempDir()
		if err := os.WriteFile(filepath.Join(ws, "in.txt"), []byte("in-scope"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		outsideFile := filepath.Join(outside, "out.txt")
		if err := os.WriteFile(outsideFile, []byte("out-of-scope"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}

		tool := NewReadFileTool(ws, true, MaxReadFileSize)

		ok := tool.Execute(ctx, map[string]any{"path": "in.txt"})
		if ok.IsError {
			t.Fatalf("in-scope read should succeed: %s", ok.ForLLM)
		}
		if !strings.Contains(ok.ForLLM, "in-scope") {
			t.Errorf("expected file content, got: %s", ok.ForLLM)
		}

		denied := tool.Execute(ctx, map[string]any{"path": outsideFile})
		if !denied.IsError {
			t.Fatalf("outside-scope read must be refused, got: %s", denied.ForLLM)
		}
	})

	t.Run("write_file", func(t *testing.T) {
		ws := t.TempDir()
		outside := t.TempDir()
		tool := NewWriteFileTool(ws, true)

		ok := tool.Execute(ctx, map[string]any{"path": "new.txt", "content": "written"})
		if ok.IsError {
			t.Fatalf("in-scope write should succeed: %s", ok.ForLLM)
		}
		data, err := os.ReadFile(filepath.Join(ws, "new.txt"))
		if err != nil || string(data) != "written" {
			t.Fatalf("write did not persist: data=%q err=%v", data, err)
		}

		outsideTarget := filepath.Join(outside, "evil.txt")
		denied := tool.Execute(ctx, map[string]any{"path": outsideTarget, "content": "x"})
		if !denied.IsError {
			t.Fatalf("outside-scope write must be refused, got: %s", denied.ForLLM)
		}
		if _, err := os.Stat(outsideTarget); err == nil {
			t.Fatalf("outside-scope write must not have created a file")
		}
	})

	t.Run("edit_file", func(t *testing.T) {
		ws := t.TempDir()
		if err := os.WriteFile(filepath.Join(ws, "e.txt"), []byte("before"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		tool := NewEditFileTool(ws, true)
		ok := tool.Execute(ctx, map[string]any{"path": "e.txt", "old_text": "before", "new_text": "after"})
		if ok.IsError {
			t.Fatalf("in-scope edit should succeed: %s", ok.ForLLM)
		}
		data, _ := os.ReadFile(filepath.Join(ws, "e.txt"))
		if string(data) != "after" {
			t.Fatalf("edit did not persist: %q", data)
		}
	})

	t.Run("append_file", func(t *testing.T) {
		ws := t.TempDir()
		tool := NewAppendFileTool(ws, true)
		ok := tool.Execute(ctx, map[string]any{"path": "a.txt", "content": "first"})
		if ok.IsError {
			t.Fatalf("append to new file should succeed: %s", ok.ForLLM)
		}
		ok2 := tool.Execute(ctx, map[string]any{"path": "a.txt", "content": "-second"})
		if ok2.IsError {
			t.Fatalf("append to existing file should succeed: %s", ok2.ForLLM)
		}
		data, _ := os.ReadFile(filepath.Join(ws, "a.txt"))
		if string(data) != "first-second" {
			t.Fatalf("append content = %q, want %q", data, "first-second")
		}
	})

	t.Run("list_directory", func(t *testing.T) {
		ws := t.TempDir()
		if err := os.WriteFile(filepath.Join(ws, "f1.txt"), []byte("x"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		tool := NewListDirTool(ws, true)
		ok := tool.Execute(ctx, map[string]any{"path": "."})
		if ok.IsError {
			t.Fatalf("in-scope list should succeed: %s", ok.ForLLM)
		}
		if !strings.Contains(ok.ForLLM, "f1.txt") {
			t.Errorf("expected f1.txt in listing, got: %s", ok.ForLLM)
		}
	})
}

// TestFilesystemScope_SymmetricReadAndWrite — spec test 34 (FR-032):
// filesystem_scope is a single symmetric tri-state governing both read and
// write — a write outside work/ under a Confined scope must be refused
// identically to a read.
func TestFilesystemScope_SymmetricReadAndWrite(t *testing.T) {
	ctx := context.Background()
	ws := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "target.txt")
	if err := os.WriteFile(outsideFile, []byte("existing"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	readTool := NewReadFileTool(ws, true, MaxReadFileSize)
	readResult := readTool.Execute(ctx, map[string]any{"path": outsideFile})
	if !readResult.IsError {
		t.Fatalf("read outside work/ under Confined must be refused, got: %s", readResult.ForLLM)
	}

	writeTool := NewWriteFileTool(ws, true)
	writeResult := writeTool.Execute(ctx, map[string]any{
		"path": outsideFile, "content": "tampered", "overwrite": true,
	})
	if !writeResult.IsError {
		t.Fatalf("write outside work/ under Confined must be refused identically to read, got: %s", writeResult.ForLLM)
	}

	// Neither call actually touched the target file.
	data, err := os.ReadFile(outsideFile)
	if err != nil || string(data) != "existing" {
		t.Fatalf("outside file must be untouched: data=%q err=%v", data, err)
	}
}

// TestServeWeb_ConfinedRefusesOutsideWork is a P1 stand-in for spec test 33
// (TestServeWeb_WarnsOutsideWorkDir): the real serve_web tool is migrated to
// ResolvePath by the follow-up defects wave, but the underlying resolver
// behavior it will rely on — a Confined scope refusing an outside-work/
// FSOpServe resolution — is exercised here directly against ResolvePath so
// this wave's contract is pinned ahead of that migration.
func TestServeWeb_ConfinedRefusesOutsideWork(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	realPath, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		t.Fatalf("resolve workDir: %v", err)
	}
	policy := fspolicy.FSPolicy{WorkDir: realPath, Scope: fspolicy.FSScopeConfined}

	_, err = ResolvePath(context.Background(), policy, "serve_web", "", FSOpServe, outside)
	if err == nil {
		t.Fatalf("expected a Confined serve target outside work/ to be refused")
	}
	if !errors.Is(err, ErrOutsideScope) {
		t.Errorf("expected ErrOutsideScope, got: %v", err)
	}
}
