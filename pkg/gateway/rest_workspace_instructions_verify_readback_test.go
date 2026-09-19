package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/workspace"
)

func TestWorkspaceInstructionsPut_VerifyReadFailureOmitsRevision(t *testing.T) {
	api, id := buildWorkspaceInstructionsTestAPI(t)
	emptyRev := workspace.RevisionForInstructions("")
	orig := readWorkspaceInstructionsAfterWrite
	readWorkspaceInstructionsAfterWrite = func(string, string) (string, error) {
		return "", errors.New("injected readback failure")
	}
	t.Cleanup(func() { readWorkspaceInstructionsAfterWrite = orig })

	rec := putInstructions(t, api, id, `{"content":"written-bytes","revision":"`+emptyRev+`"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500 body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["revision"]; ok {
		t.Fatalf("verify failure must omit revision, got %v", payload["revision"])
	}
	if payload["error_stage"] != "verify_instructions" {
		t.Fatalf("error_stage=%v", payload["error_stage"])
	}
	if payload["persistence_status"] != "partial" {
		t.Fatalf("persistence_status=%v", payload["persistence_status"])
	}
	msg, _ := payload["message"].(string)
	if !strings.Contains(msg, "revision is unknown") {
		t.Fatalf("message=%q", msg)
	}
	if strings.Contains(rec.Body.String(), emptyRev) {
		t.Fatal("pre-write digest must not be reported as current")
	}
	got, err := workspace.ReadInstructions(api.homePath, id)
	if err != nil || got != "written-bytes" {
		t.Fatalf("write should have landed: got=%q err=%v", got, err)
	}
}
