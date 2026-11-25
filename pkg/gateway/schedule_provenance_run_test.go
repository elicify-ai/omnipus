package gateway

// I-2 / S-111 (ruling-check-delta "S-111 writers"): a fired schedule's turn
// runs with the schedule's STORED provenance. An agent-created schedule from a
// Telegram-origin turn keeps connector:telegram; it is never laundered into
// independent. Real runner, actual ProcessScheduled, provider-edge observation.
import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
)

func TestScheduleProvenance_RunUsesTheStoredProvenance(t *testing.T) {
	telegram := bus.Origin("connector:telegram")
	require.True(t, bus.IsConnectorOrigin(telegram), "instrument: the row uses a real connector origin")
	for _, row := range []struct {
		name       string
		provenance []string
		createdBy  string
		want       bus.Provenance
	}{
		{"agent_created_from_telegram_runs_as_connector_telegram", []string{string(telegram)}, "", bus.NewProvenance(telegram)},
		{"person_created_runs_as_independent", []string{string(bus.OriginIndependent)}, "alice", bus.NewProvenance(bus.OriginIndependent)},
		// Legacy partitions (ruling "S-111 writers"): a job stored before
		// provenance existed. A person-created one (CreatedBy set) runs as
		// independent; one with no creator stays unresolved (fail closed).
		{"legacy_person_created_without_provenance_runs_as_independent", nil, "alice", bus.NewProvenance(bus.OriginIndependent)},
		{"legacy_unattributed_without_provenance_stays_unresolved", nil, "", bus.UnresolvedProvenance()},
	} {
		t.Run(row.name, func(t *testing.T) {
			x := newProgramsTaskFixture(t)
			fresh, err := x.al.GetSessionStore().NewScheduledSession("dev")
			require.NoError(t, err)
			inst, registered := x.al.GetRegistry().GetAgent("dev")
			require.True(t, registered)
			inst.Provider = x.provider
			x.provider.setScript(fresh.ID, []providers.LLMResponse{wu5StopGW("S111 scheduled turn completed")})
			before := len(x.provider.observedOrigins(fresh.ID))
			runner := newScheduledRunner(x.al, agentCheckerFunc(func(id string) bool {
				a, ok := x.al.GetRegistry().GetAgent(id)
				return ok && a != nil
			}), x.bus, nil, x.al.GetConfig)
			runner.home = x.home
			job := &cron.CronJob{
				ID: row.name, Name: row.name, AgentID: "dev", SessionID: fresh.ID,
				SessionMode: cron.SessionModeContinue, CreatedBy: row.createdBy, Provenance: row.provenance,
				Payload: cron.CronPayload{Kind: "agent_turn", Message: "Run the scheduled test instruction"},
			}
			sid, err := runner.RunScheduled(context.Background(), job)
			require.NoError(t, err)
			require.Equal(t, fresh.ID, sid, "instrument: the runner admitted the intended session")
			x.waitEnds(t, sid)
			observed := x.provider.observedOrigins(sid)[before:]
			require.Len(t, observed, 1, "instrument: this schedule made one model request")
			require.Equal(t, row.want, observed[0], "I-2/S-111: the scheduled turn runs with the schedule's stored provenance")
		})
	}
}
