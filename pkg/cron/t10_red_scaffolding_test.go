package cron

// RED scaffolding only. These adapters never implement a scheduler, dispatcher,
// admission policy, or persistence. Bind the two unbound adapters to production
// seams when those seams exist, then delete their Fatal branches. A BLOCKED
// result is a failure, not evidence that the assertions below it ran.

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"
)

type t10Clock struct {
	mu sync.Mutex
	at time.Time
}

func (c *t10Clock) Now() time.Time   { c.mu.Lock(); defer c.mu.Unlock(); return c.at }
func (c *t10Clock) Set(at time.Time) { c.mu.Lock(); c.at = at; c.mu.Unlock() }

type t10RunnerFunc func(context.Context, *CronJob) (string, error)

func (f t10RunnerFunc) RunScheduled(ctx context.Context, job *CronJob) (string, error) {
	return f(ctx, job)
}

func t10Spec(t *testing.T, kind string) JobSpec {
	t.Helper()
	var spec JobSpec
	field := reflect.ValueOf(&spec).Elem().FieldByName("Kind")
	if !field.IsValid() {
		t.Fatal("BLOCKED: JobSpec.Kind is missing — T10-FR-02, S-A.3/S-D.1")
	}
	if field.Kind() != reflect.String || !field.CanSet() {
		t.Fatalf("JobSpec.Kind must be a settable string, got %v", field.Type())
	}
	field.SetString(kind)
	spec.Name, spec.AgentID, spec.Message = "job-"+kind, "mia", "message-"+kind
	return spec
}

func t10InstallHandlers(t *testing.T, _ *CronService, _ map[string]ScheduledRunner) {
	t.Helper()
	t.Fatal("BLOCKED: merged engine kind-dispatch registration is not implemented; bind this RED adapter to the production dispatcher — T10-FR-01/22, S-A.2/S-A.4")
}

func t10InstallMemoryProvider(t *testing.T, _ *CronService, _ string) {
	t.Helper()
	t.Fatal("BLOCKED: injected shared memory-admission seam (cap-visibility fix round) is not available; do not implement a test-only gate — T10-FR-07, S-D.2–S-D.4")
}

func t10Await[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("test synchronization failed: production callback did not arrive")
		var zero T
		return zero
	}
}
