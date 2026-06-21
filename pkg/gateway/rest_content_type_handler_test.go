//go:build !cgo

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
)

// TestHandleAgentsCreate_201IsJSON drives the real 201 handler end-to-end
// (not just the writeJSON helper) and asserts the Content-Type, guarding #96 at
// the handler layer: the bug was a handler calling w.WriteHeader(201) before
// jsonOK, so a future edit reintroducing that ordering must fail a test that
// exercises the handler — the helper-level test alone would stay green.
func TestHandleAgentsCreate_201IsJSON(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/agents",
		strings.NewReader(`{"name":"Scout","soul":"Scout soul"}`),
	)
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("201 Content-Type = %q, want application/json (#96)", ct)
	}
	var resp gen.Agent
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("201 body is not valid JSON: %v (body=%s)", err, w.Body.String())
	}
	if resp.Name != "Scout" {
		t.Errorf("created agent name = %q, want Scout", resp.Name)
	}
}
