package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// Issue #1626 / T3 explicitly requires this no-writer GREEN control: Run
// mounts the configured enabled hook when globally on, and none when off.
// Expected hook identities, priorities, factory inputs and call counts are
// fixture/spec inputs, not values learned by executing hook loading.
func TestAgentLoop_RunLoadsConfiguredHooksWithoutConfigPublication(t *testing.T) {
	for _, tc := range []struct {
		name             string
		enabled          bool
		wantEnabledCalls int
		wantMountedCount int
	}{
		{name: "enabled", enabled: true, wantEnabledCalls: 1, wantMountedCount: 1},
		{name: "disabled", enabled: false, wantEnabledCalls: 0, wantMountedCount: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enabledName := t.Name() + "-enabled-builtin"
			disabledName := t.Name() + "-disabled-builtin"
			enabledHook := &llmObserverHook{}
			disabledHook := &llmObserverHook{}
			factoryCalls := map[string]int{enabledName: 0, disabledName: 0}
			factoryInputs := map[string]config.BuiltinHookConfig{}
			for name, hook := range map[string]*llmObserverHook{
				enabledName: enabledHook, disabledName: disabledHook,
			} {
				if err := RegisterBuiltinHook(name, func(_ context.Context, spec config.BuiltinHookConfig) (any, error) {
					factoryCalls[name]++
					factoryInputs[name] = spec
					return hook, nil
				}); err != nil {
					t.Fatalf("register builtin %q: %v", name, err)
				}
				t.Cleanup(func() { unregisterBuiltinHook(name) })
			}

			// Priorities and opaque payloads are deliberate fixture values.
			enabledSpec := config.BuiltinHookConfig{
				Enabled: true, Priority: 17, Config: json.RawMessage(`{"fixture":"enabled"}`),
			}
			disabledSpec := config.BuiltinHookConfig{
				Enabled: false, Priority: -3, Config: json.RawMessage(`{"fixture":"disabled"}`),
			}
			al := newConfiguredHookLoop(t, &llmHookTestProvider{}, config.HooksConfig{
				Enabled: tc.enabled,
				Builtins: map[string]config.BuiltinHookConfig{
					enabledName: enabledSpec, disabledName: disabledSpec,
				},
			})
			defer al.Close()
			al.bus.Close() // Terminate after startup, without a model/network call.
			runDone := make(chan error, 1)
			go func() { runDone <- al.Run(context.Background()) }()
			if err := <-runDone; err != nil {
				t.Fatalf("Run without a concurrent config publisher: %v", err)
			}

			wantCalls := map[string]int{enabledName: tc.wantEnabledCalls, disabledName: 0}
			if !reflect.DeepEqual(factoryCalls, wantCalls) {
				t.Fatalf("hook factory calls = %#v, want %#v", factoryCalls, wantCalls)
			}
			wantInputs := map[string]config.BuiltinHookConfig{}
			if tc.wantEnabledCalls == 1 {
				wantInputs[enabledName] = enabledSpec
			}
			if !reflect.DeepEqual(factoryInputs, wantInputs) {
				t.Fatalf("hook factory inputs = %#v, want %#v", factoryInputs, wantInputs)
			}
			mounted := al.hooks.snapshotHooks()
			if len(mounted) != tc.wantMountedCount {
				t.Fatalf("mounted hooks = %#v, want exactly %d", mounted, tc.wantMountedCount)
			}
			if tc.wantMountedCount == 1 {
				want := HookRegistration{
					Name: enabledName, Priority: enabledSpec.Priority, Source: HookSourceInProcess, Hook: enabledHook,
				}
				if mounted[0] != want {
					t.Fatalf("mounted hook = %#v, want %#v", mounted[0], want)
				}
			}
			t.Logf("Run joined: mounted exactly %d hook(s); enabled factory calls %d; disabled factory calls 0", tc.wantMountedCount, tc.wantEnabledCalls)
		})
	}
}
