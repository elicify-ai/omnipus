package gateway

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const opencodeR5Client = "r5-refused-task-input"
const opencodeR5Input = "R5 person input: this message must not be admitted"

// not-wire-format: observations decoded from the existing generated carriers.
type opencodeR5VisibleNotice struct {
	text, sessionID, clientID string
	nonTerminal               bool
}

func opencodeR5Send(t *testing.T, f *opencodeR4GWFixture, sid, workspaceID string, explicit bool) {
	t.Helper()
	clientID := opencodeR5Client
	frame := generated.MessageFrame{
		Type: string(generated.WsFrameTypeMessage), Content: opencodeR5Input,
		SessionId: &sid, ClientMessageId: &clientID,
		Metadata: map[string]any{"workspace_id": workspaceID},
	}
	if explicit {
		worker := opencodeR4GWWorker
		frame.AgentId = &worker
	}
	opencodeR4GatewaySend(t, f.handler, f.conn, "r4-worker-chat", frame)
}

func opencodeR5Visible(t *testing.T, f *opencodeR4GWFixture) ([]opencodeR5VisibleNotice, []string) {
	t.Helper()
	var visible []opencodeR5VisibleNotice
	var types []string
	for {
		select {
		case raw := <-f.conn.sendCh:
			var envelope wsTypeOnly
			require.NoError(t, json.Unmarshal(raw, &envelope))
			types = append(types, envelope.Type)
			switch envelope.Type {
			case string(generated.WsFrameTypeError):
				var frame generated.ErrorFrame
				require.NoError(t, json.Unmarshal(raw, &frame))
				n := opencodeR5VisibleNotice{text: frame.Message}
				if frame.SessionId != nil {
					n.sessionID = *frame.SessionId
				}
				if frame.ClientMessageId != nil {
					n.clientID = *frame.ClientMessageId
				}
				n.nonTerminal = n.sessionID != "" && n.clientID != "" && frame.FirstMessageError == nil
				visible = append(visible, n)
			case string(generated.WsFrameTypeReplayMessage):
				var frame generated.ReplayMessageFrame
				require.NoError(t, json.Unmarshal(raw, &frame))
				if frame.Role == "system" {
					visible = append(visible, opencodeR5VisibleNotice{text: frame.Content,
						sessionID: frame.SessionId, nonTerminal: true})
				}
			}
		default:
			return visible, types // Intake and the observed publication are synchronous.
		}
	}
}

// Fault the real journal, not the entire store: metadata/transcript remain usable.
func opencodeR5FaultLifecycle(t *testing.T, f *opencodeR4GWFixture) (func(), error) {
	t.Helper()
	path := filepath.Join(f.handler.agentLoop.GetSessionLifecycleStore().Dir(), f.sid+".jsonl")
	backup := filepath.Join(t.TempDir(), "lifecycle.saved")
	require.NoError(t, os.Rename(path, backup))
	require.NoError(t, os.Mkdir(path, 0o700))
	var once sync.Once
	restore := func() {
		once.Do(func() {
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.Rename(backup, path))
		})
	}
	t.Cleanup(restore)
	_, err := f.handler.agentLoop.GetSessionLifecycleStore().Load(f.sid)
	var pathErr *os.PathError
	require.ErrorAs(t, err, &pathErr, "instrument: actual lifecycle Load must fail")
	require.Equal(t, path, pathErr.Path)
	_, metaErr := f.handler.agentLoop.ResolveSessionStore(f.sid).GetMeta(f.sid)
	require.NoError(t, metaErr, "instrument: session lookup still works during lifecycle-only fault")
	return restore, err
}

func opencodeR5CaptureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	previous := slog.Default()
	var output bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	logsafeError("r5-storage-log-control", "control", "r5-observed-logger")
	require.Contains(t, output.String(), "r5-observed-logger", "instrument: real gateway logger reaches the observer")
	return &output
}

func opencodeR5AssertCauseLogged(t *testing.T, output *bytes.Buffer, cause error) {
	t.Helper()
	var found bool
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var row map[string]any // not-wire-format: structured server-log observation.
		require.NoError(t, json.Unmarshal([]byte(line), &row))
		text, _ := row["error"].(string)
		if row["level"] == "ERROR" && strings.Contains(text, cause.Error()) {
			found = true
		}
	}
	assert.True(t, found, "SFH-R5: preserve the full storage cause in an error-level log; cause=%q logs=%s", cause, output.String())
}

// Literal oracle: prompt-r4/TEXT.md section 4, external-task-chat variant.
// Neither the refused task input nor the refusal was saved; this is not a
// catalogue sentence, and must not use the helper-chat variant.
func opencodeR5AssertUnsavedWarning(t *testing.T, text string) {
	t.Helper()
	const want = "I couldn't save your message or the refusal above, so both may be missing after a reload. Copy them before reloading."
	assert.Equal(t, want, text, "SFH-R5-1: show the exact approved task-chat save warning separately")
}

func opencodeR5ErrorRows(entries []session.TranscriptEntry) []session.TranscriptEntry {
	var rows []session.TranscriptEntry
	for _, entry := range entries {
		if entry.Type == session.EntryTypeSystem && entry.Status == "error" {
			rows = append(rows, entry)
		}
	}
	return rows
}
