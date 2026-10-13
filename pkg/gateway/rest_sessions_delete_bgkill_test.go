package gateway

// Tree Stop before DELETE kills the session's background shells (FR-022, founder
// decision Q13). A shell the Stop could not kill must refuse the delete visibly:
// deleting the row would leave a running process with nothing to show for it.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
)

func TestStopBeforeDelete_BackgroundKillFailureRefusesDelete(t *testing.T) {
	cases := []struct {
		name string
		res  agent.StopResult
	}{
		{"one failed kill refuses", agent.StopResult{BackgroundFailed: 1}},
		{"failed kills beside successes still refuse", agent.StopResult{BackgroundKilled: 2, BackgroundFailed: 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := &restAPI{
				agentLoop: &agent.AgentLoop{},
				stopSession: func(context.Context, agent.StopRequest) (agent.StopResult, error) {
					return tc.res, nil
				},
			}
			r := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/s1", nil)
			summary := api.stopBeforeDelete(r, "s1")
			require.NotEmpty(t, summary, "a failed background kill must refuse the delete")
			assert.Contains(t, summary, "background")
			assert.True(t, strings.Contains(summary, "nothing was deleted"), "summary=%q", summary)
		})
	}
}
