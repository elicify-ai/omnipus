package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const opencodeFix1895Worker = "worker-1895"

// not-wire-format: observations decoded using generated frame types only.
type opencodeFix1895Frames struct {
	tokens []gen.TokenFrame
	done   []gen.DoneFrame
	errors []gen.ErrorFrame
	tools  int
	raw    [][]byte
}

// opencodeFix1895Launch drives the real launcher/runner and real WS streamer /
// session hub. Only the external program is a process-edge fixture. Both
// subscribers bind before dispatch, as two people viewing the helper chat do.
func opencodeFix1895Launch(t *testing.T, program, mode string, configure ...func(*config.Config, *bus.MessageBus)) (*agent.AgentLoop, steer.LaunchResult, [2]*wsConn, net.Conn) {
	t.Helper()
	addr, awaitProcess := opencodeFix1895Gate(t)
	home := t.TempDir()
	worker := config.AgentConfig{ID: opencodeFix1895Worker, Home: filepath.Join(home, "worker")}
	if mode != "native" {
		worker.Subagents = &config.SubagentsConfig{Executor: &config.ExecutorConfig{
			Kind: config.ExecutorKindExternalCLI, CLI: "opencode",
			CLIArgs: "--fixture-address=" + addr + " --fixture-mode=" + mode,
		}}
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: home, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxToolIterations: 10},
			List:     []config.AgentConfig{{ID: "mia", Home: filepath.Join(home, "mia")}, worker},
		},
		Sandbox: config.OmnipusSandboxConfig{ThirdPartyAgents: &config.ThirdPartyAgentsConfig{
			CLIs: map[string]config.ThirdPartyCLIConfig{"opencode": {Path: program}},
		}},
	}
	messages := bus.NewMessageBus()
	t.Cleanup(messages.Close)
	for _, setup := range configure {
		setup(cfg, messages)
	}
	al := mustAgentLoop(t, cfg, messages, &opencodeFix1895NativeProvider{})
	classifier := agent.NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())
	al.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(classifier), steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())
	h := newWSHandler(messages, al, "")
	messages.SetStreamDelegate(h)
	t.Cleanup(h.Wait)
	store := al.GetSessionStore()
	parent, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	workspaceID := testHarnessWorkspaceMembershipID
	require.NoError(t, store.SetMeta(parent.ID, session.MetaPatch{WorkspaceID: &workspaceID}))
	launcher := agent.NewSteerLauncher(al)
	child, err := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parent.ID, TargetAgentID: opencodeFix1895Worker,
		Task: "answer in the helper's own chat", Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-1895"},
	})
	require.NoError(t, err)
	viewers := [2]*wsConn{makeTestConn(), makeTestConn()}
	for i, viewer := range viewers {
		bindTestConnToSession(h, fmt.Sprintf("viewer-1895-%d", i), child.SessionID, viewer)
	}
	_, err = launcher.Dispatch(context.Background(), child.SessionID, child.Generation)
	require.NoError(t, err)
	if mode == "native" {
		return al, child, viewers, nil
	}
	return al, child, viewers, awaitProcess()
}

type opencodeFix1895NativeProvider struct{}

func (*opencodeFix1895NativeProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "native helper answer 1895"}, nil
}
func (*opencodeFix1895NativeProvider) GetDefaultModel() string { return "test-model" }

func opencodeFix1895Collect(t *testing.T, viewer *wsConn, sessionID string) opencodeFix1895Frames {
	t.Helper()
	var frames opencodeFix1895Frames
	for {
		select {
		case raw := <-viewer.sendCh:
			var envelope map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &envelope))
			var kind, sid string
			require.NoError(t, json.Unmarshal(envelope["type"], &kind))
			require.NoError(t, json.Unmarshal(envelope["session_id"], &sid))
			// helper_event and goal/loop status are intentionally broadcast to
			// other chats (hubBroadcastWithSequencedCopy). Only conversation
			// frames under test must be addressed to this helper session.
			switch kind {
			case "token", "done", "error", "tool_call_start", "tool_call_result":
				require.Equal(t, sessionID, sid, "conversation frames must belong to this helper, not the parent")
				frames.raw = append(frames.raw, raw)
			}
			switch kind {
			case "token":
				var token gen.TokenFrame
				require.NoError(t, json.Unmarshal(raw, &token))
				frames.tokens = append(frames.tokens, token)
			case "done":
				var done gen.DoneFrame
				require.NoError(t, json.Unmarshal(raw, &done))
				frames.done = append(frames.done, done)
			case "error":
				var failure gen.ErrorFrame
				require.NoError(t, json.Unmarshal(raw, &failure))
				frames.errors = append(frames.errors, failure)
			case "tool_call_start", "tool_call_result":
				frames.tools++
			}
		default:
			return frames
		}
	}
}

func opencodeFix1895Finish(t *testing.T, al *agent.AgentLoop, child steer.LaunchResult, process net.Conn) {
	t.Helper()
	if process != nil {
		_, err := fmt.Fprintln(process, "finish")
		require.NoError(t, err)
	}
	// Closing intake before the native provider call would cancel that call.
	// Wait for real lifecycle completion first, then Close JOINs the complete
	// dispatch/continuation tail. The timeout is only a deadlock failsafe.
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
		if assert.NoError(collect, err) {
			assert.True(collect, rec.Terminal(), "the real helper must finish before its frames are inspected; state=%s", rec.State)
		}
	}, 10*time.Second, 10*time.Millisecond)
	al.Close()
}

// PIN instrument: a native helper really produces these ordinary frame types
// on the same two-viewer harness. The external cases must match this contract,
// not fake frames generated by a recording Streamer test double.
func TestOpenCodeFix1895_NativeHelperTokenAndDoneFrameControl(t *testing.T) {
	al, child, viewers, process := opencodeFix1895Launch(t, "", "native")
	opencodeFix1895Finish(t, al, child, process)
	entries, err := al.ResolveSessionStore(child.SessionID).ReadTranscript(child.SessionID)
	require.NoError(t, err)
	for _, viewer := range viewers {
		opencodeFix1895AssertAnswer(t, opencodeFix1895Collect(t, viewer, child.SessionID), entries, "native helper answer 1895")
	}
}

func opencodeFix1895AssertAnswer(t *testing.T, frames opencodeFix1895Frames, entries []session.TranscriptEntry, answer string) {
	t.Helper()
	var saved []session.TranscriptEntry
	for _, entry := range entries {
		if entry.Role == "assistant" && entry.Content == answer {
			saved = append(saved, entry)
		}
	}
	require.Len(t, saved, 1, "#1895: finalize must not append the external answer a second time")
	var live string
	for _, token := range frames.tokens {
		live += token.Content
		assert.Equal(t, &saved[0].ID, token.MessageId, "#1895: live text must reuse the saved row id so reload cannot duplicate it")
		assert.Equal(t, &saved[0].TurnID, token.TurnId, "live token must belong to the producing turn")
	}
	assert.Equal(t, answer, live, "#1895: the final answer must reach a helper WebSocket subscriber without reload")
	if assert.Len(t, frames.done, 1, "#1895: ordinary done frame must close this helper turn exactly once") {
		assert.Equal(t, &saved[0].ID, frames.done[0].MessageId)
		assert.Equal(t, &saved[0].TurnID, frames.done[0].TurnId)
		if frames.done[0].Stats != nil && frames.done[0].Stats.TurnFailed != nil {
			assert.False(t, *frames.done[0].Stats.TurnFailed)
		}
	}
	assert.Empty(t, frames.errors, "a successful helper must not receive a terminal error frame")
}

// #1895 / ADR-091 D1: external helper text uses the native token/done path.
func TestOpenCodeFix1895_ExternalHelperAnswerFramesReuseSavedRowID(t *testing.T) {
	program := opencodeFix1895Program(t)
	al, child, viewers, process := opencodeFix1895Launch(t, program, "success")
	opencodeFix1895Finish(t, al, child, process)
	entries, err := al.ResolveSessionStore(child.SessionID).ReadTranscript(child.SessionID)
	require.NoError(t, err)
	var observations [2]opencodeFix1895Frames
	for i, viewer := range viewers {
		observations[i] = opencodeFix1895Collect(t, viewer, child.SessionID)
		require.Equal(t, 2, observations[i].tools, "instrument: the real CLI tool-call frames reached this subscribed viewer")
		opencodeFix1895AssertAnswer(t, observations[i], entries, "external helper answer 1895")
	}
	assert.Equal(t, observations[0].raw, observations[1].raw, "all helper subscribers receive the same sequenced bytes")
}

// #1895: runner fatal events AND failures returned before the runner event
// stream exists must each publish a terminal error and failed done. The latter
// is a real no-native-id Resume refusal, not an error event injected into a hub.
func TestOpenCodeFix1895_ExternalHelperTerminalErrorFrames(t *testing.T) {
	program := opencodeFix1895Program(t)
	for _, mode := range []string{"runner_error", "resume_refusal"} {
		t.Run(mode, func(t *testing.T) {
			al, child, viewers, process := opencodeFix1895Launch(t, program, mode)
			if mode == "resume_refusal" {
				_, _, err := al.EnqueueSteeringMessageWithStatus(child.SessionID, opencodeFix1895Worker,
					providers.Message{Role: "user", Content: "follow-up whose resume must visibly refuse"}, "frame-refusal-1895")
				require.NoError(t, err)
			}
			opencodeFix1895Finish(t, al, child, process)
			rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
			require.NoError(t, err)
			require.Equal(t, session.LifecycleFailed, rec.State, "precondition: exercised an actually failed helper")
			for _, viewer := range viewers {
				frames := opencodeFix1895Collect(t, viewer, child.SessionID)
				require.Equal(t, 2, frames.tools, "instrument: helper subscribers still receive real tool frames")
				if assert.Len(t, frames.errors, 1, "#1895: terminal CLI failure must reach the helper's WebSocket subscribers") {
					assert.Equal(t, &child.SessionID, frames.errors[0].SessionId)
					assert.NotEmpty(t, frames.errors[0].Message, "the terminal error must be visible, not an empty frame")
					require.NotNil(t, frames.errors[0].Payload, "match the native structured error frame")
					assert.NotEmpty(t, frames.errors[0].Payload.LlmError.Code)
				}
				var failedDone []gen.DoneFrame
				for _, done := range frames.done {
					if done.Stats != nil && done.Stats.TurnFailed != nil && *done.Stats.TurnFailed {
						failedDone = append(failedDone, done)
					}
				}
				assert.Len(t, failedDone, 1, "#1895: one ordinary failed done must close the failed helper turn, including a pre-stream resume refusal")
			}
		})
	}
}
