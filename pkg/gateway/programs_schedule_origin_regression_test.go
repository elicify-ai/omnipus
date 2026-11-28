package gateway

// I2/FR041/S124: only a workspace heartbeat starts with known independent
// origin. Other schedules without stored lineage stay unresolved. Real runner,
// actual ProcessScheduled and paid-provider-edge observation, not fakeExecutor.
import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
)

func TestProgramProvenance_ScheduleOriginRegression(t *testing.T) {
	for _, row := range []struct {
		name, kind string
		want       bus.Provenance
	}{
		{"S124_workspace_heartbeat_is_independent", "heartbeat", bus.NewProvenance(bus.OriginIndependent)},
		{"S124_other_schedule_with_no_lineage_is_unresolved", "task", bus.UnresolvedProvenance()},
		{"S124_untyped_schedule_with_no_lineage_is_unresolved", "", bus.UnresolvedProvenance()},
	} {
		t.Run(row.name, func(t *testing.T) {
			x := newProgramsTaskFixture(t)
			expectedSID, mode := x.dev, cron.SessionModeMain
			if row.kind != "heartbeat" {
				// A user schedule need not own a standing main. Use the real
				// continue-session path; creating its empty record writes no origin.
				fresh, createErr := x.al.GetSessionStore().NewScheduledSession("dev")
				require.NoError(t, createErr)
				expectedSID, mode = fresh.ID, cron.SessionModeContinue
			}
			inst, registered := x.al.GetRegistry().GetAgent("dev")
			require.True(t, registered)
			inst.Provider = x.provider // The existing process-edge scripted model, not the task-only wrapper.
			x.provider.setScript(expectedSID, []providers.LLMResponse{wu5StopGW("S124 actual scheduled turn completed")})
			before := len(x.provider.observedOrigins(expectedSID))
			runner := newScheduledRunner(x.al, agentCheckerFunc(func(id string) bool {
				registeredAgent, ok := x.al.GetRegistry().GetAgent(id)
				return ok && registeredAgent != nil
			}), x.bus, nil, x.al.GetConfig)
			runner.home = x.home
			job := &cron.CronJob{ID: row.name, Name: "heartbeat:sales:dev", AgentID: "dev", SessionID: expectedSID, SessionMode: mode, Payload: cron.CronPayload{Kind: row.kind, Message: "Run the scheduled test instruction"}}
			sid, err := runner.RunScheduled(context.Background(), job)
			require.NoError(t, err)
			require.Equal(t, expectedSID, sid, "instrument: actual scheduled runner admitted the intended real session")
			x.waitEnds(t, sid)
			observed := x.provider.observedOrigins(sid)[before:]
			require.Len(t, observed, 1, "instrument: this actual schedule made one model request")
			require.Equal(t, row.want, observed[0], "I2/S124: only the trusted workspace heartbeat can assert independent; other jobs do not inherit a permissive default")
		})
	}
}
