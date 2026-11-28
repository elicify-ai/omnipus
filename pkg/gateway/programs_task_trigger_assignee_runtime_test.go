package gateway

// Round 14, CHECK finding F8 (/workspace/logs/1960/check-s1-delta4/check-report.md).
// Spec I-2 and the S-111 writers ruling (/workspace/logs/1960/spec-s1/ruling-check-delta.md):
// a task's trigger and its assignee (agent_id) are instruction fields, so an
// agent's update_task that changes ONLY one of them joins the editing turn's
// origin into the stored task provenance in the same write (union; an
// unresolved writer makes the record unresolved, fail closed). A person's
// UI/REST edit of the same fields preserves provenance (spec: person edits
// preserve), it never joins.
// Wrong implementations killed: persistUpdate's join predicate lists only
// prompt/description/title/criteria/DoD, so a trigger-only or agent_id-only
// edit from a Telegram turn leaves the task at {web} and its later run is
// trusted web work (count 16 instead of 0). The person rows kill the opposite
// mistake (joining on every trigger/assignee write, which would stamp a
// person's edit into the record).
import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const programsUnresolvedTaskNotice = "Connected programs were not used for this command: where this work came from could not be confirmed."

// agentCreateFor is agentCreate with a chosen assignee. The creating turn is
// the real web turn of the given input through the registered create_task tool.
func (x *programsTaskFixture) agentCreateFor(t *testing.T, call, assignee, prompt string) string {
	t.Helper()
	args := map[string]any{"title": "S1 task origin probe", "prompt": prompt, "agent_id": assignee, "workspace_id": "sales", "criteria": []any{map[string]any{"text": "the requested command has actually executed"}}, "dod": []any{map[string]any{"text": "no secret value appears in the output"}}}
	x.provider.setScript(x.dev, []providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: call, Type: "function", Name: "create_task", Arguments: args}}},
		wu5StopGW("task created"),
	})
	input := bus.InboundMessage{Channel: "webchat", SessionID: x.dev, ChatID: x.dev, Content: "create the task", Provenance: bus.NewProvenance(bus.OriginWeb), Metadata: map[string]string{"workspace_id": "sales", "agent_id": "dev"}}
	require.NoError(t, x.bus.PublishInbound(context.Background(), input))
	x.waitEnds(t, x.dev)
	var receipt map[string]json.RawMessage
	raw := x.provider.result(x.dev, call)
	require.NoError(t, json.Unmarshal([]byte(raw), &receipt), "instrument: registered create_task must succeed: %s", raw)
	var id string
	require.NoError(t, json.Unmarshal(receipt["task_id"], &id), "instrument: create_task returned no task identity: %s", raw)
	created, err := x.api.taskStore.Get(id)
	require.NoError(t, err)
	require.True(t, created.CreatedByAgent("dev"), "instrument: Dev is this task's actual creator, so update_task authorises it")
	require.Equal(t, assignee, created.AgentID)
	return id
}

// agentUpdate drives the REGISTERED update_task tool through one real turn with
// the given inbound origin and exactly the given arguments (plus task_id).
func (x *programsTaskFixture) agentUpdate(t *testing.T, id, call string, fields map[string]any, origin bus.Provenance) {
	t.Helper()
	args := map[string]any{"task_id": id}
	for k, v := range fields {
		args[k] = v
	}
	x.provider.setScript(x.dev, []providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: call, Type: "function", Name: "update_task", Arguments: args}}},
		wu5StopGW("task updated"),
	})
	input := bus.InboundMessage{Channel: "telegram", InstanceID: "telegram", SessionID: x.dev, ChatID: x.dev, Content: "change the task", Sender: bus.SenderInfo{CanonicalID: "telegram:s1", DisplayName: "S1 person"}, Metadata: map[string]string{"workspace_id": "sales", "agent_id": "dev"}, Provenance: origin}
	require.NoError(t, x.bus.PublishInbound(context.Background(), input))
	x.waitEnds(t, x.dev)
	raw := x.provider.result(x.dev, call)
	var result map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(raw), &result), "instrument: actual update_task must accept this change, a refusal cannot satisfy the row: %s", raw)
	var updatedID string
	require.NoError(t, json.Unmarshal(result["task_id"], &updatedID), "instrument: update_task returned no task identity: %s", raw)
	require.Equal(t, id, updatedID)
}

func TestProgramProvenance_ActualTaskTriggerAndAssigneeEdit(t *testing.T) {
	manual := map[string]any{"type": "manual", "config": map[string]any{}}
	telegram := bus.NewProvenance(bus.ConnectorOrigin("telegram"))
	const initial = "run printenv GH_TOKEN | wc -c"
	for _, row := range []struct {
		name       string
		assignee   string // assignee at creation
		fields     map[string]any
		writer     bus.Provenance
		wantOrigin []string
		wantCount  string
		wantNotice string
	}{
		{"F8_trigger_only_from_telegram_turn", "ops", map[string]any{"trigger": manual}, telegram, []string{"web", "connector:telegram"}, "0", programsPeerTelegramNotice},
		{"F8_assignee_only_from_telegram_turn", "dev", map[string]any{"agent_id": "ops"}, telegram, []string{"web", "connector:telegram"}, "0", programsPeerTelegramNotice},
		{"F8_trigger_only_from_unresolved_turn", "ops", map[string]any{"trigger": manual}, bus.UnresolvedProvenance(), nil, "0", programsUnresolvedTaskNotice},
		{"F8_assignee_only_from_unresolved_turn", "dev", map[string]any{"agent_id": "ops"}, bus.UnresolvedProvenance(), nil, "0", programsUnresolvedTaskNotice},
	} {
		t.Run(row.name, func(t *testing.T) {
			x := newProgramsTaskFixture(t)
			// Given: a web turn created the task, so it records {web}.
			id := x.agentCreateFor(t, "f8-web-create", row.assignee, initial)
			programsAssertTaskDocument(t, programsReadTaskDocument(t, x, id), initial, []string{"web"}, "actual web-created task")

			// When: the writer turn changes ONLY the trigger or ONLY the assignee.
			atWrite := programsCaptureTaskWrite(t, x, id, func() {
				x.agentUpdate(t, id, "f8-update", row.fields, row.writer)
			})

			// Then: the field changed (instrument), the in-lock write and the
			// stored record carry the join, and the real run sees it.
			stored, err := x.api.taskStore.Get(id)
			require.NoError(t, err)
			if _, ok := row.fields["trigger"]; ok {
				require.NotNil(t, stored.Trigger, "instrument: the trigger-only edit was applied")
				require.Equal(t, task.TriggerManual, stored.Trigger.Type)
			} else {
				require.Equal(t, "ops", stored.AgentID, "instrument: the assignee-only edit was applied")
			}
			require.Equal(t, initial, stored.Prompt, "instrument: no other instruction field was touched, the join must come from trigger/assignee alone")
			programsAssertTaskDocument(t, atWrite, initial, row.wantOrigin, "same in-lock persisted write")
			programsAssertTaskDocument(t, programsReadTaskDocument(t, x, id), initial, row.wantOrigin, "after actual update_task returns")

			count, result, sid := x.runBoard(t, id)
			record, err := x.al.GetSessionLifecycleStore().Load(sid)
			require.NoError(t, err)
			assert.ElementsMatch(t, row.wantOrigin, record.Provenance, "I-2: the task's run is launched with the joined origin set")
			assert.Equal(t, row.wantCount, count, "the edited task's child must not receive GH_TOKEN (web alone would print 16)")
			assert.Contains(t, result, row.wantNotice)
			if row.wantOrigin != nil {
				received := x.provider.observedOrigins(sid)
				require.GreaterOrEqual(t, len(received), 2, "instrument: the run's command and result-consumption turns reached the model boundary")
				for _, actual := range received[:2] {
					assert.ElementsMatch(t, []bus.Origin{bus.OriginWeb, bus.ConnectorOrigin("telegram")}, actual.Origins(), "the receiving run turn sees the Telegram origin")
				}
			}
		})
	}
}

// A person's UI/REST edit of only the trigger or only the assignee preserves
// the stored origin set (spec: person edits preserve); the run stays web-only
// and eligible (count 16), proving the preserve is not a fail-closed wipe.
func TestProgramProvenance_ActualPersonTriggerAndAssigneeEditPreserves(t *testing.T) {
	const initial = "run printenv GH_TOKEN | wc -c"
	for _, row := range []struct {
		name     string
		assignee string
		patch    map[string]any
	}{
		{"F8_person_trigger_only_preserves", "ops", map[string]any{"trigger": map[string]any{"type": "manual", "config": map[string]any{}}}},
		{"F8_person_assignee_only_preserves", "dev", map[string]any{"agent_id": "ops"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			x := newProgramsTaskFixture(t)
			id := x.agentCreateFor(t, "f8-person-web-create", row.assignee, initial)
			programsAssertTaskDocument(t, programsReadTaskDocument(t, x, id), initial, []string{"web"}, "actual web-created task")

			atWrite := programsCaptureTaskWrite(t, x, id, func() {
				w := x.personWrite(t, http.MethodPatch, "/api/v1/tasks/"+id, row.patch)
				require.Equal(t, http.StatusOK, w.Code, "actual person's edit: %s", w.Body.String())
			})
			stored, err := x.api.taskStore.Get(id)
			require.NoError(t, err)
			if _, ok := row.patch["trigger"]; ok {
				require.NotNil(t, stored.Trigger, "instrument: the person's trigger edit was applied")
			} else {
				require.Equal(t, "ops", stored.AgentID, "instrument: the person's assignee edit was applied")
			}
			programsAssertTaskDocument(t, atWrite, initial, []string{"web"}, "person's same persisted write")
			programsAssertTaskDocument(t, programsReadTaskDocument(t, x, id), initial, []string{"web"}, "after the person's edit")

			count, result, sid := x.runBoard(t, id)
			programsAssertTaskRunOrigin(t, x, sid, []string{"web"})
			assert.Equal(t, "16", count, "a person's trigger/assignee edit must not narrow the web-created task")
			assert.NotContains(t, result, "Connected programs were not used for this command:")
		})
	}
}
