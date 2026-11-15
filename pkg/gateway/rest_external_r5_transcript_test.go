package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The lifecycle terminal commit precedes publication. Wait for the publisher's
// durable inbox-append receipt, not merely terminal/no-active-turn. This is a
// readiness barrier only: callers still count raw inbox messages, including
// acknowledged ones, independently of this receipt.
func opencode226R5WaitForParentDelivery(t *testing.T, api *restAPI, parent string, child steer.LaunchResult) {
	t.Helper()
	store := api.agentLoop.GetSessionLifecycleStore()
	require.Eventually(t, func() bool {
		record, err := store.Load(child.SessionID)
		if err != nil || record.FinalDelivery == nil {
			return false
		}
		commit := record.FinalDelivery
		if commit.Generation != child.Generation || commit.ParentSessionID != parent {
			return false
		}
		progress, _, _, readErr := store.FinalDeliveryState(child.SessionID, child.Generation, commit.CommitID)
		return readErr == nil && progress.InboxAppended
	}, 10*time.Second, 5*time.Millisecond, "final hand-back must actually be published after the terminal commit")
}

type opencode226R5DriverAnswerCase struct {
	cli      string
	fixture  string
	texts    []string
	answer   string
	parse    func([]byte, string, int) []runner.RunEvent
	firstArg string
	version  string
}

func TestOpencode226R5_DC_ClaudeFinalAnswerStoredAndHandedBackOnce(t *testing.T) {
	// Fixture has ONE success result (Done.), preceded by distinct narration
	// and a tool-use record. Narration is not a duplicate final answer.
	opencode226R5AssertDriverAnswerOnce(t, opencode226R5DriverAnswerCase{
		cli: "claude-code", fixture: "claude_stream_json.ndjson",
		texts: []string{"I'll help you with that task.", "Done."}, answer: "Done.",
		parse: runner.ParseClaudeStreamJSON, firstArg: "-p", version: "Claude Code 2.0.0",
	})
}

func TestOpencode226R5_DC_CodexFinalAnswerStoredAndHandedBackOnce(t *testing.T) {
	// Fixture has ONE agent_message, plus a tool-call and turn completion.
	opencode226R5AssertDriverAnswerOnce(t, opencode226R5DriverAnswerCase{
		cli: "codex", fixture: "codex_stream_json.ndjson",
		texts: []string{"Processing your request."}, answer: "Processing your request.",
		parse: runner.ParseCodexStreamJSON, firstArg: "--ask-for-approval", version: "codex-cli 0.99.0",
	})
}

func opencode226R5AssertDriverAnswerOnce(t *testing.T, tc opencode226R5DriverAnswerCase) {
	t.Helper()
	capture, err := os.ReadFile(filepath.Join("..", "agent", "runner", "testdata", "fixtures", tc.fixture))
	require.NoError(t, err, "use the complete existing driver fixture, not invented parser output")
	t.Run("real_parser_control", func(t *testing.T) {
		var outputs []string
		// The fixtures contain one completed turn; two is a non-binding cap.
		for _, event := range tc.parse(capture, "oc226-r5-"+tc.cli, 2) {
			require.NotEqual(t, runner.EventKindError, event.Kind, "fixture must parse without an error event")
			if event.Kind == runner.EventKindOutput {
				require.NotNil(t, event.Output)
				outputs = append(outputs, event.Output.Text)
			}
		}
		require.Equal(t, tc.texts, outputs, "real parser must preserve exactly the fixture's text records")
	})

	api, id := opencode226R5WorkerAPIForCLI(t, tc.cli)
	fixture := opencode226R5NewStreamProcessFixture(t, tc.version, capture)
	opencode226R5SaveProgramForCLI(t, api, tc.cli, fixture.program)
	parent := opencode226R5Parent(t, api)
	child := opencode226R5Dispatch(t, api, parent, id, "r5-one-final-answer-"+tc.cli)
	decoder, conn, observation := fixture.accept(t)
	require.NotEmpty(t, observation.Args)
	assert.Equal(t, tc.firstArg, observation.Args[0], "instrument: the selected real driver must invoke the fixture")
	opencode226R5FinishProcess(t, decoder, conn, observation)
	opencode226R5AssertCompleted(t, api, child)
	require.Equal(t, int32(1), fixture.runs.Load(), "instrument: exactly one process replayed the fixture")

	t.Run("helper_transcript", func(t *testing.T) {
		entries, readErr := api.agentLoop.GetSessionStore().ReadTranscript(child.SessionID)
		require.NoError(t, readErr)
		for _, text := range tc.texts {
			var standalone []string
			occurrences := 0
			for _, entry := range entries {
				if entry.Role != "assistant" {
					continue
				}
				if entry.Content == text {
					standalone = append(standalone, entry.Content)
				}
				occurrences += strings.Count(entry.Content, text)
			}
			// Check both forms: a missing standalone answer is not rescued by
			// an aggregate, and an extra aggregate must not hide beside it.
			assert.Equal(t, []string{text}, standalone, "%s: fixture text %q must be stored as exactly one assistant message", tc.cli, text)
			assert.Equal(t, 1, occurrences, "%s: fixture text %q must occur once across ALL assistant messages, including aggregates", tc.cli, text)
		}
	})
	t.Run("parent_handback", func(t *testing.T) {
		opencode226R5WaitForParentDelivery(t, api, parent, child)
		entries, readErr := api.agentLoop.GetMessageInboxStore().Entries(parent)
		require.NoError(t, readErr)
		var handbacks []gen.SessionMessageHandback
		for _, entry := range entries {
			if entry.Kind != session.InboxEntryMessage {
				continue // ack records do not erase their original message
			}
			require.NotNil(t, entry.Message)
			handback, decodeErr := entry.Message.AsSessionMessageHandback()
			require.NoError(t, decodeErr)
			handbacks = append(handbacks, handback)
		}
		require.Len(t, handbacks, 1, "%s: exactly one raw final hand-back, even if already acknowledged", tc.cli)
		assert.Equal(t, child.SessionID, handbacks[0].SessionId)
		assert.Equal(t, gen.SessionMessageHandbackDirectionChildToParent, handbacks[0].Direction)
		assert.Equal(t, gen.SessionMessageHandbackModeFinal, handbacks[0].Mode)
		assert.Equal(t, 1, strings.Count(handbacks[0].ResultSoFar, tc.answer), "%s: parent receives the fixture's final answer exactly once", tc.cli)
		for _, text := range tc.texts {
			assert.Equal(t, 1, strings.Count(handbacks[0].ResultSoFar, text), "%s: parent text must not duplicate any fixture text", tc.cli)
		}
		if tc.cli == "codex" {
			assert.Equal(t, tc.answer, handbacks[0].ResultSoFar, "Codex fixture has only one text record, so its entire hand-back is specified")
		}
	})
}
