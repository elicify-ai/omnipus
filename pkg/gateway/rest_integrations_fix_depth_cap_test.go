package gateway

// Issue #1055: the default-search card needs the operator's effective Tavily
// depth cap, not a guessed value. Contract-first GREEN adds an OPTIONAL
// search_depth_cap on the generated IntegrationProvider schema, then populates
// it in the real GET response. This RED test decodes raw JSON until that
// generated field exists; no parallel wire-format type is introduced.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestFix1055_IntegrationGETReportsEffectiveTavilyDepthCap(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured string
		want       string
	}{
		{"operator basic cap", "basic", "basic"},
		{"unset legacy cap uses effective advanced", "", "advanced"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, user, cfg := newRolesTestAPI(t)
			cfg.Tools.Web.Tavily = config.TavilyConfig{Enabled: true, SearchDepth: tc.configured}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/providers", nil)
			req = req.WithContext(context.WithValue(req.Context(), UserContextKey{}, user))
			w := httptest.NewRecorder()
			api.HandleIntegrationProviders(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("GET integrations status = %d, want 200: %s", w.Code, w.Body.String())
			}

			var response map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode GET integrations JSON: %v", err)
			}
			rows, ok := response["search"].([]any)
			if !ok {
				t.Fatalf("search catalogue = %T, want array", response["search"])
			}
			foundTavily, foundDDG := false, false
			for _, item := range rows {
				row, ok := item.(map[string]any)
				if !ok {
					t.Fatalf("search row = %T, want object", item)
				}
				switch row["id"] {
				case "tavily":
					foundTavily = true
					depthCap, present := row["search_depth_cap"]
					if !present {
						t.Fatal("BLOCKED: optional search_depth_cap on IntegrationProvider GET row not implemented — required by #1055 depth-cap acceptance criterion")
					}
					if depthCap != tc.want {
						t.Fatalf("Tavily search_depth_cap = %v, want %q from effective operator setting", depthCap, tc.want)
					}
				case "duckduckgo":
					foundDDG = true
					if depthCap, present := row["search_depth_cap"]; present {
						t.Fatalf("DuckDuckGo must have no depth-cap field, got %v", depthCap)
					}
				}
			}
			if !foundTavily || !foundDDG {
				t.Fatalf("GET search catalogue missing Tavily or DuckDuckGo: tavily=%v duckduckgo=%v", foundTavily, foundDDG)
			}
		})
	}
}
