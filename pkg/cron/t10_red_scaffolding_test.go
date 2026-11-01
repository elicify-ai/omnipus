package cron

// RED scaffolding only. These adapters never implement a scheduler, dispatcher,
// admission policy, or persistence. Bind equivalent production registration
// when it exists; memory measurements already use the real config seam. A BLOCKED
// result is a failure, not evidence that the assertions below it ran.

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
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

func t10InstallHandlers(t *testing.T, cs *CronService, handlers map[string]ScheduledRunner) {
	t.Helper()
	registry, ok := any(cs).(interface {
		RegisterKindHandler(string, ScheduledRunner) error
	})
	if !ok {
		t.Fatal("BLOCKED: merged engine kind-dispatch registration is not implemented; bind equivalent production registry — T10-FR-01/22, S-A.2/S-A.4")
	}
	for _, kind := range []string{"heartbeat", "loop", "task"} {
		if err := registry.RegisterKindHandler(kind, handlers[kind]); err != nil {
			t.Fatalf("register real %s handler: %v", kind, err)
		}
	}
}

func t10InstallMemoryProvider(t *testing.T, _ *CronService, state string) {
	t.Helper()
	// This existing process-edge hook is shared with real admission consumers.
	// It supplies measurements, never an admission decision or a test-only gate.
	t.Cleanup(config.SetMemoryProviderForTest(func() (bool, bool) {
		return state == "pressure", state != "unmeasurable"
	}, func() (uint64, bool) {
		if state == "healthy" {
			return 16 << 30, true
		}
		return 0, state != "unmeasurable"
	}))
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
