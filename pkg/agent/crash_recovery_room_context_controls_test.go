//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

const closureRoomDemand = "CAUSAL_RETIRED_G_DEMAND"
const closureRoomPlan = "CAUSAL_RETIRED_G_PLAN"

func crashRequireRoomCausality(t *testing.T) {
	t.Helper()
	if _, ok := reflect.TypeOf(session.EntrySource{}).FieldByName("RoomRequest"); !ok {
		t.Fatal("BLOCKED: trusted room-input execution correlation and selective retirement not implemented — required by crash-recovery DESIGN Decision 3 / Q13")
	}
}

type crashRoomContext struct {
	h                 *crashHarness
	id, path, request string
	view              session.WindowView
	release           func()
	done              <-chan error
}

func crashConsumedRoomContext(t *testing.T, hold bool, configure ...func(*crashHarness, *crashScript)) *crashRoomContext {
	t.Helper()
	crashRequireRoomCausality(t)
	p := &crashScript{}
	h := crashNew(t, p)
	h.al.SetAddressDeps(&fakeAddressDeps{eligible: map[string]bool{testHarnessWorkspaceMembershipID + "/" + testDefaultAgentID: true}})
	h.al.SetRoomRuntime(&fakeRoomRuntime{allow: true})
	work, err := workspace.EnsureWorkDir(h.home, testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	f := &crashRoomContext{h: h, path: filepath.Join(work, "causal-effects.txt"), request: "causal-G"}
	main, err := h.al.GetSessionStore().GetOrCreateMainSession(testHarnessWorkspaceMembershipID, testDefaultAgentID)
	require.NoError(t, err)
	f.id = main.ID
	crashAppend(t, h, f.id, providers.Message{Role: "user", Content: "independent old turn"})
	crashAppend(t, h, f.id, providers.Message{Role: "assistant", Content: "independent settled old answer"})
	second := make(chan struct{}, 1)
	release := make(chan struct{})
	f.release = sync.OnceFunc(func() { close(release) })
	t.Cleanup(f.release)
	p.next = func(i int, _ []providers.Message) (*providers.LLMResponse, error) {
		if i == 0 {
			return &providers.LLMResponse{Content: closureRoomPlan, ToolCalls: []providers.ToolCall{crashCall("causal-effect", "append_file", map[string]any{"path": f.path, "content": "G1\n"})}, FinishReason: "tool_calls"}, nil
		}
		second <- struct{}{}
		if hold {
			<-release
		}
		return &providers.LLMResponse{Content: closureRoomPlan + " unfinished room answer", FinishReason: "stop"}, nil
	}
	for _, apply := range configure {
		apply(h, p)
	}
	_, err = h.al.AdmitRoomRequest(context.Background(), RoomRequestAdmission{Receiver: addressing.Pair{WorkspaceID: testHarnessWorkspaceMembershipID, AgentID: testDefaultAgentID}, DeliveryID: f.request, RoomID: "context-room", RoomName: "Context", Sender: addressing.Sender{Principal: "owner"}, SenderLabel: "the user", DisplayText: closureRoomDemand, ModelText: closureRoomDemand})
	require.NoError(t, err)
	var message bus.InboundMessage
	select {
	case message = <-h.al.bus.InboundChan():
	default:
		t.Fatal("fixture: admitted fresh room request published no wake")
	}
	done := make(chan error, 1)
	f.done = done
	go func() {
		defer close(done)
		_, _, err := h.al.processMessage(context.Background(), message)
		done <- err
	}()
	t.Cleanup(func() { f.release(); require.NoError(t, crashAwait(t, done)) })
	crashAwait(t, second)
	if !hold {
		require.NoError(t, crashAwait(t, done))
	}
	f.view, err = h.al.GetSessionStore().WindowView(context.Background(), f.id)
	require.NoError(t, err)
	capture, found, err := h.al.RequestLedger().Get(f.id, f.request)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, capture.Delivered)
	fields := crashFields(t, capture)
	require.NotNil(t, fields["model_input"], "server correlated the consumed input to its exact archive address")
	require.NotNil(t, fields["model_execution"])
	bytes, err := os.ReadFile(f.path)
	require.NoError(t, err)
	require.Equal(t, "G1\n", string(bytes))
	return f
}
func crashAssertNoCausalRoomDemand(t *testing.T, messages []providers.Message) {
	t.Helper()
	var effects int
	for _, m := range messages {
		if m.Role == "user" || m.Role == "assistant" {
			assert.NotContains(t, m.Content, closureRoomDemand, "retired request instruction is not active model input")
			assert.NotContains(t, m.Content, closureRoomPlan, "retired derived plan/narrative is not active model input")
		}
		if m.Role == "tool" && m.ToolCallID == "causal-effect" {
			effects++
		}
	}
	assert.Equal(t, 1, effects, "settled effect survives selective retirement")
}

func TestCrashRecovery_RetirementSurvivesHumanReportTrimAndRecallEntries(t *testing.T) {
	for _, entry := range []string{"human", "helper_report", "trim_reload", "recall_injection"} {
		t.Run(entry, func(t *testing.T) {
			f := crashConsumedRoomContext(t, false)
			require.NoError(t, f.h.al.DiscardRequest(f.id, f.request))
			crashAppend(t, f.h, f.id, providers.Message{Role: "user", Content: "INDEPENDENT_PENDING_INPUT"})
			crashAppend(t, f.h, f.id, providers.Message{Role: "system", Content: "INDEPENDENT_CONTROL"})
			if entry == "trim_reload" {
				inst := f.h.al.GetRegistry().primaryChatAgentForTest()
				result, changed := f.h.al.trimWindowChecked(context.Background(), inst, f.id, f.id, true)
				require.NoError(t, result.Err)
				require.True(t, changed)
				f.h.reboot(t, &crashScript{})
			}
			if entry == "recall_injection" {
				var msgs []providers.Message
				var ordinals []int
				for _, slot := range f.view.Live {
					m := slot.Message
					if strings.Contains(m.Content, closureRoomDemand) || strings.Contains(m.Content, closureRoomPlan) || m.ToolCallID == "causal-effect" {
						msgs = append(msgs, m)
						ordinals = append(ordinals, slot.Ordinal)
					}
				}
				f.h.al.setRecallSpan(agentSessionKey(testDefaultAgentID, roomWakeMessage(addressing.Capture{ReceiverSessionID: f.id})), newRecallSpan(1, 1, msgs, ordinals))
			}
			p := &crashScript{}
			f.h.al.GetRegistry().primaryChatAgentForTest().Provider = p
			if entry == "helper_report" {
				id := appendWakeInboxEntry(t, f.h.al, f.id, "INDEPENDENT_HELPER_REPORT")
				_, err := f.h.al.processSteeredSystemWake(context.Background(), wakeMessage(f.id, id, f.h.record(t, f.id).Generation))
				require.NoError(t, err)
			} else {
				_, err := f.h.human(t, f.id, "NEW_J_ONLY")
				require.NoError(t, err)
			}
			require.Len(t, p.calls(), 1)
			input := p.calls()[0]
			crashAssertNoCausalRoomDemand(t, input)
			all := ""
			for _, m := range input {
				all += m.Content + "\n"
			}
			assert.Contains(t, all, "INDEPENDENT_PENDING_INPUT")
			assert.Contains(t, all, "INDEPENDENT_CONTROL")
			archive, err := f.h.al.GetSessionStore().ReadArchive(context.Background(), f.id)
			require.NoError(t, err)
			require.NotEmpty(t, archive)
			bytes, err := os.ReadFile(f.path)
			require.NoError(t, err)
			assert.Equal(t, "G1\n", string(bytes))
		})
	}
}

func TestCrashRecovery_RetirementPersistenceFailureBlocksNextModel(t *testing.T) {
	f := crashConsumedRoomContext(t, true)
	blocked := filepath.Join(f.h.al.GetSessionStore().BaseDir(), f.id, "room_retirements")
	snapshot := f.h.snapshot(t)
	relative, err := filepath.Rel(f.h.home, blocked)
	require.NoError(t, err)
	// Fault only the crash image: the still-live producer must settle against
	// its healthy home before the corrupted image is reopened under a new boot.
	require.NoError(t, os.WriteFile(filepath.Join(snapshot, relative), []byte("not a directory"), 0600))
	f.release()
	require.NoError(t, crashAwait(t, f.done))
	p := &crashScript{}
	f.h.restore(t, snapshot, p)
	r := &SteerBootRecovery{Lifecycle: f.h.al.GetSessionLifecycleStore(), Sessions: f.h.al.GetSessionStore(), Inbox: f.h.al.GetMessageInboxStore(), BootEpoch: f.h.boot, Classifier: NewSteerRecordClassifier(f.h.al.GetSessionLifecycleStore(), f.h.al.GetSessionStore()), Deliverer: f.h.al.getUpwardDeliverer()}
	assert.Error(t, r.Run(context.Background()), "failed retirement is visible at real boot recovery")
	_, err = f.h.human(t, f.id, "NEW_J_MUST_NOT_RUN")
	assert.Error(t, err, "cannot assemble a later model request until exact retirement is durably established")
	assert.Equal(t, 0, len(p.calls()))
	bytes, err := os.ReadFile(f.path)
	require.NoError(t, err)
	assert.Equal(t, "G1\n", string(bytes))
}

func TestCrashRecovery_MissingRoomCorrelationIsContextRepairNotGuessedRetirement(t *testing.T) {
	f := crashConsumedRoomContext(t, false)
	capture, _, err := f.h.al.RequestLedger().Get(f.id, f.request)
	require.NoError(t, err)
	fields := crashFields(t, capture)
	delete(fields, "model_input")
	delete(fields, "model_execution")
	data, err := json.Marshal(fields)
	require.NoError(t, err)
	var withoutCorrelation addressing.Capture
	require.NoError(t, json.Unmarshal(data, &withoutCorrelation))
	require.NoError(t, f.h.al.RequestLedger().Put(withoutCorrelation))
	saved, found, err := f.h.al.RequestLedger().Get(f.id, f.request)
	require.NoError(t, err)
	require.True(t, found, "instrument: the corrupted request is still the real admitted request")
	require.Nil(t, saved.ModelInput, "instrument: saved input correlation was actually removed")
	require.Nil(t, saved.ModelExecution, "instrument: saved execution correlation was actually removed")
	err = f.h.al.DiscardRequest(f.id, f.request)
	assert.Error(t, err, "unknown correlation cannot erase a guessed span of independent history")
	after, err := f.h.al.GetSessionStore().WindowView(context.Background(), f.id)
	require.NoError(t, err)
	assert.Equal(t, f.view.Live, after.Live, "failure preserves all independently addressed archive inputs")
}
