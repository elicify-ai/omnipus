package agent

import (
	"context"
	"runtime"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// Issue #1626: Run's hook initialization must use a synchronized config
// snapshot while either supported publisher replaces the live config pointer.
// The race detector is the concurrency oracle; the mounted registration also
// proves that this exercised real configured-hook loading, not an empty path.
func TestAgentLoop_RunHookLoadingDoesNotRaceConfigPublication(t *testing.T) {
	for _, method := range []string{"MutateConfig", "SwapConfig"} {
		t.Run(method, func(t *testing.T) {
			hookName := t.Name()
			hook := &llmObserverHook{}
			factoryCalls := 0
			if err := RegisterBuiltinHook(hookName, func(context.Context, config.BuiltinHookConfig) (any, error) {
				factoryCalls++
				return hook, nil
			}); err != nil {
				t.Fatalf("register configured hook: %v", err)
			}
			t.Cleanup(func() { unregisterBuiltinHook(hookName) })

			// This priority is fixture input, not an inferred production default.
			const priority = 17
			al := newConfiguredHookLoop(t, &llmHookTestProvider{}, config.HooksConfig{
				Enabled: true,
				Builtins: map[string]config.BuiltinHookConfig{
					hookName: {Enabled: true, Priority: priority},
				},
			})
			defer al.Close()
			// A closed input bus terminates Run only AFTER both real startup
			// initializers have executed; no message/LLM turn is needed here.
			al.bus.Close()
			publish := configPublisher1626(t, al, method)
			ready := make(chan struct{})
			stopWriter := make(chan struct{})
			writerDone := make(chan configPublicationResult1626, 1)
			go publishConfigUntilStopped1626(publish, ready, stopWriter, writerDone)
			<-ready

			runDone := make(chan error, 1)
			go func() { runDone <- al.Run(context.Background()) }()
			runErr := <-runDone // Join Run before inspecting the hook/factory.
			close(stopWriter)
			result := <-writerDone // Join the publisher before fixture cleanup.

			if runErr != nil {
				t.Fatalf("Run with an unchanged enabled hook: %v", runErr)
			}
			if result.err != nil {
				t.Fatalf("%s during Run startup: %v", method, result.err)
			}
			if result.writes < minimumConfigPublications1626 {
				t.Fatalf("config publications = %d, want at least %d", result.writes, minimumConfigPublications1626)
			}
			if factoryCalls != 1 {
				t.Fatalf("configured hook factory calls = %d, want exactly 1", factoryCalls)
			}
			mounted := al.hooks.snapshotHooks()
			if len(mounted) != 1 {
				t.Fatalf("mounted hook count = %d, want exactly 1", len(mounted))
			}
			want := HookRegistration{Name: hookName, Priority: priority, Source: HookSourceInProcess, Hook: hook}
			if mounted[0] != want {
				t.Fatalf("mounted hook = %#v, want %#v", mounted[0], want)
			}
			t.Logf("Run loaded the exact configured hook; joined %s writer after %d publications", method, result.writes)
		})
	}
}

// Repetition supplies scheduling opportunities, not a timing assertion or a
// product limit. Even if startup finishes quickly, exercise repeated writes.
const minimumConfigPublications1626 = 32

type configPublicationResult1626 struct {
	writes int
	err    error
}

func publishConfigUntilStopped1626(
	publish func() error,
	ready chan<- struct{},
	stop <-chan struct{},
	done chan<- configPublicationResult1626,
) {
	result := configPublicationResult1626{}
	defer func() { done <- result }()
	// Only setup is ordered by ready. In particular, pointer publications
	// below are NOT ordered before Run's config reads by this channel.
	close(ready)
	for {
		if result.writes >= minimumConfigPublications1626 {
			select {
			case <-stop:
				return
			default:
			}
		}
		if err := publish(); err != nil {
			result.err = err
			return
		}
		result.writes++
		runtime.Gosched() // Scheduling opportunity only; channels provide joins.
	}
}

func configPublisher1626(t *testing.T, al *AgentLoop, method string) func() error {
	t.Helper()
	switch method {
	case "MutateConfig":
		return func() error {
			// An unrelated/no-op settings save still publishes a private clone;
			// hook choices remain identical in every published config.
			return al.MutateConfig(func(*config.Config) error { return nil })
		}
	case "SwapConfig":
		// Prepare both complete configs BEFORE concurrency. Never mutate the
		// Hooks maps in place or race a test-owned fixture against the loader.
		first, err := al.GetConfig().Clone()
		if err != nil {
			t.Fatalf("clone first swap config: %v", err)
		}
		second, err := al.GetConfig().Clone()
		if err != nil {
			t.Fatalf("clone second swap config: %v", err)
		}
		next := first
		return func() error {
			al.SwapConfig(next)
			if next == first {
				next = second
			} else {
				next = first
			}
			return nil
		}
	default:
		t.Fatalf("unsupported config publisher %q", method)
		return nil
	}
}
