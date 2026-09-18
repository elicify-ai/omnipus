package gateway

import (
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A reviewed save must distinguish an unreadable existing record from deletion.
func TestWorkspaceDelegationPut_ReadFailureClassification(t *testing.T) {
	for _, kind := range []string{"workspace", "delegation", "missing"} {
		t.Run(kind, func(t *testing.T) {
			api, id := buildWorkspaceDelegationTestAPI(t)
			state, err := workspace.ReadState(api.homePath, id)
			if err != nil {
				t.Fatal(err)
			}
			record := filepath.Join(api.homePath, "workspaces", id+".json")
			graph := filepath.Join(workspace.DelegationStoreDir(api.homePath), id+".json")
			switch kind {
			case "workspace":
				if err := os.WriteFile(record, []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			case "delegation":
				if err := os.MkdirAll(filepath.Dir(graph), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(graph, []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(record); err != nil {
					t.Fatal(err)
				}
			}
			beforeRecord, beforeGraph := snapshotBytes(t, record), snapshotBytes(t, graph)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+id+"/delegation", strings.NewReader(`{"edges":[],"revision":"`+state.Revision+`"}`))
			req.Header.Set("Content-Type", "application/json")
			api.HandleWorkspaces(rec, req)
			want := http.StatusInternalServerError
			if kind == "missing" {
				want = http.StatusNotFound
			}
			if rec.Code != want {
				t.Fatalf("status=%d want %d body=%s", rec.Code, want, rec.Body.String())
			}
			if kind != "missing" && strings.Contains(rec.Body.String(), "not found") {
				t.Fatalf("storage error reported missing: %s", rec.Body.String())
			}
			if string(snapshotBytes(t, record)) != string(beforeRecord) || string(snapshotBytes(t, graph)) != string(beforeGraph) {
				t.Fatal("failed save changed stored bytes")
			}
		})
	}
}
