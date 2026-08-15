// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"sort"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// ownershipTestConfig builds a *config.Config with a fixed set of channel
// instances covering: a bound instance, an unbound (no workspace_id)
// instance, a second bound instance owned by a different agent, a third
// bound instance owned by the same agent but a different workspace, and the
// synthetic "webchat" channel which never gets a ChannelInstanceConfig entry
// at all (so it is simply absent from the map, matching how webchat is
// wired in production — verified via grep of pkg/config: no ChannelInstanceConfig
// entry is ever seeded for "webchat").
func ownershipTestConfig() *config.Config {
	return &config.Config{
		Channels: map[string]config.ChannelInstanceConfig{
			"telegram": {
				Type:        "telegram",
				Enabled:     true,
				WorkspaceID: "ws-1",
				Identity:    &config.ChannelIdentity{Kind: "agent", ID: "mia"},
			},
			"discord": {
				Type:    "discord",
				Enabled: true,
				// No WorkspaceID / Identity — the "No workspace (global default
				// routing)" operator choice.
			},
			"slack": {
				Type:        "slack",
				Enabled:     true,
				WorkspaceID: "ws-1",
				Identity:    &config.ChannelIdentity{Kind: "agent", ID: "jim"},
			},
			"whatsapp.eu": {
				Type:        "whatsapp",
				Enabled:     true,
				WorkspaceID: "ws-2",
				Identity:    &config.ChannelIdentity{Kind: "agent", ID: "mia"},
			},
		},
	}
}

func TestChannelOwnershipResolver_OwnerOf_Bound(t *testing.T) {
	r := newChannelOwnershipResolver(func() *config.Config { return ownershipTestConfig() })

	wsID, agentID, bound := r.OwnerOf("telegram")
	if !bound {
		t.Fatal("OwnerOf(\"telegram\") returned bound=false, want true")
	}
	if wsID != "ws-1" || agentID != "mia" {
		t.Fatalf("OwnerOf(\"telegram\") = (%q, %q), want (\"ws-1\", \"mia\")", wsID, agentID)
	}
}

func TestChannelOwnershipResolver_OwnerOf_Unbound(t *testing.T) {
	r := newChannelOwnershipResolver(func() *config.Config { return ownershipTestConfig() })

	wsID, agentID, bound := r.OwnerOf("discord")
	if bound {
		t.Fatal("OwnerOf(\"discord\") returned bound=true for an instance with no workspace binding")
	}
	if wsID != "" || agentID != "" {
		t.Fatalf("OwnerOf(\"discord\") = (%q, %q), want (\"\", \"\") when bound=false", wsID, agentID)
	}
}

func TestChannelOwnershipResolver_OwnerOf_UnknownInstance(t *testing.T) {
	r := newChannelOwnershipResolver(func() *config.Config { return ownershipTestConfig() })

	_, _, bound := r.OwnerOf("does-not-exist")
	if bound {
		t.Fatal("OwnerOf on an unknown instance id returned bound=true")
	}
}

func TestChannelOwnershipResolver_OwnerOf_Webchat(t *testing.T) {
	r := newChannelOwnershipResolver(func() *config.Config { return ownershipTestConfig() })

	_, _, bound := r.OwnerOf("webchat")
	if bound {
		t.Fatal("OwnerOf(\"webchat\") returned bound=true; webchat is synthetic and has no ChannelInstanceConfig entry")
	}
}

func TestChannelOwnershipResolver_OwnerOf_CaseInsensitiveLookup(t *testing.T) {
	// inboundInstanceID (pkg/agent/loop.go) lower-cases the instance id before
	// looking it up in cfg.Channels; OwnerOf must match that behavior so a
	// caller passing a mixed-case id (e.g. echoed from an upstream header)
	// resolves the same as the lower-case canonical form.
	r := newChannelOwnershipResolver(func() *config.Config { return ownershipTestConfig() })

	wsID, agentID, bound := r.OwnerOf("Telegram")
	if !bound {
		t.Fatal("OwnerOf(\"Telegram\") returned bound=false, want case-insensitive match on \"telegram\"")
	}
	if wsID != "ws-1" || agentID != "mia" {
		t.Fatalf("OwnerOf(\"Telegram\") = (%q, %q), want (\"ws-1\", \"mia\")", wsID, agentID)
	}
}

func TestChannelOwnershipResolver_OwnerOf_NilSafety(t *testing.T) {
	var nilResolver *channelOwnershipResolver
	if _, _, bound := nilResolver.OwnerOf("telegram"); bound {
		t.Fatal("nil *channelOwnershipResolver.OwnerOf must return bound=false, not panic")
	}

	r := newChannelOwnershipResolver(nil)
	if _, _, bound := r.OwnerOf("telegram"); bound {
		t.Fatal("resolver with nil getConfig must return bound=false, not panic")
	}

	r = newChannelOwnershipResolver(func() *config.Config { return nil })
	if _, _, bound := r.OwnerOf("telegram"); bound {
		t.Fatal("resolver whose getConfig returns nil must return bound=false, not panic")
	}

	r = newChannelOwnershipResolver(func() *config.Config { return &config.Config{} })
	if _, _, bound := r.OwnerOf("telegram"); bound {
		t.Fatal("resolver over a config with a nil Channels map must return bound=false, not panic")
	}

	r = newChannelOwnershipResolver(func() *config.Config { return ownershipTestConfig() })
	if _, _, bound := r.OwnerOf(""); bound {
		t.Fatal("OwnerOf(\"\") must return bound=false, not panic")
	}
}

func TestChannelOwnershipResolver_OwnedBy(t *testing.T) {
	r := newChannelOwnershipResolver(func() *config.Config { return ownershipTestConfig() })

	got := r.OwnedBy("ws-1", "mia")
	want := []string{"telegram"}
	if !sort.StringsAreSorted(got) {
		t.Fatalf("OwnedBy result not sorted: %v", got)
	}
	if len(got) != len(want) || (len(got) > 0 && got[0] != want[0]) {
		t.Fatalf("OwnedBy(\"ws-1\", \"mia\") = %v, want %v", got, want)
	}
}

func TestChannelOwnershipResolver_OwnedBy_ExcludesDifferentAgentSameWorkspace(t *testing.T) {
	r := newChannelOwnershipResolver(func() *config.Config { return ownershipTestConfig() })

	got := r.OwnedBy("ws-1", "jim")
	if len(got) != 1 || got[0] != "slack" {
		t.Fatalf("OwnedBy(\"ws-1\", \"jim\") = %v, want [\"slack\"] (must not include ws-1's \"telegram\", owned by mia)", got)
	}
}

func TestChannelOwnershipResolver_OwnedBy_ExcludesSameAgentDifferentWorkspace(t *testing.T) {
	r := newChannelOwnershipResolver(func() *config.Config { return ownershipTestConfig() })

	got := r.OwnedBy("ws-2", "mia")
	if len(got) != 1 || got[0] != "whatsapp.eu" {
		t.Fatalf("OwnedBy(\"ws-2\", \"mia\") = %v, want [\"whatsapp.eu\"] (must not include ws-1's \"telegram\", same agent but different workspace)", got)
	}

	// mia owns instances in both ws-1 and ws-2, so a query for ws-1 must not
	// leak the ws-2 instance either.
	gotWS1 := r.OwnedBy("ws-1", "mia")
	for _, id := range gotWS1 {
		if id == "whatsapp.eu" {
			t.Fatalf("OwnedBy(\"ws-1\", \"mia\") leaked ws-2's whatsapp.eu: %v", gotWS1)
		}
	}
}

func TestChannelOwnershipResolver_OwnedBy_Sorted(t *testing.T) {
	cfg := &config.Config{
		Channels: map[string]config.ChannelInstanceConfig{
			"whatsapp": {
				WorkspaceID: "ws-1",
				Identity:    &config.ChannelIdentity{Kind: "agent", ID: "mia"},
			},
			"discord": {
				WorkspaceID: "ws-1",
				Identity:    &config.ChannelIdentity{Kind: "agent", ID: "mia"},
			},
			"telegram": {
				WorkspaceID: "ws-1",
				Identity:    &config.ChannelIdentity{Kind: "agent", ID: "mia"},
			},
			"slack": {
				WorkspaceID: "ws-1",
				Identity:    &config.ChannelIdentity{Kind: "agent", ID: "mia"},
			},
		},
	}
	r := newChannelOwnershipResolver(func() *config.Config { return cfg })

	got := r.OwnedBy("ws-1", "mia")
	want := []string{"discord", "slack", "telegram", "whatsapp"}
	if len(got) != len(want) {
		t.Fatalf("OwnedBy returned %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("OwnedBy returned %v, want sorted %v", got, want)
		}
	}
}

func TestChannelOwnershipResolver_OwnedBy_NilSafety(t *testing.T) {
	var nilResolver *channelOwnershipResolver
	if got := nilResolver.OwnedBy("ws-1", "mia"); got != nil {
		t.Fatalf("nil *channelOwnershipResolver.OwnedBy must return nil, got %v", got)
	}

	r := newChannelOwnershipResolver(nil)
	if got := r.OwnedBy("ws-1", "mia"); got != nil {
		t.Fatalf("resolver with nil getConfig must return nil, got %v", got)
	}

	r = newChannelOwnershipResolver(func() *config.Config { return nil })
	if got := r.OwnedBy("ws-1", "mia"); got != nil {
		t.Fatalf("resolver whose getConfig returns nil must return nil, got %v", got)
	}

	r = newChannelOwnershipResolver(func() *config.Config { return &config.Config{} })
	if got := r.OwnedBy("ws-1", "mia"); got != nil {
		t.Fatalf("resolver over a config with a nil Channels map must return nil, got %v", got)
	}

	r = newChannelOwnershipResolver(func() *config.Config { return ownershipTestConfig() })
	if got := r.OwnedBy("", "mia"); got != nil {
		t.Fatalf("OwnedBy with empty workspaceID must return nil, got %v", got)
	}
	if got := r.OwnedBy("ws-1", ""); got != nil {
		t.Fatalf("OwnedBy with empty agentID must return nil, got %v", got)
	}
}
