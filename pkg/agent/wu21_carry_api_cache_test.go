package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// Functional adapter for the spec-named API. It is not a mock. Missing methods
// fail loudly on the baseline; once implemented, every call goes to the real
// method with the production scope type discovered from its signature.
func wu21Scope(t *testing.T, method reflect.Value, agent, ws string) reflect.Value {
	t.Helper()
	if !method.IsValid() || method.Type().NumIn() == 0 {
		t.Fatal("BLOCKED: immutable CarryForwardScope API not implemented — required by amendment §3.2(1/2/3)")
	}
	typ := method.Type().In(0)
	if typ.Kind() != reflect.Struct || typ.Name() != "CarryForwardScope" {
		t.Fatal("§3.2(1): carry-forward methods must accept immutable value CarryForwardScope")
	}
	v := reflect.New(typ).Elem()
	for key, value := range map[string]string{"AgentID": agent, "WorkspaceID": ws} {
		f := v.FieldByName(key)
		if !f.IsValid() || f.Kind() != reflect.String || !f.CanSet() {
			t.Fatalf("BLOCKED: CarryForwardScope.%s not implemented — required by amendment §3.2(1)", key)
		}
		f.SetString(value)
	}
	return v
}

func wu21Write(t *testing.T, ms *MemoryStore, agent, ws, content string) error {
	t.Helper()
	method := reflect.ValueOf(ms).MethodByName("WriteCarryForward")
	if !method.IsValid() {
		t.Fatal("BLOCKED: MemoryStore.WriteCarryForward not implemented — required by amendment §3.2(3)")
	}
	scope := wu21Scope(t, method, agent, ws)
	if method.Type().NumIn() != 2 || method.Type().NumOut() != 1 || method.Type().In(1) != reflect.TypeOf("") || method.Type().Out(0) != reflect.TypeOf((*error)(nil)).Elem() {
		t.Fatal("WriteCarryForward must accept scope/content and return error (§3.2(3))")
	}
	out := method.Call([]reflect.Value{scope, reflect.ValueOf(content)})
	if out[0].IsNil() {
		return nil
	}
	err, ok := out[0].Interface().(error)
	if !ok {
		t.Fatal("WriteCarryForward returned a non-error result")
	}
	return err
}

func wu21Read(t *testing.T, ms *MemoryStore, agent, ws string) (string, error) {
	t.Helper()
	method := reflect.ValueOf(ms).MethodByName("ReadCarryForward")
	if !method.IsValid() {
		t.Fatal("BLOCKED: MemoryStore.ReadCarryForward not implemented — required by amendment §3.2(4)")
	}
	scope := wu21Scope(t, method, agent, ws)
	if method.Type().NumIn() != 1 || method.Type().NumOut() != 2 || method.Type().Out(0) != reflect.TypeOf("") || method.Type().Out(1) != reflect.TypeOf((*error)(nil)).Elem() {
		t.Fatal("ReadCarryForward must accept scope and return content/error (§3.2(4))")
	}
	out := method.Call([]reflect.Value{scope})
	if out[0].Kind() != reflect.String {
		t.Fatal("ReadCarryForward content must be text")
	}
	if out[1].IsNil() {
		return out[0].String(), nil
	}
	err, ok := out[1].Interface().(error)
	if !ok {
		t.Fatal("ReadCarryForward returned a non-error result")
	}
	return out[0].String(), err
}

func TestWU21_CarryForwardAPIExactLocationScopeAndMode(t *testing.T) {
	f := wu21New(t)
	ms := f.ag.ContextBuilder.Memory()
	ms.SharedRoom(ms.scopeForWorkspace("beta"))
	if err := wu21Write(t, ms, "self", "alpha", "alpha bytes"); err != nil {
		t.Fatal(err)
	}
	if err := wu21Write(t, ms, "self", "beta", "beta bytes"); err != nil {
		t.Fatal(err)
	}
	for ws, want := range map[string]string{"alpha": "alpha bytes", "beta": "beta bytes"} {
		got, err := wu21Read(t, ms, "self", ws)
		if err != nil || got != want {
			t.Errorf("immutable scope %s read=%q want=%q err=%v", ws, got, want, err)
		}
		b, err := os.ReadFile(f.path(ws, "self"))
		if err != nil || string(b) != want {
			t.Errorf("exact pair path %s bytes=%q want=%q err=%v", ws, b, want, err)
		}
		info, err := os.Stat(f.path(ws, "self"))
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Errorf("§3.2(2): carry-forward mode=%o want 600", info.Mode().Perm())
		}
	}
	if got, err := wu21Read(t, ms, "other", "alpha"); err != nil || got != "" {
		t.Errorf("other agent inherited self's summary: %q err=%v", got, err)
	}
	if err := wu21Write(t, ms, "self", "alpha", "replacement bytes"); err != nil {
		t.Fatal(err)
	}
	got, err := wu21Read(t, ms, "self", "alpha")
	if err != nil || got != "replacement bytes" {
		t.Errorf("replacement read=%q err=%v", got, err)
	}
	t.Run("atomic_read_snapshots", func(t *testing.T) {
		// Stress fixture sizes/counts, not product limits. The invariant is
		// exact: every read during replacement is one complete published body.
		const writes, reads, fixtureBytes = 32, 64, 64 * 1024
		first, second := strings.Repeat("a", fixtureBytes), strings.Repeat("b", fixtureBytes)
		if err := wu21Write(t, ms, "self", "alpha", first); err != nil {
			t.Fatal(err)
		}
		ready := make(chan struct{})
		errCh := make(chan string, reads)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			close(ready)
			for range reads {
				body, err := wu21Read(t, ms, "self", "alpha")
				if err != nil || (body != first && body != second) {
					errCh <- "§3.2(2): read saw an error or a partial/mixed carry-forward body during replacement"
				}
			}
		}()
		<-ready
		for i := range writes {
			body := first
			if i%2 == 0 {
				body = second
			}
			if err := wu21Write(t, ms, "self", "alpha", body); err != nil {
				t.Errorf("atomic replacement writer returned error: %v", err)
			}
		}
		wg.Wait()
		close(errCh)
		for message := range errCh {
			t.Error(message)
		}
	})
}

func TestWU21_CarryForwardRejectsInvalidPairIDs(t *testing.T) {
	f := wu21New(t)
	ms := f.ag.ContextBuilder.Memory()
	for _, bad := range []string{"../escape", "a/b", `a\b`, "a..b", "x\x00y"} {
		for _, agentBad := range []bool{false, true} {
			agent, ws := "self", "alpha"
			if agentBad {
				agent = bad
			} else {
				ws = bad
			}
			if err := wu21Write(t, ms, agent, ws, "invalid write"); err == nil || !strings.Contains(err.Error(), "invalid id") {
				t.Errorf("EntityID write rejection: agent=%q workspace=%q err=%v", agent, ws, err)
			}
			if _, err := wu21Read(t, ms, agent, ws); err == nil || !strings.Contains(err.Error(), "invalid id") {
				t.Errorf("EntityID read rejection: agent=%q workspace=%q err=%v", agent, ws, err)
			}
		}
	}
	if err := wu21Write(t, ms, "", "alpha", "invalid write"); err == nil || !strings.Contains(err.Error(), "id must not be empty") {
		t.Errorf("empty agent id must use EntityID refusal: %v", err)
	}
	if _, err := wu21Read(t, ms, "", "alpha"); err == nil || !strings.Contains(err.Error(), "id must not be empty") {
		t.Errorf("empty agent read must use EntityID refusal: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(f.home, "workspaces", "*", ".omnipus", "carry-forward", "*"))
	if err != nil || len(matches) != 0 {
		t.Errorf("invalid scopes produced files: %v err=%v", matches, err)
	}
}

func TestWU21_RecapInvalidatesOnlyOwnWorkspaceVariant(t *testing.T) {
	f := wu21New(t)
	cb := f.ag.ContextBuilder
	cb.BuildSystemPromptWithCacheForWorkspace("alpha")
	beforeB := cb.BuildSystemPromptWithCacheForWorkspace("beta")
	oldA := cb.workspacePromptCache.variants["alpha"]
	oldB := cb.workspacePromptCache.variants["beta"]
	if oldA == nil || oldB == nil {
		t.Fatal("positive control: both workspace variants must be cached")
	}
	f.recap(t, "alpha", "WU21_ALPHA_CACHE_REFRESH").persistResponse()
	gotB := cb.BuildSystemPromptWithCacheForWorkspace("beta")
	newB := cb.workspacePromptCache.variants["beta"]
	t.Run("other_workspace_cache_hit", func(t *testing.T) {
		if newB != oldB || gotB != beforeB {
			t.Error("§3.2(5): alpha recap rebuilt or changed beta's cached variant")
		}
	})
	gotA := cb.BuildSystemPromptWithCacheForWorkspace("alpha")
	if cb.workspacePromptCache.variants["alpha"] == oldA || !strings.Contains(gotA, "WU21_ALPHA_CACHE_REFRESH") {
		t.Error("§3.2(5): alpha's cached variant did not rebuild with its own recap")
	}
	for ws, v := range cb.workspacePromptCache.variants {
		if _, tracked := v.existedAtCache[f.path(ws, "self")]; !tracked {
			t.Errorf("§3.2(5): %s variant does not track its own carry-forward path", ws)
		}
		other := "alpha"
		if ws == other {
			other = "beta"
		}
		if _, tracked := v.existedAtCache[f.path(other, "self")]; tracked {
			t.Errorf("§3.2(5): %s variant tracks another workspace", ws)
		}
		if _, tracked := v.existedAtCache[filepath.Join(f.agentHome, ".omnipus", "last-session.md")]; tracked {
			t.Errorf("§3.2(7): %s variant still tracks obsolete agent-wide summary", ws)
		}
	}
}

func TestWU21_OldSummaryNeverInvalidatesWorkspaceVariants(t *testing.T) {
	f := wu21New(t)
	cb := f.ag.ContextBuilder
	cb.BuildSystemPromptWithCacheForWorkspace("alpha")
	cb.BuildSystemPromptWithCacheForWorkspace("beta")
	a, b := cb.workspacePromptCache.variants["alpha"], cb.workspacePromptCache.variants["beta"]
	if err := fileutil.WriteFileAtomic(filepath.Join(f.agentHome, ".omnipus", "last-session.md"), []byte("WU21_OBSOLETE_CHANGE"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, ws := range []string{"alpha", "beta"} {
		if strings.Contains(cb.BuildSystemPromptWithCacheForWorkspace(ws), "WU21_OBSOLETE_CHANGE") {
			t.Errorf("§3.2(7): old file changed %s prompt", ws)
		}
	}
	if cb.workspacePromptCache.variants["alpha"] != a || cb.workspacePromptCache.variants["beta"] != b {
		t.Error("§3.2(5/7): obsolete file invalidated workspace variants")
	}
}
