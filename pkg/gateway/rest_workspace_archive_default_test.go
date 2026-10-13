// rest_workspace_archive_default_test.go: S2-01 (combined review, seed O4).
// The 409 for "archive the default workspace" must be decided BEFORE
// prepareWorkspacePutMembers pauses plans, exactly like the built-in removal
// refusal (rest_workspace_hidden_flag_test.go). ORACLE: the PUT contract —
// a refused request leaves plans, the workspace record and delegation as they
// were ("409 means nothing changed").

package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspacePut_DefaultArchiveRefusalPrecedesPlanEffects(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JARCHIVEDEFAULTREFUSE01"
	hiddenFlagSeedWorkspace(t, env.home, ws, true, []string{"jim", "mia"}, json.RawMessage(`{
		"jim": {"heartbeat": {"enabled": true, "interval_minutes": 10, "body": "Check."}}
	}`))

	planStore := plan.New(filepath.Join(env.home, "plans"))
	env.api.planStore = planStore
	pe := agent.NewPlanEngine(env.api.agentLoop, planStore, env.api.taskStore, env.api.taskExecutor)
	env.api.agentLoop.SetPlanEngine(pe)
	t.Cleanup(pe.Stop)

	p := &plan.Plan{Title: "jim plan", WorkspaceID: ws, OwnerAgentID: "jim", State: plan.StateRunning}
	require.NoError(t, planStore.Create(p), "seed running plan")

	// Archive the default workspace AND disable Jim's heartbeat in one request.
	body := fmt.Sprintf(`{"revision":%q,"status":"archived","member_configs":{"jim":{"heartbeat":{"enabled":false,"interval_minutes":10,"body":"Check."}}}}`,
		currentRevision(env.home, ws))
	w := hiddenFlagPutRaw(t, env.api, ws, body)
	require.Equal(t, http.StatusConflict, w.Code, "refusal body=%s", w.Body.String())

	got, err := planStore.Get(p.ID)
	require.NoError(t, err)
	assert.Equal(t, plan.StateRunning, got.State)
	assert.Equal(t, "", got.PausedReason,
		"the plan must NOT be paused: the 409 must be decided before any plan side effect")

	disk := hiddenFlagWorkspaceDiskJSON(t, env.api, ws)
	assert.Equal(t, "active", disk["status"], "the default workspace stays active")
	jimEntry, _ := disk["member_configs"].(map[string]any)["jim"].(map[string]any)
	require.NotNil(t, jimEntry)
	jimHB, _ := jimEntry["heartbeat"].(map[string]any)
	require.NotNil(t, jimHB)
	assert.Equal(t, true, jimHB["enabled"], "Jim's heartbeat stays enabled")
}
