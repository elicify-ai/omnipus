package gateway

import (
	"bufio"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1899 Q1=B, real-driver control for the agent-package human-input test:
// checking readiness on a capable fake alone could go green while the real
// OpencodeDriver still cannot report its captured native id. This test never
// names a proposed readiness method. It drives actual Run/Resume processes
// through the shared external-steer entry point, so any valid design works.
func TestOpenCodeFix1899_RealOpenCodeDriverPreservesStartingProcessAndConversation(t *testing.T) {
	program := opencodeFix1895Program(t)
	al, child, _, process := opencodeFix1895Launch(t, program, "startup_followup")
	// The external program is connected but has not emitted any event or id.
	_, err := al.DeliverExternalCLIInstruction(context.Background(), child.SessionID, opencodeFix1895Worker,
		providers.Message{Role: "user", Content: "real startup follow-up 1899"}, "real-startup-1899")
	require.NoError(t, err, "Q1=B accepts and queues startup input rather than refusing it")
	require.NoError(t, process.SetDeadline(time.Now().Add(5*time.Second)), "deadlock failsafe for the process liveness probe")
	_, writeErr := fmt.Fprintln(process, "probe")
	assert.NoError(t, writeErr, "#1899: accepted startup input must not kill the original CLI process")
	if writeErr == nil {
		reply, readErr := bufio.NewReader(process).ReadString('\n')
		assert.NoError(t, readErr, "#1899: the real OpenCode process must remain alive, without a native id, after intake")
		if readErr == nil {
			assert.Equal(t, "still-starting-without-native-id\n", reply)
		}
	}
	opencodeFix1895Finish(t, al, child, process)
	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleCompleted, rec.State, "#1899: the real driver must finish and resume the original conversation")
	entries, err := al.ResolveSessionStore(child.SessionID).ReadTranscript(child.SessionID)
	require.NoError(t, err)
	answers := map[string]int{}
	for _, entry := range entries {
		if entry.Role == "assistant" {
			answers[entry.Content]++
		}
		assert.NotContains(t, entry.Content, "A queued follow-up message could not be processed", "startup input must not be abandoned")
	}
	assert.Equal(t, 1, answers["external helper answer 1895"], "the initial run must finish its original work")
	// The process fixture emits this only after validating --session uses the
	// originally emitted native id and the prompt contains only the new input.
	assert.Equal(t, 1, answers["real startup follow-up delivered 1899"], "deliver the queued input to that same conversation exactly once")
}
