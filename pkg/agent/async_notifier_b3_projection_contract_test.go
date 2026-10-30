package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracles: B3-1..7, founder Q2=A, and the settled private-fit design (2026-10-23).
// Synthetic canaries belong only to this new test source and restricted raw logs.
const b3PrivateBody = "B3_SYNTHETIC_RESULT_PRIVATE_71 question=fixture-only approval=fixture-only"
const b3AttemptMessage = "Async notification publish failed"
const b3RejectMessage = "Async notification origin warning enqueue rejected"
const b3RejectedText = "async notification origin warning: outbound enqueue not accepted"
const b3JoinWatchdog = 45 * time.Second // deadlock/setup guard, NOT a turn/caller budget or oracle

// b3DeliveryDeadlineCtx bounds the caller deadline for the failure-projection
// subtests whose oracles are WHAT a failed delivery projects: the saved
// transcript warning, the WARN record shape, the outbound enqueue/rejection
// outcome and the DeadlineExceeded cause. Those oracles are identical after one
// real failed publish as after the full three-attempt budget:
// b3AssertDiagnostic asserts attempt_budget (the constant 3) and only ALLOWS
// the attempt/status keys, and reportAsyncNotifyFailure runs for any
// attempts > 0 unless the caller was canceled (async_notifier.go Notify).
// The full retry-budget arithmetic stays owned by
// TestAsyncNotifierB3RetryContract on real timers (b3CausalRepair's 5+1+5s
// bound, b3ThreeFailures' three records); b3CallerDeadline there already pins
// this single-attempt projection shape with a bounded caller context.
// 500ms is far below the 5s per-attempt publish timeout, so exactly one real
// failed attempt runs against the untouched full inbound queue before the
// caller deadline ends the loop.
func b3DeliveryDeadlineCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	t.Cleanup(cancel)
	return ctx
}

type b3Boundary struct {
	boundary steer.Boundary
	sid      string
	audience steer.Audience
}
type b3BoundaryRecorder struct {
	mu    sync.Mutex
	calls []b3Boundary
}

func (r *b3BoundaryRecorder) Observe(b steer.Boundary, sid string, a steer.Audience) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, b3Boundary{b, sid, a})
}
func (r *b3BoundaryRecorder) seen(b steer.Boundary, sid string, a steer.Audience) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if c.boundary == b && c.sid == sid && c.audience == a {
			return true
		}
	}
	return false
}

type b3Fixture struct {
	al         *AgentLoop
	bus        *bus.MessageBus
	sid        string
	event      AsyncNotifyEvent
	boundary   *b3BoundaryRecorder
	eventsMu   sync.Mutex
	events     []Event
	observedMu sync.Mutex
	observed   []AsyncNotifyEvent
	logPath    string
}

func b3New(t *testing.T) *b3Fixture {
	t.Helper()
	al, mb := newAsyncNotifierTestLoop(t)
	installTestSessionLauncher(t, al)
	boundary := &b3BoundaryRecorder{}
	al.SetSteerAudienceDeps(NewSteerAudienceResolver(NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())), boundary, NewSteerUpwardDeliverer())
	launched, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		TargetAgentID: testDefaultAgentID, Task: "B3 fixture ordinary origin", Origin: steer.Origin{Kind: steer.OriginKindChannel},
		WorkspaceID: testHarnessWorkspaceMembershipID, Owner: "b3-fixture-owner",
	})
	require.NoError(t, err)
	f := &b3Fixture{al: al, bus: mb, sid: launched.SessionID, boundary: boundary}
	f.event = AsyncNotifyEvent{Channel: "telegram.b3-origin", ChatID: "b3-origin-chat", AgentID: testDefaultAgentID,
		TranscriptSessionID: f.sid, SourceKind: "goal_idle_settle", Content: b3PrivateBody}
	a, c, err := NewSteerAudienceResolver(NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())).Audience(context.Background(), f.sid)
	require.NoError(t, err)
	require.Equal(t, steer.ClassOrdinaryRoot, c)
	require.Equal(t, steer.AudienceUser, a)
	al.SetEventSyncTap(func(e Event) { f.eventsMu.Lock(); f.events = append(f.events, e); f.eventsMu.Unlock() })
	t.Cleanup(func() { al.SetEventSyncTap(nil) })
	al.asyncNotifier.registerObserver(func(e AsyncNotifyEvent) {
		f.observedMu.Lock()
		f.observed = append(f.observed, e)
		f.observedMu.Unlock()
	})
	f.logPath = b3CaptureNative(t)
	t.Cleanup(mb.Close)
	return f
}

func b3CaptureNative(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "native-private.jsonl")
	level := logger.GetLevel()
	require.NoError(t, logger.EnableFileLogging(path))
	logger.SetLevel(logger.WARN)
	t.Cleanup(func() { logger.DisableFileLogging(); logger.SetLevel(level) })
	logger.WarnCF("agent", "B3 native capture positive control", map[string]any{"control": true})
	rows := b3Logs(t, path, "B3 native capture positive control")
	require.Len(t, rows, 1, "SETUP: native WarnCF file instrument must see a real positive record")
	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), st.Mode().Perm())
	return path
}

func b3Logs(t *testing.T, path, message string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var out []map[string]any
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &row), "SETUP: only complete native records after producer join")
		if row["message"] == message {
			out = append(out, row)
		}
	}
	return out
}

func b3FillInbound(t *testing.T, mb *bus.MessageBus) {
	t.Helper()
	require.Equal(t, 0, len(mb.InboundChan()))
	for i := 0; i < cap(mb.InboundChan()); i++ {
		require.NoError(t, mb.PublishInbound(context.Background(), bus.InboundMessage{Channel: "system", ChatID: fmt.Sprintf("b3-filler-%d", i), Content: "filler"}))
	}
	require.Equal(t, cap(mb.InboundChan()), len(mb.InboundChan()))
}
func b3FillOutbound(t *testing.T, mb *bus.MessageBus) {
	t.Helper()
	require.Equal(t, 0, len(mb.OutboundChan()))
	for i := 0; i < cap(mb.OutboundChan()); i++ {
		require.True(t, mb.TryPublishOutbound(bus.OutboundMessage{Channel: "telegram.filler", ChatID: fmt.Sprint(i)}))
	}
	require.Equal(t, cap(mb.OutboundChan()), len(mb.OutboundChan()))
}
func b3DrainInbound(mb *bus.MessageBus) []bus.InboundMessage {
	var out []bus.InboundMessage
	for {
		select {
		case m, ok := <-mb.InboundChan():
			if !ok {
				return out
			}
			out = append(out, m)
		default:
			return out
		}
	}
}
func b3DrainOutbound(mb *bus.MessageBus) []bus.OutboundMessage {
	var out []bus.OutboundMessage
	for {
		select {
		case m, ok := <-mb.OutboundChan():
			if !ok {
				return out
			}
			out = append(out, m)
		default:
			return out
		}
	}
}
func (f *b3Fixture) counts() (int, int) {
	f.observedMu.Lock()
	n := len(f.observed)
	f.observedMu.Unlock()
	f.eventsMu.Lock()
	defer f.eventsMu.Unlock()
	q := 0
	for _, e := range f.events {
		if e.Kind == EventKindFollowUpQueued {
			q++
		}
	}
	return n, q
}
func b3Rows(t *testing.T, al *AgentLoop, sid string) []session.TranscriptEntry {
	t.Helper()
	store, err := session.NewUnifiedStore(al.GetSessionStore().BaseDir())
	require.NoError(t, err)
	view, err := store.ReadTranscriptView(sid)
	require.NoError(t, err)
	var out []session.TranscriptEntry
	for _, row := range view.Entries {
		if row.Type == session.EntryTypeSystem && row.Role == "system" && strings.Contains(row.Content, "did not wake the agent") || row.Type == session.EntryTypeSystem && row.Role == "system" && strings.Contains(row.Content, "agent was not woken by this notification") {
			out = append(out, row)
		}
	}
	return out
}
func b3Ref(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])[:12]
}

func b3AssertDiagnostic(t *testing.T, rows []map[string]any, rejection bool, source, sid, tool, callID string) {
	t.Helper()
	require.NotEmpty(t, rows, "required positive native records must exist before privacy assertions")
	for _, r := range rows {
		assert.Equal(t, "agent", r["component"])
		assert.Equal(t, "warn", r["level"])
		assert.Equal(t, source, r["source"])
		id, ok := r["notice_id"].(string)
		assert.True(t, ok && id != "", "server notice identity required")
		allowed := map[string]bool{"level": true, "time": true, "caller": true, "component": true, "message": true, "source": true, "notice_id": true, "tool": true, "tool_ref": true, "session_ref": true}
		if !rejection {
			allowed["attempt"] = true
			allowed["attempt_budget"] = true
			allowed["status"] = true
			assert.Equal(t, float64(3), r["attempt_budget"])
		}
		for key := range r {
			assert.True(t, allowed[key], "unexpected diagnostic key %q", key)
		}
		if value, ok := r["session_ref"]; ok {
			assert.Equal(t, b3Ref(sid), value)
		}
		if tool == "" {
			_, has := r["tool"]
			assert.False(t, has)
			if callID == "" {
				_, has = r["tool_ref"]
				assert.False(t, has)
			} else if ref, present := r["tool_ref"]; present {
				assert.Equal(t, b3Ref(callID), ref)
			}
		} else {
			if v, ok := r["tool"]; ok {
				assert.Equal(t, tool, v)
			}
			if v, ok := r["tool_ref"]; ok {
				assert.Equal(t, b3Ref(callID), v)
			}
		}
		encoded, err := json.Marshal(r)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), b3PrivateBody)
		assert.NotContains(t, string(encoded), "telegram.b3-origin")
		assert.NotContains(t, string(encoded), "b3-origin-chat")
	}
}

func b3AssertSaved(t *testing.T, f *b3Fixture, want string) session.TranscriptEntry {
	t.Helper()
	rows := b3Rows(t, f.al, f.sid)
	require.Len(t, rows, 1, "B3 unsuccessful handoff must save exactly one ordinary system warning")
	row := rows[0]
	assert.NotEmpty(t, row.ID)
	assert.False(t, row.Timestamp.IsZero())
	assert.Equal(t, "", row.Status)
	assert.Equal(t, "", row.SystemSubtype)
	assert.Equal(t, want, row.Content)
	assert.NotContains(t, row.Content, b3PrivateBody)
	f.eventsMu.Lock()
	events := append([]Event(nil), f.events...)
	f.eventsMu.Unlock()
	errorsSeen := 0
	for _, e := range events {
		if p, ok := e.Payload.(ErrorPayload); e.Kind == EventKindError && ok && p.Stage == "async_notify_delivery" {
			errorsSeen++
			assert.Equal(t, f.sid, p.SessionID)
			assert.Equal(t, want, p.Message)
			assert.Nil(t, p.ProviderError)
			assert.Empty(t, p.Detail)
		}
	}
	assert.Equal(t, 1, errorsSeen, "saved warning must use the existing same-session typed error event")
	return row
}

func b3AssertOrigin(t *testing.T, f *b3Fixture, row session.TranscriptEntry) {
	t.Helper()
	out := b3DrainOutbound(f.bus)
	require.Len(t, out, 1, "positive actual origin enqueue and all foreign destinations must discriminate an empty bus")
	assert.Equal(t, bus.OutboundMessage{Channel: f.event.Channel, ChatID: f.event.ChatID, SessionID: f.sid, Content: row.Content}, out[0])
	assert.True(t, f.boundary.seen(steer.BoundaryAsyncToolFeedback, f.sid, steer.AudienceUser), "actual outward boundary must be observed")
}

func TestAsyncNotifierB3FailureProjection(t *testing.T) {
	t.Run("OriginOnlyNotLastConversationOrInstance", b3OriginOnly)
	t.Run("FullOutboundRetainsOriginalAndSecondaryFailure", func(t *testing.T) { b3RejectedWarning(t, false) })
	t.Run("ClosedOutboundIsRejectionNotPositiveReceipt", func(t *testing.T) { b3RejectedWarning(t, true) })
	for _, kind := range []string{"task", "verifier", "steered", "damaged", "recordless", "unwired", "nil-origin", "webchat", "internal", "unknown"} {
		t.Run("Audience_"+kind, func(t *testing.T) { b3ExcludedOrigin(t, kind) })
	}
	t.Run("GenericSourceIsFixedNotBodyIdentity", b3GenericSources)
	t.Run("MissingExplicitSessionNeverUsesLastSession", b3MissingTarget)
	t.Run("ActualNoticeSaveErrorRetainsPublishCause", b3SaveFailure)
}

func b3OriginOnly(t *testing.T) {
	f := b3New(t)
	for _, route := range [][2]string{{"telegram.b3-origin", "b3-foreign-chat"}, {"telegram.b3-foreign", "b3-origin-chat"}} {
		meta, err := f.al.GetSessionStore().NewChannelSession("telegram", route[0], route[1], testDefaultAgentID, "B3 decoy")
		require.NoError(t, err)
		require.NotEqual(t, f.sid, meta.ID)
	}
	require.NoError(t, f.al.RecordLastChannel("telegram.b3-foreign"))
	require.NoError(t, f.al.RecordLastChatID("b3-origin-chat"))
	b3FillInbound(t, f.bus)
	err := f.al.asyncNotifier.Notify(b3DeliveryDeadlineCtx(t), f.event)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NotErrorIs(t, err, bus.ErrBusClosed)
	n, q := f.counts()
	assert.Equal(t, 1, n)
	assert.Equal(t, 0, q)
	assert.Len(t, b3Rows(t, f.al, f.sid), 1, "origin warning must really be saved")
	assert.Equal(t, 1, len(f.bus.OutboundChan()), "real positive origin receipt required; a globally empty bus cannot prove foreign exclusion")
	rows := b3Logs(t, f.logPath, b3AttemptMessage)
	b3AssertDiagnostic(t, rows, false, "goal follow-up", f.sid, "", "")
	row := b3AssertSaved(t, f, "This goal follow-up notification did not wake the agent.")
	b3AssertOrigin(t, f, row)
	assert.Empty(t, b3Logs(t, f.logPath, b3RejectMessage))
	b3DrainInbound(f.bus)
}

func b3RejectedWarning(t *testing.T, closed bool) {
	f := b3New(t)
	notifyCtx := context.Background()
	if closed {
		f.bus.Close()
	} else {
		// Open bus: bound the caller deadline (see b3DeliveryDeadlineCtx).
		// The closed-bus branch fails fast without any real attempt and
		// needs no bound.
		b3FillInbound(t, f.bus)
		b3FillOutbound(t, f.bus)
		notifyCtx = b3DeliveryDeadlineCtx(t)
	}
	err := f.al.asyncNotifier.Notify(notifyCtx, f.event)
	require.Error(t, err)
	if closed {
		assert.ErrorIs(t, err, bus.ErrBusClosed)
	} else {
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.NotErrorIs(t, err, bus.ErrBusClosed)
	}
	assert.True(t, b3ErrorHasExact(err, b3RejectedText), "fixed secondary rejected-enqueue error must be preserved separately from the real inbound cause")
	n, q := f.counts()
	assert.Equal(t, 1, n)
	assert.Equal(t, 0, q)
	if !closed {
		assert.Equal(t, cap(f.bus.OutboundChan()), len(f.bus.OutboundChan()), "Notify returned before ANY outbound consumer repaired fullness")
	}
	reject := b3Logs(t, f.logPath, b3RejectMessage)
	assert.Len(t, reject, 1, "actual false TryPublishOutbound requires one distinct normal rejection record")
	if len(reject) > 0 {
		b3AssertDiagnostic(t, reject, true, "goal follow-up", f.sid, "", "")
	}
	b3AssertSaved(t, f, "This goal follow-up notification did not wake the agent.")
	for _, m := range b3DrainOutbound(f.bus) {
		assert.Equal(t, "telegram.filler", m.Channel, "no warning can be accepted into the full/closed queue")
	}
	b3DrainInbound(f.bus)
}

func b3ExcludedOrigin(t *testing.T, kind string) {
	f := b3New(t)
	switch kind {
	case "task", "verifier":
		origin := steer.Origin{Kind: steer.OriginKindTask, TaskID: "b3-real-task-input"}
		if kind == "verifier" {
			origin = steer.Origin{Kind: steer.OriginKindVerifier}
		}
		launched, err := NewSteerLauncher(f.al).Launch(context.Background(), steer.LaunchRequest{TargetAgentID: testDefaultAgentID, Task: "B3 internal origin", Origin: origin, WorkspaceID: testHarnessWorkspaceMembershipID, Owner: "b3-fixture-owner"})
		require.NoError(t, err)
		f.sid = launched.SessionID
	case "steered":
		root := newTestSteeringSession(t, f.al, testHarnessWorkspaceMembershipID)
		launched, err := NewSteerLauncher(f.al).Launch(context.Background(), steer.LaunchRequest{SteeringSessionID: root, TargetAgentID: testDefaultAgentID, Task: "B3 actual child", Origin: steer.Origin{Kind: steer.OriginKindDelegate}})
		require.NoError(t, err)
		f.sid = launched.SessionID
	case "damaged", "recordless":
		typ := session.SessionTypeChat
		if kind == "damaged" {
			typ = session.SessionTypeDelegate
		}
		meta, err := f.al.GetSessionStore().NewSession(typ, "telegram.b3-origin", testDefaultAgentID)
		require.NoError(t, err)
		f.sid = meta.ID
	case "nil-origin":
		require.NoError(t, f.al.GetSessionLifecycleStore().Mutate(f.sid, func(r *session.LifecycleRecord) error { r.Origin = nil; return nil }))
	case "unwired":
		f.al.SetSteerAudienceDeps(nil, f.boundary, nil)
	case "webchat":
		f.event.Channel = "webchat"
	case "internal":
		f.event.Channel = "system"
	case "unknown":
		f.event.Channel = "b3-unknown"
	}
	f.event.TranscriptSessionID = f.sid
	if kind != "unwired" {
		a, c, err := NewSteerAudienceResolver(NewSteerRecordClassifier(f.al.GetSessionLifecycleStore(), f.al.GetSessionStore())).Audience(context.Background(), f.sid)
		require.NoError(t, err)
		switch kind {
		case "damaged":
			require.Equal(t, steer.ClassDamagedChild, c)
			require.Equal(t, steer.AudienceNone, a)
		case "steered":
			require.Equal(t, steer.ClassSteered, c)
			require.Equal(t, steer.AudienceSteeringSession, a)
		default:
			require.Equal(t, steer.ClassOrdinaryRoot, c)
			require.Equal(t, steer.AudienceUser, a)
		}
	}
	b3FillInbound(t, f.bus)
	err := f.al.asyncNotifier.Notify(b3DeliveryDeadlineCtx(t), f.event)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, b3DrainOutbound(f.bus), "ineligible origin never gains inherited/last-channel human delivery")
	assert.Empty(t, b3Logs(t, f.logPath, b3RejectMessage), "skipped route is NOT a failed enqueue")
	n, q := f.counts()
	assert.Equal(t, 1, n)
	assert.Equal(t, 0, q)
	if kind == "unwired" {
		assert.True(t, f.boundary.seen(steer.BoundaryAsyncToolFeedback, f.sid, steer.AudienceNone), "strict new missing-resolver refusal must be observed")
	}
	if kind == "damaged" {
		f.eventsMu.Lock()
		for _, e := range f.events {
			if p, ok := e.Payload.(ErrorPayload); ok && e.Kind == EventKindError {
				assert.NotEqual(t, "async_notify_delivery", p.Stage)
			}
		}
		f.eventsMu.Unlock()
	}
	assert.Len(t, b3Rows(t, f.al, f.sid), 1, "addressed history is separate from stricter external authorization")
	b3DrainInbound(f.bus)
}

func b3GenericSources(t *testing.T) {
	for _, tc := range []struct{ source, label string }{{"goal_idle_settle", "goal follow-up"}, {"task_goal_loop", "task follow-up"}, {"plan_stopped_by_user", "plan update"}, {"message_parent:handback", "parent update"}, {"b3-untrusted-source", "asynchronous update"}} {
		t.Run(tc.label, func(t *testing.T) {
			f := b3New(t)
			f.event.SourceKind = tc.source
			f.bus.Close()
			err := f.al.asyncNotifier.Notify(context.Background(), f.event)
			assert.ErrorIs(t, err, bus.ErrBusClosed)
			b3AssertSaved(t, f, "This "+tc.label+" notification did not wake the agent.")
			b3AssertDiagnostic(t, b3Logs(t, f.logPath, b3AttemptMessage), false, tc.label, f.sid, "", "")
		})
	}
}
func b3MissingTarget(t *testing.T) {
	f := b3New(t)
	f.event.TranscriptSessionID = ""
	f.bus.Close()
	err := f.al.asyncNotifier.Notify(context.Background(), f.event)
	assert.ErrorIs(t, err, bus.ErrBusClosed)
	assert.Empty(t, b3Rows(t, f.al, f.sid))
	assert.Empty(t, b3DrainOutbound(f.bus))
	n, q := f.counts()
	assert.Equal(t, 1, n)
	assert.Equal(t, 0, q)
	b3AssertDiagnostic(t, b3Logs(t, f.logPath, b3AttemptMessage), false, "goal follow-up", "", "", "")
}

func b3ErrorHasExact(err error, text string) bool {
	if err == nil {
		return false
	}
	if err.Error() == text {
		return true
	}
	if many, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range many.Unwrap() {
			if b3ErrorHasExact(child, text) {
				return true
			}
		}
		return false
	}
	return b3ErrorHasExact(errors.Unwrap(err), text)
}

func b3SaveFailure(t *testing.T) {
	f := b3New(t)
	dir := filepath.Join(f.al.GetSessionStore().BaseDir(), f.sid)
	backup := dir + ".b3-original"
	require.NoError(t, os.Rename(dir, backup))
	var once sync.Once
	restore := func() {
		once.Do(func() { require.NoError(t, os.Remove(dir)); require.NoError(t, os.Rename(backup, dir)) })
	}
	require.NoError(t, os.WriteFile(dir, []byte("B3 deterministic not-a-directory save fault"), 0600))
	t.Cleanup(restore)
	f.bus.Close()
	err := f.al.asyncNotifier.Notify(context.Background(), f.event)
	assert.ErrorIs(t, err, bus.ErrBusClosed, "original actual publish cause must survive a secondary storage failure")
	var pathErr *os.PathError
	assert.True(t, errors.As(err, &pathErr), "real addressed transcript I/O failure must also be returned, never log-only")
	restore()
	assert.Empty(t, b3Rows(t, f.al, f.sid), "a failed save cannot claim a durable warning")
	n, q := f.counts()
	assert.Equal(t, 1, n)
	assert.Equal(t, 0, q)
}
