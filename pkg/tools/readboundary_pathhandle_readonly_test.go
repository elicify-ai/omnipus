// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// 8-reviewer gate finding T2 (type-design-analyzer): a PathHandle built by
// resolvepath.go::readConfinedMountHandle (the Judge's read-confined
// FSOpRead/FSOpList reach into a workspace mount, #920 D6) was, at the type
// level, identical to every write-capable PathHandle — nothing in the type
// itself said "this one must never write". The only thing stopping a write
// was caller discipline: readConfinedMountHandle's own guard
// (`rp.op != FSOpRead && rp.op != FSOpList`) never calling WriteFile/
// MkdirAll on the handles it returns. That is exactly the shape
// type-design-analyzer flags — an invariant enforced by convention, not by
// the type.
//
// Oracle: the fix instruction itself — an unexported readOnly flag set only
// in readConfinedMountHandle, checked by every write-capable method
// (WriteFile, MkdirAll — the only two; resolvepath.go's PathHandle carries
// no other write/create/remove method) — never from reading the pre-fix
// implementation.
package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestPathHandle_ReadConfinedMountHandleRefusesWriteFile is T2's core case:
// a handle ResolvePath builds for the Judge's read-confined reach into a
// mount must refuse WriteFile at the type level, leave the file untouched,
// and still serve ReadFile normally.
func TestPathHandle_ReadConfinedMountHandleRefusesWriteFile(t *testing.T) {
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

	h, err := ResolvePath(jctx, policy, "read_file", "", FSOpRead, mountFile)
	if err != nil {
		t.Fatalf("control: FSOpRead of a mount file must be admitted in a read-confined turn, got %v", err)
	}
	defer h.Close()

	// Reads still work — T2 must not narrow what was already admitted.
	content, err := h.ReadFile()
	if err != nil {
		t.Fatalf("T2: ReadFile must still work on a read-confined mount handle, got %v", err)
	}
	if string(content) != "needle\n" {
		t.Fatalf("ReadFile content = %q, want %q", content, "needle\n")
	}

	if writeErr := h.WriteFile([]byte("tampered\n")); writeErr == nil {
		t.Fatal("T2: WriteFile on a read-confined mount handle must fail at the type level")
	}

	onDisk, err := os.ReadFile(mountFile)
	if err != nil {
		t.Fatalf("re-reading %q after the refused write: %v", mountFile, err)
	}
	if string(onDisk) != "needle\n" {
		t.Fatalf("T2: nothing may be written on disk; %q now contains %q", mountFile, onDisk)
	}
}

// TestPathHandle_ReadConfinedMountHandleRefusesMkdirAll is T2's second
// write-capable method: MkdirAll must refuse too, and the directory it
// would have created must never appear on disk. A not-yet-existing path is
// used deliberately (newMountRootHandle's own contract: AllowedRoots may
// name a not-yet-existing leaf), so an ENOTDIR on an existing file could
// never be mistaken for the read-only refusal this test proves.
func TestPathHandle_ReadConfinedMountHandleRefusesMkdirAll(t *testing.T) {
	f := newRBFixture(t)
	jctx := f.judgeCtx(t)
	policy, err := ResolveTurnFSPolicy(jctx, f.ws, true)
	if err != nil {
		t.Fatalf("ResolveTurnFSPolicy: %v", err)
	}
	newDir := filepath.Join(f.mnt, "should-not-be-created")

	h, err := ResolvePath(jctx, policy, "list_directory", "", FSOpList, newDir)
	if err != nil {
		t.Fatalf("control: FSOpList of a not-yet-existing mount path must be admitted in a read-confined turn, got %v", err)
	}
	defer h.Close()

	if mkErr := h.MkdirAll(0o755); mkErr == nil {
		t.Fatal("T2: MkdirAll on a read-confined mount handle must fail at the type level")
	}

	if _, statErr := os.Stat(newDir); statErr == nil {
		t.Fatalf("T2: nothing may be written on disk; %q was created", newDir)
	} else if !os.IsNotExist(statErr) {
		t.Fatalf("os.Stat(%q): %v", newDir, statErr)
	}
}

// TestPathHandle_OrdinaryMountWriteHandleStaysWritable is the discriminating
// control: an ordinary FSOpWrite mount handle (not the Judge's read-confined
// reach; readConfinedMountHandle is never consulted for FSOpWrite —
// resolveValidatedPath's FSOpWrite/FSOpServe case calls newMountRootHandle
// directly) must keep working exactly as before — T2 narrows only the one
// call site the finding named, never every mount handle.
func TestPathHandle_OrdinaryMountWriteHandleStaysWritable(t *testing.T) {
	f := newRBFixture(t)
	ctx := WithWorkspaceID(context.Background(), rbWorkspaceID)
	ctx = WithTurnWorkspaceDir(ctx, f.ws)
	policy, err := ResolveTurnFSPolicy(ctx, f.ws, true)
	if err != nil {
		t.Fatalf("ResolveTurnFSPolicy: %v", err)
	}
	if policy.ReadConfined {
		t.Fatal("precondition: this turn must NOT be read-confined")
	}
	target := filepath.Join(f.mnt, "src", "b.md")

	h, err := ResolvePath(ctx, policy, "write_file", "", FSOpWrite, target)
	if err != nil {
		t.Fatalf("control: FSOpWrite into an owned mount must be admitted, got %v", err)
	}
	defer h.Close()

	if writeErr := h.WriteFile([]byte("updated\n")); writeErr != nil {
		t.Fatalf("T2 must not narrow an ordinary write handle: WriteFile failed with %v", writeErr)
	}
	onDisk, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("re-reading %q: %v", target, err)
	}
	if string(onDisk) != "updated\n" {
		t.Fatalf("%q = %q, want the write to have landed (%q)", target, onDisk, "updated\n")
	}
}
