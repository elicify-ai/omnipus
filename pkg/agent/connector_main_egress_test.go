package agent

// connector_main_egress_test.go — T-40, the #1507 publish sites
// (channels-spec-r8: T-40; US-8 AS1–AS3; FR-070; S-60 rows 1–5; S-61;
// SC-007; m-03; mutation list row "remove the #1507 guard at the media
// site → S-60 row 1 fails").
//
// Oracle (FR-070, normative): when mainConnectorTurn(ts.channel,
// ts.transcriptSessionID) is true, the turn MUST NOT publish to the
// connector at the five sites — tool-call feedback, async ForUser, tool
// media, sync ForUser, and publishOriginWarning for that session. At the
// media site the tool result is kept whole (Media, ArtifactTags —
// buildArtifactTags still runs — model attachments, persisted replay
// descriptors, IsError and the original result text unchanged) and exactly
// one line is appended to the model-facing result text:
//
//	Note: the file was not sent to <channel>: in a main conversation, files
//	stay in this conversation. Nothing was sent outside it.
//
// (the result is NOT replaced with ErrorResult, unlike the delivery-failure
// branch). The other sites log one info line `main connector tool output
// not sent` (tool, channel, session id; no text) and increment the existing
// refusal counter; transcript and model content are unchanged there.
//
// S-61 is the regression control: a NON-main session on the connector is
// unchanged — SendMedia still happens as today.
//
// Fixture: the real turn loop (ProcessScheduled / runTurn on a real MAIN
// session), a real channels.Manager with a fakeMediaChannel under
// "telegram", a real FileMediaStore, and the real bus — the same real
// components the media-workspace and steer-boundary lanes use. No publish
// site is faked.
//
// RED expectation: today no site carries the #1507 guard, so every
// non-publication assertion fails on an observed publish (sentMedia / bus
// outbound), and no note line or refusal log exists. The S-61 control
// PASSES today.

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

const t40Workspace = "ws-t40"
const t40RefusalLogLine = "main connector tool output not sent"

type t40Fixture struct {
	al         *AgentLoop
	bus        *bus.MessageBus
	ch         *fakeMediaChannel
	main       *session.UnifiedMeta
	store      *session.UnifiedStore
	agent      *AgentInstance
	mediaStore media.MediaStore
	imagePath  string
}

// t40NewFixture wires the real loop and a connector-fed MAIN session whose
// lifecycle record carries the connector origin (so the notifier's origin
// gate and the steering classifier see exactly what production sees for a
// connection-fed main). withManager attaches a real channels.Manager with a
// recording fake channel under "telegram" — needed only by the MEDIA rows,
// whose site calls SendMedia directly; the TEXT sites publish to the bus,
// and a running manager consumes bus outbound (that is its job), so text
// rows run manager-free and observe the bus itself. FR-070's guard lives in
// the loop, before the bus, so the bus is the guard's observation point.
func t40NewFixture(t *testing.T, provider providers.LLMProvider, toolFeedback, withManager bool) *t40Fixture {
	t.Helper()
	home := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              home,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
				ToolFeedback: config.ToolFeedbackConfig{
					Enabled:       toolFeedback,
					MaxArgsLength: 300,
				},
			},
			List: []config.AgentConfig{{ID: testDefaultAgentID, Home: home}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)
	mintGenuineBootEpochForLoop(t, al)

	lifecycle := session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
	inbox := session.NewMessageInboxStore(filepath.Join(home, "session_messages"))
	al.SetSessionMessagingStores(inbox, lifecycle)
	al.SetSteerAudienceDeps(
		NewSteerAudienceResolver(NewSteerRecordClassifier(lifecycle, al.GetSessionStore())),
		steer.NopBoundaryObserver{},
		NewSteerUpwardDeliverer(),
	)

	mediaStore := media.NewFileMediaStore()
	al.SetMediaStore(mediaStore)
	ch := &fakeMediaChannel{fakeChannel: fakeChannel{id: "rid-telegram"}}
	if withManager {
		al.SetChannelManager(newStartedTestChannelManager(t, msgBus, mediaStore, "telegram", ch))
	}

	agent, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok, "SETUP: agent must be registered")
	main, err := al.GetSessionStore().GetOrCreateMainSession(t40Workspace, testDefaultAgentID)
	require.NoError(t, err, "SETUP: main session must exist")
	// The connection-fed origin, as production stamps it for a channel-fed
	// session (OriginKindChannel; never delegate/task/verifier).
	require.NoError(t, lifecycle.Persist(&session.LifecycleRecord{
		SessionID:      main.ID,
		Generation:     1,
		State:          session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman,
		Origin:         &session.Origin{Kind: session.OriginKindChannel},
	}), "SETUP: connector-fed lifecycle record")

	imagePath := filepath.Join(home, "t40-screenshot.png")
	require.NoError(t, os.WriteFile(imagePath, []byte("t40 fake screenshot"), 0o644))

	return &t40Fixture{
		al: al, bus: msgBus, ch: ch, main: main,
		store: al.GetSessionStore(), agent: agent,
		mediaStore: mediaStore, imagePath: imagePath,
	}
}

// t40RunScheduledTurn drives a real scheduled-shaped turn on the connector-fed
// main (ProcessScheduled threads TranscriptSessionID, the entry the
// steer-boundary containment lane proved reaches every publication boundary).
func (f *t40Fixture) t40RunScheduledTurn(t *testing.T, content string) {
	t.Helper()
	_, err := f.al.ProcessScheduled(context.Background(), testDefaultAgentID, f.main.ID, content, "telegram", "c1")
	require.NoError(t, err)
}

// t40RunDirectTurn drives runTurn with a hand-built turnState — the only way
// to reach the sync-ForUser site, which needs SendResponse on (ProcessScheduled
// hardcodes it off).
func (f *t40Fixture) t40RunDirectTurn(t *testing.T, sendResponse bool) {
	t.Helper()
	ts := newTurnState(f.agent, processOptions{
		SessionKey:          "agent:" + testDefaultAgentID + ":telegram:c1",
		Channel:             "telegram",
		ChatID:              "c1",
		UserMessage:         "t40 direct request",
		DefaultResponse:     defaultResponse,
		SendResponse:        sendResponse,
		TranscriptSessionID: f.main.ID,
		TranscriptStore:     f.store,
		WorkspaceID:         t40Workspace,
	}, f.al.newTurnEventScope(f.agent.ID, f.main.ID))
	require.NoError(t, ts.windowError, "SETUP: healthy real window")
	stream := &wu22Stream{}
	ts.setLastStreamer(stream)
	f.al.bus.SetStreamDelegate(&identityStampStreamDelegate{streamer: stream})
	t.Cleanup(func() { f.al.bus.SetStreamDelegate(nil) })
	_, err := f.al.runTurn(context.Background(), ts)
	require.NoError(t, err)
}

// t40RefusalsDelta reads the existing refusal counter around a turn.
func t40RefusalsDelta(t *testing.T, run func()) int64 {
	t.Helper()
	before := connectorDefaultSendRefusals.Load()
	run()
	return connectorDefaultSendRefusals.Load() - before
}

// t40CaptureLog captures the process log as JSONL for the refusal-line
// oracle, restoring the previous default logger afterwards.
func t40CaptureLog(t *testing.T) func() []map[string]any {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "t40-log.jsonl")
	t.Setenv("OMNIPUS_LOG_FILE", logPath)
	previous := slog.Default()
	level := logger.GetLevel()
	logger.ConfigureFromEnv()
	return func() []map[string]any {
		slog.SetDefault(previous)
		logger.SetLevel(level)
		raw, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("instrument: refusal log %s unreadable: %v", logPath, err)
		}
		var out []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if line == "" {
				continue
			}
			var entry map[string]any
			if err := json.Unmarshal([]byte(line), &entry); err == nil {
				out = append(out, entry)
			}
		}
		return out
	}
}

func t40CountRefusalLines(entries []map[string]any) int {
	n := 0
	for _, e := range entries {
		if e["msg"] == t40RefusalLogLine {
			n++
		}
	}
	return n
}

// --- the five sites ---------------------------------------------------------

func TestT40_MainConnectorTurn_ToolMediaIsKeptAndNeverPublished(t *testing.T) {
	// S-60 row 1 (m-03): a tool returns media in a connection-fed main —
	// nothing reaches telegram (bus and SendMedia both checked); the result
	// is kept whole and exactly one note line is appended.
	f := t40NewFixture(t,
		testutil.NewScenario().WithToolCall("handled_media_tool", `{}`).WithText("Here is the screenshot."),
		false, true)
	allowProbeTool(t, f.al, &handledMediaTool{store: f.mediaStore, path: f.imagePath})

	f.t40RunScheduledTurn(t, "take a screenshot")

	require.Empty(t, f.ch.sentMedia,
		"S-60 row 1: media must not reach the connector via SendMedia")
	require.Empty(t, drainOutboundMedia(f.bus),
		"S-60 row 1: media must not reach the outbound media bus")
	require.Empty(t, drainOutboundText(f.bus),
		"S-60 row 1: nothing else may publish to the connector either")

	// The result is kept whole: original text, media refs, not an error,
	// and exactly one appended note line with the FR-070 text.
	entries, err := f.store.ReadTranscript(f.main.ID)
	require.NoError(t, err)
	toolTexts := 0
	for _, e := range entries {
		if e.Role != "tool" {
			continue
		}
		toolTexts++
		require.NotEqual(t, "error", e.Status,
			"S-60 row 1/m-03: the result must not be replaced with ErrorResult")
		require.Contains(t, e.Content, "Attachment delivered by tool.",
			"S-60 row 1/m-03: the original result text stays")
		require.Len(t, e.Attachments, 1,
			"S-60 row 1/m-03: the media descriptor is preserved for replay")
		note := "Note: the file was not sent to telegram: in a main conversation, files stay in this conversation. Nothing was sent outside it."
		require.Equal(t, 1, strings.Count(e.Content, note),
			"S-60 row 1/FR-070: exactly one appended note line; content: %q", e.Content)
	}
	require.Equal(t, 1, toolTexts, "instrument: exactly one tool entry expected")
}

func TestT40_MainConnectorTurn_AsyncForUserTextNotPublished(t *testing.T) {
	// S-60 row 2: an async tool finishes with user-facing text — nothing is
	// published, one refusal log line, the counter increments.
	f := t40NewFixture(t,
		testutil.NewScenario().WithToolCall(boundaryAsyncFeedbackProbeToolName, `{}`).WithText("done"),
		false, false)
	allowProbeTool(t, f.al, &boundaryAsyncFeedbackProbe{})
	readLog := t40CaptureLog(t)

	delta := t40RefusalsDelta(t, func() { f.t40RunScheduledTurn(t, "run the async probe") })

	require.Empty(t, drainOutboundText(f.bus),
		"S-60 row 2: the async ForUser text must not reach the connector")
	require.Equal(t, int64(1), delta,
		"S-60 row 2/FR-070: the existing refusal counter increments once")
	require.Equal(t, 1, t40CountRefusalLines(readLog()),
		"S-60 row 2/FR-070: exactly one `main connector tool output not sent` info line")
}

func TestT40_MainConnectorTurn_SyncForUserTextNotPublished(t *testing.T) {
	// S-60 row 3: a sync tool has ForUser text with SendResponse on — same
	// refusal contract. Driven through runTurn (SendResponse cannot be on
	// under ProcessScheduled).
	f := t40NewFixture(t,
		testutil.NewScenario().WithToolCall(t40UserToolName, `{}`).WithText("done"),
		false, false)
	allowProbeTool(t, f.al, &t40UserTool{})
	readLog := t40CaptureLog(t)

	delta := t40RefusalsDelta(t, func() { f.t40RunDirectTurn(t, true) })

	require.Empty(t, drainOutboundText(f.bus),
		"S-60 row 3: the sync ForUser text must not reach the connector")
	require.Equal(t, int64(1), delta,
		"S-60 row 3/FR-070: the existing refusal counter increments once")
	require.Equal(t, 1, t40CountRefusalLines(readLog()),
		"S-60 row 3/FR-070: exactly one refusal info line")
}

func TestT40_MainConnectorTurn_ToolCallFeedbackNotPublished(t *testing.T) {
	// S-60 row 4: tool-call feedback enabled and a tool runs — no `[tool]`
	// message to the connector; one refusal log line.
	f := t40NewFixture(t,
		testutil.NewScenario().WithToolCall(t40UserToolName, `{}`).WithText("done"),
		true, false)
	allowProbeTool(t, f.al, &t40UserTool{})
	readLog := t40CaptureLog(t)

	delta := t40RefusalsDelta(t, func() { f.t40RunScheduledTurn(t, "run the probe") })

	out := drainOutboundText(f.bus)
	for _, m := range out {
		require.NotContains(t, m.Content, "[tool]",
			"S-60 row 4: no tool-call feedback message may reach the connector")
	}
	require.Empty(t, out, "S-60 row 4: nothing else may publish either")
	require.Equal(t, int64(1), delta,
		"S-60 row 4/FR-070: the existing refusal counter increments once")
	require.Equal(t, 1, t40CountRefusalLines(readLog()),
		"S-60 row 4/FR-070: exactly one refusal info line")
}

func TestT40_MainConnectorTurn_OriginWarningNotPublished(t *testing.T) {
	// S-60 row 5: the async notifier's origin warning for this session is
	// not published. Driven through the real notifier path
	// (reportAsyncNotifyFailure → publishOriginWarning) on the
	// connector-fed main with its connector origin record.
	f := t40NewFixture(t,
		testutil.NewScenario().WithText("t40 done"), false, false)
	notifier := &asyncNotifierImpl{loop: f.al}
	event := AsyncNotifyEvent{
		Channel:             "telegram",
		ChatID:              "c1",
		TranscriptSessionID: f.main.ID,
		AgentID:             testDefaultAgentID,
		Content:             "t40 async origin warning content",
	}

	require.NoError(t, notifier.reportAsyncNotifyFailure(context.Background(), event, "t40-notice-1"),
		"S-60 row 5: the guarded site must decline silently, not error")
	require.Empty(t, drainOutboundText(f.bus),
		"S-60 row 5: the origin warning must not reach the connector")
}

func TestT40_NonMainConnectorSession_MediaStillSent(t *testing.T) {
	// S-61 (positive control — passes today): a NON-main session on the
	// connector is unchanged; SendMedia happens as today.
	f := t40NewFixture(t,
		testutil.NewScenario().WithToolCall("handled_media_tool", `{}`).WithText("Here is the screenshot."),
		false, true)
	allowProbeTool(t, f.al, &handledMediaTool{store: f.mediaStore, path: f.imagePath})
	chat, err := f.al.GetSessionStore().NewSession(session.SessionTypeChat, "telegram", testDefaultAgentID)
	require.NoError(t, err)

	_, err = f.al.ProcessScheduled(context.Background(), testDefaultAgentID, chat.ID, "take a screenshot", "telegram", "c2")
	require.NoError(t, err)

	require.Len(t, f.ch.sentMedia, 1,
		"S-61: a non-main connector session still gets its media (regression control)")
}

// t40UserTool returns user-facing text through the sync path.
const t40UserToolName = "t40_user_text_probe"

type t40UserTool struct{ tools.BaseTool }

func (*t40UserTool) Name() string        { return t40UserToolName }
func (*t40UserTool) Description() string { return "t40 probe returning user-facing text" }
func (*t40UserTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (*t40UserTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (*t40UserTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	return tools.UserResult("t40 fetched content for the user")
}
