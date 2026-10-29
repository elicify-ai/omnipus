package gateway

// C1 DESIGN-v2 + chief independent clocks. Expectations precede implementation.
// Raw JSON crosses the real generated decoder; no future Go field is required.
import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func TestPerformanceGoalTimersC1(t *testing.T) {
	t.Run("ExistingHandlerAndStepUpInstrument", func(t *testing.T) {
		api := c1NewPerformanceAPI(t)
		require.Equal(t, float64(7), mtiGetPerf(t, api)["goal_max_rounds"])
		w := mtiPutPerf(t, api, `{"goal_max_rounds":9}`)
		require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
		require.Equal(t, float64(9), mtiGetPerf(t, api)["goal_max_rounds"])
		w = mtiPutPerf(t, api, `{"goal_max_rounds":0}`)
		require.Equal(t, http.StatusBadRequest, w.Code)
	})
	for _, mode := range []string{"Defaults", "ZeroPlanning", "IdleExpiryOnly", "NullPointers", "ExplicitFalse"} {
		t.Run("ResolvedGET/"+mode, func(t *testing.T) {
			api := c1NewPerformanceAPI(t)
			if mode == "ExplicitFalse" {
				c1SeedPerformance(t, api, map[string]any{"goal_reminder_quiet_seconds": 120, "goal_stalled_checkin_enabled": false, "goal_stalled_checkin_minutes": 45})
			} else if mode != "Defaults" {
				cfg, err := api.agentLoop.GetConfig().Clone()
				require.NoError(t, err)
				cfg.Planning = config.PlanningConfig{}
				if mode == "IdleExpiryOnly" {
					cfg.Planning.IdleExpiryDays = 1
				}
				if mode == "NullPointers" {
					require.NoError(t, json.Unmarshal([]byte(`{"goal_reminder_quiet_seconds":null,"goal_stalled_checkin_enabled":null,"goal_stalled_checkin_minutes":null}`), &cfg.Planning))
				}
				api.agentLoop.SwapConfig(cfg)
			}
			got := mtiGetPerf(t, api)
			require.NotNil(t, got["goal_max_rounds"], "existing GET field proves handler instrumentation")
			if mode == "ExplicitFalse" {
				c1PerformanceTuple(t, got, 120, false, 45)
			} else {
				c1PerformanceTuple(t, got, 60, true, 30)
			}
		})
	}
	patches := []map[string]any{
		{"goal_reminder_quiet_seconds": 180}, {"goal_stalled_checkin_enabled": false}, {"goal_stalled_checkin_minutes": 90},
		{"goal_reminder_quiet_seconds": 180, "goal_stalled_checkin_enabled": true},
		{"goal_reminder_quiet_seconds": 180, "goal_stalled_checkin_enabled": true, "goal_stalled_checkin_minutes": 90},
		{"goal_stalled_checkin_enabled": true}, {"goal_max_rounds": 11},
	}
	for i, patch := range patches {
		t.Run(fmt.Sprintf("PartialPUT/%d", i), func(t *testing.T) { c1PerformancePartial(t, patch) })
	}
	for _, spec := range []struct {
		key    string
		values []int
	}{
		{"goal_reminder_quiet_seconds", []int{30, 31, 3599, 3600}}, {"goal_stalled_checkin_minutes", []int{5, 6, 1439, 1440}},
	} {
		for _, value := range spec.values {
			t.Run(fmt.Sprintf("InclusivePUT/%s/%d", spec.key, value), func(t *testing.T) { c1PerformancePartial(t, map[string]any{spec.key: value}) })
		}
	}
	for _, inbound := range []bool{false, true} {
		t.Run(fmt.Sprintf("EmptyPUT/validation_%t", inbound), func(t *testing.T) {
			api := c1NewPerformanceAPI(t)
			c1PerformanceInbound(t, api, inbound)
			before, err := os.ReadFile(api.configPath())
			require.NoError(t, err)
			live := mtiGetPerf(t, api)
			w := mtiPutPerf(t, api, `{}`)
			require.Equal(t, http.StatusBadRequest, w.Code)
			require.Contains(t, strings.ToLower(w.Body.String()), "required")
			after, err := os.ReadFile(api.configPath())
			require.NoError(t, err)
			require.Equal(t, before, after)
			require.Equal(t, live, mtiGetPerf(t, api))
		})
		for _, spec := range []struct {
			key string
			bad []any
		}{
			{"goal_reminder_quiet_seconds", []any{-1, 0, 29, 3601, 30.5, "60", false}},
			{"goal_stalled_checkin_minutes", []any{-1, 0, 4, 1441, 5.5, "30", false}},
			{"goal_stalled_checkin_enabled", []any{0, "false"}},
		} {
			for i, value := range spec.bad {
				t.Run(fmt.Sprintf("RejectPUT/validation_%t/%s/%d", inbound, spec.key, i), func(t *testing.T) {
					api := c1NewPerformanceAPI(t)
					c1PerformanceInbound(t, api, inbound)
					before, err := os.ReadFile(api.configPath())
					require.NoError(t, err)
					live := mtiGetPerf(t, api)
					// A recognized sibling prevents today's unknown-key no-op400
					// from falsely proving C1 range validation with validation OFF.
					body := map[string]any{"goal_max_rounds": 9, "goal_stalled_checkin_enabled": false}
					body[spec.key] = value
					w := mtiPutPerf(t, api, c1PerformanceJSON(t, body))
					require.Equal(t, http.StatusBadRequest, w.Code, "invalid field, including minutes while OFF, must refuse before any write: %s", w.Body.String())
					refusal := mtiDecode(t, w.Body.Bytes())
					require.NotEmpty(t, refusal["error"])
					after, err := os.ReadFile(api.configPath())
					require.NoError(t, err)
					require.Equal(t, before, after)
					require.Equal(t, live, mtiGetPerf(t, api))
				})
			}
		}
	}
	t.Run("ExistingRefreshFailureInstrument", c1ExistingRefreshFailure)
	for i, patch := range []map[string]any{
		{"goal_reminder_quiet_seconds": 180}, {"goal_stalled_checkin_enabled": false}, {"goal_stalled_checkin_minutes": 90},
		{"goal_max_rounds": 9, "goal_reminder_quiet_seconds": 180, "goal_stalled_checkin_enabled": false, "goal_stalled_checkin_minutes": 90},
	} {
		t.Run(fmt.Sprintf("SavedNotApplied/%d", i), func(t *testing.T) { c1PerformanceRefreshFailure(t, patch) })
	}
	t.Run("AuthenticatedPUTReachesSameBootEngineWithoutRestart", c1PerformanceBootLive)
}

func c1PerformanceJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}
func c1NewPerformanceAPI(t *testing.T) *restAPI {
	t.Helper()
	api := newMTIAPI(t, "1000")
	c1SeedPerformance(t, api, map[string]any{})
	require.Equal(t, float64(7), mtiGetPerf(t, api)["goal_max_rounds"], "known load/GET premise")
	return api
}
func c1SeedPerformance(t *testing.T, api *restAPI, values map[string]any) {
	t.Helper()
	b, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	raw := mtiDecode(t, b)
	planning := map[string]any{"goal_max_rounds": 7}
	for k, v := range values {
		planning[k] = v
	}
	raw["planning"] = planning
	data, err := json.Marshal(raw)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(api.configPath(), data, 0600))
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()))
}
func c1PerformanceInbound(t *testing.T, api *restAPI, enabled bool) {
	t.Helper()
	cfg, err := api.agentLoop.GetConfig().Clone()
	require.NoError(t, err)
	cfg.Gateway.ValidateInbound = enabled
	api.agentLoop.SwapConfig(cfg)
	require.Equal(t, enabled, api.agentLoop.GetConfig().Gateway.ValidateInbound)
}
func c1PerformanceTuple(t *testing.T, raw map[string]any, quiet int, enabled bool, minutes int) {
	t.Helper()
	got := map[string]any{}
	for _, k := range []string{"goal_reminder_quiet_seconds", "goal_stalled_checkin_enabled", "goal_stalled_checkin_minutes"} {
		got[k] = raw[k]
	}
	require.Equal(t, map[string]any{"goal_reminder_quiet_seconds": float64(quiet), "goal_stalled_checkin_enabled": enabled, "goal_stalled_checkin_minutes": float64(minutes)}, got)
}
func c1PerformancePartial(t *testing.T, patch map[string]any) {
	t.Helper()
	api := c1NewPerformanceAPI(t)
	seed := map[string]any{"goal_reminder_quiet_seconds": 120, "goal_stalled_checkin_enabled": false, "goal_stalled_checkin_minutes": 45}
	c1SeedPerformance(t, api, seed)
	before := mtiGetPerf(t, api)
	w := mtiPutPerf(t, api, c1PerformanceJSON(t, patch))
	require.Equal(t, http.StatusOK, w.Code, "valid C1 partial update must be recognized: %s", w.Body.String())
	want := map[string]any{}
	for k, v := range seed {
		want[k] = v
	}
	for k, v := range patch {
		want[k] = v
	}
	quiet, quietOK := want["goal_reminder_quiet_seconds"].(int)
	if !quietOK {
		t.Fatalf("C1 test precondition: %s must be int", "goal_reminder_quiet_seconds")
	}
	enabled, enabledOK := want["goal_stalled_checkin_enabled"].(bool)
	if !enabledOK {
		t.Fatalf("C1 test precondition: %s must be bool", "goal_stalled_checkin_enabled")
	}
	minutes, minutesOK := want["goal_stalled_checkin_minutes"].(int)
	if !minutesOK {
		t.Fatalf("C1 test precondition: %s must be int", "goal_stalled_checkin_minutes")
	}
	for _, got := range []map[string]any{mtiDecode(t, w.Body.Bytes()), mtiGetPerf(t, api)} {
		c1PerformanceTuple(t, got, quiet, enabled, minutes)
		for _, key := range []string{"max_parallel_agents", "tools_on_demand", "max_tool_iterations"} {
			require.Equal(t, before[key], got[key], "unrequested existing sibling must not change")
		}
		goalRounds := before["goal_max_rounds"]
		if v, ok := patch["goal_max_rounds"]; ok {
			rounds, roundsOK := v.(int)
			if !roundsOK {
				t.Fatalf("C1 test precondition: %s must be int", "goal_max_rounds")
			}
			goalRounds = float64(rounds)
		}
		require.Equal(t, goalRounds, got["goal_max_rounds"])
	}
	data, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	disk := mtiDecode(t, data)
	planning, ok := disk["planning"].(map[string]any)
	require.True(t, ok)
	c1PerformanceTuple(t, planning, quiet, enabled, minutes)
	fresh, err := config.LoadConfig(api.configPath())
	require.NoError(t, err)
	b, err := json.Marshal(fresh.Planning)
	require.NoError(t, err)
	c1PerformanceTuple(t, mtiDecode(t, b), quiet, enabled, minutes)
}
func c1RefreshFault(t *testing.T, api *restAPI) func() {
	t.Helper()
	files := func() map[string]bool {
		out := map[string]bool{}
		require.NoError(t, filepath.WalkDir(api.homePath, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				out[p] = true
			}
			return nil
		}))
		return out
	}
	before := files()
	breakInMemoryRefresh(t, api)
	created := []string{}
	for p := range files() {
		if !before[p] {
			created = append(created, p)
		}
	}
	require.Len(t, created, 1, "existing fault adds exactly one test-owned malformed entity")
	restored := false
	restore := func() {
		if !restored {
			require.NoError(t, os.Remove(created[0]))
			restored = true
		}
	}
	t.Cleanup(restore)
	return restore
}
func c1ExistingRefreshFailure(t *testing.T) {
	api := c1NewPerformanceAPI(t)
	restore := c1RefreshFault(t, api)
	before := mtiGetPerf(t, api)["goal_max_rounds"]
	w := mtiPutPerf(t, api, `{"goal_max_rounds":9}`)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	got := decodeReloadFailed(t, w.Body.Bytes())
	require.Equal(t, "performance_reload_failed", got.Code)
	require.Equal(t, gen.PerformanceReloadFailedDetailsStageRefresh, got.Details.Stage)
	require.Equal(t, []gen.PerformanceReloadFailedDetailsChangedFields{gen.PerformanceReloadFailedDetailsChangedFieldsGoalMaxRounds}, got.Details.ChangedFields)
	require.Equal(t, before, mtiGetPerf(t, api)["goal_max_rounds"])
	restore()
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()))
	require.Equal(t, float64(9), mtiGetPerf(t, api)["goal_max_rounds"])
}
func c1PerformanceRefreshFailure(t *testing.T, patch map[string]any) {
	t.Helper()
	api := c1NewPerformanceAPI(t)
	c1PerformanceInbound(t, api, false)
	c1SeedPerformance(t, api, map[string]any{"goal_reminder_quiet_seconds": 120, "goal_stalled_checkin_enabled": true, "goal_stalled_checkin_minutes": 45})
	before := mtiGetPerf(t, api)
	restore := c1RefreshFault(t, api)
	w := mtiPutPerf(t, api, c1PerformanceJSON(t, patch))
	require.Equal(t, http.StatusInternalServerError, w.Code, "recognized save must reach real refresh failure: %s", w.Body.String())
	got := decodeReloadFailed(t, w.Body.Bytes())
	require.Equal(t, "performance_reload_failed", got.Code)
	require.Equal(t, gen.PerformanceReloadFailedDetailsStageRefresh, got.Details.Stage)
	want := make([]string, 0, len(patch))
	for key := range patch {
		want = append(want, key)
	}
	sort.Strings(want)
	actual := make([]string, 0, len(got.Details.ChangedFields))
	for _, key := range got.Details.ChangedFields {
		actual = append(actual, string(key))
	}
	sort.Strings(actual)
	require.Equal(t, want, actual, "requested C1 keys cannot disappear from changed_fields")
	expected := make(map[string]any, len(before)+1)
	for key, value := range before {
		expected[key] = value
	}
	// Q111: contract enum order, filtered by request keys only.
	pendingFields := []any{}
	for _, field := range []string{
		"max_parallel_agents", "tools_on_demand", "goal_max_rounds",
		"goal_reminder_quiet_seconds", "goal_stalled_checkin_enabled",
		"goal_stalled_checkin_minutes", "max_tool_iterations",
	} {
		if _, present := patch[field]; present {
			pendingFields = append(pendingFields, field)
		}
	}
	expected["pending_apply"] = map[string]any{"stage": "refresh", "changed_fields": pendingFields}
	require.Equal(t, expected, mtiGetPerf(t, api))
	b, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	disk, diskOK := mtiDecode(t, b)["planning"].(map[string]any)
	if !diskOK {
		t.Fatalf("C1 test precondition: %s must be an object", "planning")
	}
	for key, value := range patch {
		if n, ok := value.(int); ok {
			require.Equal(t, float64(n), disk[key])
		} else {
			require.Equal(t, value, disk[key])
		}
	}
	restore()
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()))
	after := mtiGetPerf(t, api)
	for key := range patch {
		require.Equal(t, disk[key], after[key])
	}
}
func c1PerformanceBootLive(t *testing.T) {
	f := b1CompBoot(t, false, false)
	f.arm(t)
	require.NoError(t, config.SaveConfig(filepath.Join(f.home, "config.json"), f.al.GetConfig()))
	api := &restAPI{agentLoop: f.al, homePath: f.home}
	same := f.pe
	sid, gid := f.sid, f.gid
	before, err := f.al.GetSessionLifecycleStore().Load(sid)
	require.NoError(t, err)
	require.True(t, before.Terminal())
	require.NotNil(t, before.ExecutionID)
	w := mtiPutPerf(t, api, `{"goal_reminder_quiet_seconds":120}`)
	require.Equal(t, http.StatusOK, w.Code, "normal authenticated PUT must apply C1 live: %s", w.Body.String())
	last := time.Now().UTC().Add(-90 * time.Second)
	_, err = goal.NewStore(f.home).Update(gid, func(g *goal.Goal) error { g.LastActivityAt = last; g.StartedAt = &last; return nil })
	require.NoError(t, err)
	require.Empty(t, f.mb.InboundChan())
	same.Tick(context.Background())
	require.Empty(t, f.mb.InboundChan(), "quiet120 must suppress at90; old60/min(old,new) must not wake")
	w = mtiPutPerf(t, api, `{"goal_reminder_quiet_seconds":60}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.Same(t, same, agent.GetPlanEngine(f.al))
	require.Equal(t, sid, f.sid)
	require.Equal(t, gid, f.gid)
	f.startReceiver(t)
	same.Tick(context.Background())
	f.finishProduced(t)
	after, err := session.NewLifecycleStore(f.al.GetSessionLifecycleStore().Dir()).Load(sid)
	require.NoError(t, err)
	require.True(t, after.Terminal())
	require.Equal(t, before.Generation+1, after.Generation)
	require.NotEqual(t, before.ExecutionID.RunID, after.ExecutionID.RunID)
}
