package agent

// Spec rev8 S78/FR041/I-2 + ruling-H49-v2: explicit Stop, actual rebuilt boot,
// parent's real respond, same helper/generation. Never a seeded parked record.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func (x *programsStoppedHelperFixture) parentTool(t *testing.T, call string, args map[string]any) string {
	t.Helper()
	x.f.provider.setScript(x.parent, []providers.LLMResponse{{ToolCalls: []providers.ToolCall{{ID: call, Type: "function", Name: "delegate", Arguments: args}}}, wu5Stop("S78 parent action complete")})
	input := programsConnectorInput()
	input.SessionID, input.ChatID = x.parent, x.parent
	_, _, err := x.f.al.processMessage(context.Background(), input)
	require.NoError(t, err)
	x.f.waitForCalls(x.parent, 2, call+" actual parent tool response")
	return x.f.provider.resultForCall(x.parent, call)
}

func programsStoppedHelperRecord(t *testing.T, f *wu5Fixture, child string, generation int, state session.LifecycleState) *session.LifecycleRecord {
	t.Helper()
	record, err := f.al.GetSessionLifecycleStore().Load(child)
	require.NoError(t, err)
	require.Equal(t, child, record.SessionID)
	require.Equal(t, generation, record.Generation, "S78v2: Stop/boot/respond never starts the next generation")
	require.Equal(t, state, record.State)
	require.Equal(t, session.ProvenanceModeLineage, record.ProvenanceMode)
	require.Equal(t, []string{"connector:telegram"}, record.Provenance, "S78/I-2: durable launch origin, not a web/independent/unresolved substitute")
	require.Nil(t, record.NeedsInput, "H49v2: the question is ordinary; actual Stop is the only pause")
	return record
}

func TestProgramProvenance_ActualStoppedHelperBoot(t *testing.T) {
	t.Run("S78_provenance_survives_restart", func(t *testing.T) {
		x := newProgramsStoppedHelperFixture(t)
		raw := x.parentTool(t, "s78-real-delegate", map[string]any{"action": "run", "agent_id": "dev", "task": "Count the connected token bytes; ask the parent before the next count."})
		var launched gen.DelegateSessionResponse
		require.NoError(t, json.NewDecoder(strings.NewReader(raw)).Decode(&launched), "actual registered delegate launch must succeed: %s", raw)
		child := launched.SessionId
		require.NotEmpty(t, child)
		require.NotEqual(t, x.parent, child)
		select {
		case held := <-x.provider.held:
			require.Equal(t, child, held, "the actually launched helper is held after its real question")
		case <-time.After(5 * time.Second):
			t.Fatal("instrument: actual helper did not reach its post-question model edge")
		}
		before, err := os.ReadFile(filepath.Join(x.work, child+"-before"))
		require.NoError(t, err)
		require.Equal(t, "16", strings.TrimSpace(string(before)), "instrument: this same helper actually receives the connected token while Telegram is on")
		require.Contains(t, x.f.provider.resultForCall(child, "s78-count-before"), "S78_HELPER_CHILD_RAN")
		record := programsStoppedHelperRecord(t, x.f, child, 1, session.LifecycleRunning)
		require.Equal(t, x.parent, record.SteeredBy.SteeringSessionID, "actual parent edge was written by the delegate producer")
		question, err := x.f.al.GetMessageInboxStore().LookupCorrelation(x.parent, child, programsStoppedCorrelation)
		require.NoError(t, err)
		require.Equal(t, session.CorrelationOpen, question.State, "instrument: actual message_parent created the real open question, no seeded inbox entry")
		require.Equal(t, []string{programsStoppedCorrelation}, question.OpenIDs)
		stop := x.parentTool(t, "s78-real-stop", map[string]any{"action": "stop_all", "session_id": child})
		require.Equal(t, oneStopReply(child), stop, "S78v2: the registered parent action reaches the one real Stop path")
		stopped := awaitLandedStopped(t, x.f.al, child, "S78 explicitly stopped helper")
		joinGoalFixtureRuns(t, x.f.al)
		programsStoppedHelperRecord(t, x.f, child, 1, session.LifecycleStopped)
		require.NotNil(t, stopped.StopNote)
		channel := x.f.al.GetConfig().Channels["telegram"]
		channel.ProgramsAllowed = false
		x.f.al.GetConfig().Channels["telegram"] = channel
		x.restart(t, child)

		// Recover over the exact reopened stores and already-minted writing boot.
		al := x.f.al
		bootEpochRegistryMu.Lock()
		writingBoot := bootEpochRegistry[al]
		bootEpochRegistryMu.Unlock()
		require.NotNil(t, writingBoot)
		var notices []string
		recovery := &SteerBootRecovery{Lifecycle: al.GetSessionLifecycleStore(), Sessions: al.GetSessionStore(), Inbox: al.GetMessageInboxStore(), BootEpoch: writingBoot, Classifier: NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore()), Deliverer: al.getUpwardDeliverer(), OperatorNotice: func(message string) { notices = append(notices, message) }}
		require.NoError(t, recovery.Run(context.Background()), "S78: actual boot recovery, not just constructor/reload")
		joinGoalFixtureRuns(t, al)
		require.Empty(t, notices, "instrument: no unreadable/refused boot record can masquerade as preserved Stop")
		afterBoot := programsStoppedHelperRecord(t, x.f, child, 1, session.LifecycleStopped)
		require.Equal(t, stopped.StopNote, afterBoot.StopNote, "accepted explicit Stop survives real boot unchanged")
		require.Zero(t, x.f.provider.callCount(child), "H49v2: boot must not automatically revive an accepted Stop")
		require.NoFileExists(t, filepath.Join(x.work, child+"-after"), "negative execution instrument before the parent's respond")
		question, err = al.GetMessageInboxStore().LookupCorrelation(x.parent, child, programsStoppedCorrelation)
		require.NoError(t, err)
		require.Equal(t, session.CorrelationOpen, question.State, "real question persisted through boot")

		// No direct helper input: only the Telegram-origin parent can answer.
		response := x.parentTool(t, "s78-real-respond", map[string]any{"action": "respond", "session_id": child, "correlation_id": programsStoppedCorrelation, "text": "Run the next byte count now."})
		// DelegateRespondResponse's contract is an acknowledgement-only JSON
		// object (additionalProperties:false), not the internal fallback prose.
		var acknowledgement gen.DelegateRespondResponse
		decoder := json.NewDecoder(strings.NewReader(response))
		decoder.DisallowUnknownFields()
		require.NoError(t, decoder.Decode(&acknowledgement), "actual respond contract: %s", response)
		require.Equal(t, gen.DelegateRespondResponse{Acknowledged: true}, acknowledgement)
		x.f.waitForCalls(child, 2, "S78 resumed helper actual bash result")
		joinGoalFixtureRuns(t, al)
		result := x.f.provider.resultForCall(child, "s78-count-after")
		require.Contains(t, result, "S78_HELPER_CHILD_RAN", "a tool denial/empty result cannot prove token withholding")
		count, err := os.ReadFile(filepath.Join(x.work, child+"-after"))
		require.NoError(t, err)
		require.Equal(t, "0", strings.TrimSpace(string(count)), "S78: persisted Telegram origin remains subject to its current off setting")
		require.Contains(t, result, programsTelegramNotice, "S78: full original-channel notice, not web or unresolved")
		finished := programsStoppedHelperRecord(t, x.f, child, 1, session.LifecycleCompleted)
		require.Equal(t, x.parent, finished.SteeredBy.SteeringSessionID)
		origins := x.provider.observed(child)
		require.Len(t, origins, 2, "the same real observer sees the admitted resumed command and its result")
		for _, provenance := range origins {
			require.Equal(t, []bus.Origin{bus.ConnectorOrigin("telegram")}, provenance.Origins(), "actual receiving turn provenance, not joined/stamped by the fixture")
		}
		question, err = al.GetMessageInboxStore().LookupCorrelation(x.parent, child, programsStoppedCorrelation)
		require.NoError(t, err)
		require.Equal(t, session.CorrelationAnswered, question.State, "the actual parent's answer was durably consumed")
		t.Logf("actual Telegram delegate/child16/question/Stop -> new boot%d preserves stopped -> parent respond -> same helper/gen%d child0/full Telegram notice", al.bootEpochFor(), finished.Generation)
	})
}
