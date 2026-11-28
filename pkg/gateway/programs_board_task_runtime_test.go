package gateway

// Spec rev8 S77 / I-2 / A-1: actual Telegram-created and person-created
// board tasks with the same prompt. No seeded task, lineage or runtime carrier.
import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgramProvenance_ActualBoardTasks(t *testing.T) {
	t.Run("S77_telegram_to_new_board_task_ui_task_independent", func(t *testing.T) {
		x := newProgramsTaskFixture(t)
		const prompt = "run printenv GH_TOKEN | wc -c"
		input := bus.InboundMessage{Channel: "telegram", InstanceID: "telegram", Content: "create the counted task for Ops", Sender: bus.SenderInfo{CanonicalID: "telegram:s1", DisplayName: "S1 person"}, Provenance: bus.NewProvenance(bus.ConnectorOrigin("telegram"))}
		agentID := x.agentCreate(t, "s77-agent-create", input, prompt)
		creatorOrigins := x.provider.observedOrigins(x.dev)
		require.NotEmpty(t, creatorOrigins, "instrument: actual creator turn reached the registered task tool")
		assert.Equal(t, bus.NewProvenance(bus.ConnectorOrigin("telegram")), creatorOrigins[0])
		personID := x.personCreate(t, prompt)
		require.NotEqual(t, agentID, personID, "S77 requires two separately created real tasks")
		for _, row := range []struct {
			id, label, expected string
			origins             []string
		}{
			{agentID, "Telegram-created", "0", []string{"connector:telegram"}},
			{personID, "person-created", "16", []string{"independent"}},
		} {
			count, result, sid := x.runBoard(t, row.id)
			record, err := x.al.GetSessionLifecycleStore().Load(sid)
			require.NoError(t, err)
			assert.Equal(t, session.ProvenanceModeLineage, record.ProvenanceMode, "I-2: actual task sessions are lineage, regardless of parentlessness")
			assert.Equal(t, row.origins, record.Provenance, "S77: the real task launch inherits the producer's exact origin, never unresolved or web")
			assert.Equal(t, row.expected, count, "S77/A-1: actual %s task command", row.label)
			if row.id == agentID {
				assert.Contains(t, result, programsPeerTelegramNotice, "S77: actual inherited Telegram-off notice, not unresolved")
			} else {
				assert.NotContains(t, result, "Connected programs were not used for this command:", "S77/A-1: independent UI task follows the enabled web setting")
			}
			received := x.provider.observedOrigins(sid)
			require.GreaterOrEqual(t, len(received), 2, "instrument: actual task requested the command and consumed the executed result")
			expected := bus.NewProvenance(bus.Origin(row.origins[0]))
			assert.Equal(t, expected, received[0], "actual task command context")
			assert.Equal(t, expected, received[1], "actual task result-consumption context")
			t.Logf("actual %s task child_ran=true count=%s stored_origins=%v receiving_origins=%v", row.label, count, record.Provenance, received[:2])
		}
	})
}
