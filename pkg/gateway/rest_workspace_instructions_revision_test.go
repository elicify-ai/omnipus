package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/workspace"
)

func TestWorkspaceInstructionsPut_RequiresReviewedRevisionAndReadback(t *testing.T) {
	api, id := buildWorkspaceInstructionsTestAPI(t)
	emptyRev := workspace.RevisionForInstructions("")
	rec := putInstructions(t, api, id, `{"content":"first","revision":"`+emptyRev+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	var state map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state["persistence_status"] != "complete" || state["activation_status"] != "active" {
		t.Fatalf("envelope=%v", state)
	}
	got, err := workspace.ReadInstructions(api.homePath, id)
	if err != nil || got != "first" {
		t.Fatalf("readback=%q err=%v", got, err)
	}
	if state["revision"] != workspace.RevisionForInstructions(got) {
		t.Fatalf("response revision=%v want %s", state["revision"], workspace.RevisionForInstructions(got))
	}
}

func TestWorkspaceInstructionsPut_StaleRevisionIs409ZeroWrite(t *testing.T) {
	api, id := buildWorkspaceInstructionsTestAPI(t)
	emptyRev := workspace.RevisionForInstructions("")
	if rec := putInstructions(t, api, id, `{"content":"first","revision":"`+emptyRev+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("seed write failed: %s", rec.Body.String())
	}
	path := filepath.Join(api.homePath, "workspaces", id, "AGENT.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stale := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	rec := putInstructions(t, api, id, `{"content":"second","revision":"`+stale+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409 body=%s", rec.Code, rec.Body.String())
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("conflict wrote disk: %q err=%v", after, err)
	}
}

func TestWorkspaceInstructionsPut_MalformedRevisionIs400ZeroWrite(t *testing.T) {
	api, id := buildWorkspaceInstructionsTestAPI(t)
	dir := filepath.Join(api.homePath, "workspaces", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadDir(dir)
	rec := putInstructions(t, api, id, `{"content":"nope","revision":"not-a-revision"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
	}
	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatal("malformed revision wrote files")
	}
}

func TestWorkspaceInstructionsGet_ReturnsRevisionOfEmptyContent(t *testing.T) {
	api, id := buildWorkspaceInstructionsTestAPI(t)
	rec := getInstructions(t, api, id)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["content"] != "" {
		t.Fatalf("content=%v want empty", payload["content"])
	}
	if payload["revision"] != workspace.RevisionForInstructions("") {
		t.Fatalf("revision=%v", payload["revision"])
	}
}

func TestWorkspaceInstructionsGet_OversizedFileReturns500WithoutEditableSnapshot(t *testing.T) {
	api, id := buildWorkspaceInstructionsTestAPI(t)
	dir := workspace.WorkspaceDir(api.homePath, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "AGENT.md")
	original := []byte(strings.Repeat("x", 262144+1)) // Workspace instructions contract maximum + 1 byte.
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}

	rec := getInstructions(t, api, id)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500 body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["content"]; ok {
		t.Fatalf("oversized management read exposed editable content: %v", payload["content"])
	}
	if _, ok := payload["revision"]; ok {
		t.Fatalf("oversized management read exposed synthetic revision: %v", payload["revision"])
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatal("GET changed the oversized instructions file")
	}
}
