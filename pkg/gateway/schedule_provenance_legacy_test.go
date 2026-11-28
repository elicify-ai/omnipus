package gateway

// Round 13, CHECK finding F5 (/workspace/logs/1960/check-s1-delta3/check-report.md):
// the legacy fallback of scheduledJobProvenance had no row that exercises it.
// The existing person row supplies Provenance=[independent] itself and so takes
// the stored-origin branch. Oracle: ruling-check-delta.md S-111 writers and
// FR-050's rule for records older than the field: a job with NO stored
// provenance is independent when it is a heartbeat or a schedule a person
// created (CreatedBy is set only by the REST create), and unresolved (fail
// closed) for every other record, including an agent-created or unattributed
// one. Wrong implementations killed: the person partition deleted
// (`|| job.CreatedBy != ""` removed: a legacy person schedule loses programs),
// and the opposite error of granting every missing-origin job independent.
import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Partition table over the pure decision, every partition separately.
func TestScheduleProvenance_LegacyFallbackPartitions(t *testing.T) {
	independent := bus.NewProvenance(bus.OriginIndependent)
	for _, row := range []struct {
		name string
		job  cron.CronJob
		want bus.Provenance
	}{
		{"legacy_person_created_no_stored_origin_is_independent",
			cron.CronJob{CreatedBy: "alice", Payload: cron.CronPayload{Kind: "agent_turn"}}, independent},
		{"legacy_heartbeat_no_stored_origin_is_independent",
			cron.CronJob{Payload: cron.CronPayload{Kind: heartbeatJobKind}}, independent},
		{"unknown_unattributed_no_stored_origin_is_unresolved",
			cron.CronJob{Payload: cron.CronPayload{Kind: "agent_turn"}}, bus.UnresolvedProvenance()},
		{"empty_stored_origin_list_is_the_same_as_none",
			cron.CronJob{Provenance: []string{}, Payload: cron.CronPayload{Kind: "agent_turn"}}, bus.UnresolvedProvenance()},
		{"stored_origin_wins_over_a_person_creator",
			cron.CronJob{CreatedBy: "alice", Provenance: []string{"connector:telegram"}, Payload: cron.CronPayload{Kind: "agent_turn"}},
			bus.NewProvenance(bus.ConnectorOrigin("telegram"))},
	} {
		t.Run(row.name, func(t *testing.T) {
			job := row.job
			assert.Equal(t, row.want, scheduledJobProvenance(&job))
		})
	}
}

// The same partitions through the real runner and the provider-edge
// observation, as the existing row does.
func TestScheduleProvenance_LegacyJobRunsWithTheFallbackProvenance(t *testing.T) {
	for _, row := range []struct {
		name      string
		createdBy string
		want      bus.Provenance
	}{
		{"legacy_person_created_schedule_runs_as_independent", "alice", bus.NewProvenance(bus.OriginIndependent)},
		{"legacy_unattributed_schedule_runs_unresolved", "", bus.UnresolvedProvenance()},
	} {
		t.Run(row.name, func(t *testing.T) {
			x := newProgramsTaskFixture(t)
			fresh, err := x.al.GetSessionStore().NewScheduledSession("dev")
			require.NoError(t, err)
			inst, registered := x.al.GetRegistry().GetAgent("dev")
			require.True(t, registered)
			inst.Provider = x.provider
			x.provider.setScript(fresh.ID, []providers.LLMResponse{wu5StopGW("legacy scheduled turn completed")})
			before := len(x.provider.observedOrigins(fresh.ID))
			runner := newScheduledRunner(x.al, agentCheckerFunc(func(id string) bool {
				a, ok := x.al.GetRegistry().GetAgent(id)
				return ok && a != nil
			}), x.bus, nil, x.al.GetConfig)
			runner.home = x.home
			job := &cron.CronJob{
				ID: row.name, Name: row.name, AgentID: "dev", SessionID: fresh.ID,
				SessionMode: cron.SessionModeContinue, CreatedBy: row.createdBy, // no Provenance: a record older than the field
				Payload: cron.CronPayload{Kind: "agent_turn", Message: "Run the legacy scheduled instruction"},
			}
			sid, err := runner.RunScheduled(context.Background(), job)
			require.NoError(t, err)
			require.Equal(t, fresh.ID, sid, "instrument: the runner admitted the intended session")
			x.waitEnds(t, sid)
			observed := x.provider.observedOrigins(sid)[before:]
			require.Len(t, observed, 1, "instrument: this schedule made one model request")
			require.Equal(t, row.want, observed[0], "FR-050: a record older than the field is classified by who made it")
		})
	}
}
