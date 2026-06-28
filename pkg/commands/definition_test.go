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

// TestDefinition_AvailableWhileStreaming_CancelOnly verifies that only the
// /cancel definition has AvailableWhileStreaming=true in BuiltinDefinitions.
func TestDefinition_AvailableWhileStreaming_CancelOnly(t *testing.T) {
	defs := BuiltinDefinitions()
	for _, d := range defs {
		if d.Name == "cancel" {
			if !d.AvailableWhileStreaming {
				t.Errorf("/cancel must have AvailableWhileStreaming=true")
			}
		} else {
			if d.AvailableWhileStreaming {
				t.Errorf("%q must NOT have AvailableWhileStreaming=true", d.Name)
			}
		}
	}
}
