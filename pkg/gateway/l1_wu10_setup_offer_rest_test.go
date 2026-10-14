package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FR-047 / OSA-9 W8 / binding 201-flush ruling: the response snapshot must
// still expose pending=true, even though the later producer consumes it.
func TestL1WU10RESTCreate_EmptyTeamResponsePreservesPendingOffer(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit_empty_%t", explicit), func(t *testing.T) {
			h := newL1WU10SetupHarness(t)
			req := gen.WorkspaceCreateRequest{Name: "Research kickoff"}
			if explicit {
				empty := []string{}
				req.CoreTeam = &empty
			}
			created := h.post(t, req)
			h.awaitHandler(t)
			require.NotNil(t, created.SetupPending, "FR-047: empty-team 201 must carry setup_pending=true; missing is not false-compatible")
			assert.True(t, *created.SetupPending, "restored CHECK F06 oracle")
			if created.CoreTeam != nil {
				assert.Empty(t, *created.CoreTeam)
			}
			stored, readErr := readWorkspaceFile(h.api.homePath, created.Id)
			require.NoError(t, readErr)
			assert.Empty(t, stored.CoreTeam)
			edges, readable := workspace.LoadDelegation(h.api.homePath, created.Id)
			require.True(t, readable)
			assert.Empty(t, edges, "an empty team has no seeded delegation edges")
		})
	}
}

func TestL1WU10RESTCreate_OneServerOfferReusesOmnipusMainAndDefaultAnchor(t *testing.T) {
	h := newL1WU10SetupHarness(t)
	before, beforeErr := h.store.ListSessions()
	require.NoError(t, beforeErr)
	created := h.post(t, gen.WorkspaceCreateRequest{Name: "Research named offer"})
	msg := h.awaitOffer(t, created.Id)
	h.awaitHandler(t)
	assert.Equal(t, "webchat", msg.Channel)
	assert.Equal(t, "omnipus", msg.Metadata["agent_id"])
	assert.Equal(t, h.defaultID, msg.Metadata["workspace_id"], "OSA-9.1: target workspace is not turn binding")
	assert.Equal(t, "1", msg.Metadata["owner_wake"], "synthetic setup offer must use the non-human owner-wake path")
	assert.False(t, msg.OperatorPrompt, "a setup offer is not an operator-composed prompt")
	assert.False(t, msg.UserInitiated, "a setup offer is not human-originated agent work")
	assert.Contains(t, msg.Content, created.Name)
	assert.Contains(t, msg.Content, created.Id)
	h.assertMainAnchor(t)
	entries, transcriptErr := h.store.ReadTranscript(h.mainID)
	require.NoError(t, transcriptErr)
	require.Len(t, entries, 2, "one existing entry plus exactly ONE system offer; no fake user bubble")
	assert.Equal(t, "prior-omnipus", entries[0].ID)
	assert.Equal(t, "system", entries[1].Role)
	assert.Equal(t, session.EntryTypeSystem, entries[1].Type)
	assert.Equal(t, "omnipus", entries[1].AgentID)
	assert.Contains(t, entries[1].Content, created.Name, "final notice names the workspace; wording is not an oracle")
	miaEntries, miaErr := h.store.ReadTranscript(h.miaMainID)
	require.NoError(t, miaErr)
	require.Len(t, miaEntries, 1, "no offer goes to another agent")
	assert.Equal(t, "prior-mia", miaEntries[0].ID)
	after, afterErr := h.store.ListSessions()
	require.NoError(t, afterErr)
	assert.ElementsMatch(t, l1WU10SessionIDs(before), l1WU10SessionIDs(after), "empty-workspace creation must not mint a new setup conversation")
	got := h.get(t, created.Id)
	assert.False(t, l1WU10Pending(got), "successful publication consumes pending; GET sees false")
	h.assertNoFurtherWake(t)
}

func TestL1WU10RESTCreate_SameWorkspaceReplayCannotPostTwice(t *testing.T) {
	h := newL1WU10SetupHarness(t)
	created := h.post(t, gen.WorkspaceCreateRequest{Name: "Replay Research"})
	h.awaitOffer(t, created.Id)
	h.awaitHandler(t)
	before, readErr := h.store.ReadTranscript(h.mainID)
	require.NoError(t, readErr)
	require.Len(t, before, 2)
	// No invented HTTP idempotency key: repeat the real producer for this
	// SAME already-created workspace. Distinct POSTs are a separate case.
	producer := h.requireProducer(t)
	producer.postWorkspaceSetupOffer(created.Id, "kickoff-test-user")
	producer.postWorkspaceSetupOffer(created.Id, "kickoff-test-user")
	after, afterErr := h.store.ReadTranscript(h.mainID)
	require.NoError(t, afterErr)
	assert.Equal(t, before, after, "duplicate consume must append no offer entry")
	h.assertNoFurtherWake(t)
	assert.False(t, l1WU10Pending(h.get(t, created.Id)))
	h.assertMainAnchor(t)
}

func TestL1WU10RESTCreate_DistinctCreatesEachOfferIntoTheSameMain(t *testing.T) {
	h := newL1WU10SetupHarness(t)
	first := h.post(t, gen.WorkspaceCreateRequest{Name: "Same display name"})
	h.awaitOffer(t, first.Id)
	h.awaitHandler(t)
	second := h.post(t, gen.WorkspaceCreateRequest{Name: "Same display name"})
	h.awaitOffer(t, second.Id)
	h.awaitHandler(t)
	assert.NotEqual(t, first.Id, second.Id, "POST generates distinct workspaces; name is not an idempotency key")
	entries, readErr := h.store.ReadTranscript(h.mainID)
	require.NoError(t, readErr)
	require.Len(t, entries, 3, "one prior entry and exactly one offer for each distinct create")
	for _, entry := range entries[1:] {
		assert.Equal(t, "system", entry.Role)
		assert.Equal(t, "omnipus", entry.AgentID)
		assert.Contains(t, entry.Content, first.Name)
	}
	h.assertNoFurtherWake(t)
	h.assertMainAnchor(t)
}

func TestL1WU10RESTCreate_ExplicitTeamAndDefaultHaveNoSetupOffer(t *testing.T) {
	h := newL1WU10SetupHarness(t)
	before, beforeErr := h.store.ReadTranscript(h.mainID)
	require.NoError(t, beforeErr)
	team := []string{"mia"}
	created := h.post(t, gen.WorkspaceCreateRequest{Name: "Already staffed", CoreTeam: &team})
	h.awaitHandler(t)
	assert.False(t, l1WU10Pending(created))
	assert.False(t, l1WU10Pending(h.get(t, created.Id)))
	h.assertNoFurtherWake(t)
	defaultWorkspace, defaultErr := readWorkspaceFile(h.api.homePath, h.defaultID)
	require.NoError(t, defaultErr)
	assert.True(t, defaultWorkspace.IsDefault)
	assert.False(t, defaultWorkspace.SetupPending)
	// A producer replay against default or already-consumed records is a
	// no-op, even though those records and the existing main are real.
	producer := h.requireProducer(t)
	producer.postWorkspaceSetupOffer(h.defaultID, "kickoff-test-user")
	producer.postWorkspaceSetupOffer(created.Id, "kickoff-test-user")
	after, afterErr := h.store.ReadTranscript(h.mainID)
	require.NoError(t, afterErr)
	assert.Equal(t, before, after)
	h.assertNoFurtherWake(t)
}

// A closed bus tests the real poster, not a configured success/failure
// mock. Handler completion is the barrier before examining restored state.
func TestL1WU10RESTCreate_PublishFailureRestoresPendingAndVisibleNotice(t *testing.T) {
	h := newL1WU10SetupHarness(t)
	capture := &l1WU10ErrorCapture{}
	previous := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(previous) })
	slog.Error("wu10-log-instrument")
	require.True(t, capture.contains("wu10-log-instrument"), "known ERROR proves capture can see failure logs")
	h.bus.Close()
	created := h.post(t, gen.WorkspaceCreateRequest{Name: "Failed Research offer"})
	h.awaitHandler(t)
	h.requireProducer(t)
	assert.True(t, l1WU10Pending(created), "201 snapshot stays pending even on later failure")
	assert.True(t, l1WU10Pending(h.get(t, created.Id)), "actual publication failure must restore pending")
	entries, readErr := h.store.ReadTranscript(h.mainID)
	require.NoError(t, readErr)
	require.Len(t, entries, 3, "prior entry, offered notice, and visible failure note")
	assert.Equal(t, "system", entries[2].Role)
	assert.Equal(t, "omnipus", entries[2].AgentID)
	assert.Contains(t, entries[2].Content, created.Name)
	assert.Contains(t, entries[2].Content, "could not be started")
	assert.Contains(t, entries[2].Content, "Team tab")
	assert.True(t, capture.contains(created.Id), "ERROR must identify the failed target workspace")
	before := append([]session.TranscriptEntry(nil), entries...)
	// Read-only visits do not act as an automatic retry or append another
	// misleading offer/failure note.
	assert.True(t, l1WU10Pending(h.get(t, created.Id)))
	after, afterErr := h.store.ReadTranscript(h.mainID)
	require.NoError(t, afterErr)
	assert.Equal(t, before, after)
	h.assertMainAnchor(t)
}

func TestL1WU10ServerSetupOffer_MissingWorkspaceCreatesNothing(t *testing.T) {
	h := newL1WU10SetupHarness(t)
	before, beforeErr := h.store.ReadTranscript(h.mainID)
	require.NoError(t, beforeErr)
	producer := h.requireProducer(t)
	const missingID = "01WU10MISSINGWORKSPACE"
	require.False(t, workspace.Exists(h.api.homePath, missingID), "missing-target instrument")
	require.NotPanics(t, func() { producer.postWorkspaceSetupOffer(missingID, "kickoff-test-user") })
	assert.False(t, workspace.Exists(h.api.homePath, missingID), "refusal must not recreate a missing target")
	after, afterErr := h.store.ReadTranscript(h.mainID)
	require.NoError(t, afterErr)
	assert.Equal(t, before, after)
	h.assertNoFurtherWake(t)
	h.assertMainAnchor(t)
}

func TestL1WU10WorkspaceAgentTool_DoesNotInitiateRESTOnlyOffer(t *testing.T) {
	h := newL1WU10SetupHarness(t)
	rc := &runContextWithOptions{
		homePath: h.api.homePath, configPath: h.api.configPath(), cfg: h.loop.GetConfig(), agentLoop: h.loop,
		runningServices: &services{restAPIRef: h.api}, centralMCPReg: tools.NewMCPRegistry(),
	}
	rc.wireSystemTools()
	omnipus, native := h.loop.GetRegistry().GetAgent("omnipus")
	require.True(t, native)
	create, registered := omnipus.Tools.Get("create_workspace")
	require.True(t, registered, "exercise the actual registered tool body, not a no-op/missing tool")
	before, beforeErr := h.store.ReadTranscript(h.mainID)
	require.NoError(t, beforeErr)
	const name = "Tool-created workspace has no REST offer"
	result := create.Execute(tools.WithAgentID(context.Background(), "omnipus"), map[string]any{"name": name})
	require.NotNil(t, result)
	require.False(t, result.IsError, "the real tool must successfully create a workspace: %s", result.ForLLM)
	workspaces, readErr := listWorkspaceFiles(h.api.homePath)
	require.NoError(t, readErr)
	count := 0
	for _, record := range workspaces {
		if record.Name == name {
			count++
			assert.Empty(t, record.CoreTeam)
		}
	}
	assert.Equal(t, 1, count, "successful creation is the no-offer instrument")
	after, afterErr := h.store.ReadTranscript(h.mainID)
	require.NoError(t, afterErr)
	assert.Equal(t, before, after, "Q1: agent tool creation does not post the REST-owned offer")
	h.assertNoFurtherWake(t)
}

func l1WU10SessionIDs(metas []*session.UnifiedMeta) []string {
	ids := make([]string, 0, len(metas))
	for _, meta := range metas {
		ids = append(ids, meta.ID)
	}
	return ids
}

type l1WU10ErrorCapture struct {
	mu       sync.Mutex
	messages []string
}

func (c *l1WU10ErrorCapture) Enabled(context.Context, slog.Level) bool { return true }
func (c *l1WU10ErrorCapture) Handle(_ context.Context, record slog.Record) error {
	if record.Level >= slog.LevelError {
		c.mu.Lock()
		defer c.mu.Unlock()
		text := record.Message
		record.Attrs(func(attr slog.Attr) bool { text += " " + attr.Key + "=" + attr.Value.String(); return true })
		c.messages = append(c.messages, text)
	}
	return nil
}
func (c *l1WU10ErrorCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *l1WU10ErrorCapture) WithGroup(string) slog.Handler      { return c }
func (c *l1WU10ErrorCapture) contains(text string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, message := range c.messages {
		if strings.Contains(message, text) {
			return true
		}
	}
	return false
}
