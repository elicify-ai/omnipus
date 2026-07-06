package agent

// agent_session_key_instance_test.go — ADR-029 TDD #12: agentSessionKey uses InstanceID.
//
// FR-023 specifies that the session key for channel inbound MUST incorporate the
// stamped InstanceID so two same-type instances (e.g. "whatsapp.eu" and "whatsapp.us")
// with the same ChatID do not share a transcript key.
//
// The spec note is explicit: "Do NOT assert on BuildAgentPeerSessionKey — that's the
// discarded path."  agentSessionKey is the actual inbound session-key path
// (pkg/agent/loop.go:4744-4755).
//
// Traces to: channel-instance-workspace-binding-spec.md §7 TDD #12,
//            FR-023, MAJ-002, US-9 AC-2.

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// TestAgentSessionKey_IncludesInstanceID verifies TDD #12 / FR-023:
// two inbound messages on different instances of the SAME channel type but with
// the SAME ChatID must produce DISTINCT session keys, and each key must contain
// the instance ID.
//
// BDD: Given messages from "whatsapp.eu" and "whatsapp.us", same ChatID, same agent,
// When agentSessionKey is called,
// Then the two keys are distinct and each contains its own instance ID.
func TestAgentSessionKey_IncludesInstanceID(t *testing.T) {
	const agentID = "ray"
	const chatID = "15555550100@s.whatsapp.net"

	msgEU := bus.InboundMessage{
		Channel:    "whatsapp",
		InstanceID: "whatsapp.eu",
		ChatID:     chatID,
	}
	msgUS := bus.InboundMessage{
		Channel:    "whatsapp",
		InstanceID: "whatsapp.us",
		ChatID:     chatID,
	}

	keyEU := agentSessionKey(agentID, msgEU)
	keyUS := agentSessionKey(agentID, msgUS)

	// Both keys must be non-empty.
	if keyEU == "" {
		t.Fatal("agentSessionKey returned empty string for whatsapp.eu")
	}
	if keyUS == "" {
		t.Fatal("agentSessionKey returned empty string for whatsapp.us")
	}

	// The two keys must be DISTINCT (differentiation test — same ChatID, different instance).
	if keyEU == keyUS {
		t.Errorf(
			"agentSessionKey produced the SAME key for two different instances with the same ChatID: %q — "+
				"this violates FR-023; same-type instances must not share a transcript key",
			keyEU,
		)
	}

	// Each key must contain its instance ID.
	if !strings.Contains(keyEU, "whatsapp.eu") {
		t.Errorf("agentSessionKey for whatsapp.eu = %q; does not contain 'whatsapp.eu'", keyEU)
	}
	if !strings.Contains(keyUS, "whatsapp.us") {
		t.Errorf("agentSessionKey for whatsapp.us = %q; does not contain 'whatsapp.us'", keyUS)
	}

	// Verify the expected key format: agent:<id>:chat:<instanceID>:<chatID>
	wantEU := "agent:" + agentID + ":chat:whatsapp.eu:" + chatID
	wantUS := "agent:" + agentID + ":chat:whatsapp.us:" + chatID
	if keyEU != wantEU {
		t.Errorf("agentSessionKey(eu) = %q, want %q", keyEU, wantEU)
	}
	if keyUS != wantUS {
		t.Errorf("agentSessionKey(us) = %q, want %q", keyUS, wantUS)
	}
}

// TestAgentSessionKey_FallsBackToChannelWhenNoInstanceID verifies that when
// InstanceID is not stamped (empty), agentSessionKey falls back to msg.Channel
// (the type key), preserving backward-compatible behavior.
//
// Traces to: channel-instance-workspace-binding-spec.md DS-4 row 4 (legacy back-compat).
func TestAgentSessionKey_FallsBackToChannelWhenNoInstanceID(t *testing.T) {
	const agentID = "mia"
	const chatID = "user42"

	msg := bus.InboundMessage{
		Channel:    "telegram",
		InstanceID: "", // no stamp — legacy adapter
		ChatID:     chatID,
	}

	key := agentSessionKey(agentID, msg)

	// Must use the channel type as the fallback.
	want := "agent:" + agentID + ":chat:telegram:" + chatID
	if key != want {
		t.Errorf("agentSessionKey fallback = %q, want %q", key, want)
	}
}

// TestAgentSessionKey_SessionIDPathTakesPrecedence verifies that when msg.SessionID
// is set (webchat path), agentSessionKey returns the session-ID–based key and
// ignores both InstanceID and Channel.
func TestAgentSessionKey_SessionIDPathTakesPrecedence(t *testing.T) {
	const agentID = "jim"

	msg := bus.InboundMessage{
		Channel:    "webchat",
		InstanceID: "whatsapp.eu", // InstanceID would normally be used
		ChatID:     "some-chat",
		SessionID:  "ses-abc123",
	}

	key := agentSessionKey(agentID, msg)
	want := "agent:" + agentID + ":session:ses-abc123"
	if key != want {
		t.Errorf("agentSessionKey with SessionID = %q, want %q", key, want)
	}
}
