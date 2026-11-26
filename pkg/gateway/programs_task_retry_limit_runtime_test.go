package gateway

// Round 15, CHECK finding F9 (/workspace/logs/1960/check-s1-delta5/check-report.md).
// Spec I-2 and the S-111 writers ruling (/workspace/logs/1960/spec-s1/ruling-check-delta.md):
// max_attempts (the retry limit) is a creator-authored DEFINITION field
// (pkg/task/fieldrules.go row TK-05, GroupDefinition, not an engine counter),
// so an agent's update_task that changes ONLY max_attempts, or CLEARS it
// (clear_max_attempts), joins the editing turn's origin into the stored task
// provenance in the same write (union; an unresolved writer makes the record
// unresolved, fail closed). A person's REST edit of max_attempts preserves
// provenance. A metadata-only update (priority, a GroupOrganisation field
// that is not an instruction) joins nothing.
// Wrong implementations killed: persistUpdate's join predicate omits
// MaxAttempts, so a retry-limit-only edit from a Telegram turn leaves the task
// at {web} and its later run is trusted web work (count 16 instead of 0). The
// person and metadata rows kill the opposite mistake (joining on every
// max_attempts write or on every update).
import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agentCreateWithRetryLimit creates the task through the registered create_task
// tool in a real web turn, with an explicit creation-time max_attempts.
func (x *programsTaskFixture) agentCreateWithRetryLimit(t *testing.T, call, prompt string, maxAttempts int) string {
	t.Helper()
	args := map[string]any{"title": "S1 task origin probe", "prompt": prompt, "agent_id": "ops", "workspace_id": "sales", "max_attempts": maxAttempts, "criteria": []any{map[string]any{"text": "the requested command has actually executed"}}, "dod": []any{map[string]any{"text": "no secret value appears in the output"}}}
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
	require.True(t, created.CreatedByAgent("dev"), "instrument: Dev is this task's actual creator, so update_task authorises the definition edit")
	require.NotNil(t, created.MaxAttempts, "instrument: the task starts with an explicit retry limit")
	require.Equal(t, maxAttempts, *created.MaxAttempts)
	return id
}

func TestProgramProvenance_ActualTaskRetryLimitEdit(t *testing.T) {
	telegram := bus.NewProvenance(bus.ConnectorOrigin("telegram"))
	const initial = "run printenv GH_TOKEN | wc -c"
	for _, row := range []struct {
		name       string
		fields     map[string]any
		writer     bus.Provenance
		wantLimit  *int // stored limit after the edit; nil is cleared
		wantOrigin []string
		wantCount  string
		wantNotice string
	}{
		{"F9_set_max_attempts_only_from_telegram_turn", map[string]any{"max_attempts": 2}, telegram, intPtrGW(2), []string{"web", "connector:telegram"}, "0", programsPeerTelegramNotice},
		{"F9_clear_max_attempts_only_from_telegram_turn", map[string]any{"clear_max_attempts": true}, telegram, nil, []string{"web", "connector:telegram"}, "0", programsPeerTelegramNotice},
		{"F9_set_max_attempts_only_from_unresolved_turn", map[string]any{"max_attempts": 2}, bus.UnresolvedProvenance(), intPtrGW(2), nil, "0", programsUnresolvedTaskNotice},
		{"F9_clear_max_attempts_only_from_unresolved_turn", map[string]any{"clear_max_attempts": true}, bus.UnresolvedProvenance(), nil, nil, "0", programsUnresolvedTaskNotice},
	} {
		t.Run(row.name, func(t *testing.T) {
			x := newProgramsTaskFixture(t)
			// Given: a web turn created the task with max_attempts 1, so it records {web}.
			id := x.agentCreateWithRetryLimit(t, "f9-web-create", initial, 1)
			programsAssertTaskDocument(t, programsReadTaskDocument(t, x, id), initial, []string{"web"}, "actual web-created task")

			// When: the writer turn changes ONLY the retry limit.
			atWrite := programsCaptureTaskWrite(t, x, id, func() {
				x.agentUpdate(t, id, "f9-update", row.fields, row.writer)
			})

			// Then: the limit changed (instrument), the in-lock write and the
			// stored record carry the join, and the real run sees it.
			stored, err := x.api.taskStore.Get(id)
			require.NoError(t, err)
			if row.wantLimit == nil {
				require.Nil(t, stored.MaxAttempts, "instrument: clear_max_attempts was applied")
			} else {
				require.NotNil(t, stored.MaxAttempts, "instrument: the max_attempts edit was applied")
				require.Equal(t, *row.wantLimit, *stored.MaxAttempts)
			}
			require.Equal(t, initial, stored.Prompt, "instrument: no other instruction field was touched, the join must come from the retry limit alone")
			require.Equal(t, "ops", stored.AgentID, "instrument: the assignee was not touched")
			require.Nil(t, stored.Trigger, "instrument: the trigger was not touched")
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

// Controls. A person's REST edit of max_attempts, and an agent's metadata-only
// (priority) update from a Telegram turn, preserve the stored {web} origin set;
// the run stays web-only and eligible (count 16), proving neither is a
// fail-closed wipe nor a join.
func TestProgramProvenance_ActualTaskRetryLimitPreserveControls(t *testing.T) {
	const initial = "run printenv GH_TOKEN | wc -c"
	telegram := bus.NewProvenance(bus.ConnectorOrigin("telegram"))
	for _, row := range []struct {
		name  string
		write func(t *testing.T, x *programsTaskFixture, id string)
		check func(t *testing.T, x *programsTaskFixture, id string)
	}{
		{
			"F9_person_max_attempts_edit_preserves",
			func(t *testing.T, x *programsTaskFixture, id string) {
				w := x.personWrite(t, http.MethodPatch, "/api/v1/tasks/"+id, map[string]any{"max_attempts": 2})
				require.Equal(t, http.StatusOK, w.Code, "actual person's edit: %s", w.Body.String())
			},
			func(t *testing.T, x *programsTaskFixture, id string) {
				stored, err := x.api.taskStore.Get(id)
				require.NoError(t, err)
				require.NotNil(t, stored.MaxAttempts, "instrument: the person's max_attempts edit was applied")
				require.Equal(t, 2, *stored.MaxAttempts)
			},
		},
		{
			"F9_metadata_only_priority_update_from_telegram_joins_nothing",
			func(t *testing.T, x *programsTaskFixture, id string) {
				x.agentUpdate(t, id, "f9-priority", map[string]any{"priority": 1}, telegram)
			},
			func(t *testing.T, x *programsTaskFixture, id string) {
				stored, err := x.api.taskStore.Get(id)
				require.NoError(t, err)
				require.NotNil(t, stored.MaxAttempts, "instrument: the retry limit was not touched")
				require.Equal(t, 1, *stored.MaxAttempts)
				require.Equal(t, 1, stored.Priority, "instrument: the priority edit was applied")
			},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			x := newProgramsTaskFixture(t)
			id := x.agentCreateWithRetryLimit(t, "f9-control-create", initial, 1)
			programsAssertTaskDocument(t, programsReadTaskDocument(t, x, id), initial, []string{"web"}, "actual web-created task")

			atWrite := programsCaptureTaskWrite(t, x, id, func() { row.write(t, x, id) })
			row.check(t, x, id)
			programsAssertTaskDocument(t, atWrite, initial, []string{"web"}, "same persisted write")
			programsAssertTaskDocument(t, programsReadTaskDocument(t, x, id), initial, []string{"web"}, "after the edit")

			count, result, sid := x.runBoard(t, id)
			programsAssertTaskRunOrigin(t, x, sid, []string{"web"})
			assert.Equal(t, "16", count, "a person's retry-limit edit or a metadata-only edit must not narrow the web-created task")
			assert.NotContains(t, result, "Connected programs were not used for this command:")
		})
	}
}
