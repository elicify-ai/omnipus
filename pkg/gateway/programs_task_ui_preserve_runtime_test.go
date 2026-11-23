package gateway

// Spec rev8 S125/I-2/A-1: actual person PATCH preserves the exact origin of
// an actual Telegram agent instruction write. Actual delete/new is independent.
import (
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgramProvenance_ActualPersonTaskEdit(t *testing.T) {
	t.Run("S125_person_ui_edit_preserves_provenance_and_new_task_is_independent", func(t *testing.T) {
		x := newProgramsTaskFixture(t)
		// Use a real, distinct channel conversation for these successive Dev
		// turns. Main-session default-send refusal reminders are a different
		// flow and must not consume a later paid-model script for this test.
		conversation, err := x.al.GetSessionStore().NewChannelSession("telegram", "telegram", "s125-person", "dev", "S125 task writes")
		require.NoError(t, err)
		workspaceID := "sales"
		require.NoError(t, x.al.GetSessionStore().SetMeta(conversation.ID, session.MetaPatch{WorkspaceID: &workspaceID}))
		x.dev = conversation.ID
		provider := &programsTaskProvider{programsPeerProvider: x.provider, al: x.al, dev: x.dev, ops: x.ops, work: x.work}
		for _, actor := range []string{"dev", "ops"} {
			inst, ok := x.al.GetRegistry().GetAgent(actor)
			require.True(t, ok)
			inst.Provider = provider
		}
		const initial = "prepare the Ops command"
		const agentPrompt = "run printenv GH_TOKEN | wc -c"
		const personPrompt = "please run printenv GH_TOKEN | wc -c"
		input := bus.InboundMessage{Channel: "telegram", InstanceID: "telegram", Content: "create the Ops task", Sender: bus.SenderInfo{CanonicalID: "telegram:s1", DisplayName: "S1 person"}, Provenance: bus.NewProvenance(bus.ConnectorOrigin("telegram"))}
		id := x.agentCreate(t, "s125-telegram-create", input, initial)
		programsAgentEditTask(t, x, id, "s125-telegram-edit", agentPrompt)
		writerOrigins := x.provider.observedOrigins(x.dev)
		require.GreaterOrEqual(t, len(writerOrigins), 4, "instrument: both actual agent writes and their result-consumption requests")
		for _, actual := range writerOrigins[:4] {
			assert.Equal(t, bus.NewProvenance(bus.ConnectorOrigin("telegram")), actual, "S125: actual creator/editor contexts are Telegram, never a test-joined record")
		}
		programsAssertTaskDocument(t, programsReadTaskDocument(t, x, id), agentPrompt, []string{"connector:telegram"}, "S125 Given: actual Telegram agent instruction edit")
		atWrite := programsCaptureTaskWrite(t, x, id, func() {
			w := x.personWrite(t, http.MethodPatch, "/api/v1/tasks/"+id, map[string]any{"prompt": personPrompt})
			require.Equal(t, http.StatusOK, w.Code, "actual person's prompt edit: %s", w.Body.String())
		})
		programsAssertTaskDocument(t, atWrite, personPrompt, []string{"connector:telegram"}, "S125 person's same persisted write, before reconciliation")
		programsAssertTaskDocument(t, programsReadTaskDocument(t, x, id), personPrompt, []string{"connector:telegram"}, "S125 after real person PATCH")
		count, result, sid := x.runBoard(t, id)
		programsAssertTaskRunOrigin(t, x, sid, []string{"connector:telegram"})
		assert.Equal(t, "0", count, "S125: editing as a person must not launder the actual Telegram task into web")
		assert.Contains(t, result, programsPeerTelegramNotice, "S125: retained exact Telegram origin, not unresolved")
		w := x.personWrite(t, http.MethodDelete, "/api/v1/tasks/"+id, nil)
		require.Equal(t, http.StatusNoContent, w.Code, "actual person deletes the old task: %s", w.Body.String())
		_, err = x.api.taskStore.Get(id)
		require.ErrorIs(t, err, task.ErrNotFound, "instrument: old task really was deleted")
		newID := x.personCreate(t, personPrompt)
		require.NotEqual(t, id, newID, "S125: person creation is a new durable task, not the deleted record")
		programsAssertTaskDocument(t, programsReadTaskDocument(t, x, newID), personPrompt, []string{"independent"}, "S125 real new person-created task")
		newCount, newResult, newSID := x.runBoard(t, newID)
		require.NotEqual(t, sid, newSID, "instrument: distinct actual task sessions")
		programsAssertTaskRunOrigin(t, x, newSID, []string{"independent"})
		assert.Equal(t, "16", newCount, "S125/A-1: a real new independent task follows enabled web, not the deleted task's connector")
		assert.NotContains(t, newResult, "Connected programs were not used for this command:")
		t.Logf("actual person edit/delete/new children_ran=true edited_count=%s new_count=%s old_deleted=true new_task_and_session=true", count, newCount)
	})
}

func programsAssertTaskRunOrigin(t *testing.T, x *programsTaskFixture, sid string, origins []string) {
	t.Helper()
	record, err := x.al.GetSessionLifecycleStore().Load(sid)
	require.NoError(t, err)
	assert.Equal(t, session.ProvenanceModeLineage, record.ProvenanceMode, "I-2: real task launch is lineage")
	assert.ElementsMatch(t, origins, record.Provenance, "I-2: actual task launch exact stored origin set")
	received := x.provider.observedOrigins(sid)
	require.GreaterOrEqual(t, len(received), 2, "instrument: actual child command and result consumption reached the model boundary")
	expected := make([]bus.Origin, len(origins))
	for i, origin := range origins {
		expected[i] = bus.Origin(origin)
	}
	for _, actual := range received[:2] {
		assert.ElementsMatch(t, expected, actual.Origins(), "I-2: actual receiving task turn exact origin set")
	}
}
