//go:build goolm && stdjson

package agent

// R1 RED plan, #1081: derive checkpoint outcomes from ADR-066's 2026-09-30
// amendment (MAJ-CW-001/002/004/005/006/007/012), not existing implementation.
// Real units: admission, finishCall, request assembly, beginIteration, retries,
// JSONL persistence, projection/reload, and final provider adapters. The HTTP
// server is the network edge; the atomicity test obstructs real filesystem writes.
// Cases: repeated total/share/both pressure; equality; incomplete/completed groups;
// Unicode newest relief; original user/media anchor; write failure; actual-start
// abort with/without appends; steering inject/slide/send and pre-send failure;
// locally fitting rejection, ordered forced relief, exact halving, no progress,
// existing retry ceiling; four final adapter bodies and partial-group erasure.
// Negative cases deliberately include partial groups, write errors, aborted turns,
// pre-send failure, repeated rejection, exhausted text and mark-overhead growth.
// CHECK (fresh instance) owns GREEN and mutation: remove a checkpoint, cut half a
// group, refresh the snapshot, halve a mark, resend unchanged, reset the counter,
// or silently sanitize away a retained group. No mutations in this RED lane.
// Gaps: R2 archive-range recall; Settings HTTP; Q33/terminal integration; unit 3
// exact transient notice text/byte exclusion; control-plane restart/receipt
// redesign is outside R1. Joint UAT is pending, not executed by this pack.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

type cwR1Harness struct {
	al    *AgentLoop
	agent *AgentInstance
	store *session.UnifiedStore
	cfg   *config.Config
	dir   string
	key   string
	// turnsMu guards turns: every turn state this harness minted, so a
	// pre-seeded assistant call can be registered as the issuer of its tool
	// results on ALL of them (effects design step 2 requires the turn to know
	// the exact issuing assistant before a result is admitted).
	turnsMu sync.Mutex
	turns   []*turnState
}

func cwR1New(t *testing.T, window int) *cwR1Harness {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	dir := filepath.Join(home, "context")
	store, err := session.NewUnifiedStore(dir)
	require.NoError(t, err, "real JSONL fixture must open")
	t.Cleanup(func() { require.NoError(t, store.Close(), "close real store") })
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	cfg := &config.Config{Context: config.DefaultContextSettings()}
	cfg.Agents.Defaults.Home = home
	cfg.Agents.Defaults.MaxToolIterations = 30
	cb := NewContextBuilder(home)
	// Prime the real static prompt before deriving the budget. No fake prompt,
	// cap-policy hook, checkpoint override, or production test flag is installed.
	cb.BuildSystemPromptWithCache()
	agent := &AgentInstance{
		ID: "r1", Name: "R1", Home: home, Model: "scripted-model",
		ContextWindow: window, MaxTokens: 256, MaxIterations: 30,
		ContextBuilder: cb, Sessions: store,
		Tools: tools.NewToolRegistry(),
	}
	return &cwR1Harness{
		al: &AgentLoop{cfg: cfg, bus: msgBus}, agent: agent,
		store: store, cfg: cfg, dir: dir, key: "cw-r1",
	}
}

func (h *cwR1Harness) turn(user string) *turnState {
	ts := newTurnState(h.agent, processOptions{
		SessionKey: h.key, UserMessage: user, Channel: "scheduled",
		SkipInitialSteeringPoll: true,
	}, turnEventScope{turnID: "cw-r1-turn"})
	h.turnsMu.Lock()
	h.turns = append(h.turns, ts)
	h.turnsMu.Unlock()
	return ts
}

func (h *cwR1Harness) append(t *testing.T, msgs ...providers.Message) {
	t.Helper()
	for _, m := range msgs {
		h.store.AddFullMessage(h.key, m)
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		// The just-appended assistant record is the issuer of these calls: the
		// address its own checked append assigned is the last live slot.
		view, err := h.store.WindowView(context.Background(), h.key)
		require.NoError(t, err, "read the window to address the appended assistant")
		require.NotEmpty(t, view.Live, "the appended assistant must occupy a live slot")
		addr := view.Live[len(view.Live)-1].Addr
		h.turnsMu.Lock()
		for _, ts := range h.turns {
			ts.recordCallIssuers(m, addr)
		}
		h.turnsMu.Unlock()
	}
}

func (h *cwR1Harness) archive(t *testing.T) []memory.ArchivedMessage {
	t.Helper()
	out, err := h.store.ReadArchive(context.Background(), h.key)
	require.NoError(t, err, "read full archive")
	return out
}

func (h *cwR1Harness) meta(t *testing.T) map[string]any {
	t.Helper()
	// session-core DEL-10: the window cursor lives in the archive backend's own
	// content-free meta beside the session's one archive directory.
	data, err := os.ReadFile(filepath.Join(h.dir, h.key, "backend_meta.json"))
	require.NoError(t, err, "read actual persisted window metadata")
	var out map[string]any
	require.NoError(t, json.Unmarshal(data, &out), "metadata must decode")
	delete(out, "updated_at") // Transport/storage time is not checkpoint state.
	return out
}

func cwR1Skip(t *testing.T, h *cwR1Harness) int {
	t.Helper()
	value, ok := h.meta(t)["skip"].(float64)
	require.True(t, ok, "Skip must be read from actual metadata, never inferred from slice length")
	return int(value)
}

func cwR1SetFraction(t *testing.T, h *cwR1Harness, f float64) {
	t.Helper()
	// A JSON bridge keeps RED compilable before the internal field exists. This
	// config fixture is not a Settings-HTTP test and does not supply a fake policy.
	data := []byte(fmt.Sprintf(`{"tool_result_share_fraction":%g}`, f))
	require.NoError(t, json.Unmarshal(data, &h.cfg.Context), "set real internal config")
	encoded, err := json.Marshal(h.cfg.Context)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(encoded, &fields))
	got, present := fields["tool_result_share_fraction"]
	if !present {
		t.Fatal("BLOCKED: internal tool_result_share_fraction not implemented — required by ADR-066 MAJ-CW-007 / build unit 2")
	}
	require.Equal(t, f, got, "fixture must set the actual runtime fraction, not silently ignore it")
}

func cwR1Call(id string) providers.ToolCall {
	return providers.ToolCall{ID: id, Type: "function",
		Function: &providers.FunctionCall{Name: "r1_tool", Arguments: `{"purpose":"checkpoint"}`}}
}

func cwR1Step(id, narration, result string) []providers.Message {
	return []providers.Message{
		{Role: "assistant", Content: narration, ToolCalls: []providers.ToolCall{cwR1Call(id)}},
		{Role: "tool", ToolCallID: id, Content: result},
	}
}

func cwR1Clone(t *testing.T, messages []providers.Message) []providers.Message {
	t.Helper()
	data, err := json.Marshal(messages)
	require.NoError(t, err)
	var out []providers.Message
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}

func cwR1Result(t *testing.T, messages []providers.Message, id string) providers.Message {
	t.Helper()
	var found []providers.Message
	for _, m := range messages {
		if m.Role == "tool" && m.ToolCallID == id {
			found = append(found, m)
		}
	}
	require.Len(t, found, 1, "result slot %s must survive exactly once", id)
	return found[0]
}

func cwR1HasCall(messages []providers.Message, id string) bool {
	for _, m := range messages {
		for _, call := range m.ToolCalls {
			if call.ID == id {
				return true
			}
		}
	}
	return false
}

func (h *cwR1Harness) check(t *testing.T, ts *turnState, messages []providers.Message) []providers.Message {
	t.Helper()
	out, err := h.al.midTurnWindowCheck(ts, messages, nil)
	require.NoError(t, err, "ADR-066 MAJ-CW-004: pressure alone cannot end a turn")
	return out
}

// sessionDir is the one archive directory the owning session id names (the
// routing key "cw-r1" is its own owning id), where the checkpoint meta lives.
func (h *cwR1Harness) sessionDir() string {
	return filepath.Join(h.dir, h.key)
}
