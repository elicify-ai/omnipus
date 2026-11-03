package tools_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// Literal oracle: channels-spec-r8 FR-035, with founder Q4 (2026-11-03)
// deleting only the obsolete "Only Omnipus holds this tool." sentence. No
// replacement wording is invented. Neither this text nor the golden entry
// is generated from the tool implementation or accepted with -update.
const q7OutboundDescription = "Change which people and groups an agent may start messages to on one chat connection, or switch the connection back to only answering. Supply id, policy (reply_only = Only answer; agent_may_start = Answer and also start messages), the whole targets list (exact ids as the allowed-senders list writes them: a person's user id for a direct message, a group's chat id; no @names, no *; at most 20) and expected_revision from list_channels. Only Discord and Telegram can start messages; on Discord, a channel id must be a text channel, announcement channel or thread. Refused for a connection bound to you. It always shows an approval card, even in God Mode, and every approval applies to that one request only — it is never remembered."

func TestQ7T27_ProviderDefinitionsIncludeTheNormativeOutboundEntry(t *testing.T) {
	reg := buildTestBuiltinRegistry()
	_, control := reg.Get("connect_channel")
	require.True(t, control, "instrument: the actual sysagent builtin registry is populated")
	tool, present := reg.Get("set_channel_outbound")
	if !present {
		t.Fatal("BLOCKED: set_channel_outbound is not registered in the provider builtin registry — required by FR-030/FR-032/FR-035/T-27")
	}
	require.Equal(t, q7OutboundDescription, tool.Description(), "FR-035: exact normative description")
	want := tools.BuiltinEntry{Name: "set_channel_outbound", Description: q7OutboundDescription, Scope: tools.ScopeCore, Category: tools.CategoryChannels, Source: "builtin"}
	var actual []tools.BuiltinEntry
	for _, entry := range reg.Describe() {
		if entry.Name == want.Name {
			actual = append(actual, entry)
		}
	}
	require.Equal(t, []tools.BuiltinEntry{want}, actual, "FR-032/035: exactly one canonical provider entry")
	raw, err := os.ReadFile(goldenFilePath(t))
	require.NoError(t, err)
	var golden []tools.BuiltinEntry
	require.NoError(t, json.Unmarshal(raw, &golden))
	var expectedEntries []tools.BuiltinEntry
	for _, entry := range golden {
		if entry.Name == want.Name {
			expectedEntries = append(expectedEntries, entry)
		}
	}
	require.Equal(t, []tools.BuiltinEntry{want}, expectedEntries, "T-27: the checked-in golden contains the same spec-derived entry; the existing full byte comparator remains mandatory")
}
