package gateway

// Additive C1 null oracles: canonical non-nullable PerformanceSettingsUpdate.
// Config nil/default semantics do not define an HTTP null-as-clear operation.
// Correct implementation already exists: post-API baselines, NOT original RED.
import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestPerformanceGoalTimersC1NullContract(t *testing.T) {
	keys := []string{"goal_reminder_quiet_seconds", "goal_stalled_checkin_enabled", "goal_stalled_checkin_minutes"}
	for _, validation := range []bool{false, true} {
		t.Run(fmt.Sprintf("KnownAuthenticatedWrite/validation_%t", validation), func(t *testing.T) {
			api := c1NullContractSeed(t, validation, false)
			before := mtiGetPerf(t, api)
			require.NotContains(t, before, "pending_apply", "ordinary successful refresh must not manufacture pending metadata")
			c1NullContractValues(t, before, 11, false)
		})
		for _, enabled := range []bool{false, true} {
			for _, key := range keys {
				t.Run(fmt.Sprintf("RejectPresentNull/validation_%t/stalled_%t/%s", validation, enabled, key), func(t *testing.T) {
					api := c1NullContractSeed(t, validation, enabled)
					// Establish nonempty pending metadata through an actual failed
					// refresh, not a handwritten pending object or callback hook.
					live := mtiGetPerf(t, api)
					restore := c1RefreshFault(t, api)
					defer restore()
					failed := mtiPutPerf(t, api, `{"tools_on_demand":true}`)
					require.Equal(t, http.StatusInternalServerError, failed.Code, "existing real refresh fault must be reached: %s", failed.Body.String())
					failure := decodeReloadFailed(t, failed.Body.Bytes())
					require.Equal(t, "performance_reload_failed", failure.Code)
					require.Equal(t, "refresh", string(failure.Details.Stage))
					expectedPending := map[string]any{"stage": "refresh", "changed_fields": []any{"tools_on_demand"}}
					expectedLive := make(map[string]any, len(live)+1)
					for field, value := range live {
						expectedLive[field] = value
					}
					expectedLive["pending_apply"] = expectedPending
					beforeGET := mtiGetPerf(t, api)
					require.Equal(t, expectedLive, beforeGET, "pending metadata has an exact independent contract shape")
					beforeDisk, err := os.ReadFile(api.configPath())
					require.NoError(t, err)
					require.Equal(t, validation, api.agentLoop.GetConfig().Gateway.ValidateInbound)
					// Recognized sibling differs from current11. Removing ONLY
					// null refusal with validationOFF would otherwise save it.
					body := map[string]any{"goal_max_rounds": 19, key: nil}
					if key != "goal_stalled_checkin_enabled" {
						body["goal_stalled_checkin_enabled"] = enabled
					}
					refused := mtiPutPerf(t, api, c1PerformanceJSON(t, body))
					require.Equal(t, http.StatusBadRequest, refused.Code, "present null is not omission, including while OFF: %s", refused.Body.String())
					envelope := mtiDecode(t, refused.Body.Bytes())
					message, ok := envelope["error"].(string)
					require.True(t, ok, "canonical error envelope requires a string error")
					require.NotEmpty(t, message)
					afterDisk, err := os.ReadFile(api.configPath())
					require.NoError(t, err)
					require.Equal(t, beforeDisk, afterDisk, "invalid request must write ZERO saved configuration bytes")
					afterGET := mtiGetPerf(t, api)
					require.Equal(t, beforeGET, afterGET, "whole live GET and pending metadata must stay identical")
					require.Equal(t, expectedPending, afterGET["pending_apply"])
					c1NullContractValues(t, afterGET, 11, enabled)
				})
			}
		}
		t.Run(fmt.Sprintf("OmittedTimersRetainFalseAndNondefaults/validation_%t", validation), func(t *testing.T) {
			api := c1NullContractSeed(t, validation, false)
			beforeGET := mtiGetPerf(t, api)
			beforeDisk, err := os.ReadFile(api.configPath())
			require.NoError(t, err)
			expectedGET := make(map[string]any, len(beforeGET))
			for field, value := range beforeGET {
				expectedGET[field] = value
			}
			expectedGET["goal_max_rounds"] = float64(19)
			expectedDisk := mtiDecode(t, beforeDisk)
			planning, ok := expectedDisk["planning"].(map[string]any)
			require.True(t, ok)
			planning["goal_max_rounds"] = float64(19)
			accepted := mtiPutPerf(t, api, `{"goal_max_rounds":19}`)
			require.Equal(t, http.StatusOK, accepted.Code, "omission is a valid partial update, unlike null: %s", accepted.Body.String())
			require.Equal(t, expectedGET, mtiDecode(t, accepted.Body.Bytes()), "whole PUT response differs only in the requested old sibling")
			require.Equal(t, expectedGET, mtiGetPerf(t, api))
			afterDisk, err := os.ReadFile(api.configPath())
			require.NoError(t, err)
			require.Equal(t, expectedDisk, mtiDecode(t, afterDisk), "full saved config differs only in requested goal tries")
			c1NullContractValues(t, mtiGetPerf(t, api), 19, false)
			fresh, err := config.LoadConfig(api.configPath())
			require.NoError(t, err)
			encoded, err := json.Marshal(fresh.Planning)
			require.NoError(t, err)
			c1NullContractValues(t, mtiDecode(t, encoded), 19, false)
		})
	}
}

func c1NullContractSeed(t *testing.T, validation, enabled bool) *restAPI {
	t.Helper()
	api := c1NewPerformanceAPI(t)
	c1PerformanceInbound(t, api, validation)
	body := map[string]any{
		"max_parallel_agents": 4, "tools_on_demand": false, "goal_max_rounds": 11,
		"goal_reminder_quiet_seconds": 120, "goal_stalled_checkin_enabled": enabled, "goal_stalled_checkin_minutes": 45,
	}
	accepted := mtiPutPerf(t, api, c1PerformanceJSON(t, body))
	require.Equal(t, http.StatusOK, accepted.Code, "real authenticated valid timer write must work before null observations: %s", accepted.Body.String())
	live := mtiGetPerf(t, api)
	require.Equal(t, live, mtiDecode(t, accepted.Body.Bytes()), "full returned state and live GET must agree")
	c1NullContractValues(t, live, 11, enabled)
	require.Equal(t, float64(4), live["max_parallel_agents"])
	require.Equal(t, false, live["tools_on_demand"])
	data, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	planning, ok := mtiDecode(t, data)["planning"].(map[string]any)
	require.True(t, ok)
	c1NullContractValues(t, planning, 11, enabled)
	// Successful refresh reloads config. Reapply the existing test-only schema
	// switch immediately before subject requests, never assume it survived.
	c1PerformanceInbound(t, api, validation)
	return api
}

func c1NullContractValues(t *testing.T, raw map[string]any, tries int, enabled bool) {
	t.Helper()
	require.Equal(t, float64(tries), raw["goal_max_rounds"])
	require.Equal(t, float64(120), raw["goal_reminder_quiet_seconds"])
	require.Equal(t, enabled, raw["goal_stalled_checkin_enabled"])
	require.Equal(t, float64(45), raw["goal_stalled_checkin_minutes"])
}
