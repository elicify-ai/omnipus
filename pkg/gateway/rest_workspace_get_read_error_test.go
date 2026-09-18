package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/workspace"
)

func getWorkspaceHTTP(t *testing.T, api *restAPI, id string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+id, nil)
	r.URL.Path = "/api/v1/workspaces/" + id
	api.HandleWorkspaces(w, r)
	return w
}

func decodeRESTError(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %d body: %v\n%s", rec.Code, err, rec.Body.String())
	}
	return body
}

func snapshotFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return b
}

func TestHandleWorkspaceGet_MissingIs404AndZeroWrite(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	dir := filepath.Join(api.homePath, "workspaces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec := getWorkspaceHTTP(t, api, "01JXNOTEXISTENT00000000000")
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
		t.Fatalf("GET missing mutated workspaces dir before=%d after=%d", len(before), len(after))
	}
}

func TestHandleWorkspaceGet_InvalidIDIs400AndZeroWrite(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	dir := filepath.Join(api.homePath, "workspaces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A single malformed segment reaches ID validation; a slash selects another route.
	rec := getWorkspaceHTTP(t, api, "bad..id")
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

func TestHandleWorkspaceGet_UnreadableDelegationIs500Not404AndZeroWrite(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "Keep Me", "")
	storePath := filepath.Join(workspace.DelegationStoreDir(api.homePath), id+".json")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o700); err != nil {
		t.Fatal(err)
	}
	truncated := []byte(`{"workspace_id":"` + id + `","delegation":[{"from_agent":"jim"`)
	if err := os.WriteFile(storePath, truncated, 0o600); err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(api.homePath, "workspaces", id+".json")
	beforeRecord := snapshotFile(t, recordPath)
	beforeStore := snapshotFile(t, storePath)

	rec := getWorkspaceHTTP(t, api, id)
	if rec.Code == http.StatusNotFound {
		t.Fatalf("unreadable delegation reported as missing: %s", rec.Body.String())
	}
	if rec.Code == http.StatusOK {
		t.Fatalf("unreadable delegation must not succeed with omitted graph: %s", rec.Body.String())
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500 body=%s", rec.Code, rec.Body.String())
	}
	body := decodeRESTError(t, rec)
	msg := strings.ToLower(strings.TrimSpace(stringifyRESTError(body)))
	if !strings.Contains(msg, "delegation") || !strings.Contains(msg, "unreadable") {
		t.Fatalf("error must name unreadable delegation storage: %v", body)
	}
	if strings.Contains(msg, "not found") {
		t.Fatalf("must not claim the workspace is missing: %v", body)
	}
	if strings.Contains(msg, "remove") {
		t.Fatalf("must not default to deleting the store: %v", body)
	}
	if string(snapshotFile(t, recordPath)) != string(beforeRecord) || string(snapshotFile(t, storePath)) != string(beforeStore) {
		t.Fatal("GET mutated workspace or delegation bytes")
	}
}

func TestHandleWorkspaceGet_CorruptRecordIs500Not404AndZeroWrite(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "Keep Me", "")
	recordPath := filepath.Join(api.homePath, "workspaces", id+".json")
	corrupt := []byte(`{"id":"` + id + `","name":`)
	if err := os.WriteFile(recordPath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotFile(t, recordPath)

	rec := getWorkspaceHTTP(t, api, id)
	if rec.Code == http.StatusNotFound {
		t.Fatalf("corrupt record reported as missing: %s", rec.Body.String())
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500 body=%s", rec.Code, rec.Body.String())
	}
	body := decodeRESTError(t, rec)
	msg := strings.ToLower(stringifyRESTError(body))
	if !strings.Contains(msg, "unreadable") {
		t.Fatalf("corrupt record must be reported unreadable: %v", body)
	}
	if strings.Contains(msg, "workspace not found") {
		t.Fatalf("corrupt record must not use the missing-workspace message: %v", body)
	}
	if string(snapshotFile(t, recordPath)) != string(before) {
		t.Fatal("GET mutated the corrupt record")
	}
}

func stringifyRESTError(body map[string]any) string {
	if v, ok := body["error"].(string); ok {
		return v
	}
	return ""
}
