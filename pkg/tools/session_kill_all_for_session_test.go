package tools

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeUnusedPID is a PID value picked well above any real process ID this
// sandbox will ever allocate but still within a valid pid_t range, so
// killProcessGroup's underlying syscall.Kill(-pid, ...) reliably returns
// ESRCH (verified: treated as success — see session_process_unix.go). Using a
// distinct fake PID per test session keeps kill/no-kill assertions readable
// in failure output without depending on any real subprocess.
const fakeUnusedPIDBase = 999990000

// TestSessionManager_KillAllForSession covers FR-B10/FR-B11/FR-B14: only the
// running sessions owned by the requested sessionID are killed, done/other-
// owner sessions are left untouched, and the kill counter/INFO log line fire
// once per session actually killed.
func TestSessionManager_KillAllForSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		sessions      []*ProcessSession
		killSessionID string
		wantKilled    int
		wantStatus    map[string]string // ProcessSession.ID -> expected Status after the call
	}{
		{
			name: "kills only running sessions owned by the target session",
			sessions: []*ProcessSession{
				{
					ID:             "s1",
					OwnerSessionID: "owner-A",
					Status:         "running",
					PID:            fakeUnusedPIDBase + 1,
					StartTime:      time.Now().Unix() - 30,
				},
				{
					ID:             "s2",
					OwnerSessionID: "owner-A",
					Status:         "running",
					PID:            fakeUnusedPIDBase + 2,
					StartTime:      time.Now().Unix() - 5,
				},
				{
					ID:             "s3",
					OwnerSessionID: "owner-B",
					Status:         "running",
					PID:            fakeUnusedPIDBase + 3,
					StartTime:      time.Now().Unix(),
				},
			},
			killSessionID: "owner-A",
			wantKilled:    2,
			wantStatus: map[string]string{
				// killProcess sets Status "done" on a successful kill;
				// KillAllForSession then relabels to "canceled" so a poll
				// after a RequestCancel cascade is distinguishable from a
				// natural exit or an explicit action=kill call.
				"s1": "canceled",
				"s2": "canceled",
				"s3": "running", // different owner — untouched
			},
		},
		{
			name: "skips already-done sessions for the same owner",
			sessions: []*ProcessSession{
				{
					ID:             "s4",
					OwnerSessionID: "owner-C",
					Status:         "running",
					PID:            fakeUnusedPIDBase + 4,
					StartTime:      time.Now().Unix(),
				},
				{
					ID:             "s5",
					OwnerSessionID: "owner-C",
					Status:         "done",
					PID:            fakeUnusedPIDBase + 5,
					StartTime:      time.Now().Unix(),
				},
			},
			killSessionID: "owner-C",
			wantKilled:    1,
			wantStatus: map[string]string{
				"s4": "canceled",
				"s5": "done", // already done, untouched by Kill(), still "done" (no relabel applied)
			},
		},
		{
			name: "no sessions for the given owner is a no-op",
			sessions: []*ProcessSession{
				{
					ID:             "s6",
					OwnerSessionID: "owner-D",
					Status:         "running",
					PID:            fakeUnusedPIDBase + 6,
					StartTime:      time.Now().Unix(),
				},
			},
			killSessionID: "owner-does-not-exist",
			wantKilled:    0,
			wantStatus: map[string]string{
				"s6": "running",
			},
		},
		{
			name: "empty sessionID is a no-op, never a panic",
			sessions: []*ProcessSession{
				{
					ID:             "s7",
					OwnerSessionID: "",
					Status:         "running",
					PID:            fakeUnusedPIDBase + 7,
					StartTime:      time.Now().Unix(),
				},
			},
			killSessionID: "",
			wantKilled:    0,
			wantStatus:    map[string]string{"s7": "running"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sm := NewSessionManager()
			for _, s := range tt.sessions {
				sm.Add(s)
			}

			before := sm.KilledBackgroundSessionsCount()

			killed := sm.KillAllForSession(tt.killSessionID)
			require.Equal(t, tt.wantKilled, killed, "unexpected killed count")

			after := sm.KilledBackgroundSessionsCount()
			require.Equal(
				t,
				before+int64(tt.wantKilled),
				after,
				"KilledBackgroundSessionsCount must increment by exactly the number of sessions actually killed (FR-B14)",
			)

			for id, wantStatus := range tt.wantStatus {
				got, err := sm.Get(id)
				require.NoError(t, err)
				require.Equal(t, wantStatus, got.GetStatus(), "unexpected status for session %s", id)
			}
		})
	}
}

// TestSessionManager_KillAllForSession_DoesNotAffectOtherOwners is a focused
// scoping regression test mirroring BDD Scenario "Canceling one session does
// not affect another session's background work" (User Story 5, Acceptance
// Scenario 3) directly at the SessionManager level.
func TestSessionManager_KillAllForSession_DoesNotAffectOtherOwners(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager()
	sessA := &ProcessSession{
		ID:             "a1",
		OwnerSessionID: "session-A",
		Status:         "running",
		PID:            fakeUnusedPIDBase + 100,
		StartTime:      time.Now().Unix(),
	}
	sessB := &ProcessSession{
		ID:             "b1",
		OwnerSessionID: "session-B",
		Status:         "running",
		PID:            fakeUnusedPIDBase + 101,
		StartTime:      time.Now().Unix(),
	}
	sm.Add(sessA)
	sm.Add(sessB)

	killed := sm.KillAllForSession("session-A")
	require.Equal(t, 1, killed)

	gotA, err := sm.Get("a1")
	require.NoError(t, err)
	require.Equal(t, "canceled", gotA.GetStatus(), "session A's background work must be killed and relabeled canceled")

	gotB, err := sm.Get("b1")
	require.NoError(t, err)
	require.Equal(t, "running", gotB.GetStatus(), "session B's background work must be left untouched")
}

// captureSlogInfo redirects slog's default logger to a buffer at INFO level
// for the duration of the test, restoring the previous default on cleanup.
// Mirrors the established log-capture pattern in
// pkg/config/shell_tool_policy_migration_test.go's captureSlog (which
// captures WARN-level output for the malformed-legacy-value test) — same
// mechanism, lower level threshold, since FR-B14's kill-cascade line is
// logged at INFO, not WARN.
func captureSlogInfo(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	oldDefault := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(oldDefault) })
	return &buf
}

// findLogEntry scans buf as JSONL (one JSON object per line, the shape
// slog.NewJSONHandler produces) and returns the first entry whose "msg"
// field contains substr, or nil if none matched.
func findLogEntry(t *testing.T, buf *bytes.Buffer, substr string) map[string]any {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(buf.String()))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if msg, ok := entry["msg"].(string); ok && strings.Contains(msg, substr) {
			return entry
		}
	}
	return nil
}

// TestSessionManager_KillAllForSession_LogsSessionDetails covers FR-B14's
// observability requirement directly: the BDD Scenario "Canceling a session
// kills its background bash sessions" (bash-tool-spec.md line 472) requires
// KillAllForSession to log ONE INFO line naming the session ID, PID, and
// elapsed runtime — not just increment the kill counter, which
// TestSessionManager_KillAllForSession above already covers but never
// inspects the log content itself. A regression that incremented the counter
// correctly but dropped or garbled the log line (e.g. omitted the PID, or
// logged the OTHER session's ID) would pass every existing assertion in this
// file today.
func TestSessionManager_KillAllForSession_LogsSessionDetails(t *testing.T) {
	buf := captureSlogInfo(t)

	sm := NewSessionManager()
	const wantElapsedMin = 42
	startTime := time.Now().Unix() - wantElapsedMin
	pid := fakeUnusedPIDBase + 777
	sess := &ProcessSession{
		ID:             "sess-log-details",
		OwnerSessionID: "owner-log-details",
		Status:         "running",
		PID:            pid,
		StartTime:      startTime,
	}
	sm.Add(sess)

	killed := sm.KillAllForSession("owner-log-details")
	require.Equal(t, 1, killed)

	entry := findLogEntry(t, buf, "killed background session on cancel cascade")
	require.NotNil(t, entry, "expected an INFO log line for the killed session; got log output: %s", buf.String())

	assert.Equal(t, "sess-log-details", entry["session_id"], "log line must name the session ID that was killed")
	assert.Equal(t, "owner-log-details", entry["owner_session_id"], "log line must name the owning session")

	// JSON numbers decode as float64 via encoding/json's default map[string]any.
	gotPID, ok := entry["pid"].(float64)
	require.True(t, ok, "log line must carry a numeric pid field, got %#v", entry["pid"])
	assert.Equal(t, float64(pid), gotPID, "log line must name the REAL PID of the killed session")

	gotElapsed, ok := entry["elapsed_seconds"].(float64)
	require.True(t, ok, "log line must carry a numeric elapsed_seconds field, got %#v", entry["elapsed_seconds"])
	assert.GreaterOrEqual(t, gotElapsed, float64(wantElapsedMin),
		"elapsed_seconds must reflect the REAL runtime (session started %ds ago)", wantElapsedMin)
	assert.Less(t, gotElapsed, float64(wantElapsedMin+10),
		"elapsed_seconds must not be wildly larger than the real elapsed time")
}
