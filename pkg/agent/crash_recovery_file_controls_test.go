//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

func crashRequireFileProof(t *testing.T) {
	t.Helper()
	if _, ok := reflect.TypeOf((*tools.AppendFileTool)(nil)).MethodByName("ReconcileFileEffect"); !ok {
		t.Fatal("BLOCKED: FileEffectReconciler and durable witnesses not implemented — required by crash-recovery DESIGN Decision 2 / Q15")
	}
}
func crashReadback(t *testing.T, f *crashFileFixture, raw []byte, workspaceID string) map[string]any {
	t.Helper()
	work, err := workspace.EnsureWorkDir(f.h.home, testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	var tool any
	switch f.tool {
	case "append_file":
		tool = tools.NewAppendFileTool(work, true)
	case "write_file":
		tool = tools.NewWriteFileTool(work, true)
	case "edit_file":
		tool = tools.NewEditFileTool(work, true)
	}
	method := reflect.ValueOf(tool).MethodByName("ReconcileFileEffect")
	require.True(t, method.IsValid())
	value := reflect.New(method.Type().In(1))
	require.NoError(t, json.Unmarshal(raw, value.Interface()))
	ctx := tools.WithWorkspaceID(tools.WithTurnWorkspaceDir(context.Background(), work), workspaceID)
	out := method.Call([]reflect.Value{reflect.ValueOf(ctx), value.Elem()})
	return crashFields(t, out[0].Interface())
}

func TestCrashRecovery_FileProofByteBoundaries(t *testing.T) {
	cases := []struct{ name, tool, variant, where, want string }{
		{"absent_append_before", "append_file", "absent", "before", "effect_absent"}, {"absent_append_after", "append_file", "absent", "after", "effect_present"},
		{"empty_append_before", "append_file", "empty", "before", "effect_absent"}, {"empty_append_after", "append_file", "empty", "after", "effect_present"},
		{"identical_append_suffix_not_full_postimage", "append_file", "identical", "before", "effect_absent"}, {"two_identical_appends_full_postimage", "append_file", "identical", "after", "effect_present"},
		{"append_noop", "append_file", "noop", "after", "unknown"}, {"write_noop", "write_file", "noop", "after", "unknown"}, {"edit_noop", "edit_file", "noop", "after", "unknown"},
		{"create_absent_before", "write_file", "absent", "before", "effect_absent"}, {"create_absent_after", "write_file", "absent", "after", "effect_present"},
		{"existing_empty_not_absent", "write_file", "empty", "before", "effect_absent"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			crashRequireFileProof(t)
			f := crashFileCut(t, c.tool, func(f *crashFileFixture) {
				switch c.variant {
				case "absent":
					f.before = ""
					f.beforeExists = false
					if f.tool == "append_file" {
						f.after = "A\n"
					} else {
						f.args["overwrite"] = false
					}
				case "empty":
					f.before = ""
					if f.tool == "append_file" {
						f.after = "A\n"
					}
				case "identical":
					f.before = "A\n"
					f.after = "A\nA\n"
				case "noop":
					f.after = f.before
					switch f.tool {
					case "append_file":
						f.args["content"] = ""
					case "write_file":
						f.args["content"] = f.before
					case "edit_file":
						f.args["new_text"] = "base"
					}
				}
			})
			raw, err := os.ReadFile(f.witnessFile(t, f.snapshot))
			require.NoError(t, err)
			var witness map[string]any
			require.NoError(t, json.Unmarshal(raw, &witness))
			assert.Equal(t, crashImage(f.before, f.beforeExists), witness["before"])
			assert.Equal(t, crashImage(f.after, true), witness["after"])
			f.h.restore(t, f.snapshot, &crashScript{})
			if c.where == "before" {
				if f.beforeExists {
					require.NoError(t, os.WriteFile(f.path, []byte(f.before), 0600))
				} else {
					require.NoError(t, os.Remove(f.path))
				}
			}
			ob := crashReadback(t, f, raw, testHarnessWorkspaceMembershipID)
			assert.Equal(t, c.want, ob["disposition"], "full before/after existence and image, never substring or tail matching")
		})
	}
}

func TestCrashRecovery_ChangedTargetAndPolicyAreUnknown(t *testing.T) {
	for _, name := range []string{"append_file", "write_file", "edit_file"} {
		for _, change := range []string{"workspace_identity", "symlink_target", "anchor_identity"} {
			t.Run(name+"/"+change, func(t *testing.T) {
				crashRequireFileProof(t)
				f := crashFileCut(t, name)
				raw, err := os.ReadFile(f.witnessFile(t, f.snapshot))
				require.NoError(t, err)
				f.h.restore(t, f.snapshot, &crashScript{})
				ws := testHarnessWorkspaceMembershipID
				switch change {
				case "workspace_identity":
					ws = "another-workspace"
				case "symlink_target":
					other := f.path + ".other"
					require.NoError(t, os.WriteFile(other, []byte(f.after), 0600))
					require.NoError(t, os.Remove(f.path))
					require.NoError(t, os.Symlink(other, f.path))
				case "anchor_identity":
					work := filepath.Dir(f.path)
					require.NoError(t, os.Rename(work, work+".old"))
					require.NoError(t, os.Mkdir(work, 0700))
					require.NoError(t, os.WriteFile(f.path, []byte(f.after), 0600))
				}
				ob := crashReadback(t, f, raw, ws)
				assert.Equal(t, "unknown", ob["disposition"], "same bytes do not prove the original authorized target when identity/policy changed")
			})
		}
	}
}

func TestCrashRecovery_CorruptWitnessBlocksNativeDispatch(t *testing.T) {
	for _, name := range []string{"append_file", "write_file", "edit_file"} {
		t.Run(name, func(t *testing.T) {
			crashRequireFileProof(t)
			f := crashFileCut(t, name)
			require.NoError(t, os.WriteFile(f.witnessFile(t, f.snapshot), []byte("{corrupt\n"), 0600))
			p := &crashScript{}
			f.h.restore(t, f.snapshot, p)
			f.h.recover(t)
			got := f.h.record(t, f.record.SessionID)
			assert.Equal(t, "interrupted", string(got.State))
			assert.Equal(t, "reconciliation_required", crashFields(t, got)["interruption_reason"])
			assert.Equal(t, 0, len(p.calls()))
			bytes, err := os.ReadFile(f.path)
			require.NoError(t, err)
			assert.Equal(t, f.after, string(bytes))
		})
	}
}

func TestCrashRecovery_WitnessPersistenceFailureMeansNoTargetWrite(t *testing.T) {
	for _, name := range []string{"append_file", "write_file", "edit_file"} {
		t.Run(name, func(t *testing.T) {
			gate := make(chan struct{})
			p := &crashScript{gate: gate, entered: make(chan int, 8)}
			h := crashNew(t, p)
			t.Cleanup(func() {
				select {
				case <-gate:
				default:
					close(gate)
				}
			})
			work, err := workspace.EnsureWorkDir(h.home, testHarnessWorkspaceMembershipID)
			require.NoError(t, err)
			path := filepath.Join(work, "witness-failure.txt")
			require.NoError(t, os.WriteFile(path, []byte("base\n"), 0600))
			args := map[string]any{"path": path}
			switch name {
			case "append_file":
				args["content"] = "A\n"
			case "write_file":
				args["content"] = "new\n"
				args["overwrite"] = true
			case "edit_file":
				args["old_text"] = "base"
				args["new_text"] = "edited"
			}
			p.next = func(i int, _ []providers.Message) (*providers.LLMResponse, error) {
				if i == 0 {
					return &providers.LLMResponse{ToolCalls: []providers.ToolCall{crashCall("blocked-witness", name, args)}, FinishReason: "tool_calls"}, nil
				}
				return &providers.LLMResponse{Content: "reported failed write", FinishReason: "stop"}, nil
			}
			observer := &crashFileResultBarrier{tool: name, entered: make(chan *ToolResultHookResponse, 1), release: make(chan struct{}), passThrough: true}
			require.NoError(t, h.al.MountHook(NamedHook("observe-failed-witness-write", observer)))
			rec, owner := crashChild(t, h, p)
			blocked := filepath.Join(h.al.GetSessionStore().BaseDir(), rec.SessionID, "file_effects")
			require.NoError(t, os.WriteFile(blocked, []byte("cannot be a directory"), 0600))
			close(gate)
			crashAwaitDone(t, owner.done)
			result := crashAwait(t, observer.entered)
			assert.True(t, result.Result.IsError, "failure to durably save the witness must be a visible tool error")
			bytes, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, "base\n", string(bytes), "no target bytes may change when the witness did not land")
		})
	}
}

func TestCrashRecovery_AfterResultCutPreservesKnownOutcome(t *testing.T) {
	for _, c := range []struct {
		name, tool string
		refused    bool
	}{{"append", "append_file", false}, {"write", "write_file", false}, {"edit", "edit_file", false}, {"overwrite_refused", "write_file", true}, {"nonunique_edit_refused", "edit_file", true}} {
		t.Run(c.name, func(t *testing.T) {
			f := crashFileCut(t, c.tool, func(f *crashFileFixture) {
				f.afterResult = true
				if c.refused {
					f.expectError = true
					f.after = f.before
					if f.tool == "write_file" {
						f.args["overwrite"] = false
					} else {
						f.before = "base base\n"
						f.after = f.before
					}
				}
			})
			old := f.h.al.GetSessionStore().GetHistory(f.record.SessionID)
			var known []providers.Message
			for _, m := range old {
				if m.Role == "tool" && m.ToolCallID == "uncertain" {
					known = append(known, m)
				}
			}
			require.Len(t, known, 1)
			require.NoError(t, os.RemoveAll(filepath.Join(f.snapshot, "sessions", f.record.SessionID, "file_effects")))
			p := &crashScript{}
			f.h.restore(t, f.snapshot, p)
			f.h.recover(t)
			got := f.h.record(t, f.record.SessionID)
			if owner := f.h.al.executionDispositionFor(f.h.al.executionClaimFor(got)); owner != nil {
				crashAwaitDone(t, owner.done)
			}
			require.Len(t, p.calls(), 1, "a committed result, including a refusal, needs no guessed proof or automatic replay")
			var delivered []providers.Message
			for _, m := range p.calls()[0] {
				if m.Role == "tool" && m.ToolCallID == "uncertain" {
					delivered = append(delivered, m)
				}
			}
			assert.Equal(t, known, delivered, "original saved return is retained byte-for-field, not replaced by a recovery observation")
			bytes, err := os.ReadFile(f.path)
			require.NoError(t, err)
			assert.Equal(t, f.after, string(bytes))
		})
	}
}
