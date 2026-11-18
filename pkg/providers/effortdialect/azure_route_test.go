package effortdialect

// azure_route_test.go — code CHECK C3: the azure adapter speaks the
// Responses API and never encodes effort, so an azure route must not be
// served reasoning levels it would drop silently. It resolves to a route with
// no encoder row: the served object is muted (DE-5 "no Omnipus encoder for
// this route") and the mapping records the not-sent outcome.

import "testing"

func TestAzureRoute_NoEncoderServedMuted(t *testing.T) {
	for _, id := range []string{"azure", "azure-openai", "Azure", "azure-cognitive-services"} {
		t.Run(id, func(t *testing.T) {
			// An empty catalog protocol, exactly as the shipped azure rows carry.
			r := RouteFor(id, "", "gpt-5")
			if r.Protocol != ProtocolOpenAIResponses {
				t.Fatalf("RouteFor(%q).Protocol = %q, want %q (the azure adapter speaks Responses)", id, r.Protocol, ProtocolOpenAIResponses)
			}
			if enc, ok := Default().EncodingFor(r); ok {
				t.Fatalf("azure route has an encoder row %+v; the azure adapter encodes no effort", enc)
			}
			levels, sw := FilterServed([]string{"low", "medium", "high"}, true, r)
			if levels == nil || len(levels) != 0 || sw {
				t.Fatalf("served = (%v, %v), want the muted object ([], false)", levels, sw)
			}
			target := Target{Levels: levels, Switch: sw, Reasons: true}
			if m := MapEffort("high", target); m.Value != ValueProviderDefault || m.NoteKind != "nothing_sendable" {
				t.Errorf("stored high on azure maps to %+v, want provider_default with the nothing_sendable note", m)
			}
			if m := MapEffort("off", target); !m.Blocked || !m.ProviderDefaultEscape || m.NoteKind != "nothing_sendable" {
				t.Errorf("stored off on azure maps to %+v, want blocked with the provider-default escape", m)
			}
		})
	}
	// Other empty-protocol rows keep the OpenAI-compatible chat route.
	if r := RouteFor("example-compat", "", "m"); r.Protocol != ProtocolOpenAIChat {
		t.Errorf("non-azure empty protocol = %q, want %q", r.Protocol, ProtocolOpenAIChat)
	}
	if RouteFor("openai-chatgpt", "", "gpt-5").Protocol != ProtocolOpenAIResponses {
		t.Error("openai-chatgpt keeps the Responses route")
	}
}
