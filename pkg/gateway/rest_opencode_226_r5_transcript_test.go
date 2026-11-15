package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// D-C: count oracle is the REAL capture (step_start, ONE text, step_finish),
// not observed production behavior. The UAT transcript's two identical answers
// and doubled helper-chat rendering motivated this regression; neither the
// parser nor transcript/completion/inbox is mocked in the actual delegation.
func TestOpencode226R5_DC_OneTextIsStoredAndHandedBackOnce(t *testing.T) {
	capture, err := os.ReadFile(filepath.Join("..", "agent", "runner", "testdata", "opencode-2.2.6", "plain.stdout.ndjson"))
	require.NoError(t, err)
	var capturedAnswers []string
	for _, line := range strings.Split(strings.TrimSpace(string(capture)), "\n") {
		var event map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(line), &event))
		if string(event["type"]) == `"text"` {
			var part map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(event["part"], &part))
			var text string
			require.NoError(t, json.Unmarshal(part["text"], &text))
			capturedAnswers = append(capturedAnswers, text)
		}
	}
	require.Equal(t, []string{"CAPTURE-OK"}, capturedAnswers, "instrument: the real capture independently contains exactly one answer")
	t.Run("driver_single_output_control", func(t *testing.T) {
		var outputs []string
		for _, event := range runner.ParseOpencodeStreamJSON(capture, "oc226-r5-dc-control", 2) {
			if event.Kind == runner.EventKindOutput {
				require.NotNil(t, event.Output)
				outputs = append(outputs, event.Output.Text)
			}
		}
		require.Equal(t, []string{"CAPTURE-OK"}, outputs, "the input/driver must not already contain duplicate text events")
	})

	api, id := opencode226R5WorkerAPI(t)
	fixture := opencode226R5NewProcessFixture(t)
	opencode226R5SaveProgram(t, api, fixture.program)
	parent := opencode226R5Parent(t, api)
	child := opencode226R5Dispatch(t, api, parent, id, "r5-one-captured-answer")
	decoder, conn, observation := fixture.accept(t)
	opencode226R5FinishProcess(t, decoder, conn, observation)
	opencode226R5AssertCompleted(t, api, child)
	require.Equal(t, int32(1), fixture.runs.Load(), "instrument: exactly one real process replayed the one-text capture")

	t.Run("helper_transcript", func(t *testing.T) {
		entries, readErr := api.agentLoop.GetSessionStore().ReadTranscript(child.SessionID)
		require.NoError(t, readErr)
		var answers []string
		for _, entry := range entries {
			if entry.Role == "assistant" && entry.Content == opencode226R5Answer {
				answers = append(answers, entry.Content)
			}
		}
		assert.Equal(t, []string{"CAPTURE-OK"}, answers,
			"D-C: ONE captured text event must produce exactly ONE stored assistant chat message in the helper session")
	})
	t.Run("parent_handback", func(t *testing.T) {
		entries, readErr := api.agentLoop.GetMessageInboxStore().Entries(parent)
		require.NoError(t, readErr)
		var handbacks []gen.SessionMessageHandback
		for _, entry := range entries {
			if entry.Kind != session.InboxEntryMessage {
				continue
			}
			require.NotNil(t, entry.Message)
			handback, decodeErr := entry.Message.AsSessionMessageHandback()
			require.NoError(t, decodeErr)
			handbacks = append(handbacks, handback)
		}
		require.Len(t, handbacks, 1, "D-C: one run must hand its answer back to the parent exactly once, including already-acked entries")
		assert.Equal(t, child.SessionID, handbacks[0].SessionId)
		assert.Equal(t, gen.SessionMessageHandbackDirectionChildToParent, handbacks[0].Direction)
		assert.Equal(t, gen.SessionMessageHandbackModeFinal, handbacks[0].Mode)
		assert.Equal(t, "CAPTURE-OK", handbacks[0].ResultSoFar, "D-C: hand-back must carry the answer once, not concatenate duplicate transcript copies")
	})
}
