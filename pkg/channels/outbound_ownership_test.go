// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package channels

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func ownershipCfg() *config.Config {
	return &config.Config{
		Channels: map[string]config.ChannelInstanceConfig{
			"telegram": {
				WorkspaceID: "W1",
				Identity:    &config.ChannelIdentity{Kind: "agent", ID: "mia"},
			},
			// Configured by an operator and deliberately left unbound —
			// "No workspace (global default routing)".
			"discord": {},
		},
	}
}

// TestDispatchOwnership_SystemSendsAreExempt pins the exemption BY ENUMERATION
// rather than as an accident of an empty field. Roughly 19 of the ~20 outbound
// producers are system-originated — streamed replies, notifyDrop backpressure,
// schedule delivery, device notifications — and every one must pass unchecked.
func TestDispatchOwnership_SystemSendsAreExempt(t *testing.T) {
	before := OutboundOwnershipRefusals()
	if !allowAgentOriginatedSend(ownershipCfg(), "telegram", "", "") {
		t.Fatal("a send with no AgentID is system-originated and must never be refused")
	}
	if OutboundOwnershipRefusals() != before {
		t.Error("an exempt send must not move the refusal counter")
	}
}

// TestDispatchOwnership_OwnerMaySend: the ordinary agent-originated case.
func TestDispatchOwnership_OwnerMaySend(t *testing.T) {
	if !allowAgentOriginatedSend(ownershipCfg(), "telegram", "mia", "W1") {
		t.Fatal("the owning (workspace, agent) pair must be allowed")
	}
}

// TestDispatchOwnership_NonOwnerIsRefused is the requirement: this is the last
// point before the wire, and it must stop what the tool layer should already
// have stopped.
func TestDispatchOwnership_NonOwnerIsRefused(t *testing.T) {
	cases := []struct {
		name          string
		agentID, wsID string
	}{
		{"another agent, same workspace", "ava", "W1"},
		{"same agent, another workspace", "mia", "W2"},
		{"neither matches", "jim", "W9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := OutboundOwnershipRefusals()
			if allowAgentOriginatedSend(ownershipCfg(), "telegram", tc.agentID, tc.wsID) {
				t.Fatal("a non-owner must be refused at dispatch")
			}
			if OutboundOwnershipRefusals() != before+1 {
				t.Error("a refusal must increment the ownership counter, so it is " +
					"distinguishable from a transport failure")
			}
		})
	}
}

// TestDispatchOwnership_UnownedChannelsAreUnrestricted covers FR-9 and the
// operator's webchat ruling together: an unbound instance, an unknown id, and
// webchat are all unowned, so nothing is enforced.
func TestDispatchOwnership_UnownedChannelsAreUnrestricted(t *testing.T) {
	for _, target := range []string{"discord", "webchat", "never-configured"} {
		t.Run(target, func(t *testing.T) {
			if !allowAgentOriginatedSend(ownershipCfg(), target, "mia", "W1") {
				t.Fatalf("%s is not workspace-bound, so nobody owns it and nothing is enforced", target)
			}
		})
	}
}

// TestDispatchOwnership_CaseInsensitiveInstanceLookup matches the inbound
// path's documented convention (inboundInstanceID lower-cases to match config
// map keys). A check that missed on case would fail open.
func TestDispatchOwnership_CaseInsensitiveInstanceLookup(t *testing.T) {
	if allowAgentOriginatedSend(ownershipCfg(), "TELEGRAM", "ava", "W1") {
		t.Fatal("instance lookup must be case-insensitive, or the check silently fails open")
	}
}

// TestDispatchOwnership_NilConfigFailsOpenDeliberately documents a choice
// rather than leaving it to be discovered.
//
// With no config there is nothing to check against. Refusing would drop real
// user traffic during a reload window; the tool layer has already refused
// anything unowned, so this is the safer of two imperfect options. It is
// recorded here so nobody "fixes" it into a fail-closed without weighing that.
func TestDispatchOwnership_NilConfigFailsOpenDeliberately(t *testing.T) {
	if !allowAgentOriginatedSend(nil, "telegram", "ava", "W1") {
		t.Fatal("nil config must fail open at dispatch — see the doc comment for why")
	}
}
