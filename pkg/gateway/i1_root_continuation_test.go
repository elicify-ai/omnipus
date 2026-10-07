package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func i1ContinueHumanAndDelegate(t *testing.T, al *agent.AgentLoop, stg *setupAndStartServicesState, p *uatD2bFinalProvider, msgBus *bus.MessageBus, root string, crashed *session.LifecycleRecord, prior []session.TranscriptEntry) {
	t.Helper()
	h := newWSHandler(msgBus, al, "")
	msgBus.SetStreamDelegate(h)
	wc, _ := makeForwarderTestConn(256)
	meta, err := al.GetSessionStore().GetMeta(root)
	require.NoError(t, err)
	wc.userID = meta.Owner
	ctx, cancel := context.WithCancel(context.Background())
	var releaseOnce, closeOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(p.release) }) }
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(ctx) }()
	finish := func() { closeOnce.Do(func() { release(); cancel(); al.Close() }) }
	t.Cleanup(func() { finish(); <-runDone })
	reader := &wsHandlerReadLoop{h: h, wc: wc, ctx: ctx, chatID: "i1-continued-chat"}
	const prompt = "Continue this recovered conversation exactly once."
	frame := generated.MessageFrame{Type: string(generated.WsFrameTypeMessage), SessionId: &root, Content: prompt, AgentId: &crashed.AgentID, Metadata: map[string]any{"workspace_id": crashed.WorkspaceID}}
	raw, err := json.Marshal(frame)
	require.NoError(t, err)
	require.Equal(t, wsHandlerReadLoopNext, reader.dispatchFrame(raw, wsTypeOnly{Type: string(generated.WsFrameTypeMessage)}))
	select {
	case <-p.entered:
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatal("I1: new human message never entered the provider")
	}
	live, err := stg.lifecycleStore.Load(root)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleRunning, live.State)
	assert.Equal(t, crashed.Generation, live.Generation, "explicit continuation of stopped(restart) uses the SAME generation")
	require.NotNil(t, live.ExecutionID)
	assert.NotEqual(t, crashed.ExecutionID.RunID, live.ExecutionID.RunID, "human input owns a fresh execution")
	assert.Equal(t, stg.bootEpoch.Current(), live.ExecutionID.BootSeq)
	assert.Nil(t, live.StopNote, "explicit human Resume clears the current restart note")

	// Reachability: use the actual registered product delegate tool, not a
	// launcher stub, while the resumed human execution owns the root.
	inst, ok := al.GetRegistry().GetAgent(crashed.AgentID)
	require.True(t, ok)
	registered, ok := inst.Tools.Get("delegate")
	require.True(t, ok, "real resumed agent must have registered delegation")
	dt, ok := registered.(*tools.DelegateTool)
	require.True(t, ok)
	toolCtx := tools.WithTranscriptSessionID(tools.WithAgentID(tools.WithWorkspaceID(ctx, crashed.WorkspaceID), crashed.AgentID), root)
	toolCtx = tools.WithToolCallID(toolCtx, "i1-real-delegate")
	result := dt.Execute(toolCtx, map[string]any{"action": "run", "agent_id": crashed.AgentID, "task": "Check the resumed conversation's helper work", "label": "I1 helper"})
	require.NotNil(t, result)
	require.False(t, result.IsError, "I1: registered delegation after restart refused: %s", result.ForLLM)
	select {
	case <-p.entered:
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatal("I1: real delegated helper did not execute")
	}
	children, err := stg.lifecycleStore.List(session.LifecycleFilter{SteeringSessionID: root})
	require.NoError(t, err)
	require.Len(t, children, 1, "one registered delegate action must create one actual helper")
	require.Equal(t, session.LifecycleRunning, children[0].State)
	require.NotNil(t, children[0].ExecutionID)
	res, err := al.StopSession(ctx, agent.StopRequest{SessionID: children[0].SessionID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: meta.Owner}, Channel: "webchat"})
	require.NoError(t, err)
	require.NoError(t, res.RootErr)
	require.Empty(t, al.AwaitStoppedTurns(ctx, []string{children[0].SessionID}), "join actual helper cleanup before continuing the root")
	release()
	select {
	case answer := <-msgBus.OutboundChan():
		assert.Equal(t, root, answer.SessionID)
		assert.Equal(t, uatD2bFinalAnswer, answer.Content)
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatal("I1: resumed human turn produced no final answer")
	}
	finish() // Join the actual worker and its disposition tail before reading.
	entries, err := al.GetSessionStore().ReadTranscript(root)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entries), len(prior))
	assert.Equal(t, prior, entries[:len(prior)], "crash history survives continuation and delegation")
	var human, answers []string
	for _, e := range entries[len(prior):] {
		if e.Role == "user" {
			human = append(human, e.Content)
		}
		if e.Role == "assistant" && e.Content != "" {
			answers = append(answers, e.Content)
		}
	}
	assert.Equal(t, []string{prompt}, human, "one human send is saved exactly once")
	assert.Equal(t, []string{uatD2bFinalAnswer}, answers, "one resumed human turn answers exactly once")
	journal, err := os.ReadFile(filepath.Join(stg.lifecycleStore.Dir(), root+".jsonl"))
	require.NoError(t, err)
	runs := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(journal)), "\n") {
		var r session.LifecycleRecord
		require.NoError(t, json.Unmarshal([]byte(line), &r))
		if r.ExecutionID != nil && r.ExecutionID.RunID != crashed.ExecutionID.RunID {
			runs[r.ExecutionID.RunID] = true
		}
	}
	assert.Equal(t, map[string]bool{live.ExecutionID.RunID: true}, runs, "exactly one fresh root execution, never boot replay or double admission")
	t.Logf("I1 continuation: same generation=%d; one fresh root run; one answer; registered helper executed", live.Generation)
}
