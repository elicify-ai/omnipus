// Omnipus — get_workspace read-error classification (ADR-090 follow-up).
//
// Spec: FR-012 distinguishes NOT_FOUND from other faults; FR-007 repair
// re-reads actual state and must not be told the workspace is missing when
// the record is present and only auxiliary storage is unreadable.
// Destructive "remove the file" recovery is not default advice.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
)

func decodeToolError(t *testing.T, raw string) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	block, _ := payload["error"].(map[string]any)
	if block == nil {
		t.Fatalf("missing error block: %s", raw)
	}
	return block
}

func snapshotPath(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return b
}

func assertNoWrite(t *testing.T, path string, before []byte) {
	t.Helper()
	after := snapshotPath(t, path)
	if string(before) != string(after) {
		t.Fatalf("read path mutated %s\nbefore=%q\nafter=%q", path, before, after)
	}
}

func seedReadableWorkspace(t *testing.T, home, id string) {
	t.Helper()
	dir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"` + id + `","name":"Readable","status":"active"}`
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeCorruptDelegation(t *testing.T, home, id string) {
	t.Helper()
	dir := workspacepkg.DelegationStoreDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	truncated := `{"workspace_id":"` + id + `","delegation":[{"from_agent":"jim"`
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(truncated), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceReadError_MissingRecordIsNotFound(t *testing.T) {
	home := t.TempDir()
	_, err := workspacepkg.ReadState(home, "missing")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("setup error=%v want os.ErrNotExist", err)
	}
	result := workspaceReadError("missing", err)
	if !result.IsError {
		t.Fatal("expected error result")
	}
	block := decodeToolError(t, result.ForLLM)
	if block["code"] != "WORKSPACE_NOT_FOUND" {
		t.Fatalf("code=%v want WORKSPACE_NOT_FOUND", block["code"])
	}
}

func TestWorkspaceReadError_UnsafeIDIsInvalidInput(t *testing.T) {
	home := t.TempDir()
	_, err := workspacepkg.ReadState(home, "../escape")
	if !errors.Is(err, workspacepkg.ErrInvalidWorkspaceID) {
		t.Fatalf("setup error=%v want ErrInvalidWorkspaceID", err)
	}
	block := decodeToolError(t, workspaceReadError("../escape", err).ForLLM)
	if block["code"] != "INVALID_INPUT" {
		t.Fatalf("code=%v want INVALID_INPUT", block["code"])
	}
}

func TestWorkspaceReadError_UnreadableDelegationIsNotNotFound(t *testing.T) {
	home := t.TempDir()
	seedReadableWorkspace(t, home, "w1")
	writeCorruptDelegation(t, home, "w1")
	_, err := workspacepkg.ReadState(home, "w1")
	if err == nil {
		t.Fatal("expected unreadable delegation error")
	}
	block := decodeToolError(t, workspaceReadError("w1", err).ForLLM)
	if block["code"] == "WORKSPACE_NOT_FOUND" {
		t.Fatalf("unreadable auxiliary store misclassified as missing: %v", block)
	}
	if block["code"] != "DELEGATION_STORE_UNREADABLE" {
		t.Fatalf("code=%v want DELEGATION_STORE_UNREADABLE", block["code"])
	}
	suggestion := fmt.Sprint(block["suggestion"])
	if !strings.Contains(suggestion, "entities/delegation") || !strings.Contains(suggestion, "w1") {
		t.Fatalf("suggestion must name the delegation record: %q", suggestion)
	}
	if strings.Contains(strings.ToLower(suggestion), "remove") {
		t.Fatalf("default recovery must not suggest deleting the record: %q", suggestion)
	}
}

func TestWorkspaceReadError_CorruptRecordAndIOAreReadFailedNotNotFound(t *testing.T) {
	home := t.TempDir()
	seedReadableWorkspace(t, home, "w1")
	if err := os.WriteFile(filepath.Join(home, "workspaces", "w1.json"), []byte(`{"id":"w1","name":`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, corruptErr := workspacepkg.ReadState(home, "w1")
	if corruptErr == nil {
		t.Fatal("expected parse failure")
	}
	block := decodeToolError(t, workspaceReadError("w1", corruptErr).ForLLM)
	if block["code"] != "READ_FAILED" {
		t.Fatalf("corrupt record code=%v want READ_FAILED", block["code"])
	}
	if block["code"] == "WORKSPACE_NOT_FOUND" {
		t.Fatal("corrupt record must not be reported missing")
	}

	ioErr := fmt.Errorf("workspace: read w1: %w", fs.ErrPermission)
	block = decodeToolError(t, workspaceReadError("w1", ioErr).ForLLM)
	if block["code"] != "READ_FAILED" {
		t.Fatalf("I/O code=%v want READ_FAILED", block["code"])
	}
	if !strings.Contains(fmt.Sprint(block["message"]), fs.ErrPermission.Error()) {
		t.Fatalf("I/O message must carry the underlying error: %v", block)
	}
	if strings.Contains(strings.ToLower(fmt.Sprint(block["suggestion"])), "remove") {
		t.Fatalf("I/O recovery must not default to deletion: %v", block)
	}
}

func TestGetWorkspace_DelegationCorruptionNotNotFoundAndZeroWrite(t *testing.T) {
	deps := outcomeTestDeps(t)
	created := NewWorkspaceCreateTool(deps).Execute(context.Background(), map[string]any{"name": "Keep Me"})
	if created.IsError {
		t.Fatalf("create failed: %s", created.ForLLM)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(created.ForLLM), &payload); err != nil {
		t.Fatal(err)
	}
	id, _ := payload["id"].(string)
	writeCorruptDelegation(t, deps.Home, id)

	recordPath := filepath.Join(deps.Home, "workspaces", id+".json")
	storePath := filepath.Join(workspacepkg.DelegationStoreDir(deps.Home), id+".json")
	beforeRecord := snapshotPath(t, recordPath)
	beforeStore := snapshotPath(t, storePath)

	result := NewWorkspaceGetTool(deps).Execute(context.Background(), map[string]any{"id": id})
	if !result.IsError {
		t.Fatalf("get_workspace succeeded over corrupt delegation: %s", result.ForLLM)
	}
	block := decodeToolError(t, result.ForLLM)
	if block["code"] != "DELEGATION_STORE_UNREADABLE" {
		t.Fatalf("code=%v want DELEGATION_STORE_UNREADABLE not WORKSPACE_NOT_FOUND", block["code"])
	}
	assertNoWrite(t, recordPath, beforeRecord)
	assertNoWrite(t, storePath, beforeStore)
}

func TestGetWorkspace_MissingStillNotFoundAndZeroWrite(t *testing.T) {
	deps := outcomeTestDeps(t)
	dir := filepath.Join(deps.Home, "workspaces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	result := NewWorkspaceGetTool(deps).Execute(context.Background(), map[string]any{"id": "01JZZZZZZZZZZZZZZZZZZZZZZZ"})
	if !result.IsError {
		t.Fatal("expected not-found")
	}
	block := decodeToolError(t, result.ForLLM)
	if block["code"] != "WORKSPACE_NOT_FOUND" {
		t.Fatalf("code=%v want WORKSPACE_NOT_FOUND", block["code"])
	}
	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("missing-id read created files: before=%d after=%d", len(before), len(after))
	}
}
