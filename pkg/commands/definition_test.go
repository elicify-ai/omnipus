package commands

import (
	"testing"
)

func TestDefinition_EffectiveUsage_NoSubCommands(t *testing.T) {
	d := Definition{Name: "start", Usage: "/start"}
	if got := d.EffectiveUsage(); got != "/start" {
		t.Fatalf("EffectiveUsage()=%q, want %q", got, "/start")
	}
}

func TestDefinition_EffectiveUsage_WithSubCommands(t *testing.T) {
	d := Definition{
		Name: "show",
		SubCommands: []SubCommand{
			{Name: "model"},
			{Name: "channel"},
			{Name: "agents"},
		},
	}
	want := "/show [model|channel|agents]"
	if got := d.EffectiveUsage(); got != want {
		t.Fatalf("EffectiveUsage()=%q, want %q", got, want)
	}
}

func TestDefinition_EffectiveUsage_WithArgsUsage(t *testing.T) {
	d := Definition{
		Name: "session",
		SubCommands: []SubCommand{
			{Name: "list"},
			{Name: "resume", ArgsUsage: "<id>"},
		},
	}
	want := "/session [list|resume <id>]"
	if got := d.EffectiveUsage(); got != want {
		t.Fatalf("EffectiveUsage()=%q, want %q", got, want)
	}
}

// TestDefinition_EffectiveDelivery covers the defaulting behavior introduced to
// fix the Constraint #8 wire bug: non-web commands leave Delivery unset (""), but
// the SlashCommand.yaml schema requires delivery ∈ [client, agent]. EffectiveDelivery
// must return DeliveryAgent in the zero-value case.
func TestDefinition_EffectiveDelivery_DefaultsToAgent(t *testing.T) {
	d := Definition{Name: "agents"} // Delivery deliberately not set (zero value "")
	if got := d.EffectiveDelivery(); got != DeliveryAgent {
		t.Errorf("EffectiveDelivery()=%q, want %q (should default to DeliveryAgent)", got, DeliveryAgent)
	}
}

func TestDefinition_EffectiveDelivery_ReturnsSetValue(t *testing.T) {
	cases := []struct {
		name     string
		delivery DeliveryMode
		want     DeliveryMode
	}{
		{"explicit client", DeliveryClient, DeliveryClient},
		{"explicit agent", DeliveryAgent, DeliveryAgent},
	}
	for _, tc := range cases {
		d := Definition{Name: tc.name, Delivery: tc.delivery}
		if got := d.EffectiveDelivery(); got != tc.want {
			t.Errorf("%s: EffectiveDelivery()=%q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestDefinition_AvailableWhileStreaming_StopFamilyOnly verifies that only
// /cancel, /stop and /stop-redirect have AvailableWhileStreaming=true in
// BuiltinDefinitions. ADR-20260928 (sub-agent control plane) D9 adds /stop and
// /stop-redirect as their own commands next to /cancel; every other command
// stays false while a turn streams.
func TestDefinition_AvailableWhileStreaming_StopFamilyOnly(t *testing.T) {
	streaming := map[string]bool{"cancel": true, "stop": true, "stop-redirect": true}
	seen := map[string]bool{}
	for _, d := range BuiltinDefinitions() {
		seen[d.Name] = true
		if want := streaming[d.Name]; d.AvailableWhileStreaming != want {
			t.Errorf("%q: AvailableWhileStreaming=%v, want %v", d.Name, d.AvailableWhileStreaming, want)
		}
	}
	for name := range streaming {
		if !seen[name] {
			t.Errorf("/%s is not registered in BuiltinDefinitions", name)
		}
	}
}
