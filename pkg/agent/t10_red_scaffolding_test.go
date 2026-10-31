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
	first := reflect.ValueOf(path)
	if ctor.Type().In(0) == reflect.TypeOf((*cron.CronService)(nil)) {
		first = reflect.ValueOf(cron.NewCronService(path))
	}
	return ctor.Call([]reflect.Value{first, reflect.ValueOf(al)})[0].Interface().(*LoopScheduler)
}

func t10NeedsT8(t *testing.T, scenario, assertion string) {
	t.Helper()
	t.Fatalf("BLOCKED: needs T8 backend (tool-parity-spec-r17.md TK-30, tests 12b–12f; t8-rebase-note-6b-replay.md applies): %s — %s. No substitute implementation or passing claim.", scenario, assertion)
}

func t10PostTurnGate(t *testing.T) {
	t.Helper()
	t.Fatal("BLOCKED: deterministic pause AFTER ProcessScheduled returns, BEFORE session post-turn mutations is not exposed; S-B.8/D-3b.14–15 requires this exact interleaving, not a provider-before-return approximation")
}

func t10FailSingleRecoveryWrite(t *testing.T) {
	t.Helper()
	t.Fatal("BLOCKED: single session loop-identity write failure injector is not exposed; S-B.9/D-3b.7/17 must distinguish one failed repair from a successful fallback clear")
}
