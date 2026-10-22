//go:build goolm && stdjson

package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

type crashCapturedWitness struct {
	bytes []byte
	err   error
}

// The observer is the backend's real post-fsync/pre-replacement hook. Reflection
// only permits the tests to compile on the baseline where that method is absent;
// it neither supplies a substitute witness nor bypasses the actual file engine.
func crashBeforeReplacement(t *testing.T, name string) {
	t.Helper()
	gate := make(chan struct{})
	openGate := sync.OnceFunc(func() { close(gate) })
	p := &crashScript{entered: make(chan int, 8), gate: gate}
	h := crashNew(t, p)
	t.Cleanup(openGate)
	work, err := workspace.EnsureWorkDir(h.home, testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	f := &crashFileFixture{h: h, path: filepath.Join(work, "before-write.txt"), tool: name, before: "base\n"}
	var tool tools.Tool
	switch name {
	case "append_file":
		tool = tools.NewAppendFileTool(work, true)
		f.args = map[string]any{"path": f.path, "content": "A\n"}
		f.after = "base\nA\n"
	case "write_file":
		tool = tools.NewWriteFileTool(work, true)
		f.args = map[string]any{"path": f.path, "content": "replacement\n", "overwrite": true}
		f.after = "replacement\n"
	case "edit_file":
		tool = tools.NewEditFileTool(work, true)
		f.args = map[string]any{"path": f.path, "old_text": "base", "new_text": "edited"}
		f.after = "edited\n"
	default:
		t.Fatalf("unknown adapter %q", name)
	}
	setter := reflect.ValueOf(tool).MethodByName("SetFileEffectBeforeWriteHook")
	if !setter.IsValid() {
		t.Fatal("BLOCKED: durable-witness-before-replacement observer not implemented at baseline — required by crash-recovery DESIGN Deterministic RED Q15")
	}
	require.Equal(t, 1, setter.Type().NumIn())
	require.Equal(t, 1, setter.Type().NumOut())
	hookType := setter.Type().In(0)
	require.Equal(t, reflect.Func, hookType.Kind())
	require.Equal(t, 1, hookType.NumIn())
	require.Equal(t, 1, hookType.NumOut())
	require.Equal(t, reflect.TypeOf((*error)(nil)).Elem(), hookType.Out(0))
	entered := make(chan crashCapturedWitness, 1)
	release := make(chan struct{})
	openCut := sync.OnceFunc(func() { close(release) })
	hook := reflect.MakeFunc(hookType, func(values []reflect.Value) []reflect.Value {
		bytes, encodeErr := json.Marshal(values[0].Interface())
		entered <- crashCapturedWitness{bytes: bytes, err: encodeErr}
		<-release
		return []reflect.Value{reflect.Zero(hookType.Out(0))}
	})
	restore := setter.Call([]reflect.Value{hook})[0]
	t.Cleanup(func() { restore.Call(nil) })
	t.Cleanup(openCut)
	h.al.GetRegistry().primaryChatAgentForTest().Tools.RegisterReplacing(tool)
	require.NoError(t, os.WriteFile(f.path, []byte(f.before), 0600))
	p.next = func(i int, _ []providers.Message) (*providers.LLMResponse, error) {
		if i == 0 {
			return &providers.LLMResponse{ToolCalls: []providers.ToolCall{crashCall("uncertain", name, f.args)}, FinishReason: "tool_calls"}, nil
		}
		return &providers.LLMResponse{Content: "original fixture writer released after snapshot", FinishReason: "stop"}, nil
	}
	rec, owner := crashChild(t, h, p)
	f.record = rec
	openGate()
	capture := crashAwait(t, entered)
	require.NoError(t, capture.err)
	before, err := os.ReadFile(f.path)
	require.NoError(t, err)
	require.Equal(t, f.before, string(before), "Q15 pre-write cut must not have replaced the target")
	var witness map[string]any
	require.NoError(t, json.Unmarshal(capture.bytes, &witness))
	assert.Equal(t, crashImage(f.before, true), witness["before"])
	assert.Equal(t, crashImage(f.after, true), witness["after"])
	path := f.witnessFile(t, h.home)
	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	var durable map[string]any
	require.NoError(t, json.Unmarshal(saved, &durable))
	assert.Equal(t, witness, durable, "the observer is AFTER the exact production witness landed, not an imaginary pre-write cut")
	f.snapshot = h.snapshot(t)
	openCut()
	crashAwaitDone(t, owner.done)
	restore.Call(nil) // No observer from the old process survives the simulated reboot.
	facts := make(chan map[string]any, 1)
	next := &crashScript{entered: make(chan int, 8)}
	next.next = func(i int, messages []providers.Message) (*providers.LLMResponse, error) {
		if i != 0 {
			return &providers.LLMResponse{Content: "new authorized effect completed", FinishReason: "stop"}, nil
		}
		for _, m := range messages {
			if m.Role == "tool" && m.ToolCallID == "uncertain" {
				var observation map[string]any
				if err := json.Unmarshal([]byte(m.Content), &observation); err != nil {
					return nil, err
				}
				facts <- observation
				if observation["disposition"] != "effect_absent" {
					return nil, fmt.Errorf("pre-write cut was not proved absent: %v", observation["disposition"])
				}
				return &providers.LLMResponse{ToolCalls: []providers.ToolCall{crashCall("new-authorized-write", name, f.args)}, FinishReason: "tool_calls"}, nil
			}
		}
		return nil, fmt.Errorf("pre-write recovery omitted the missing occurrence's observation")
	}
	h.restore(t, f.snapshot, next)
	counter := &crashMutationCounter{}
	require.NoError(t, h.al.MountHook(NamedHook("count-new-prewrite-redo", counter)))
	h.recover(t)
	got := h.record(t, rec.SessionID)
	if owner := h.al.executionDispositionFor(h.al.executionClaimFor(got)); owner != nil {
		crashAwaitDone(t, owner.done)
	}
	require.Equal(t, 2, len(next.calls()), "proved absence allows one normal native continuation")
	observation := crashAwait(t, facts)
	assert.Equal(t, "effect_absent", observation["disposition"])
	assert.Equal(t, false, observation["original_result_recovered"])
	assert.Equal(t, crashImage(f.before, true), observation["current_image"])
	assert.Equal(t, 1, counter.count(), "only a new authorized call, never replay of the historical invocation")
	after, err := os.ReadFile(f.path)
	require.NoError(t, err)
	assert.Equal(t, f.after, string(after))
}
