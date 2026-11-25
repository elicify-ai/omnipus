package gateway

// Spec rev8 S143 / I-2 / FR-041 / FR-050: a Telegram turn of Dev creates a task
// for Ops through the real create_task tool; the real Board start runs it in its
// own parentless task session; a web message then goes into that session; the
// in-process gateway runtime is rebuilt from the same home and the session is
// revived by another web message. No task, lifecycle record, provenance or
// session record is seeded: each was written by its production producer. Only
// the paid-model response and the external gh executable are process-edge fakes.
// The instrument twin below seeds ONE root-classified task record on purpose: it
// shows the same re-entry path prints 16 for a record the classifier wrongly
// called root, so a lineage regression would be seen by the scenario.
import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// programsTaskReentry sends one web message into the task session sid and
// returns the counted GH_TOKEN length, the executed command's result and the
// origins of the two model requests this re-entry made (the command request and
// the consumption of its real result).
func programsTaskReentry(t *testing.T, x *programsTaskFixture, sid, call string) (string, string, []bus.Provenance) {
	t.Helper()
	before := len(x.provider.observedOrigins(sid))
	path := filepath.Join(x.work, call+"-count")
	command := fmt.Sprintf("printenv GH_TOKEN | wc -c > %q; printf 'PROGRAM_TASK_CHILD_RAN'", path)
	x.provider.setScript(sid, []providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: call, Type: "function", Name: "bash", Arguments: map[string]any{"command": command}}}},
		wu5StopGW("task re-entry count completed"),
	})
	input := bus.InboundMessage{Channel: "webchat", SessionID: sid, ChatID: sid, Content: "web follow-up into the task session", Provenance: bus.NewProvenance(bus.OriginWeb)}
	require.NoError(t, x.bus.PublishInbound(context.Background(), input))
	x.waitEnds(t, sid)
	result := x.provider.result(sid, call)
	require.Contains(t, result, "PROGRAM_TASK_CHILD_RAN", "instrument: the re-entry's real bash ran; a refused or missed turn cannot establish zero")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	origins := x.provider.observedOrigins(sid)
	require.GreaterOrEqual(t, len(origins), before+2, "instrument: the re-entry requested the command and consumed its real result")
	return strings.TrimSpace(string(raw)), result, origins[before : before+2]
}

func TestProgramProvenance_ActualTaskSessionReentry(t *testing.T) {
	t.Run("S143_telegram_task_session_keeps_restriction_on_web_reentry_and_restart", func(t *testing.T) {
		x := newProgramsTaskFixture(t)
		const prompt = "run printenv GH_TOKEN | wc -c"
		telegram := bus.InboundMessage{Channel: "telegram", InstanceID: "telegram", Content: "create the counted task for Ops", Sender: bus.SenderInfo{CanonicalID: "telegram:s1", DisplayName: "S1 person"}, Provenance: bus.NewProvenance(bus.ConnectorOrigin("telegram"))}
		id := x.agentCreate(t, "s143-agent-create", telegram, prompt)
		creator := x.provider.observedOrigins(x.dev)
		require.NotEmpty(t, creator, "instrument: the actual creator turn reached the registered task tool")
		require.Equal(t, bus.NewProvenance(bus.ConnectorOrigin("telegram")), creator[0], "Given: T was created by a Telegram turn of Dev")

		// When: T's real Board run executes the command, in its own task session.
		count, result, sid := x.runBoard(t, id)
		meta, err := x.al.GetSessionStore().GetMeta(sid)
		require.NoError(t, err)
		require.Empty(t, meta.ParentSessionID, "Given: T's run is a parentless task session")
		require.Equal(t, id, meta.TaskID, "Given: the task identity is the session's task evidence")
		assertRecord := func(label string) {
			t.Helper()
			record, loadErr := x.al.GetSessionLifecycleStore().Load(sid)
			require.NoError(t, loadErr, "S143 %s: the lifecycle record loads", label)
			assert.Equal(t, session.ProvenanceModeLineage, record.ProvenanceMode, "S143 %s: a parentless task session is lineage", label)
			assert.Equal(t, []string{"connector:telegram"}, record.Provenance, "S143 %s: the record keeps the Telegram producer's exact origin set", label)
		}
		assertRecord("after the real run")
		assert.Equal(t, "0", count, "S143: T's own run prints 0")
		assert.Contains(t, result, programsPeerTelegramNotice, "S143: the Telegram-off notice, not unresolved")

		// Then a web message goes into T's task session: still withheld, and the
		// turn is {connector:telegram, web}, never web alone.
		both := []bus.Origin{bus.OriginWeb, bus.ConnectorOrigin("telegram")}
		count, result, origins := programsTaskReentry(t, x, sid, "s143-web-reentry")
		assert.Equal(t, "0", count, "S143: the web re-entry turn prints 0")
		assert.Contains(t, result, programsPeerTelegramNotice, "S143: the web re-entry is withheld for the Telegram origin it inherits")
		for _, actual := range origins {
			assert.ElementsMatch(t, both, actual.Origins(), "S143: the web re-entry turn is {connector:telegram, web}")
		}
		assertRecord("after the web re-entry")

		// Then the gateway restarts and the session is revived by a web message.
		programsRestartTaskRuntime(t, x)
		assertRecord("after the gateway-domain restart")
		count, result, origins = programsTaskReentry(t, x, sid, "s143-restart-reentry")
		assert.Equal(t, "0", count, "S143: the revived session's web turn prints 0")
		assert.Contains(t, result, programsPeerTelegramNotice, "S143: the revived turn is still withheld for Telegram")
		for _, actual := range origins {
			assert.ElementsMatch(t, both, actual.Origins(), "S143: the revived web turn is {connector:telegram, web}")
		}
		assertRecord("after the revived session ran")
	})

	t.Run("S143_instrument_root_classified_task_record_prints_16_on_the_same_reentry", func(t *testing.T) {
		x := newProgramsTaskFixture(t)
		store := x.al.GetSessionStore()
		meta, err := store.NewSession(session.SessionTypeTask, "webchat", "ops")
		require.NoError(t, err)
		workspaceID, owner, taskID := "sales", wu5Owner, "s143-root-twin"
		require.NoError(t, store.SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &workspaceID, Owner: &owner, TaskID: &taskID}))
		require.NoError(t, x.al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
			SessionID: meta.ID, Generation: 1, State: session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeHuman,
			WorkspaceID: "sales", AgentID: "ops", Origin: &session.Origin{Kind: session.OriginKindTask, TaskID: taskID},
			ProvenanceMode: session.ProvenanceModeRoot,
		}))
		count, result, _ := programsTaskReentry(t, x, meta.ID, "s143-root-twin")
		assert.Equal(t, "16", count, "S143 instrument: a parentless task record the classifier called root lets the web re-entry reach the token, so the scenario above fails on that regression")
		assert.NotContains(t, result, "Connected programs were not used for this command:")
	})
}
