package gateway

import (
	"context"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestWireSteerDeps_InstallsEveryProductionDependency is the boot-level
// regression for ADR-091's composition root. It inspects the live objects
// produced by wireSteerDeps, rather than source text, so omitting any setter
// leaves the corresponding dependency nil and fails this test.
func TestWireSteerDeps_InstallsEveryProductionDependency(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: home, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)

	askReg := askuser.NewRegistry(al.GetSessionStore(), nil, askuser.Options{})
	channelManager := channels.NewManagerForTesting(nil)
	stg := &setupAndStartServicesState{
		ctx:            context.Background(),
		agentLoop:      al,
		lifecycleStore: lifecycle,
		runningServices: &services{
			ChannelManager: channelManager,
		},
		wsHandler: &WSHandler{askUserReg: askReg},
		tExecutor: agent.GetTaskExecutor(al),
	}

	SetGatewaySteerAudienceDeps(nil, nil)
	t.Cleanup(func() { SetGatewaySteerAudienceDeps(nil, nil) })
	stg.wireSteerDeps()

	if stg.runningServices.SteerDeps.Launcher == nil ||
		stg.runningServices.SteerDeps.Deliverer == nil ||
		stg.runningServices.SteerDeps.Canceller == nil ||
		stg.runningServices.SteerAudienceResolver == nil {
		t.Fatalf("wireSteerDeps left the steer bundle incomplete: %+v", stg.runningServices.SteerDeps)
	}
	assertPointerFieldNonNil(t, al, "upwardDeliverer")
	assertPointerFieldNonNil(t, channelManager, "steerAudience")
	assertPointerFieldNonNil(t, askReg, "steerAudience")
	assertPointerFieldNonNil(t, stg.runningServices.SteerDeps.Canceller, "cancelTurn")

	assertPackageInterfaceNonNil(t, "gateway audience resolver", steerAudienceResolver)

	inst, ok := al.GetRegistry().GetAgent("mia")
	if !ok || inst == nil {
		t.Fatal("test agent mia was not registered")
	}
	rawDelegate, ok := inst.Tools.Get("delegate")
	if !ok {
		t.Fatal("delegate tool was not registered")
	}
	delegateTool, ok := rawDelegate.(*tools.DelegateTool)
	if !ok {
		t.Fatalf("delegate tool has type %T", rawDelegate)
	}
	assertPointerFieldNonNil(t, delegateTool, "launcher")

	if stg.tExecutor == nil {
		t.Fatal("production TaskExecutor is nil")
	}
	assertPointerFieldNonNil(t, stg.tExecutor, "launcher")
	if got := gatewaySteerCanceller(al); got != stg.runningServices.SteerDeps.Canceller {
		t.Fatal("gateway Stop surfaces did not receive the composition-root Canceller")
	}
	// Deliverer is already statically typed steer.UpwardDeliverer (the
	// interface field), so asserting to that same interface would check
	// nothing (staticcheck S1040) — assert the real concrete production
	// type wireSteerDeps installs instead (agent.NewSteerUpwardDeliverer).
	if _, ok := stg.runningServices.SteerDeps.Deliverer.(*agent.SteerUpwardDeliverer); !ok {
		t.Fatalf("deliverer has unexpected type %T", stg.runningServices.SteerDeps.Deliverer)
	}
}

func assertPointerFieldNonNil(t *testing.T, target any, field string) {
	t.Helper()
	v := reflect.ValueOf(target)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	f := v.FieldByName(field)
	if !f.IsValid() {
		t.Fatalf("%T has no field %q", target, field)
	}
	if f.Kind() != reflect.Interface && f.Kind() != reflect.Pointer && f.Kind() != reflect.Func {
		t.Fatalf("%T.%s has unsupported kind %s", target, field, f.Kind())
	}
	if f.IsNil() {
		t.Fatalf("%T.%s is nil after wireSteerDeps", target, field)
	}
}

func assertPackageInterfaceNonNil(t *testing.T, name string, value any) {
	t.Helper()
	if value == nil {
		t.Fatalf("%s is nil after wireSteerDeps", name)
	}
}
