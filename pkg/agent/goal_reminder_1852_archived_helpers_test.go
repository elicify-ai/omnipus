package agent

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Issue #1852 (founder decision Q2 A, 15 Nov 2026). Oracles come from the
// issue text, never from the implementation:
//   - only helpers active since the goal started are considered: created at
//     or after the goal start, or last active at or after it (a helper
//     started before the goal but still working counts);
//   - old helpers outside that window never block or affect the reminder;
//   - an in-window helper whose transcript cannot be opened is skipped, not
//     fatal, with a visible log line naming the session and the reason, and
//     a skipped helper never counts as progress.
//
// An "archived" helper is modelled exactly as the founder's instance had it:
// its lifecycle record still names the chat as steering parent, but its
// session is absent from the live session store (moved to
// sessions-archive-<date>), so the store cannot be resolved for it.

const gr1852UnavailableError = "descendant store for" // the abandoned-assessment error text from the issue

type gr1852Helper struct {
	createdAt    time.Time
	lastActivity time.Time
	state        session.LifecycleState
	readable     bool      // false = archived: no session in the live store
	outputAt     time.Time // zero = no assistant output in its transcript
}

// gr1852AddHelper records one helper steered by the harness chat, through the
// real lifecycle store, and returns its session id.
func gr1852AddHelper(t *testing.T, h *grHarness, spec gr1852Helper, archivedID string) string {
	t.Helper()
	id := archivedID
	if spec.readable {
		meta, err := h.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", testDefaultAgentID)
		require.NoError(t, err)
		id = meta.ID
		if !spec.outputAt.IsZero() {
			require.NoError(t, h.al.GetSessionStore().AppendTranscript(id, session.TranscriptEntry{
				ID: "helper-output-" + id, Role: "assistant", AgentID: testDefaultAgentID,
				Content: "helper produced real work", Timestamp: spec.outputAt,
			}))
		}
	} else {
		require.Nil(t, h.al.ResolveSessionStore(id), "SETUP: an archived helper must be missing from the live session store")
	}
	state := spec.state
	if state == "" {
		state = session.LifecycleCompleted
	}
	rec := &session.LifecycleRecord{
		SessionID: id, Generation: 1, State: state,
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: h.id,
		Origin:    &session.Origin{Kind: session.OriginKindDelegate},
		SteeredBy: &session.SteeredBy{SteeringSessionID: h.id, RootSessionID: h.id},
		CreatedAt: spec.createdAt, LastActivityAt: spec.lastActivity,
	}
	require.NoError(t, h.al.GetSessionLifecycleStore().Persist(rec))
	stored, err := h.al.GetSessionLifecycleStore().Load(id)
	require.NoError(t, err)
	require.True(t, stored.CreatedAt.Equal(spec.createdAt), "SETUP: CreatedAt must be durably the fixture value")
	require.True(t, stored.LastActivityAt.Equal(spec.lastActivity.UTC()), "SETUP: LastActivityAt must be durably the fixture value")
	return id
}

func gr1852GoalStart(t *testing.T, h *grHarness) time.Time {
	t.Helper()
	rec := goalRecordForSession(t, h.id)
	require.NotNil(t, rec.StartedAt, "SETUP: an active goal carries its start")
	return rec.StartedAt.UTC()
}

// gr1852Collect runs the real assessment reader over a private window and
// returns its result plus the log lines written while it ran.
func gr1852Collect(t *testing.T, h *grHarness, since time.Time) (goalReminderProgress, *goalReminderWork, []map[string]any, error) {
	t.Helper()
	rec := goalRecordForSession(t, h.id)
	work := &goalReminderWork{events: make(map[string]time.Time), processes: make(map[string]*tools.ProcessSession)}
	logPath := filepath.Join(t.TempDir(), "agent.log")
	require.NoError(t, logger.EnableFileLogging(logPath))
	work.mu.Lock()
	progress, _, err := h.al.collectGoalReminderProgress(h.id, rec, work, since, time.Now().UTC())
	work.mu.Unlock()
	logger.DisableFileLogging()
	return progress, work, gr1852ReadLog(t, logPath), err
}

func gr1852ReadLog(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	var lines []map[string]any
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var line map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &line), "log line must be JSON: %s", scanner.Text())
		lines = append(lines, line)
	}
	require.NoError(t, scanner.Err())
	return lines
}

// gr1852LinesNaming returns every log line whose session_id field is id.
func gr1852LinesNaming(lines []map[string]any, id string) []map[string]any {
	var out []map[string]any
	for _, line := range lines {
		if line["session_id"] == id {
			out = append(out, line)
		}
	}
	return out
}

// Acceptance 1: a goal whose chat has archived helpers from before the goal
// start completes its assessment and dispatches reminders through the real
// keeper; no "descendant store ... unavailable" error is ever surfaced.
func TestGoalReminder1852ArchivedPreGoalHelpersDoNotBlockReminders(t *testing.T) {
	h := grNewRecordedBrake(t)
	start := gr1852GoalStart(t, h)
	old := start.Add(-19 * 24 * time.Hour) // archived helpers were from 27 Oct; the goal ran on 15 Nov
	archived := []string{
		gr1852AddHelper(t, h, gr1852Helper{createdAt: old, lastActivity: old.Add(time.Hour)}, "session_1852ARCHIVEDA"),
		gr1852AddHelper(t, h, gr1852Helper{createdAt: old, lastActivity: start.Add(-time.Minute)}, "session_1852ARCHIVEDB"),
	}

	first := h.reminder(t)
	grTextReminder(t, h, first)
	h.age(t)
	second := h.reminder(t) // fails the test if the keeper did not publish
	assert.Equal(t, 1, goalRecordForSession(t, h.id).ZeroOutputPushes,
		"the second reminder must assess the completed text-only outcome as one unproductive reminder")
	grTextReminder(t, h, second)

	for _, entry := range h.visibleHistory(t) {
		assert.NotContains(t, entry.Content, gr1852UnavailableError, "no assessment may fail on an archived pre-goal helper")
		assert.NotContains(t, entry.Content, "Goal state could not be read", "the assessment must not be deferred")
	}
	_, _, lines, err := gr1852Collect(t, h, start)
	require.NoError(t, err)
	for _, id := range archived {
		assert.Empty(t, gr1852LinesNaming(lines, id), "a helper outside the goal window must not be opened, so it is not even skipped: %s", id)
	}
}

// Acceptance 2: a helper created before the goal but active after the goal
// start is still included — its post-goal assistant output is progress.
func TestGoalReminder1852PreGoalHelperActiveAfterStartIsIncluded(t *testing.T) {
	h := grNewRecordedBrake(t)
	start := gr1852GoalStart(t, h)
	gr1852AddHelper(t, h, gr1852Helper{createdAt: start.Add(-48 * time.Hour), lastActivity: start.Add(-47 * time.Hour)}, "session_1852ARCHIVEDOLD")
	activeAt := start.Add(30 * time.Minute)
	gr1852AddHelper(t, h, gr1852Helper{createdAt: start.Add(-2 * time.Hour), lastActivity: activeAt, readable: true, outputAt: activeAt}, "")

	progress, work, _, err := gr1852Collect(t, h, start)
	require.NoError(t, err, "an archived pre-goal helper must not abandon the assessment")
	assert.True(t, progress.descendant, "post-goal assistant output of a pre-goal helper still working must count")
	assert.True(t, work.observedProgress, "the window must latch the observed progress")
}

// Acceptance 2 (still-working form): a helper admitted before the goal whose
// turn is still running has no newer activity stamp yet (activity is stamped
// at admission and at the end), but it is still working and must count.
func TestGoalReminder1852PreGoalHelperStillRunningIsIncluded(t *testing.T) {
	h := grNewRecordedBrake(t)
	start := gr1852GoalStart(t, h)
	gr1852AddHelper(t, h, gr1852Helper{createdAt: start.Add(-48 * time.Hour), lastActivity: start.Add(-47 * time.Hour)}, "session_1852ARCHIVEDOLD")
	gr1852AddHelper(t, h, gr1852Helper{createdAt: start.Add(-2 * time.Hour), lastActivity: start.Add(-2 * time.Hour),
		state: session.LifecycleRunning, readable: true, outputAt: start.Add(20 * time.Minute)}, "")

	progress, _, _, err := gr1852Collect(t, h, start)
	require.NoError(t, err)
	assert.True(t, progress.descendant, "a running helper started before the goal is still working; its new output counts")
}

// Acceptance 3: an in-window helper whose store cannot be opened is skipped
// with a visible WARN line naming it and the reason; the other signals
// (here a readable helper's new output) still decide.
func TestGoalReminder1852InWindowUnavailableHelperIsSkippedVisibly(t *testing.T) {
	h := grNewRecordedBrake(t)
	start := gr1852GoalStart(t, h)
	at := start.Add(10 * time.Minute)
	lost := gr1852AddHelper(t, h, gr1852Helper{createdAt: at, lastActivity: at}, "session_1852LOSTINWINDOW")
	gr1852AddHelper(t, h, gr1852Helper{createdAt: at, lastActivity: at.Add(time.Minute), readable: true, outputAt: at.Add(time.Minute)}, "")

	progress, _, lines, err := gr1852Collect(t, h, start)
	require.NoError(t, err, "an unavailable in-window helper is skipped, never fatal")
	assert.True(t, progress.descendant, "the remaining readable helper's output still decides")
	named := gr1852LinesNaming(lines, lost)
	require.Len(t, named, 1, "exactly one visible log line names the skipped helper")
	assert.Equal(t, "warn", named[0]["level"], "the skip is logged at WARN")
	reason, _ := named[0]["reason"].(string)
	assert.Contains(t, reason, "unavailable", "the log line states why the helper was skipped")
	message, _ := named[0]["message"].(string)
	assert.Contains(t, message, "skip", "the log line says the helper was skipped")
}

// Acceptance 4: a skipped helper never counts as progress. With no other
// progress the outcome is "no progress", and the real keeper still
// dispatches the next reminder and counts the unproductive outcome.
func TestGoalReminder1852SkippedHelperNeverCountsAsProgress(t *testing.T) {
	h := grNewRecordedBrake(t)
	start := gr1852GoalStart(t, h)
	at := start.Add(5 * time.Minute)
	lost := gr1852AddHelper(t, h, gr1852Helper{createdAt: at, lastActivity: at}, "session_1852LOSTNOPROGRESS")

	progress, work, lines, err := gr1852Collect(t, h, start)
	require.NoError(t, err)
	assert.Equal(t, goalReminderProgress{}, progress, "no signal may report progress for a skipped helper")
	assert.False(t, work.observedProgress, "a skipped helper must not latch progress")
	assert.Empty(t, work.events, "a skipped helper must not record any seen activity")
	assert.Len(t, gr1852LinesNaming(lines, lost), 1, "the skip is still visible")

	first := h.reminder(t)
	grTextReminder(t, h, first)
	h.age(t)
	second := h.reminder(t)
	assert.Equal(t, 1, goalRecordForSession(t, h.id).ZeroOutputPushes, "the skipped helper's window is an unproductive reminder outcome")
	grTextReminder(t, h, second)
}

// Boundary: activity exactly at the goal start is inside the window.
func TestGoalReminder1852ActivityExactlyAtGoalStartIsIncluded(t *testing.T) {
	h := grNewRecordedBrake(t)
	start := gr1852GoalStart(t, h)
	gr1852AddHelper(t, h, gr1852Helper{createdAt: start.Add(-time.Hour), lastActivity: start, readable: true, outputAt: start}, "")
	// A neighbour exactly one second before the start makes the old code fail.
	gr1852AddHelper(t, h, gr1852Helper{createdAt: start.Add(-time.Second), lastActivity: start.Add(-time.Second)}, "session_1852EDGEBEFORE")

	progress, _, _, err := gr1852Collect(t, h, start)
	require.NoError(t, err)
	assert.True(t, progress.descendant, "last activity equal to the goal start is 'at or after' the start")
}

// Boundary: created and last active one second before the goal start is
// outside the window — not opened, so not even logged as skipped.
func TestGoalReminder1852OneSecondBeforeGoalStartIsExcluded(t *testing.T) {
	h := grNewRecordedBrake(t)
	start := gr1852GoalStart(t, h)
	before := gr1852AddHelper(t, h, gr1852Helper{createdAt: start.Add(-time.Second), lastActivity: start.Add(-time.Second)}, "session_1852EDGEBEFORE")
	// Created exactly at the start (no later activity) is inside: it is opened and skipped visibly.
	atStart := gr1852AddHelper(t, h, gr1852Helper{createdAt: start, lastActivity: start}, "session_1852EDGEATSTART")

	progress, _, lines, err := gr1852Collect(t, h, start)
	require.NoError(t, err)
	assert.False(t, progress.descendant)
	assert.Empty(t, gr1852LinesNaming(lines, before), "1 s before the start with CreatedAt also before is excluded, never opened")
	assert.Len(t, gr1852LinesNaming(lines, atStart), 1, "created exactly at the start is inside the window")
	for _, line := range lines {
		msg, _ := line["message"].(string)
		assert.False(t, strings.Contains(msg, gr1852UnavailableError), "no abandoned-assessment error")
	}
}
