//go:build goolm && stdjson

package gateway

// Order 58 / CHECK F17/F21. Oracle: skills-tools-redesign-spec.md::C-26,
// S-119/S-124/S-125. These are real handler/adapter/store/frame component
// tests, not a launched task, gateway restart or native sandbox acceptance.
// Only a wall/monotonic clock input may be injected. There is no tolerance
// around a specified duration and no callback supplying coverage/audit rows.
// The absent controlled clock remains loud. Gateway lifecycle and automatic
// egress expiry are separate obligations (ST-29/ST-30), never inferred here.
// GREEN, implementation mutations and proof-of-failability defer to CHECK.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type timedCardHarness struct {
	api     *restAPI
	reg     *approvalRegistryV2
	handler *WSHandler
	next    int
}

func newTimedCardHarness(t *testing.T) *timedCardHarness {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvConfig, filepath.Join(home, "config.json"))
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.List = nil
	cfg.Sandbox.AuditLog = true
	cfg.Sandbox.AutoApprove, cfg.Sandbox.GodMode = false, false
	cfg.Sandbox.ToolPolicies["delete_task"] = "ask"
	for _, id := range []string{"test-agent", "peer-agent"} {
		record := config.AgentConfig{ID: id, Name: id, Type: config.AgentTypeCustom, Tools: coreagent.NewCustomAgentToolsCfg()}
		require.NoError(t, agentstore.New(home).Create(id, &record))
		cfg.Agents.List = append(cfg.Agents.List, record)
	}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), raw, 0o600))
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	require.NotNil(t, al.AuditLogger(), "fixture: actual loop-owned app-home audit logger")
	reg := newApprovalRegistryV2(64, 30*time.Second)
	handler := newWSHandler(msgBus, al, "")
	handler.approvalRegV2 = reg
	al.SetToolApprover(newPolicyApproverAdapter(reg, handler))
	t.Cleanup(handler.Wait)
	return &timedCardHarness{api: &restAPI{agentLoop: al, homePath: home, approvalReg: reg, auditor: al.AuditLogger()}, reg: reg, handler: handler}
}

type timedCardWaiter struct{ approved, recorded bool }

// Use the actual policy approver admission and waiter, not requestApproval in
// the test. No scripted result decides whether the HTTP answer released it.
func (h *timedCardHarness) card(t *testing.T, tool string) (*approvalEntry, <-chan timedCardWaiter) {
	t.Helper()
	h.next++
	callNumber := h.next
	meta, err := h.api.agentLoop.GetSessionStore().NewSession(session.SessionTypeChat, "web", "test-agent")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan timedCardWaiter, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		approved, _, recorded := newPolicyApproverAdapter(h.reg, h.handler).RequestApproval(ctx, agent.PolicyApprovalReq{
			ToolCallID: fmt.Sprintf("timed-call-%d", callNumber), ToolName: tool,
			Args: map[string]any{"task_id": "t1"}, AgentID: "test-agent",
			SessionID: meta.ID, TurnID: fmt.Sprintf("timed-turn-%d", callNumber),
		})
		result <- timedCardWaiter{approved: approved, recorded: recorded}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("fixture: actual policy approver did not join after cancellation")
		}
	})
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		if entries := h.reg.pendingApprovals(); len(entries) > 0 {
			require.Len(t, entries, 1, "one actual admitted call")
			return entries[0], result
		}
		select {
		case <-finished:
			t.Fatal("fixture: real approver completed without the pending card")
		case <-deadline.C:
			t.Fatal("fixture UNVERIFIED: no native pending-card admission")
		case <-tick.C:
		}
	}
}

func awaitTimedCard(t *testing.T, result <-chan timedCardWaiter, recorded bool) {
	t.Helper()
	select {
	case got := <-result:
		assert.True(t, got.approved, "actual pending call released as approved")
		assert.Equal(t, recorded, got.recorded, "actual approver's remember decision")
	case <-time.After(5 * time.Second):
		t.Fatal("native waiter did not release after the HTTP answer")
	}
}

func answerTimedCard(t *testing.T, h *timedCardHarness, duration string) (gen.ToolApprovalResponse, *approvalEntry) {
	t.Helper()
	entry, result := h.card(t, "delete_task")
	w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"allow_for","duration":"`+duration+`"}`)
	require.Equal(t, http.StatusOK, w.Code, "C-26/S-124 actual timed answer: %s", w.Body.String())
	var response gen.ToolApprovalResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, entry.ApprovalID, response.ApprovalId)
	assert.Equal(t, gen.ToolApprovalResponseActionAllowFor, response.Action)
	assert.Equal(t, gen.ToolApprovalResponseStatusOk, response.Status)
	require.Equal(t, boolPtr(true), response.GrantRecorded)
	require.NotNil(t, response.ExpiresAt)
	awaitTimedCard(t, result, true)
	return response, entry
}

func controlledTimedCardClock(t *testing.T, h *timedCardHarness) *scheduledAllowanceClock {
	t.Helper()
	clock := &scheduledAllowanceClock{wall: time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC)}
	nativebinding.Require(t, "pkg/agent", injectScheduledAllowanceClock,
		"C-26 Length/S-124: named production loop-to-store wall/monotonic input; no approximate or synthetic end")
	injectScheduledAllowanceClock(h.api.agentLoop, clock.wallNow, clock.elapsedNow)
	return clock
}

func TestToolApprovals_TimedActions(t *testing.T) {
	t.Run("instrument native requested and approve-once audit under the actual app home", func(t *testing.T) {
		h := newTimedCardHarness(t)
		entry, result := h.card(t, "delete_task")
		w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"allow_once"}`)
		require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
		awaitTimedCard(t, result, false)
		rows := readAllAuditEntries(t, filepath.Join(h.api.homePath, "system"))
		assertTimedDecisionIdentity(t, rows, entry, "", "", time.Time{})
	})
	t.Run("instrument app-home observer detects a new path and changed bytes", testTimedCardTreeObserver)
	t.Run("S-119 native ordinary card frame", func(t *testing.T) {
		h := newTimedCardHarness(t)
		entry, _ := h.card(t, "delete_task")
		snap := h.handler.pendingApprovalSnapshot(entry)
		assert.Equal(t, []string{"allow_once", "allow_for", "deny", "cancel"}, snap.OfferedActions)
		assert.Equal(t, []string{"1h", "3h", "1d"}, snap.AllowDurations)
	})
	t.Run("S-124 native timed HTTP answer releases actual waiter", func(t *testing.T) {
		h := newTimedCardHarness(t)
		answerTimedCard(t, h, "1h")
	})
	for _, row := range []struct {
		name   string
		length time.Duration
	}{{"1h", time.Hour}, {"3h", 3 * time.Hour}, {"1d", 24 * time.Hour}} {
		t.Run("C-26 exact duration and boundary "+row.name, func(t *testing.T) {
			h := newTimedCardHarness(t)
			clock := controlledTimedCardClock(t, h)
			start := clock.wallNow()
			response, _ := answerTimedCard(t, h, row.name)
			assert.Equal(t, start.Add(row.length), response.ExpiresAt.UTC(), "exact server wall end, never ±5 minutes")
			assertTimedCardCoverage(t, h, true)
			clock.advance(row.length - time.Nanosecond)
			assertTimedCardCoverage(t, h, true)
			// Wall jumps cannot expire or extend the monotonic lifetime.
			clock.mu.Lock()
			clock.wall = clock.wall.Add(48 * time.Hour)
			clock.mu.Unlock()
			assertTimedCardCoverage(t, h, true)
			clock.mu.Lock()
			clock.wall = clock.wall.Add(-96 * time.Hour)
			clock.mu.Unlock()
			assertTimedCardCoverage(t, h, true)
			clock.advance(time.Nanosecond)
			assertTimedCardCoverage(t, h, false)
			clock.advance(time.Nanosecond)
			assertTimedCardCoverage(t, h, false)
		})
	}
	t.Run("C-26 second answer never shortens then extends the actual record", testTimedCardLaterEnd)
	t.Run("C-26 HTTP writes no allowance storage anywhere in app home", func(t *testing.T) {
		h := newTimedCardHarness(t)
		entry, result := h.card(t, "delete_task")
		before := timedCardHomeTree(t, h.api.homePath)
		w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"allow_for","duration":"1h"}`)
		require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
		awaitTimedCard(t, result, true)
		assert.Equal(t, before, timedCardHomeTree(t, h.api.homePath), "C-26: no file/config key/agent field/new storage object; only actual audit JSONL excluded")
		assertTimedCardCoverage(t, h, true)
	})
	t.Run("C-26 audit has exact native event action actor subject and end", func(t *testing.T) {
		h := newTimedCardHarness(t)
		clock := controlledTimedCardClock(t, h)
		start := clock.wallNow()
		_, entry := answerTimedCard(t, h, "3h")
		assertTimedDecisionIdentity(t, readAllAuditEntries(t, filepath.Join(h.api.homePath, "system")), entry, "allow_for", "3h", start.Add(3*time.Hour))
	})
	t.Run("S-124 each retired or malformed answer preserves the same card", testTimedCardMalformed)
	t.Run("S-125 always-ask keeps one-request actions and refuses time", func(t *testing.T) {
		h := newTimedCardHarness(t)
		entry, _ := h.card(t, "set_channel_outbound")
		snap := h.handler.pendingApprovalSnapshot(entry)
		assert.Equal(t, []string{"allow_once", "deny", "cancel"}, snap.OfferedActions)
		assert.Empty(t, snap.AllowDurations)
		w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"allow_for","duration":"1h"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code, "%s", w.Body.String())
		assert.Contains(t, w.Body.String(), "always asks for approval", "S-125 retains the existing always-ask refusal")
		assert.Equal(t, entry, h.reg.get(entry.ApprovalID), "refused time choice cannot consume the card")
		assert.False(t, h.api.agentLoop.ApprovalGrants().IsAllowed(entry.SessionID, entry.AgentID, entry.ToolName, entry.Args))
	})
}

func assertTimedCardCoverage(t *testing.T, h *timedCardHarness, active bool) {
	t.Helper()
	store := h.api.agentLoop.ApprovalGrants()
	for _, sessionID := range []string{"another-chat", "self-helper", "unattended-run"} {
		for _, task := range []string{"t1", "different-arguments"} {
			assert.Equal(t, active, store.IsAllowed(sessionID, "test-agent", "delete_task", map[string]any{"task_id": task}), "C-26: actual agent/tool key, session=%s task=%s", sessionID, task)
			assert.False(t, store.IsAllowed(sessionID, "peer-agent", "delete_task", map[string]any{"task_id": task}), "C-26: peer never borrows this answer")
		}
	}
}

func testTimedCardLaterEnd(t *testing.T) {
	h := newTimedCardHarness(t)
	clock := controlledTimedCardClock(t, h)
	start := clock.wallNow()
	first, _ := answerTimedCard(t, h, "3h")
	assert.Equal(t, start.Add(3*time.Hour), first.ExpiresAt.UTC())
	clock.advance(30 * time.Minute)
	shorter, _ := answerTimedCard(t, h, "1h")
	assert.Equal(t, start.Add(3*time.Hour), shorter.ExpiresAt.UTC(), "later end wins; a shorter answer cannot truncate consent")
	longer, _ := answerTimedCard(t, h, "1d")
	end := start.Add(24*time.Hour + 30*time.Minute)
	assert.Equal(t, end, longer.ExpiresAt.UTC())
	clock.advance(24*time.Hour - time.Nanosecond)
	assertTimedCardCoverage(t, h, true)
	clock.advance(time.Nanosecond)
	assertTimedCardCoverage(t, h, false)
	clock.advance(time.Nanosecond)
	assertTimedCardCoverage(t, h, false)
}

func testTimedCardMalformed(t *testing.T) {
	for _, row := range []struct{ body, reason string }{
		{`{"action":"allow"}`, "This choice is no longer offered. Choose Approve Once or a time limit."},
		{`{"action":"allow_for"}`, "Choose 1 hour, 3 hours or 1 day."},
		{`{"action":"allow_for","duration":"2h"}`, "Choose 1 hour, 3 hours or 1 day."},
		{`{"action":"allow_once","duration":"1h"}`, "A time limit applies only to Allow for."},
	} {
		t.Run(row.body, func(t *testing.T) {
			h := newTimedCardHarness(t)
			entry, result := h.card(t, "delete_task")
			before := timedCardHomeTree(t, h.api.homePath)
			w := postToolApprovalRaw(t, h.api, entry.ApprovalID, row.body)
			assert.Equal(t, http.StatusBadRequest, w.Code, "%s", w.Body.String())
			var refusal gen.ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &refusal))
			assert.Equal(t, row.reason, refusal.Error)
			require.Equal(t, entry, h.reg.get(entry.ApprovalID), "same card, not a replacement")
			h.reg.mu.Lock()
			state := entry.state
			h.reg.mu.Unlock()
			assert.Equal(t, ApprovalStatePending, state)
			assertTimedCardCoverage(t, h, false)
			assert.Equal(t, before, timedCardHomeTree(t, h.api.homePath), "refused choice changes no persisted state")
			for _, auditRow := range readAllAuditEntries(t, filepath.Join(h.api.homePath, "system")) {
				assert.NotEqual(t, audit.EventToolPolicyAskGranted, auditRow["event"], "refused choice cannot grant consent")
			}
			w = postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"allow_for","duration":"1h"}`)
			require.Equal(t, http.StatusOK, w.Code, "same original pending card can still accept a valid answer: %s", w.Body.String())
			awaitTimedCard(t, result, true)
		})
	}
}

func assertTimedDecisionIdentity(t *testing.T, rows []map[string]any, entry *approvalEntry, action, duration string, end time.Time) {
	t.Helper()
	requested, granted := 0, 0
	for _, row := range rows {
		// Consent emitters use audit.Record.Fields, not legacy audit.Entry.Details.
		// Read the production envelope; keep the spec-derived identity exact.
		details, _ := row["fields"].(map[string]any)
		if details["approval_id"] != entry.ApprovalID {
			continue
		}
		switch row["event"] {
		case audit.EventToolPolicyAskRequested:
			requested++
		case audit.EventToolPolicyAskGranted:
			granted++
			assert.Equal(t, entry.ToolCallID, details["tool_call_id"])
			assert.Equal(t, entry.SessionID, details["session_id"])
			assert.Equal(t, entry.TurnID, details["turn_id"])
			assert.Equal(t, "admin", details["approver_user_id"])
			assert.Equal(t, "test-agent", details["agent_id"])
			assert.Equal(t, "delete_task", details["tool_name"])
			if action != "" {
				assert.Equal(t, action, details["action"])
				assert.Equal(t, duration, details["duration"])
				rawEnd, ok := details["expires_at"].(string)
				require.True(t, ok, "C-26 actual audit has a date-time end, not an existence-only check")
				actualEnd, err := time.Parse(time.RFC3339Nano, rawEnd)
				require.NoError(t, err)
				assert.Equal(t, end, actualEnd.UTC())
			}
		}
	}
	assert.Equal(t, 1, requested, "known-positive instrument: one actual request audit")
	assert.Equal(t, 1, granted, "one actual call-bound granted event, not any row with nonempty expiry")
}

type timedHomeEntry struct {
	Mode        fs.FileMode
	Bytes, Link string
}

func timedCardHomeTree(t *testing.T, home string) map[string]timedHomeEntry {
	t.Helper()
	tree := map[string]timedHomeEntry{}
	err := filepath.WalkDir(home, func(path string, de fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(home, path)
		if err != nil {
			return err
		}
		// Exactly the real logger's append-only output, not a broad system-dir
		// exclusion: a new allowance file in that directory must be detected.
		if rel == filepath.Join("system", "audit.jsonl") {
			return nil
		}
		info, err := de.Info()
		if err != nil {
			return err
		}
		item := timedHomeEntry{Mode: info.Mode()}
		switch {
		case info.Mode().IsRegular():
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			item.Bytes = string(raw)
		case info.Mode()&os.ModeSymlink != 0:
			item.Link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		tree[rel] = item
		return nil
	})
	require.NoError(t, err, "an unenumerated subtree is not memory-only proof")
	return tree
}

func testTimedCardTreeObserver(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.json")
	require.NoError(t, os.WriteFile(path, []byte("before"), 0o600))
	before := timedCardHomeTree(t, home)
	require.NoError(t, os.Mkdir(filepath.Join(home, "system"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, "system", "unexpected-allowance.json"), []byte("persisted"), 0o600))
	assert.NotEqual(t, before, timedCardHomeTree(t, home), "instrument sees newly added paths anywhere")
	require.NoError(t, os.Remove(filepath.Join(home, "system", "unexpected-allowance.json")))
	require.NoError(t, os.Remove(filepath.Join(home, "system")))
	require.NoError(t, os.WriteFile(path, []byte("after"), 0o600))
	assert.NotEqual(t, before, timedCardHomeTree(t, home), "instrument sees changed bytes of preexisting state")
}

// Keep this shared observer's signature for order 52 and permission-change
// tests. Callers must establish the positive audit instrument before absence.
func readAllAuditEntries(t *testing.T, auditDir string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(auditDir, "audit.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry), "line=%s", line)
		out = append(out, entry)
	}
	return out
}
