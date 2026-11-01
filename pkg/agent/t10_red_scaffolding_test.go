//go:build goolm && stdjson

package agent

// RED scaffolding: only test-side bindings. No production function is supplied,
// no fake scheduler implements the missing recovery or T8 semantics, and missing
// bindings fail loudly. The implementer may bind these to equivalent real hooks
// rather than adopting a test-invented production API.
import (
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func t10LoopRecovery(t *testing.T, s *LoopScheduler) error {
	t.Helper()
	if r, ok := any(s).(interface{ Reconcile() error }); ok {
		return r.Reconcile()
	}
	t.Fatal("BLOCKED: loop physical-boot reconciliation does not exist — T10-FR-05, S-B.5–S-B.10; bind to the actual pre-activation recovery hook")
	return nil
}

func t10NewLoop(t *testing.T, path string, al *AgentLoop) *LoopScheduler {
	t.Helper()
	ctor := reflect.ValueOf(NewLoopScheduler)
	// T10 changes constructor ownership. This compatibility is TEST-ONLY and
	// handles the existing path constructor and the specified injected engine.
	require.Equal(t, 2, ctor.Type().NumIn(), "constructor binding changed; retain shared-store behavioral assertions when rebinding")
	first := reflect.ValueOf(path)
	if ctor.Type().In(0) == reflect.TypeOf((*cron.CronService)(nil)) {
		first = reflect.ValueOf(cron.NewCronService(path))
	}
	require.True(t, first.Type().AssignableTo(ctor.Type().In(0)), "constructor binding requires an explicit update, not a reflection panic")
	require.True(t, reflect.TypeOf(al).AssignableTo(ctor.Type().In(1)))
	require.Equal(t, 1, ctor.Type().NumOut(), "bind actual constructor result")
	require.Equal(t, reflect.TypeOf((*LoopScheduler)(nil)), ctor.Type().Out(0))
	return ctor.Call([]reflect.Value{first, reflect.ValueOf(al)})[0].Interface().(*LoopScheduler)
}

// Hook adapters are prerequisites, not test implementations. Bind equivalent
// production process-edge hooks when supplied; NEVER replace them with no-ops.
func t10PostTurnGate(t *testing.T, s *LoopScheduler, hook func()) {
	t.Helper()
	r, ok := any(s).(interface{ SetPostTurnTestHook(func()) })
	if !ok {
		t.Fatal("BLOCKED: post-ProcessScheduled/pre-mutation hook not implemented — S-B.8/D-3b.14–15")
	}
	r.SetPostTurnTestHook(hook)
	t.Cleanup(func() { r.SetPostTurnTestHook(nil) })
}

func t10FailSingleRecoveryWrite(t *testing.T, s any, hook func(string, session.MetaPatch) error) func() {
	t.Helper()
	_ = s // Caller supplies its real store; interception is the existing filesystem edge.
	return t10InterceptLoopWrites(t, hook)
}
