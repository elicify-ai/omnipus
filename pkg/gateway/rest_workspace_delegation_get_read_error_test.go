package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/workspace"
)

func snapshotBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return b
}

func restErrorMessage(t *testing.T, body []byte) string {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode error body: %v\n%s", err, body)
	}
	msg, _ := payload["error"].(string)
	return msg
}

func TestHandleWorkspaceDelegationGet_MissingIs404AndZeroWrite(t *testing.T) {
	api, _ := buildWorkspaceDelegationTestAPI(t)
	dir := filepath.Join(api.homePath, "workspaces")
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec := getDelegation(t, api, "01J8NONEXISTENTWORKSPACEXX")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "workspace not found") {
		t.Fatalf("body=%s want workspace not found", rec.Body.String())
	}
	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatal("missing GET mutated the workspaces directory")
	}
}

func TestHandleWorkspaceDelegationGet_InvalidIDIs400AndZeroWrite(t *testing.T) {
	api, _ := buildWorkspaceDelegationTestAPI(t)
	dir := filepath.Join(api.homePath, "workspaces")
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec := getDelegation(t, api, "../escape")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
	}
	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatal("invalid-id GET mutated storage")
	}
}

func TestHandleWorkspaceDelegationGet_UnreadableDelegationIs500NotEmptySuccess(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)
	storePath := filepath.Join(workspace.DelegationStoreDir(api.homePath), id+".json")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o700); err != nil {
		t.Fatal(err)
	}
	truncated := []byte(`{"workspace_id":"` + id + `","delegation":[{"from_agent":"jim"`)
	if err := os.WriteFile(storePath, truncated, 0o600); err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(api.homePath, "workspaces", id+".json")
	beforeRecord := snapshotBytes(t, recordPath)
	beforeStore := snapshotBytes(t, storePath)

	rec := getDelegation(t, api, id)
	if rec.Code == http.StatusOK {
		t.Fatalf("corrupt store must not succeed as an empty graph: %s", rec.Body.String())
	}
	if rec.Code == http.StatusNotFound {
		t.Fatalf("corrupt store must not be reported missing: %s", rec.Body.String())
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500 body=%s", rec.Code, rec.Body.String())
	}
	msg := strings.ToLower(restErrorMessage(t, rec.Body.Bytes()))
	if !strings.Contains(msg, "delegation") || !strings.Contains(msg, "unreadable") {
		t.Fatalf("error must name unreadable delegation storage: %q", msg)
	}
	if strings.Contains(msg, "not found") {
		t.Fatalf("must not claim the workspace is missing: %q", msg)
	}
	if strings.Contains(msg, "remove") {
		t.Fatalf("must not default to deleting the store: %q", msg)
	}
	if string(snapshotBytes(t, recordPath)) != string(beforeRecord) || string(snapshotBytes(t, storePath)) != string(beforeStore) {
		t.Fatal("GET mutated workspace or delegation bytes")
	}
}

func TestHandleWorkspaceDelegationGet_CorruptWorkspaceRecordIs500Not404(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)
	recordPath := filepath.Join(api.homePath, "workspaces", id+".json")
	if err := os.WriteFile(recordPath, []byte(`{"id":"`+id+`","name":`), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotBytes(t, recordPath)

	rec := getDelegation(t, api, id)
	if rec.Code == http.StatusOK {
		t.Fatalf("corrupt workspace record must not succeed: %s", rec.Body.String())
	}
	if rec.Code == http.StatusNotFound {
		t.Fatalf("corrupt workspace record must not be reported missing: %s", rec.Body.String())
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500 body=%s", rec.Code, rec.Body.String())
	}
	msg := strings.ToLower(restErrorMessage(t, rec.Body.Bytes()))
	if !strings.Contains(msg, "unreadable") {
		t.Fatalf("corrupt record must be reported unreadable: %q", msg)
	}
	if strings.Contains(msg, "workspace not found") {
		t.Fatalf("corrupt record must not use the missing-workspace message: %q", msg)
	}
	if string(snapshotBytes(t, recordPath)) != string(before) {
		t.Fatal("GET mutated the corrupt workspace record")
	}
}
