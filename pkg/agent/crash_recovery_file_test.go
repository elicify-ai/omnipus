//go:build goolm && stdjson

package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// AfterTool is an existing external interceptor boundary AFTER the actual file
// tool returns but BEFORE recordToolResult. Snapshotting there does not replace
// the tool or the recovery engine, and requires no second test process.
type crashFileResultBarrier struct {
	tool         string
	entered      chan *ToolResultHookResponse
	release      chan struct{}
	once         sync.Once
	passThrough  bool
	matchContent string
	signal       chan<- struct{}
}

func (h *crashFileResultBarrier) open() { h.once.Do(func() { close(h.release) }) }
func (h *crashFileResultBarrier) BeforeTool(_ context.Context, c *ToolCallHookRequest) (*ToolCallHookRequest, HookDecision, error) {
	return c, HookDecision{Action: HookActionContinue}, nil
}
func (h *crashFileResultBarrier) AfterTool(ctx context.Context, r *ToolResultHookResponse) (*ToolResultHookResponse, HookDecision, error) {
	if r.Tool != h.tool || (h.matchContent != "" && r.Arguments["content"] != h.matchContent) {
		return r, HookDecision{Action: HookActionContinue}, nil
	}
	h.entered <- r
	if h.signal != nil {
		h.signal <- struct{}{}
	}
	if h.passThrough {
		return r, HookDecision{Action: HookActionContinue}, nil
	}
	select {
	case <-h.release:
	case <-ctx.Done():
	}
	return r, HookDecision{Action: HookActionAbortTurn, Reason: "fixture retired after captured crash cut"}, nil
}

type crashFileFixture struct {
	h                                      *crashHarness
	record                                 *session.LifecycleRecord
	snapshot, path, tool                   string
	before, after                          string
	args                                   map[string]any
	assistant                              session.ArchiveAddress
	beforeExists, afterResult, expectError bool
	keepHeld                               bool
	release                                func()
}

func crashFileCut(t *testing.T, name string, configure ...func(*crashFileFixture)) *crashFileFixture {
	t.Helper()
	gate := make(chan struct{})
	p := &crashScript{gate: gate, entered: make(chan int, 8)}
	h := crashNew(t, p)
	work, err := workspace.EnsureWorkDir(h.home, testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	f := &crashFileFixture{h: h, path: filepath.Join(work, "uncertain.txt"), tool: name, before: "base\n", args: map[string]any{}, beforeExists: true}
	switch name {
	case "append_file":
		f.args = map[string]any{"path": f.path, "content": "A\n"}
		f.after = "base\nA\n"
	case "write_file":
		f.args = map[string]any{"path": f.path, "content": "replacement\n", "overwrite": true}
		f.after = "replacement\n"
	case "edit_file":
		f.args = map[string]any{"path": f.path, "old_text": "base", "new_text": "edited"}
		f.after = "edited\n"
	default:
		t.Fatalf("unknown test adapter %q", name)
	}
	for _, set := range configure {
		set(f)
	}
	if f.beforeExists {
		require.NoError(t, os.WriteFile(f.path, []byte(f.before), 0600))
	}
	second := make(chan struct{}, 1)
	releaseSecond := make(chan struct{})
	openSecond := sync.OnceFunc(func() { close(releaseSecond) })
	t.Cleanup(openSecond)
	p.next = func(i int, _ []providers.Message) (*providers.LLMResponse, error) {
		if i == 1 && f.afterResult {
			second <- struct{}{}
			<-releaseSecond
			return &providers.LLMResponse{Content: "fixture result was already committed", FinishReason: "stop"}, nil
		}
		if i != 0 {
			return nil, fmt.Errorf("fixture crossed the held result cut")
		}
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{crashCall("uncertain", name, f.args)}, FinishReason: "tool_calls"}, nil
	}
	barrier := &crashFileResultBarrier{tool: name, entered: make(chan *ToolResultHookResponse, 1), release: make(chan struct{}), passThrough: f.afterResult}
	if name == "append_file" {
		barrier.matchContent, _ = f.args["content"].(string)
	}
	require.NoError(t, h.al.MountHook(NamedHook("crash-file-result-barrier", barrier)))
	t.Cleanup(barrier.open)
	rec, owner := crashChild(t, h, p)
	f.record = rec
	close(gate)
	if f.afterResult {
		crashAwait(t, second)
	}
	result := crashAwait(t, barrier.entered)
	require.Equal(t, f.expectError, result.Result.IsError, "actual file outcome at the chosen cut: %s", result.Result.ContentForLLM())
	bytes, err := os.ReadFile(f.path)
	require.NoError(t, err)
	require.Equal(t, f.after, string(bytes), "applied-before-result crash cut has the exact intended bytes")
	view, err := h.al.GetSessionStore().WindowView(context.Background(), rec.SessionID)
	require.NoError(t, err)
	var results, issuers int
	for _, slot := range view.Live {
		if slot.Message.Role == "tool" && slot.Message.ToolCallID == "uncertain" {
			results++
		}
		for _, call := range slot.Message.ToolCalls {
			if call.ID == "uncertain" {
				issuers++
				f.assistant = slot.Addr
			}
		}
	}
	require.Equal(t, 1, issuers, "one actual issuing assistant occurrence")
	wantResults := 0
	if f.afterResult {
		wantResults = 1
	}
	require.Equal(t, wantResults, results, "the explicit cut distinguishes before-result from after-result durability")
	f.snapshot = h.snapshot(t)
	f.release = sync.OnceFunc(func() { barrier.open(); openSecond(); crashAwaitDone(t, owner.done) })
	if f.keepHeld {
		t.Cleanup(f.release)
	} else {
		f.release()
	}
	return f
}
func (f *crashFileFixture) witnessFile(t *testing.T, home string) string {
	t.Helper()
	dir := filepath.Join(home, "sessions", f.record.SessionID, "file_effects")
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		t.Fatal("BLOCKED: durable pre-write FileEffectWitness not implemented at baseline — required by crash-recovery DESIGN Decision 2 / Q15")
	}
	require.NoError(t, err)
	var paths []string
	for _, e := range files {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	require.Len(t, paths, 1, "one production witness for the one unresolved occurrence")
	return paths[0]
}
func crashImage(data string, exists bool) map[string]any {
	if !exists {
		return map[string]any{"exists": false, "size": float64(0), "sha256": ""}
	}
	sum := sha256.Sum256([]byte(data))
	return map[string]any{"exists": true, "size": float64(len(data)), "sha256": hex.EncodeToString(sum[:])}
}

func TestCrashRecovery_MissingWitnessStopsAutomaticWrites(t *testing.T) {
	for _, name := range []string{"append_file", "write_file", "edit_file"} {
		t.Run(name, func(t *testing.T) {
			f := crashFileCut(t, name)
			// Missing witness is a deliberate adversarial fixture, even after GREEN
			// starts capturing witnesses. No imagined pre-image is substituted.
			require.NoError(t, os.RemoveAll(filepath.Join(f.snapshot, "sessions", f.record.SessionID, "file_effects")))
			p := &crashScript{entered: make(chan int, 8)}
			p.next = func(i int, _ []providers.Message) (*providers.LLMResponse, error) {
				if i == 0 {
					return &providers.LLMResponse{Content: "I checked; continue writing now.", ToolCalls: []providers.ToolCall{crashCall("unsafe-new-write", "append_file", map[string]any{"path": f.path, "content": "UNSAFE\n"})}, FinishReason: "tool_calls"}, nil
				}
				return &providers.LLMResponse{Content: "done", FinishReason: "stop"}, nil
			}
			f.h.restore(t, f.snapshot, p)
			f.h.recover(t)
			got := f.h.record(t, f.record.SessionID)
			assert.Equal(t, session.LifecycleState("interrupted"), got.State, "Q15: unknown effect with no witness must stop before helper Dispatch")
			assert.Equal(t, "reconciliation_required", crashFields(t, got)["interruption_reason"], "unknown is a durable, explicit hold")
			// If baseline admitted work, join that real owner before reading effects;
			// no timed absence assertion or race against an unscheduled goroutine.
			if owner := f.h.al.executionDispositionFor(f.h.al.executionClaimFor(got)); owner != nil {
				crashAwaitDone(t, owner.done)
			}
			bytes, err := os.ReadFile(f.path)
			require.NoError(t, err)
			assert.Equal(t, f.after, string(bytes), "Q15: model prose saying checked cannot authorize another mutation")
			assert.Equal(t, 0, len(p.calls()), "read-only reconciliation/Unknown hold must precede the first recovered model request")
		})
	}
}

// These cases intentionally stay compilable against c90310f1f. Reflection
// obtains the ACTUAL future method's witness type; no parallel production type,
// assumed storage JSON or substitute reconciler is introduced. The descriptor
// bytes come from the real tool at the held native-loop cut above.
func TestCrashRecovery_FileAdaptersProveCurrentEffect(t *testing.T) {
	for _, name := range []string{"append_file", "write_file", "edit_file"} {
		t.Run(name, func(t *testing.T) {
			for _, state := range []string{"postimage", "distinct_preimage", "divergent_extra_bytes", "partial_image", "absent_instead_of_existing_preimage", "directory_instead_of_file"} {
				t.Run(state, func(t *testing.T) {
					f := crashFileCut(t, name)
					wpath := f.witnessFile(t, f.snapshot)
					raw, err := os.ReadFile(wpath)
					require.NoError(t, err)
					var witness map[string]any
					require.NoError(t, json.Unmarshal(raw, &witness))
					assert.Equal(t, crashImage(f.before, true), witness["before"], "Q15: witness describes the actual whole pre-image")
					assert.Equal(t, crashImage(f.after, true), witness["after"], "Q15: witness describes the literal intended whole post-image")
					assert.Equal(t, f.record.SessionID, witness["session_id"])
					assert.Equal(t, "uncertain", witness["tool_call_id"])
					want := "unknown"
					switch state {
					case "postimage":
						want = "effect_present"
					case "distinct_preimage":
						want = "effect_absent"
					}
					p := &crashScript{}
					f.h.restore(t, f.snapshot, p)
					switch state {
					case "distinct_preimage":
						require.NoError(t, os.WriteFile(f.path, []byte(f.before), 0600))
					case "divergent_extra_bytes":
						require.NoError(t, os.WriteFile(f.path, []byte(f.after+"outside-edit\n"), 0600))
					case "partial_image":
						require.NoError(t, os.WriteFile(f.path, []byte(f.after[:len(f.after)/2]), 0600))
					case "absent_instead_of_existing_preimage":
						require.NoError(t, os.Remove(f.path))
					case "directory_instead_of_file":
						require.NoError(t, os.Remove(f.path))
						require.NoError(t, os.Mkdir(f.path, 0700))
					}
					var tool any
					work, err := workspace.EnsureWorkDir(f.h.home, testHarnessWorkspaceMembershipID)
					require.NoError(t, err)
					switch name {
					case "append_file":
						tool = tools.NewAppendFileTool(work, true)
					case "write_file":
						tool = tools.NewWriteFileTool(work, true)
					case "edit_file":
						tool = tools.NewEditFileTool(work, true)
					}
					method := reflect.ValueOf(tool).MethodByName("ReconcileFileEffect")
					if !method.IsValid() {
						t.Fatal("BLOCKED: FileEffectReconciler not implemented — required by crash-recovery DESIGN Decision 2 / Q15")
					}
					require.Equal(t, 2, method.Type().NumIn())
					require.Equal(t, 1, method.Type().NumOut())
					value := reflect.New(method.Type().In(1))
					require.NoError(t, json.Unmarshal(raw, value.Interface()))
					ctx := tools.WithWorkspaceID(tools.WithTurnWorkspaceDir(context.Background(), work), testHarnessWorkspaceMembershipID)
					out := method.Call([]reflect.Value{reflect.ValueOf(ctx), value.Elem()})
					observation := crashFields(t, out[0].Interface())
					assert.Equal(t, want, observation["disposition"], "Q15: full current image decides effect_present/effect_absent/unknown, never substring or historical success inference")
				})
			}
		})
	}
}

func TestCrashRecovery_FileWitnessBeforeReplacementCut(t *testing.T) {
	for _, name := range []string{"append_file", "write_file", "edit_file"} {
		t.Run(name, func(t *testing.T) { crashBeforeReplacement(t, name) })
	}
}
