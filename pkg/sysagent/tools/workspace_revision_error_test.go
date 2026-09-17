// Omnipus — System Agent Tool Tests: workspace revision-precondition error
// classification (ADR-090 security review SE-B regression).
//
// Expectations derive from docs/internal/specs/adr-090-agent-configuration-
// and-skills-spec.md FR-007 ("Missing/malformed revision rejects 400/
// INVALID_INPUT; mismatch rejects 409/CONFLICT with zero resource writes" —
// only a MISMATCH is a conflict; "no stale write is automatically retried";
// "never blindly replay create/delete") and FR-012 ("Error results must
// distinguish INVALID_INPUT, PROTECTED_FIELD, CONFLICT, NOT_FOUND,
// SAVE_FAILED"). A read that fails because storage is corrupt or unreadable
// is not a revision conflict: no revision exists to retry with.
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

// wellFormedRevision is a 64-lowercase-hex revision that cannot match any
// state written by these tests. It exists so the revision FORMAT precondition
// passes and the failure being classified comes from the read underneath —
// except in the mismatch test, where passing this stale-but-valid revision is
// exactly the scenario.
const wellFormedRevision = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// conflictRetrySuggestion is the revision-retry advice that is truthful ONLY
// for an actual revision mismatch. Every other classification below asserts
// it is absent, because advising a revision retry against unreadable storage
// sends the caller into a re-read loop that can never converge (FR-007: "no
// stale write is automatically retried").
const conflictRetrySuggestion = "Call get_workspace and retry with its current revision"

// revisionErrorBlock runs workspaceRevisionError and returns its decoded
// error block, failing the test on a success payload or an undecodable one —
// so every test below asserts on a real classification, never on accidental
// success.
func revisionErrorBlock(t *testing.T, id string, err error) map[string]any {
	t.Helper()
	result := workspaceRevisionError(id, err)
	if !result.IsError {
		t.Fatalf("workspaceRevisionError(%q, %v) returned a success result: %s", id, err, result.ForLLM)
	}
	var payload map[string]any
	if decodeErr := json.Unmarshal([]byte(result.ForLLM), &payload); decodeErr != nil {
		t.Fatalf("decode result: %v\n%s", decodeErr, result.ForLLM)
	}
	block, _ := payload["error"].(map[string]any)
	if block == nil {
		t.Fatalf("result has no error block: %s", result.ForLLM)
	}
	return block
}

// writeWorkspaceRecord persists a minimal well-formed workspace record so
// loadWorkspaceRecord succeeds and the failure under test comes from whatever
// the test corrupts next.
func writeWorkspaceRecord(t *testing.T, home, id string) {
	t.Helper()
	dir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"` + id + `","name":"Record","status":"active"}`
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// corruptDelegationStore writes a truncated-mid-write delegation record —
// the exact corruption shape a crash between writeFileAtomic's write and
// rename leaves behind in the worst case, and the shape the security review's
// SE-B scenario names.
func corruptDelegationStore(t *testing.T, home, id string) {
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

// TestWorkspaceRevisionError_MalformedRevisionIsInvalidInput pins FR-007's
// "Missing/malformed revision rejects 400/INVALID_INPUT" — the pre-existing
// classification, guarded so the new branches cannot swallow it.
func TestWorkspaceRevisionError_MalformedRevisionIsInvalidInput(t *testing.T) {
	err := workspacepkg.ValidateRevision("not-a-revision")
	block := revisionErrorBlock(t, "w1", err)
	if block["code"] != "INVALID_INPUT" {
		t.Fatalf("code = %v, want INVALID_INPUT", block["code"])
	}
	if block["suggestion"] != "Use the revision returned by get_workspace" {
		t.Fatalf("suggestion = %v, want the get_workspace guidance", block["suggestion"])
	}
}

// TestWorkspaceRevisionError_UnsafeIDIsInvalidInputNotConflict: an id that
// fails the store's safeID gate is malformed input, not a storage conflict.
// Current behavior files it under REVISION_CONFLICT, which invites a
// pointless revision retry for an id that can never be read.
func TestWorkspaceRevisionError_UnsafeIDIsInvalidInputNotConflict(t *testing.T) {
	home := t.TempDir()
	_, err := workspacepkg.CheckRevisionLocked(home, "../escape", wellFormedRevision)
	if !errors.Is(err, workspacepkg.ErrInvalidWorkspaceID) {
		t.Fatalf("setup: error = %v, want ErrInvalidWorkspaceID", err)
	}
	block := revisionErrorBlock(t, "../escape", err)
	if block["code"] != "INVALID_INPUT" {
		t.Fatalf("code = %v, want INVALID_INPUT", block["code"])
	}
	if block["code"] == "REVISION_CONFLICT" || strings.Contains(fmt.Sprint(block["suggestion"]), "retry") {
		t.Fatalf("unsafe id must not be classified as a revision conflict: %v", block)
	}
}

// TestWorkspaceRevisionError_MissingWorkspaceIsNotFound pins the missing-
// entity classification (FR-012's NOT_FOUND distinction) so the new storage
// branches cannot swallow it.
func TestWorkspaceRevisionError_MissingWorkspaceIsNotFound(t *testing.T) {
	home := t.TempDir()
	_, err := workspacepkg.CheckRevisionLocked(home, "missing", wellFormedRevision)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("setup: error = %v, want os.ErrNotExist", err)
	}
	block := revisionErrorBlock(t, "missing", err)
	if block["code"] != "WORKSPACE_NOT_FOUND" {
		t.Fatalf("code = %v, want WORKSPACE_NOT_FOUND", block["code"])
	}
}

// TestWorkspaceRevisionError_RevisionMismatchIsTheOnlyConflict: an actual
// revision mismatch — the one failure where re-reading and retrying with the
// current revision is truthful advice (FR-007: "mismatch rejects 409/CONFLICT").
func TestWorkspaceRevisionError_RevisionMismatchIsTheOnlyConflict(t *testing.T) {
	home := t.TempDir()
	writeWorkspaceRecord(t, home, "w1")
	_, err := workspacepkg.CheckRevisionLocked(home, "w1", wellFormedRevision)
	if !errors.Is(err, workspacepkg.ErrRevisionConflict) {
		t.Fatalf("setup: error = %v, want ErrRevisionConflict", err)
	}
	block := revisionErrorBlock(t, "w1", err)
	if block["code"] != "REVISION_CONFLICT" {
		t.Fatalf("code = %v, want REVISION_CONFLICT", block["code"])
	}
	if block["suggestion"] != conflictRetrySuggestion {
		t.Fatalf("suggestion = %v, want %q", block["suggestion"], conflictRetrySuggestion)
	}
}

// TestWorkspaceRevisionError_DelegationStoreUnreadableIsNotAConflict is the
// SE-B regression: a delegation record that cannot be parsed must not be
// reported as a revision conflict. The caller supplied a well-formed revision
// and the workspace record exists; the failure is unreadable auxiliary
// storage, and the guidance must name that store instead of advising a
// revision retry that can never succeed.
func TestWorkspaceRevisionError_DelegationStoreUnreadableIsNotAConflict(t *testing.T) {
	home := t.TempDir()
	writeWorkspaceRecord(t, home, "w1")
	corruptDelegationStore(t, home, "w1")
	_, err := workspacepkg.CheckRevisionLocked(home, "w1", wellFormedRevision)
	if err == nil {
		t.Fatal("setup: expected a read failure for the corrupt delegation store")
	}
	block := revisionErrorBlock(t, "w1", err)
	if block["code"] == "REVISION_CONFLICT" {
		t.Fatalf("unreadable delegation store misclassified as REVISION_CONFLICT: %v", block)
	}
	if block["code"] != "DELEGATION_STORE_UNREADABLE" {
		t.Fatalf("code = %v, want DELEGATION_STORE_UNREADABLE (existing vocabulary, workspace.go's seed-refusal path)", block["code"])
	}
	suggestion := fmt.Sprint(block["suggestion"])
	if !strings.Contains(suggestion, "entities/delegation") || !strings.Contains(suggestion, "w1") {
		t.Fatalf("suggestion must name the delegation store record: %q", suggestion)
	}
	if strings.Contains(suggestion, conflictRetrySuggestion) || strings.Contains(suggestion, "retry with its current revision") {
		t.Fatalf("suggestion must not advise a revision retry for unreadable storage: %q", suggestion)
	}
}

// TestWorkspaceRevisionError_UnreadableWorkspaceStorageIsReadFailedNotConflict
// covers the remaining read failures — a corrupt workspace record and an
// I/O-denied read — which must classify as READ_FAILED (existing vocabulary,
// skill.go/agent_read.go) with no revision-retry advice. The I/O case uses a
// synthetic error shaped like loadWorkspaceRecord's own wrapping because a
// chmod-based fixture would not fail under a root test runner.
func TestWorkspaceRevisionError_UnreadableWorkspaceStorageIsReadFailedNotConflict(t *testing.T) {
	home := t.TempDir()
	writeWorkspaceRecord(t, home, "w1")
	if err := os.WriteFile(filepath.Join(home, "workspaces", "w1.json"), []byte(`{"id":"w1","name":`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, corruptErr := workspacepkg.CheckRevisionLocked(home, "w1", wellFormedRevision)
	if corruptErr == nil {
		t.Fatal("setup: expected a parse failure for the truncated workspace record")
	}
	block := revisionErrorBlock(t, "w1", corruptErr)
	if block["code"] != "READ_FAILED" {
		t.Fatalf("corrupt record: code = %v, want READ_FAILED", block["code"])
	}
	if suggestion := fmt.Sprint(block["suggestion"]); strings.Contains(suggestion, "retry with its current revision") {
		t.Fatalf("corrupt record: suggestion must not advise a revision retry: %q", suggestion)
	}

	ioErr := fmt.Errorf("workspace: read w1: %w", fs.ErrPermission)
	block = revisionErrorBlock(t, "w1", ioErr)
	if block["code"] != "READ_FAILED" {
		t.Fatalf("I/O failure: code = %v, want READ_FAILED", block["code"])
	}
	if suggestion := fmt.Sprint(block["suggestion"]); strings.Contains(suggestion, "retry with its current revision") {
		t.Fatalf("I/O failure: suggestion must not advise a revision retry: %q", suggestion)
	}
	if message := fmt.Sprint(block["message"]); !strings.Contains(message, fs.ErrPermission.Error()) {
		t.Fatalf("I/O failure: message must carry the underlying error, got %q", message)
	}
}

// ---- behavioral coverage through the two callers ----

// TestWorkspaceUpdate_DelegationStoreCorruptionNotRevisionConflict drives the
// real update_workspace tool against a healthy workspace whose delegation
// record is then corrupted, with the caller supplying the CURRENT revision.
// Supplying the current revision excludes the competing explanation "the
// revision was stale": the only remaining cause is the unreadable store, and
// the tool must say so instead of demanding a revision retry. Zero-write is
// proven by byte-comparing the workspace record before and after.
func TestWorkspaceUpdate_DelegationStoreCorruptionNotRevisionConflict(t *testing.T) {
	deps := outcomeTestDeps(t)
	ctx := context.Background()

	created := NewWorkspaceCreateTool(deps).Execute(ctx, map[string]any{"name": "Corruptible"})
	if created.IsError {
		t.Fatalf("create failed: %s", created.ForLLM)
	}
	var createPayload map[string]any
	if err := json.Unmarshal([]byte(created.ForLLM), &createPayload); err != nil {
		t.Fatal(err)
	}
	id, _ := createPayload["id"].(string)
	if id == "" {
		t.Fatalf("create_workspace response missing id: %s", created.ForLLM)
	}

	// Read the CURRENT revision while the store is still healthy, so the
	// update below fails only because of the corruption introduced next.
	state, err := workspacepkg.ReadState(deps.Home, id)
	if err != nil {
		t.Fatal(err)
	}
	corruptDelegationStore(t, deps.Home, id)

	recordPath := filepath.Join(deps.Home, "workspaces", id+".json")
	before, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}

	result := NewWorkspaceUpdateTool(deps).Execute(ctx, map[string]any{
		"id":       id,
		"revision": state.Revision,
		"name":     "Renamed",
	})
	if !result.IsError {
		t.Fatalf("update unexpectedly succeeded over a corrupt delegation store: %s", result.ForLLM)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.ForLLM), &payload); err != nil {
		t.Fatal(err)
	}
	block, _ := payload["error"].(map[string]any)
	if block == nil {
		t.Fatalf("result has no error block: %s", result.ForLLM)
	}
	if block["code"] != "DELEGATION_STORE_UNREADABLE" {
		t.Fatalf("code = %v, want DELEGATION_STORE_UNREADABLE — a corrupt store is not a revision conflict (SE-B)", block["code"])
	}
	if suggestion := fmt.Sprint(block["suggestion"]); strings.Contains(suggestion, "retry with its current revision") {
		t.Fatalf("suggestion must not advise a revision retry over a corrupt store: %q", suggestion)
	}

	after, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("workspace record was written despite the failed precondition:\nbefore: %s\nafter:  %s", before, after)
	}
}

// TestWorkspaceDelete_DelegationStoreCorruptionNotRevisionConflict is the
// destructive-surface twin: delete_workspace with confirm:true and the
// CURRENT revision must refuse over a corrupt delegation store without
// reporting a revision conflict, and must not remove the workspace record —
// an irreversible delete must never proceed on a state it could not read.
func TestWorkspaceDelete_DelegationStoreCorruptionNotRevisionConflict(t *testing.T) {
	deps := outcomeTestDeps(t)
	ctx := context.Background()

	created := NewWorkspaceCreateTool(deps).Execute(ctx, map[string]any{"name": "Corruptible"})
	if created.IsError {
		t.Fatalf("create failed: %s", created.ForLLM)
	}
	var createPayload map[string]any
	if err := json.Unmarshal([]byte(created.ForLLM), &createPayload); err != nil {
		t.Fatal(err)
	}
	id, _ := createPayload["id"].(string)
	if id == "" {
		t.Fatalf("create_workspace response missing id: %s", created.ForLLM)
	}

	state, err := workspacepkg.ReadState(deps.Home, id)
	if err != nil {
		t.Fatal(err)
	}
	corruptDelegationStore(t, deps.Home, id)

	result := NewWorkspaceDeleteTool(deps).Execute(ctx, map[string]any{
		"id":       id,
		"revision": state.Revision,
		"confirm":  true,
	})
	if !result.IsError {
		t.Fatalf("delete unexpectedly succeeded over a corrupt delegation store: %s", result.ForLLM)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.ForLLM), &payload); err != nil {
		t.Fatal(err)
	}
	block, _ := payload["error"].(map[string]any)
	if block == nil {
		t.Fatalf("result has no error block: %s", result.ForLLM)
	}
	if block["code"] != "DELEGATION_STORE_UNREADABLE" {
		t.Fatalf("code = %v, want DELEGATION_STORE_UNREADABLE — a corrupt store is not a revision conflict (SE-B)", block["code"])
	}
	if _, statErr := os.Stat(filepath.Join(deps.Home, "workspaces", id+".json")); statErr != nil {
		t.Fatalf("workspace record must survive a delete that could not read its state: %v", statErr)
	}
}
