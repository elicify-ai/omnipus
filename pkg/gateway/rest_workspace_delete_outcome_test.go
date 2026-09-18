package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

func decodeMutationState(t *testing.T, body []byte) gen.ConfigurationMutationState {
	t.Helper()
	var state gen.ConfigurationMutationState
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatalf("decode mutation state: %v\n%s", err, body)
	}
	return state
}

func TestHandleWorkspaceDelete_CompleteEnvelopeIs200Not204(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "DeleteEnvelope", "")
	record := filepath.Join(api.homePath, "workspaces", id+".json")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, workspaceDeleteURL(t, api, id), nil)
	api.handleWorkspaceDelete(w, r, id)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", w.Code, w.Body.String())
	}
	state := decodeMutationState(t, w.Body.Bytes())
	if state.PersistenceStatus != gen.ConfigurationMutationStatePersistenceStatusComplete {
		t.Fatalf("persistence=%s want complete", state.PersistenceStatus)
	}
	if state.ActivationStatus != gen.ConfigurationMutationStateActivationStatusActive {
		t.Fatalf("activation=%s want active", state.ActivationStatus)
	}
	if state.Revision != workspace.EmptyRevision() {
		t.Fatalf("revision=%q want empty-resource digest", state.Revision)
	}
	if len(state.ChangedFields) != 1 || state.ChangedFields[0] != "workspace" {
		t.Fatalf("changed_fields=%v want [workspace]", state.ChangedFields)
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatalf("workspace record must be gone: %v", err)
	}
}

func TestHandleWorkspaceDelete_StaleRevisionIs409ZeroWrite(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "StaleDelete", "")
	record := filepath.Join(api.homePath, "workspaces", id+".json")
	before, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	stale := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/"+id+"?revision="+stale, nil)
	api.handleWorkspaceDelete(w, r, id)
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409 body=%s", w.Code, w.Body.String())
	}
	after, err := os.ReadFile(record)
	if err != nil || string(after) != string(before) {
		t.Fatalf("stale delete must not write; after=%q err=%v", after, err)
	}
}

func TestHandleWorkspaceDelete_DirectoryWipeFailureReportsPartialEnvelope(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "PartialDir", "")
	record := filepath.Join(api.homePath, "workspaces", id+".json")
	dir := workspace.WorkspaceDir(api.homePath, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	prev := removeAllFn
	removeAllFn = func(string) error { return errors.New("injected directory wipe failure") }
	t.Cleanup(func() { removeAllFn = prev })

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, workspaceDeleteURL(t, api, id), nil)
	api.handleWorkspaceDelete(w, r, id)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500 body=%s", w.Code, w.Body.String())
	}
	state := decodeMutationState(t, w.Body.Bytes())
	if state.PersistenceStatus != gen.ConfigurationMutationStatePersistenceStatusPartial {
		t.Fatalf("persistence=%s want partial", state.PersistenceStatus)
	}
	if state.ActivationStatus != gen.ConfigurationMutationStateActivationStatusNotAttempted {
		t.Fatalf("activation=%s want not_attempted", state.ActivationStatus)
	}
	if state.ErrorStage == nil || *state.ErrorStage != "remove_directory" {
		t.Fatalf("error_stage=%v want remove_directory", state.ErrorStage)
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatalf("authoritative record must already be gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENT.md")); err != nil {
		t.Fatalf("directory leftover must remain after partial wipe: %v", err)
	}
}
