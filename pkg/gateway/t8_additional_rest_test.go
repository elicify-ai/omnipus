package gateway

// Independent r17 3.8: person-written overrides are not agent ceilings.
import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT8TaskBudget_PersonMayExceedAgentCeiling(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	api.agentLoop.GetConfig().Planning.TaskMaxAttempts = 3
	ws := ensureTestWorkspace(t, api)
	tk := createTaskViaAPI(t, api, "person retry override", ws)
	w := patchTask(t, api, tk.Id, `{"max_attempts":5}`)
	require.Equal(t, http.StatusOK, w.Code, "3.8 person override is uncapped: %s", w.Body.String())
	got := getFreezeTask(t, api, tk.Id)
	require.NotNil(t, got.MaxAttempts)
	assert.EqualValues(t, 5, *got.MaxAttempts)
	persisted, err := api.taskStore.Get(tk.Id)
	require.NoError(t, err)
	require.NotNil(t, persisted.MaxAttempts)
	assert.Equal(t, 5, *persisted.MaxAttempts)
}
