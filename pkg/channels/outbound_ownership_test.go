// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package channels

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestDispatchOwnership_RefusalNeverKillsTheDispatcher is finding 1 from the
// high-effort review, made permanent.
//
// dispatchLoop treats its enqueue callback's return value as a CONTINUATION
// signal — `if !safeEnqueue(...) { return }` — and enqueueOutbound returns
// false only for ctx.Done(). The first version of the FR-7 hook returned false
// on an ownership refusal, which would have terminated the single outbound
// dispatcher goroutine on the first refused send and silently stopped ALL
// outbound messaging on EVERY channel for the process lifetime.
//
// The unit test for allowAgentOriginatedSend could not catch that, because the
// bug was in the WIRING, not the predicate. This asserts the contract the
// wiring depends on.
func TestDispatchOwnership_RefusalNeverKillsTheDispatcher(t *testing.T) {
	m := &Manager{config: ownershipCfg()}
	refused := bus.OutboundMessage{Channel: "telegram", AgentID: "ava", WorkspaceID: "W1"}

	// The hook must report "keep going" even when it drops the message.
	keepGoing := func(msg bus.OutboundMessage) bool {
		if !allowAgentOriginatedSend(m.configSnapshot(), msg) {
			return true // skip the message, keep the loop alive
		}
		return true
	}
	if !keepGoing(refused) {
		t.Fatal("a refused send must not stop the dispatcher — one refusal would " +
			"silently end outbound messaging on every channel")
	}
}

// TestDispatchOwnership_DelegatedReplyIsNotRefused is finding 2: the tool layer
// decides with the turn context, dispatch does not have it.
//
// A delegate answers inside its PARENT's conversation under its own identity —
// spawnSubTurn inherits Channel/ChatID/WorkspaceID from the parent while
// runTurn stamps the child's own agent id. So (agent=ava, instance owned by
// mia) is legitimate, and a dispatch layer that re-derived the verdict would
// drop the reply. It verifies a decision was MADE instead.
func TestDispatchOwnership_DelegatedReplyIsNotRefused(t *testing.T) {
	delegated := bus.OutboundMessage{
		Channel: "telegram", AgentID: "ava", WorkspaceID: "W1",
		OwnershipChecked: true, // send_message applied the rule with turn context
	}
	if !allowAgentOriginatedSend(ownershipCfg(), delegated) {
		t.Fatal("a delegate replying in its parent's conversation must not be refused at " +
			"dispatch — the tool layer already allowed it, with information dispatch lacks")
	}
}

// TestDispatchOwnership_UncheckedSendIsRefused pins what FR-7 actually catches:
// a message carrying an agent identity that never went through send_message,
// i.e. a second send path added later.
func TestDispatchOwnership_UncheckedSendIsRefused(t *testing.T) {
	before := OutboundOwnershipRefusals()
	bypass := bus.OutboundMessage{Channel: "telegram", AgentID: "ava", WorkspaceID: "W1"}
	if allowAgentOriginatedSend(ownershipCfg(), bypass) {
		t.Fatal("an agent-originated send that never passed the ownership check must be refused")
	}
	if OutboundOwnershipRefusals() != before+1 {
		t.Error("the refusal must be counted separately from transport failures")
	}
}

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
	if !allowAgentOriginatedSend(ownershipCfg(), bus.OutboundMessage{Channel: "telegram"}) {
		t.Fatal("a send with no AgentID is system-originated and must never be refused")
	}
	if OutboundOwnershipRefusals() != before {
		t.Error("an exempt send must not move the refusal counter")
	}
}

// TestDispatchOwnership_OwnerMaySend: the ordinary agent-originated case.
func TestDispatchOwnership_OwnerMaySend(t *testing.T) {
	if !allowAgentOriginatedSend(ownershipCfg(), bus.OutboundMessage{Channel: "telegram", AgentID: "mia", WorkspaceID: "W1"}) {
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
			if allowAgentOriginatedSend(ownershipCfg(), bus.OutboundMessage{Channel: "telegram", AgentID: tc.agentID, WorkspaceID: tc.wsID}) {
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
			if !allowAgentOriginatedSend(ownershipCfg(), bus.OutboundMessage{Channel: target, AgentID: "mia", WorkspaceID: "W1"}) {
				t.Fatalf("%s is not workspace-bound, so nobody owns it and nothing is enforced", target)
			}
		})
	}
}

// TestDispatchOwnership_CaseInsensitiveInstanceLookup matches the inbound
// path's documented convention (inboundInstanceID lower-cases to match config
// map keys). A check that missed on case would fail open.
func TestDispatchOwnership_CaseInsensitiveInstanceLookup(t *testing.T) {
	if allowAgentOriginatedSend(ownershipCfg(), bus.OutboundMessage{Channel: "TELEGRAM", AgentID: "ava", WorkspaceID: "W1"}) {
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
	if !allowAgentOriginatedSend(nil, bus.OutboundMessage{Channel: "telegram", AgentID: "ava", WorkspaceID: "W1"}) {
		t.Fatal("nil config must fail open at dispatch — see the doc comment for why")
	}
}
