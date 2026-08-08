// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// loop_command_stop_frame_test.go — regression coverage for the fix-wave
// HIGH finding: stopLoop (loop_command.go) used to emit
// emitLoopStatusFrame(sessionID, "", 0, 0, nil, "stopped") — a "stopped"
// loop_status frame with mode="" and max_runs=0. The LoopStatusFrame
// contract (contracts/components/schemas/LoopStatusFrame.yaml) requires
// mode to be one of "interval"/"self_paced" and max_runs >= 1, so the
// generated strict zod schema at the SPA edge silently dropped every stop
// frame and the loop status pill never cleared.
//
// The fix has every stopLoop caller pass the loop's OWN prior mode/
// max_runs/run (all already in hand at the call site, read before the
// state-clearing SetMeta call) so the "stopped" frame reports a real,
// schema-valid prior state instead of the post-clear zeroed one. A
// priorMode of "" (no loop was actually active) now suppresses the frame
// entirely rather than emitting an invalid one.
//
// These tests drive every real production call path to stopLoop —
// stopLoopCommand (user `/loop stop`), the replace-on-set branch
// (`/loop <new prompt>` over an active loop), LoopScheduler.RunScheduled's
// run-cap brake, and LoopScheduler.IdleExpirySweep — through the real
// AgentLoop/LoopScheduler/UnifiedStore machinery (mirroring this package's
// existing goal_loop_test.go loop tests), captures the REAL emitted
// agent.LoopStatusChangedPayload off a REAL event-bus subscription, converts
// it into the generated wire type EXACTLY the way pkg/gateway/websocket.go's
// EventKindLoopStatusChanged case does, and validates the resulting JSON
// against the real, compiled LoopStatusFrame.yaml component schema — the
// same jsonschema/v6 + yaml.v3 pattern pkg/api/generated/contract_test.go
// and pkg/gateway/sessions_wire_test.go use for wire-format conformance.
//
// Per this codebase's convention for parallel-wave-safe test authorship
// (see loop_adr057_test.go's "Per binding Rule 5" note), this is a NEW file
// reusing goal_loop_test.go's existing test helpers (newGoalLoopTestLoop,
// newGoalTestSession, newLoopSchedulerForTest, strPtrForTest, intPtr) and
// eventbus_test.go's existing helpers (collectEventStream, waitForEvent)
// rather than editing either file.
package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// ---------------------------------------------------------------------
// LoopStatusFrame.yaml schema compilation (mirrors sessions_wire_test.go /
// pkg/api/generated/contract_test.go's pattern; kept package-local since
// no such helper exists yet in pkg/agent).
// ---------------------------------------------------------------------

// loopStatusFrameYAMLLoader resolves file:// URLs by parsing YAML into
// map[string]any — the default jsonschema/v6 loader only reads JSON.
type loopStatusFrameYAMLLoader struct{}

func (loopStatusFrameYAMLLoader) Load(url string) (any, error) {
	path := strings.TrimPrefix(url, "file://")
	data, err := os.ReadFile(path) //nolint:gosec // test-only, path built from runtime.Caller
	if err != nil {
		return nil, err
	}
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return loopStatusFrameJSONifyYAML(v), nil
}

// loopStatusFrameJSONifyYAML converts yaml.v3's map[string]interface{} tree
// (already string-keyed for mapping nodes, unlike gopkg.in/yaml.v2) into a
// form jsonschema/v6 accepts, recursing through slices too.
func loopStatusFrameJSONifyYAML(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			out[k] = loopStatusFrameJSONifyYAML(val)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = loopStatusFrameJSONifyYAML(val)
		}
		return out
	default:
		return v
	}
}

// loadLoopStatusFrameSchema compiles contracts/components/schemas/
// LoopStatusFrame.yaml once per call, resolved relative to this test file's
// own location so it works regardless of the test runner's cwd.
func loadLoopStatusFrameSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller must resolve this file's path")
	schemaPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "contracts", "components", "schemas", "LoopStatusFrame.yaml")

	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(loopStatusFrameYAMLLoader{})
	schema, err := compiler.Compile("file://" + schemaPath)
	require.NoError(t, err, "must be able to compile LoopStatusFrame.yaml")
	return schema
}

// loopStatusFrameFromPayload converts an agent.LoopStatusChangedPayload into
// the generated wire type, EXACTLY mirroring pkg/gateway/websocket.go's
// EventKindLoopStatusChanged case (the only real construction site) so this
// test validates the actual production shape, not an approximation of it.
func loopStatusFrameFromPayload(p LoopStatusChangedPayload) generated.LoopStatusFrame {
	f := generated.LoopStatusFrame{
		Type:      string(generated.WsFrameTypeLoopStatus),
		SessionId: p.SessionID,
		Mode:      p.Mode,
		Run:       p.Run,
		MaxRuns:   p.MaxRuns,
		State:     p.State,
	}
	if p.NextDelay != nil {
		nd := int64(*p.NextDelay)
		f.NextDelay = &nd
	}
	return f
}

// assertValidLoopStatusFrame validates payload's wire translation against
// the real compiled schema and pins the two fields the HIGH finding named:
// state=="stopped" and mode is one of the schema's two enum values.
func assertValidLoopStatusFrame(t *testing.T, schema *jsonschema.Schema, p LoopStatusChangedPayload) {
	t.Helper()
	frame := loopStatusFrameFromPayload(p)
	raw, err := json.Marshal(frame)
	require.NoError(t, err)
	var instance any
	require.NoError(t, json.Unmarshal(raw, &instance))
	assert.NoError(t, schema.Validate(instance),
		"stopped loop_status frame must validate against LoopStatusFrame.yaml: %s", string(raw))
	assert.Equal(t, "stopped", p.State)
	assert.Contains(t, []string{loopModeInterval, loopModeSelfPaced}, p.Mode,
		"mode must be a valid enum member, not the post-clear empty string")
	assert.GreaterOrEqual(t, p.MaxRuns, 1, "max_runs must satisfy the schema's minimum:1")
}

// waitForLoopStoppedEvent blocks for the next EventKindLoopStatusChanged
// event whose State=="stopped".
func waitForLoopStoppedEvent(t *testing.T, ch <-chan Event, timeout time.Duration) LoopStatusChangedPayload {
	t.Helper()
	evt := waitForEvent(t, ch, timeout, func(e Event) bool {
		if e.Kind != EventKindLoopStatusChanged {
			return false
		}
		p, ok := e.Payload.(LoopStatusChangedPayload)
		return ok && p.State == "stopped"
	})
	p, ok := evt.Payload.(LoopStatusChangedPayload)
	require.True(t, ok, "expected LoopStatusChangedPayload, got %T", evt.Payload)
	return p
}

// ---------------------------------------------------------------------
// stopLoopCommand (`/loop stop`) — interval and self-paced modes.
// ---------------------------------------------------------------------

func TestLoopCommand_Stop_Interval_EmitsSchemaValidStoppedFrame(t *testing.T) {
	schema := loadLoopStatusFrameSchema(t)
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	require.True(t, ok)
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	ls, _ := newLoopSchedulerForTest(t, al)
	al.SetLoopScheduler(ls)

	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	matched, handled, reply := al.applyLoopCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/loop every 1m ping", UserInitiated: true}, agentInst, &opts)
	require.True(t, matched && handled)
	require.Contains(t, reply, "Loop started")

	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	matched, handled, reply = al.applyLoopCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/loop stop", UserInitiated: true}, agentInst, &opts)
	require.True(t, matched && handled)
	assert.Equal(t, "Loop stopped.", reply)

	payload := waitForLoopStoppedEvent(t, sub.C, 2*time.Second)
	assert.Equal(t, sid, payload.SessionID)
	assert.Equal(t, loopModeInterval, payload.Mode)
	assertValidLoopStatusFrame(t, schema, payload)
}

func TestLoopCommand_Stop_SelfPaced_EmitsSchemaValidStoppedFrame(t *testing.T) {
	schema := loadLoopStatusFrameSchema(t)
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	require.True(t, ok)
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	ls, _ := newLoopSchedulerForTest(t, al)
	al.SetLoopScheduler(ls)

	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	matched, handled, reply := al.applyLoopCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/loop check on the deploy", UserInitiated: true}, agentInst, &opts)
	require.True(t, matched && handled)
	require.Contains(t, reply, "Self-paced loop started")

	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	matched, handled, reply = al.applyLoopCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/loop stop", UserInitiated: true}, agentInst, &opts)
	require.True(t, matched && handled)
	assert.Equal(t, "Loop stopped.", reply)

	payload := waitForLoopStoppedEvent(t, sub.C, 2*time.Second)
	assert.Equal(t, loopModeSelfPaced, payload.Mode)
	assertValidLoopStatusFrame(t, schema, payload)
}

// TestStopLoopCommand_NoActiveLoop_NoFrameEmitted is the negative case:
// `/loop stop` with nothing running must neither crash nor emit a
// schema-invalid all-zero frame — stopLoop's priorMode=="" guard must
// suppress the frame entirely.
func TestStopLoopCommand_NoActiveLoop_NoFrameEmitted(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	require.True(t, ok)
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	ls, _ := newLoopSchedulerForTest(t, al)
	al.SetLoopScheduler(ls)

	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	reply := al.stopLoopCommand(sid, store, ls)
	assert.Equal(t, "No active loop to stop.", reply)

	// Give any (wrongly) emitted event a moment to land, then confirm the
	// bus carries no loop_status frame at all for this session.
	time.Sleep(50 * time.Millisecond)
	for _, evt := range collectEventStream(sub.C) {
		if evt.Kind == EventKindLoopStatusChanged {
			t.Fatalf("no loop_status frame should be emitted when no loop was active, got %+v", evt.Payload)
		}
	}
}

// ---------------------------------------------------------------------
// Replace-on-set (`/loop <new>` over an already-active loop).
// ---------------------------------------------------------------------

func TestLoopCommand_ReplaceOnSet_EmitsSchemaValidStoppedFrameForOldLoop(t *testing.T) {
	schema := loadLoopStatusFrameSchema(t)
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	require.True(t, ok)
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	ls, _ := newLoopSchedulerForTest(t, al)
	al.SetLoopScheduler(ls)

	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	matched, handled, reply := al.applyLoopCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/loop every 1m ping", UserInitiated: true}, agentInst, &opts)
	require.True(t, matched && handled)
	require.Contains(t, reply, "Loop started")

	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	// Replacing with a self-paced loop stops the old interval loop first.
	matched, handled, reply = al.applyLoopCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/loop check on something else", UserInitiated: true}, agentInst, &opts)
	require.True(t, matched && handled)
	require.Contains(t, reply, "Self-paced loop started")

	payload := waitForLoopStoppedEvent(t, sub.C, 2*time.Second)
	assert.Equal(t, loopModeInterval, payload.Mode, "the replaced-away OLD loop was interval-mode")
	assertValidLoopStatusFrame(t, schema, payload)

	after, err := store.GetMeta(sid)
	require.NoError(t, err)
	assert.Equal(t, loopModeSelfPaced, after.LoopMode, "the NEW loop must now be active")
}

// ---------------------------------------------------------------------
// LoopScheduler.RunScheduled's run-cap brake.
// ---------------------------------------------------------------------

func TestLoopScheduler_RunCapBoundary_EmitsSchemaValidStoppedFrame(t *testing.T) {
	schema := loadLoopStatusFrameSchema(t)
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) {
		cfg.Planning.LoopMaxRuns = 1
	})
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	require.True(t, ok)
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	ls, clk := newLoopSchedulerForTest(t, al)
	al.SetLoopScheduler(ls)

	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	matched, handled, _ := al.applyLoopCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/loop every 1m ping", UserInitiated: true}, agentInst, &opts)
	require.True(t, matched && handled)

	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	clk.Advance(time.Minute)
	ls.RunDueJobs(clk.Now())
	ls.WaitForLane()

	payload := waitForLoopStoppedEvent(t, sub.C, 2*time.Second)
	assert.Equal(t, loopModeInterval, payload.Mode)
	assert.Equal(t, 1, payload.MaxRuns)
	assert.Equal(t, 1, payload.Run, "run must report the run that just hit the cap, not 0")
	assertValidLoopStatusFrame(t, schema, payload)

	after, err := store.GetMeta(sid)
	require.NoError(t, err)
	assert.Empty(t, after.LoopMode)
}

// ---------------------------------------------------------------------
// LoopScheduler.IdleExpirySweep.
// ---------------------------------------------------------------------

func TestLoopScheduler_IdleExpirySweep_EmitsSchemaValidStoppedFrame(t *testing.T) {
	schema := loadLoopStatusFrameSchema(t)
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	require.True(t, ok)
	store := al.GetSessionStore()
	require.NotNil(t, store)
	ls, _ := newLoopSchedulerForTest(t, al)
	al.SetLoopScheduler(ls)

	now := time.Now().UTC()
	expiredAt := now.Add(-7 * 24 * time.Hour).Format(time.RFC3339)

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentInst.ID)
	require.NoError(t, err)
	sid := meta.ID
	jobID, err := ls.AddInterval(agentInst.ID, sid, 5*60*1000)
	require.NoError(t, err)
	mode := loopModeInterval
	everyMS := int64(5 * 60 * 1000)
	require.NoError(t, store.SetMeta(sid, session.MetaPatch{
		LoopMode:           &mode,
		LoopPrompt:         strPtrForTest("ping"),
		LoopRunCount:       intPtr(3),
		LoopMaxRuns:        intPtr(10),
		LoopIntervalMS:     &everyMS,
		LoopJobID:          &jobID,
		LoopStartedAt:      &expiredAt,
		LoopLastActivityAt: &expiredAt,
	}))

	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	ls.IdleExpirySweep(config.PlanningConfig{}, now)

	payload := waitForLoopStoppedEvent(t, sub.C, 2*time.Second)
	assert.Equal(t, sid, payload.SessionID)
	assert.Equal(t, loopModeInterval, payload.Mode)
	assert.Equal(t, 10, payload.MaxRuns)
	assert.Equal(t, 3, payload.Run)
	assertValidLoopStatusFrame(t, schema, payload)
}

// ---------------------------------------------------------------------
// stopLoop's defensive max_runs>=1 fallback (unreachable via the real
// start-a-loop path, since /loop always snapshots a >=1 max_runs at set
// time — exercised directly at the white-box level to pin the fallback
// itself, matching RunScheduled's own defaulting one function over).
// ---------------------------------------------------------------------

func TestStopLoop_ZeroPriorMaxRuns_FallsBackToDefault_StillSchemaValid(t *testing.T) {
	schema := loadLoopStatusFrameSchema(t)
	al, cleanup := newAL(t)
	defer cleanup()
	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "test-agent")
	require.NoError(t, err)
	sid := meta.ID

	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	al.stopLoop(sid, store, loopModeInterval, 0, 3, "test: zero prior max_runs")

	payload := waitForLoopStoppedEvent(t, sub.C, 2*time.Second)
	assert.Equal(t, config.DefaultLoopMaxRuns, payload.MaxRuns)
	assertValidLoopStatusFrame(t, schema, payload)
}
