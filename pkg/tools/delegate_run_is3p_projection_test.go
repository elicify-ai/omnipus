package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// staticRegistry answers IsExternalCLI with a fixed value: the LIVE registry
// read the old code used to build the response, so a test can make it disagree
// with the classification Launch persisted.
type staticRegistry struct{ external bool }

func (r staticRegistry) IsExternalCLI(string) bool { return r.external }

// Oracle: NEW-2 (security re-verification) — the initial delegate response
// names the child that was CREATED, so is_3p is the classification Launch
// persisted for it, never a second read of the live registry that a supported
// executor update may have changed between Launch and the response.
func TestDelegateRun_IsThreeP_ProjectsThePersistedLaunchClassification(t *testing.T) {
	cases := []struct {
		name         string
		persisted    bool
		registryLive bool
	}{
		{"native child, executor updated to external after launch", false, true},
		{"external child, executor updated to native after launch", true, false},
		{"native child, registry agrees", false, false},
		{"external child, registry agrees", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, launcher := u14PermissiveTool(t)
			launcher.launchRes = steer.LaunchResult{SessionID: "child-is3p", Generation: 1, Is3P: tc.persisted}
			tool.SetAgentRegistry(func() DelegateAgentRegistry { return staticRegistry{external: tc.registryLive} })
			ctx := WithAgentID(WithTranscriptSessionID(context.Background(), "parent-1"), "mia")

			result := tool.Execute(ctx, map[string]any{"action": "run", "agent_id": "worker", "task": "do it"})
			if result.IsError {
				t.Fatalf("delegate(run) returned error: %s", result.ForLLM)
			}
			payload, _, _ := strings.Cut(result.ForLLM, "\n")
			var response generated.DelegateSessionResponse
			if err := json.Unmarshal([]byte(payload), &response); err != nil {
				t.Fatalf("decode response: %v; payload: %s", err, payload)
			}
			if response.Is3p != tc.persisted {
				t.Fatalf("is_3p = %v, want the persisted launch classification %v (live registry said %v)",
					response.Is3p, tc.persisted, tc.registryLive)
			}
		})
	}
}
