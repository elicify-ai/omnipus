package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	memoryInputRound3A        = "f02-entry-a"
	memoryInputRound3B        = "f02-entry-b"
	memoryInputRound3Instance = "telegram.f02-entry"
	memoryInputRound3History  = "A_HISTORY_MUST_REMAIN_IN_ITS_ORIGINAL_CONVERSATION"
	memoryInputRound3Response = "same-workspace input reached the model"
	// F02 requires an actionable refusal, not the generic model/retry error.
	// This literal pins that public meaning independently of the translator.
	memoryInputRound3Refusal = "this conversation cannot change workspace; open the conversation in the workspace you want"
)

// TestMemoryRound3InputEntries traces to review F02 and the approved refusal
// contract: A cannot be reused as B, and refusal precedes every input effect.
// All stores, routes, lifecycle/admission and steering paths are real. Only
// the paid provider and external CLI process edges are test doubles.
func TestMemoryRound3InputEntries(t *testing.T) {
	t.Run("ProcessMessage", memoryInputRound3ProcessMessages)
	t.Run("NativeHelper", memoryInputRound3NativeHelpers)
	t.Run("ExternalHelper", memoryExternalInputRound3Helpers)
	t.Run("ExternalTask", memoryExternalInputRound3Tasks)
}

// memoryInputRound3Provider is a process-edge model substitute. Its atomic
// counter is also an instrument control: each accepted ordinary input must
// reach it exactly once with the actual input, whereas foreign input never may.
type memoryInputRound3Provider struct {
	calls    atomic.Int64
	mu       sync.Mutex
	contents []string
}

func (p *memoryInputRound3Provider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	p.mu.Lock()
	p.contents = nil
	for _, message := range messages {
		p.contents = append(p.contents, message.Content)
	}
	p.mu.Unlock()
	return &providers.LLMResponse{Content: memoryInputRound3Response}, nil
}

func (p *memoryInputRound3Provider) GetDefaultModel() string {
	return "memory-input-round3-model"
}

func (p *memoryInputRound3Provider) receivedContents() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.contents...)
}

func memoryInputRound3Loop(t *testing.T) (*AgentLoop, *memoryInputRound3Provider) {
	t.Helper()
	t.Setenv(config.EnvHome, t.TempDir())
	al, _ := newSteerAL(t)
	provider := &memoryInputRound3Provider{}
	agent, found := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, found, "SETUP: canonical native agent is registered")
	agent.Provider = provider
	memoryInputRound3SeedWorkspaces(t, testDefaultAgentID)
	return al, provider
}

// writeGraphFiles intentionally omits membership. Supply the actual membership
// records too, after the harness has selected its final isolated home. Both
// scopes are valid, so a missing-workspace refusal cannot masquerade as F02.
func memoryInputRound3SeedWorkspaces(t *testing.T, target string) {
	t.Helper()
	members := []string{testDefaultAgentID}
	if target != testDefaultAgentID {
		members = append(members, target)
	}
	for _, workspaceID := range []string{memoryInputRound3A, memoryInputRound3B} {
		writeGraphFiles(t, omnipusHome(), workspaceID, false, []graphEdge{
			edge(testDefaultAgentID, target, nil, nil),
		})
		data, err := json.Marshal(testHarnessWorkspaceFile{ID: workspaceID, CoreTeam: members})
		require.NoError(t, err, "SETUP: encode actual workspace membership")
		path := filepath.Join(omnipusHome(), "workspaces", workspaceID+".json")
		require.NoError(t, fileutil.WriteFileAtomic(path, data, 0o644), "SETUP: persist workspace membership")
	}
}

func memoryInputRound3SeedHistory(t *testing.T, store *session.UnifiedStore, sessionID string) {
	t.Helper()
	require.NoError(t, store.AddMessage(sessionID, "assistant", memoryInputRound3History), "SETUP: seed A model archive")
	require.NoError(t, store.AppendTranscriptStrict(sessionID, session.TranscriptEntry{
		ID:      sessionID + "-f02-a-history",
		Role:    "assistant",
		AgentID: testDefaultAgentID,
		Content: memoryInputRound3History,
	}), "SETUP: seed A visible transcript")
}

func memoryInputRound3Ordinary(
	t *testing.T, connector bool, requestedWorkspace string,
) (*AgentLoop, *memoryInputRound3Provider, bus.InboundMessage) {
	t.Helper()
	al, provider := memoryInputRound3Loop(t)
	var sessionID string
	if connector {
		sessionID = al.resolveOrCreateChannelSession("telegram", memoryInputRound3Instance,
			"f02-peer", testDefaultAgentID, "F02 Peer", memoryInputRound3A)
	} else {
		sessionID = newTestSteeringSession(t, al, memoryInputRound3A)
	}
	require.NotEmpty(t, sessionID, "SETUP: genuine stored A conversation")
	memoryInputRound3SeedHistory(t, al.GetSessionStore(), sessionID)
	gate := al.steerAdmission()
	gate.entryMu.Lock()
	_, err := al.ensureOrdinaryRootRecord(al.GetSessionLifecycleStore(), sessionID, al.GetSessionStore())
	gate.entryMu.Unlock()
	require.NoError(t, err, "SETUP: canonical ordinary lifecycle creation")

	msg := bus.InboundMessage{
		Channel:        "webchat",
		ChatID:         "f02-peer",
		SessionID:      sessionID,
		Content:        "F02 ordinary instruction for " + requestedWorkspace,
		UserInitiated:  true,
		OperatorPrompt: true,
		Metadata:       map[string]string{"agent_id": testDefaultAgentID, "workspace_id": requestedWorkspace},
	}
	if connector {
		msg.Channel = "telegram"
		msg.InstanceID = memoryInputRound3Instance
		delete(msg.Metadata, "workspace_id")
		al.GetConfig().Channels = map[string]config.ChannelInstanceConfig{
			memoryInputRound3Instance: {Type: "telegram", Enabled: true, WorkspaceID: requestedWorkspace},
		}
	}
	require.Zero(t, provider.calls.Load(), "SETUP: ordinary conversation/lifecycle creation makes no model call")
	memoryInputRound3AssertSetupOutbound(t, al)
	return al, provider, msg
}

func memoryInputRound3ProcessMessages(t *testing.T) {
	for _, source := range []struct {
		name      string
		connector bool
	}{
		{name: "ExplicitWorkspace"},
		{name: "ConfiguredInstanceFallback", connector: true},
	} {
		t.Run(source.name, func(t *testing.T) {
			t.Run("ForeignDirectRefusesBeforeEffects", func(t *testing.T) {
				al, provider, msg := memoryInputRound3Ordinary(t, source.connector, memoryInputRound3B)
				before := memoryInputRound3Capture(t, al, provider, msg.SessionID, agentSessionKey(testDefaultAgentID, msg))
				response, agent, err := al.processMessage(context.Background(), msg)
				require.ErrorIs(t, err, session.ErrWorkspaceImmutable, "F02: real processMessage must reject A addressed as B")
				require.Equal(t, "", response, "foreign input cannot produce an accepted response")
				require.Nil(t, agent, "refusal precedes routing/admission")
				require.Equal(t, memoryInputRound3Refusal, userVisibleTurnError(err), "actionable direct refusal")
				memoryInputRound3AssertUnchanged(t, before, memoryInputRound3Capture(t, al, provider, msg.SessionID, agentSessionKey(testDefaultAgentID, msg)))
				require.Empty(t, memoryInputRound3Outbound(t, al), "processMessage returns its error; it does not publish it itself")
			})
			t.Run("ForeignWorkerPublishesExactlyOneRefusal", func(t *testing.T) {
				al, provider, msg := memoryInputRound3Ordinary(t, source.connector, memoryInputRound3B)
				before := memoryInputRound3Capture(t, al, provider, msg.SessionID, agentSessionKey(testDefaultAgentID, msg))
				worker := newSessionWorker(agentSessionKey(testDefaultAgentID, msg), al, func() {})
				t.Cleanup(worker.cancel)
				// Synchronous outer shell; no worker runLoop or gateway guard masks
				// the production processMessage invocation being tested.
				worker.processTurn(context.Background(), msg)
				memoryInputRound3AssertRefusal(t, al, msg)
				memoryInputRound3AssertUnchanged(t, before, memoryInputRound3Capture(t, al, provider, msg.SessionID, agentSessionKey(testDefaultAgentID, msg)))
			})
			t.Run("SameWorkspaceReachesRealProvider", func(t *testing.T) {
				al, provider, msg := memoryInputRound3Ordinary(t, source.connector, memoryInputRound3A)
				response, agent, err := al.processMessage(context.Background(), msg)
				require.NoError(t, err, "same-workspace control must be executable, not merely pass validation")
				require.Equal(t, memoryInputRound3Response, response)
				require.NotNil(t, agent)
				require.Equal(t, testDefaultAgentID, agent.ID)
				require.EqualValues(t, 1, provider.calls.Load(), "one no-tool accepted turn reaches the paid-model edge once")
				require.Contains(t, provider.receivedContents(), msg.Content, "the actual accepted instruction reached the model")
				meta, metaErr := al.GetSessionStore().GetMeta(msg.SessionID)
				require.NoError(t, metaErr)
				require.NotNil(t, meta)
				require.Equal(t, memoryInputRound3A, meta.WorkspaceID)
				require.Empty(t, memoryInputRound3Outbound(t, al), "direct processMessage leaves publication to its caller")
			})
		})
	}
}

func memoryInputRound3LaunchHelper(t *testing.T, al *AgentLoop, target string) (string, steer.LaunchResult) {
	t.Helper()
	parentID := newTestSteeringSession(t, al, memoryInputRound3A)
	memoryInputRound3SeedHistory(t, al.GetSessionStore(), parentID)
	child, err := NewSteerLauncher(al).Launch(tools.WithWorkspaceID(context.Background(), memoryInputRound3A), steer.LaunchRequest{
		SteeringSessionID: parentID,
		TargetAgentID:     target,
		Task:              memoryInputRound3History,
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "f02-real-launch"},
	})
	require.NoError(t, err, "SETUP: real Launch must create the helper and edge")
	require.NotEmpty(t, child.SessionID)
	require.Equal(t, 1, child.Generation, "Launch starts generation one")
	rec, loadErr := al.GetSessionLifecycleStore().Load(child.SessionID)
	require.NoError(t, loadErr)
	require.NotNil(t, rec)
	require.NotNil(t, rec.SteeredBy, "SETUP: genuine helper edge, not a plain chat with a helper-shaped id")
	require.Equal(t, parentID, rec.SteeredBy.SteeringSessionID)
	require.Equal(t, parentID, rec.SteeredBy.RootSessionID)
	require.Equal(t, target, rec.AgentID)
	require.Equal(t, memoryInputRound3A, rec.WorkspaceID)
	return parentID, child
}

func memoryInputRound3HelperMessage(sessionID, workspaceID string) bus.InboundMessage {
	return bus.InboundMessage{
		Channel:        "webchat",
		ChatID:         "f02-helper-own-view",
		SessionID:      sessionID,
		Content:        "F02 helper instruction for " + workspaceID,
		UserInitiated:  true,
		OperatorPrompt: true,
		Metadata:       map[string]string{"workspace_id": workspaceID},
	}
}

func memoryInputRound3NativeHelpers(t *testing.T) {
	for _, entry := range []string{"Direct", "Public"} {
		for _, scope := range []struct {
			name      string
			workspace string
			foreign   bool
		}{
			{name: "ForeignRefusesBeforeQueueOrRevival", workspace: memoryInputRound3B, foreign: true},
			{name: "SameWorkspaceQueuesCorrectInstruction", workspace: memoryInputRound3A},
		} {
			t.Run(entry+"/"+scope.name, func(t *testing.T) {
				al, provider := memoryInputRound3Loop(t)
				parentID, child := memoryInputRound3LaunchHelper(t, al, testDefaultAgentID)
				require.False(t, child.Is3P)
				rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
				require.NoError(t, err)
				require.NotNil(t, rec)
				require.Equal(t, session.LifecycleQueued, rec.State, "SETUP: Launch-only native helper is genuinely queued")
				require.Nil(t, rec.ExecutionID, "no execution claim is fabricated for a never-dispatched child")
				t.Cleanup(func() { memoryInputRound3Stop(t, al, child.SessionID) })
				memoryInputRound3AssertSetupOutbound(t, al)
				msg := memoryInputRound3HelperMessage(child.SessionID, scope.workspace)
				before := memoryInputRound3Capture(t, al, provider, child.SessionID, agentSessionKey(testDefaultAgentID, msg))
				parentBefore := memoryInputRound3Capture(t, al, provider, parentID)
				if entry == "Public" {
					require.True(t, al.deliverHumanHelperInput(msg), "a refused helper input is handled, never passed to ordinary dispatch")
				} else {
					err = al.enqueueHelperSteeringFromMessage(msg, testDefaultAgentID, false)
					if scope.foreign {
						require.ErrorIs(t, err, session.ErrWorkspaceImmutable, "F02: native helper's real enqueue door refuses B")
						require.Equal(t, memoryInputRound3Refusal, userVisibleTurnError(err))
					} else {
						require.NoError(t, err)
					}
				}
				if scope.foreign {
					if entry == "Public" {
						memoryInputRound3AssertRefusal(t, al, msg)
					} else {
						require.Empty(t, memoryInputRound3Outbound(t, al))
					}
					memoryInputRound3AssertUnchanged(t, before, memoryInputRound3Capture(t, al, provider, child.SessionID, agentSessionKey(testDefaultAgentID, msg)))
					memoryInputRound3AssertUnchanged(t, parentBefore, memoryInputRound3Capture(t, al, provider, parentID))
				} else {
					memoryInputRound3AssertQueued(t, al, msg)
					require.Empty(t, memoryInputRound3Outbound(t, al), "accepted A instruction is not refused")
				}
				require.Zero(t, provider.calls.Load(), "queued native helper input must not dispatch a model turn")
				require.Nil(t, al.externalRunSessionIfPresent(child.SessionID), "native input cannot create an external run")
			})
		}
	}
	t.Run("StoppedHelper", memoryInputRound3StoppedNativeHelpers)
}

// These are complete values, not only workspace labels or transcript lengths.
// Captured readers return independent metadata/record/message values. The raw
// append-only journal additionally detects an attempted write of the same tail.
type memoryInputRound3Snapshot struct {
	metadata         *session.UnifiedMeta
	archive          []memory.ArchivedMessage
	transcript       []session.TranscriptEntry
	lifecycle        *session.LifecycleRecord
	journal          []byte
	controls         memoryInputRound3OptionalFile
	pending          map[string]int
	admissionActive  int
	admissionWaiting int
	providerCalls    int64
}

type memoryInputRound3OptionalFile struct {
	exists bool
	body   []byte
}

func memoryInputRound3ReadOptional(t *testing.T, path string) memoryInputRound3OptionalFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return memoryInputRound3OptionalFile{}
	}
	require.NoError(t, err, "read optional control ledger; only genuine absence is allowed")
	return memoryInputRound3OptionalFile{exists: true, body: data}
}

func memoryInputRound3Capture(
	t *testing.T, al *AgentLoop, provider *memoryInputRound3Provider, sessionID string, extraScopes ...string,
) memoryInputRound3Snapshot {
	t.Helper()
	store, lifecycle := al.GetSessionStore(), al.GetSessionLifecycleStore()
	require.NotNil(t, store, "SETUP: shared session store is real")
	require.NotNil(t, lifecycle, "SETUP: durable lifecycle store is real")
	meta, err := store.GetMeta(sessionID)
	require.NoError(t, err, "read complete session metadata")
	require.NotNil(t, meta)
	require.Equal(t, memoryInputRound3A, meta.WorkspaceID, "A conversation keeps its initial binding")
	archive, err := store.ReadArchive(context.Background(), sessionID)
	require.NoError(t, err, "read complete model archive")
	require.NotEmpty(t, archive, "SETUP: no empty-archive equality can stand in for preserving A history")
	transcript, err := store.ReadTranscript(sessionID)
	require.NoError(t, err, "read complete visible transcript")
	require.NotEmpty(t, transcript, "SETUP: real A transcript exists")
	rec, err := lifecycle.Load(sessionID)
	require.NoError(t, err, "read complete durable lifecycle record")
	require.NotNil(t, rec)
	require.Equal(t, sessionID, rec.SessionID)
	require.Equal(t, meta.AgentID, rec.AgentID, "genuine lifecycle and session agree on the canonical owner")
	require.Equal(t, memoryInputRound3A, rec.WorkspaceID, "genuine lifecycle and session agree on A")
	require.GreaterOrEqual(t, rec.Generation, 1)
	journal, err := os.ReadFile(filepath.Join(lifecycle.Dir(), sessionID+".jsonl"))
	require.NoError(t, err, "read raw lifecycle journal")
	require.NotEmpty(t, journal)
	pending := map[string]int{sessionID: al.pendingSteeringCountForScope(sessionID)}
	for _, scope := range extraScopes {
		pending[scope] = al.pendingSteeringCountForScope(scope)
	}
	gate := al.steerAdmission()
	return memoryInputRound3Snapshot{
		metadata:         meta,
		archive:          archive,
		transcript:       transcript,
		lifecycle:        rec,
		journal:          journal,
		controls:         memoryInputRound3ReadOptional(t, filepath.Join(lifecycle.Dir(), "controls", sessionID+".jsonl")),
		pending:          pending,
		admissionActive:  gate.activeCount(),
		admissionWaiting: gate.queueLen(),
		providerCalls:    provider.calls.Load(),
	}
}

func memoryInputRound3AssertUnchanged(t *testing.T, before, after memoryInputRound3Snapshot) {
	t.Helper()
	require.Equal(t, before.metadata, after.metadata, "F02: refusal changes no metadata field")
	require.Equal(t, before.archive, after.archive, "F02: refusal neither appends nor changes the model archive")
	require.Equal(t, before.transcript, after.transcript, "F02: refusal preserves the entire transcript")
	require.Equal(t, before.lifecycle.Generation, after.lifecycle.Generation, "F02: no revival/generation change")
	require.Equal(t, before.lifecycle.ExecutionID, after.lifecycle.ExecutionID, "F02: no admission/execution replacement")
	require.Equal(t, before.lifecycle, after.lifecycle, "F02: every lifecycle field stays unchanged")
	require.Equal(t, before.journal, after.journal, "F02: not even a same-tail lifecycle append is permitted")
	require.Equal(t, before.controls, after.controls, "F02: no steering/control receipt may be accepted")
	require.Equal(t, before.pending, after.pending, "F02: neither target nor routing-key steering queue changes")
	require.Equal(t, before.admissionActive, after.admissionActive, "F02: no additional admission")
	require.Equal(t, before.admissionWaiting, after.admissionWaiting, "F02: no queued dispatch awaiting a provider")
	require.Equal(t, before.providerCalls, after.providerCalls, "F02: zero additional provider dispatch")
}

func memoryInputRound3Outbound(t *testing.T, al *AgentLoop) []bus.OutboundMessage {
	t.Helper()
	var out []bus.OutboundMessage
	for {
		select {
		case msg, open := <-al.bus.OutboundChan():
			require.True(t, open, "the real outbound bus must stay open through assertions")
			out = append(out, msg)
		default:
			return out
		}
	}
}

func memoryInputRound3AssertSetupOutbound(t *testing.T, al *AgentLoop) {
	t.Helper()
	// Real launch/task status notifications use the event bus, not ordinary
	// outbound replies. No model output has been released at this barrier.
	// Drain and classify now; never discard output after the measured input.
	require.Empty(t, memoryInputRound3Outbound(t, al), "SETUP: unexpected ordinary outbound before input baseline")
}

func memoryInputRound3AssertRefusal(t *testing.T, al *AgentLoop, msg bus.InboundMessage) {
	t.Helper()
	require.Equal(t, []bus.OutboundMessage{{
		Channel:   msg.Channel,
		ChatID:    msg.ChatID,
		SessionID: msg.SessionID,
		Content:   memoryInputRound3Refusal,
	}}, memoryInputRound3Outbound(t, al), "F02: exactly one actionable refusal with the complete original destination")
}

func memoryInputRound3AssertQueued(t *testing.T, al *AgentLoop, msg bus.InboundMessage) {
	t.Helper()
	require.Equal(t, 1, al.pendingSteeringCountForScope(msg.SessionID), "same-A control: one instruction under the helper/task's own id")
	require.NotNil(t, al.steering)
	al.steering.mu.Lock()
	var messages []providers.Message
	for _, item := range al.steering.queues[msg.SessionID] {
		message := item.message
		message.Media = append([]string(nil), message.Media...)
		messages = append(messages, message)
	}
	al.steering.mu.Unlock()
	require.Equal(t, []providers.Message{{Role: "user", Content: msg.Content, Media: msg.Media}}, messages,
		"same-A control: exact user instruction, not the old launch prompt or a different scope")
	meta, err := al.GetSessionStore().GetMeta(msg.SessionID)
	require.NoError(t, err)
	require.NotNil(t, meta)
	require.Equal(t, memoryInputRound3A, meta.WorkspaceID)
	require.Zero(t, al.pendingSteeringCountForScope(agentSessionKey(meta.AgentID, msg)), "no duplicate instruction in the agent routing-key bucket")
}

// Stop failures remain failures even during cleanup; none are logged-and-lost.
// This helper does not release a fake process gate or restore a driver factory:
// the external fixture owns that ordering and joins after this real Stop.
func memoryInputRound3Stop(t *testing.T, al *AgentLoop, sessionID string) {
	t.Helper()
	result, err := al.StopSession(context.Background(), StopRequest{
		SessionID: sessionID,
		By:        steer.Principal{Kind: steer.PrincipalKindHuman, ID: "f02-test-operator"},
		Channel:   "webchat",
	})
	if err != nil {
		t.Errorf("cleanup: real StopSession(%s): %v", sessionID, err)
	}
	if result.RootErr != nil {
		t.Errorf("cleanup: StopSession root effect: %v", result.RootErr)
	}
	if result.DiscardErr != nil {
		t.Errorf("cleanup: StopSession input discard: %v", result.DiscardErr)
	}
	if len(result.Report.Unreachable) != 0 {
		t.Errorf("cleanup: StopSession unreachable sessions: %+v", result.Report.Unreachable)
	}
	if result.BackgroundFailed != 0 {
		t.Errorf("cleanup: StopSession background failures: %d", result.BackgroundFailed)
	}
}

// A separate paid-provider edge holds the same-A revival long enough to read
// its genuinely admitted execution. It wraps the atomic recorder, not the
// router, revival, admission or turn engine.
type memoryInputRound3HeldProvider struct {
	recorder    *memoryInputRound3Provider
	entered     chan struct{}
	enterOnce   sync.Once
	release     chan struct{}
	releaseOnce sync.Once
}

func (p *memoryInputRound3HeldProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	definitions []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	response, err := p.recorder.Chat(ctx, messages, definitions, model, opts)
	if err != nil {
		return nil, err
	}
	p.enterOnce.Do(func() { close(p.entered) })
	select {
	case <-p.release:
		return response, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *memoryInputRound3HeldProvider) GetDefaultModel() string {
	return p.recorder.GetDefaultModel()
}

func (p *memoryInputRound3HeldProvider) releaseNow() {
	p.releaseOnce.Do(func() { close(p.release) })
}

// The precision follow-up to F02 requires the guard BEFORE revival. Start with
// a real Launch, land a real Stop on that queued helper, then compare complete
// state for B. A has a separate fixture and must actually dispatch and join.
func memoryInputRound3StoppedNativeHelpers(t *testing.T) {
	for _, entry := range []string{"Direct", "Public"} {
		for _, scope := range []struct {
			name      string
			workspace string
			foreign   bool
		}{
			{name: "ForeignLeavesLandedStopUntouched", workspace: memoryInputRound3B, foreign: true},
			{name: "SameWorkspaceActuallyRevivesAndDispatches", workspace: memoryInputRound3A},
		} {
			t.Run(entry+"/"+scope.name, func(t *testing.T) {
				al, recorder := memoryInputRound3Loop(t)
				parentID, child := memoryInputRound3LaunchHelper(t, al, testDefaultAgentID)
				held := &memoryInputRound3HeldProvider{
					recorder: recorder, entered: make(chan struct{}), release: make(chan struct{}),
				}
				agent, found := al.GetRegistry().GetAgent(testDefaultAgentID)
				require.True(t, found)
				agent.Provider = held
				t.Cleanup(func() {
					memoryInputRound3Stop(t, al, child.SessionID)
					held.releaseNow()
					al.steerAdmission().turns.Wait()
				})
				memoryInputRound3Stop(t, al, child.SessionID)
				stopped := awaitSteeringRepairStopped(t, al, child.SessionID, child.Generation)
				al.steerAdmission().turns.Wait()
				require.Equal(t, session.LifecycleStopped, stopped.State)
				require.Nil(t, stopped.Stop, "SETUP: a stop fence in flight is not a landed stop")
				require.NotNil(t, stopped.StopNote, "SETUP: real Stop retains its lasting note")
				require.Zero(t, recorder.calls.Load(), "queued helper was stopped before its first model call")
				require.Zero(t, al.steerAdmission().activeCount())
				require.Zero(t, al.steerAdmission().queueLen())
				memoryInputRound3AssertSetupOutbound(t, al)
				msg := memoryInputRound3HelperMessage(child.SessionID, scope.workspace)
				before := memoryInputRound3Capture(t, al, recorder, child.SessionID, agentSessionKey(testDefaultAgentID, msg))
				parentBefore := memoryInputRound3Capture(t, al, recorder, parentID)
				if entry == "Public" {
					require.True(t, al.deliverHumanHelperInput(msg))
				} else {
					err := al.enqueueHelperSteeringFromMessage(msg, testDefaultAgentID, false)
					if scope.foreign {
						require.ErrorIs(t, err, session.ErrWorkspaceImmutable, "F02: B must be refused before reviving a stopped A helper")
						require.Equal(t, memoryInputRound3Refusal, userVisibleTurnError(err))
					} else {
						require.NoError(t, err)
					}
				}
				if scope.foreign {
					if entry == "Public" {
						memoryInputRound3AssertRefusal(t, al, msg)
					} else {
						require.Empty(t, memoryInputRound3Outbound(t, al))
					}
					memoryInputRound3AssertUnchanged(t, before, memoryInputRound3Capture(t, al, recorder, child.SessionID, agentSessionKey(testDefaultAgentID, msg)))
					memoryInputRound3AssertUnchanged(t, parentBefore, memoryInputRound3Capture(t, al, recorder, parentID))
					return
				}
				memoryInputRound3AssertRevived(t, al, recorder, held, child.SessionID, before, msg)
			})
		}
	}
}

func memoryInputRound3AssertRevived(
	t *testing.T, al *AgentLoop, recorder *memoryInputRound3Provider, held *memoryInputRound3HeldProvider,
	sessionID string, before memoryInputRound3Snapshot, msg bus.InboundMessage,
) {
	t.Helper()
	select {
	case <-held.entered:
	case <-time.After(asyncTaskPollTimeout):
		t.Fatal("same-A stopped-helper control never reached the real dispatched provider")
	}
	rec, err := al.GetSessionLifecycleStore().Load(sessionID)
	require.NoError(t, err)
	require.NotNil(t, rec)
	require.Equal(t, session.LifecycleRunning, rec.State, "same-A input actually revived and admitted the helper")
	require.Equal(t, before.lifecycle.Generation, rec.Generation, "a current-generation stopped resume retains its generation")
	require.Nil(t, rec.Stop)
	require.Nil(t, rec.StopNote)
	require.NotNil(t, rec.ExecutionID, "real revival/dispatch minted an execution identity")
	require.NotEqual(t, before.lifecycle.ExecutionID, rec.ExecutionID)
	require.NotEmpty(t, rec.ExecutionID.RunID)
	require.Equal(t, al.bootEpochFor(), rec.ExecutionID.BootSeq)
	require.EqualValues(t, 1, recorder.calls.Load())
	require.Contains(t, recorder.receivedContents(), msg.Content, "revival runs the new A instruction, not the old launch prompt")
	require.Empty(t, memoryInputRound3Outbound(t, al), "same-A revival input is not refused")
	held.releaseNow()
	al.steerAdmission().turns.Wait()
	require.Nil(t, al.getActiveTurnState(sessionID), "full revived owner tail has exited")
	require.Zero(t, al.steerAdmission().activeCount(), "revived admission released its slot")
	require.Zero(t, al.steerAdmission().queueLen())
	require.EqualValues(t, 1, recorder.calls.Load(), "the accepted instruction caused one real no-tool turn")
	archive, err := al.GetSessionStore().ReadArchive(context.Background(), sessionID)
	require.NoError(t, err)
	var pairs [][2]string
	for _, message := range archive {
		pairs = append(pairs, [2]string{message.Role, message.Content})
	}
	require.Contains(t, pairs, [2]string{"user", msg.Content}, "resumed instruction was durably recorded")
	require.Contains(t, pairs, [2]string{"assistant", memoryInputRound3Response}, "real resumed turn wrote its exact provider response")
}
