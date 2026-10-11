// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// Item 10 (founder ruling Q3) / ARCHITECT-ANSWER-FINAL-GATE.md item 5: one
// SERVER-SIDE invariant test over the ChatParticipant rule. The contract stays
// prose plus this test rather than a oneOf union, so the test is the thing that
// actually enforces the rule.
//
// ORACLE: contracts/components/schemas/ChatParticipant.yaml, read at test time —
// the kind enum, the display_name bounds and the source pattern are taken from
// the contract file, never from the implementation. The rule itself is
// ARCHITECT-ANSWER-PARTICIPANT.md D1: kind=agent carries `agent` and no
// `source`; kind=human carries `source` and no `agent`.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/elicify-ai/omnipus/pkg/addressing"
)

// chatParticipantRule is the contract's own shape, read from the schema file.
type chatParticipantRule struct {
	kinds     []string
	minName   int
	maxName   int
	sourcePat *regexp.Regexp
}

func loadChatParticipantRule(t *testing.T) chatParticipantRule {
	t.Helper()
	// The test runs with pkg/agent as its working directory.
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "components", "schemas", "ChatParticipant.yaml"))
	require.NoError(t, err, "read the ChatParticipant contract")
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &doc), "parse the ChatParticipant contract")
	props, ok := doc["properties"].(map[string]any)
	require.True(t, ok, "the contract must declare properties")

	kind, ok := props["kind"].(map[string]any)
	require.True(t, ok, "the contract must declare kind")
	rawKinds, ok := kind["enum"].([]any)
	require.True(t, ok, "kind must be an enum")
	kinds := make([]string, 0, len(rawKinds))
	for _, k := range rawKinds {
		kinds = append(kinds, k.(string))
	}

	name, ok := props["display_name"].(map[string]any)
	require.True(t, ok, "the contract must declare display_name")
	minName := int(name["minLength"].(int))
	maxName := int(name["maxLength"].(int))

	src, ok := props["source"].(map[string]any)
	require.True(t, ok, "the contract must declare source")
	pat := regexp.MustCompile(src["pattern"].(string))

	// Instrument check: the contract must be able to REJECT something, or the
	// pattern assertions below would be vacuous.
	require.False(t, pat.MatchString("Telegram!"), "instrument: the source pattern must reject an invalid value")
	require.True(t, pat.MatchString("web"), "instrument: the source pattern must accept a valid value")
	return chatParticipantRule{kinds: kinds, minName: minName, maxName: maxName, sourcePat: pat}
}

// TestParticipant_StampedRecordObeysTheContract is the invariant: every record
// participantFromSender produces satisfies the ChatParticipant contract, and the
// kind decides which of source/agent is present.
func TestParticipant_StampedRecordObeysTheContract(t *testing.T) {
	rule := loadChatParticipantRule(t)
	require.ElementsMatch(t, []string{"human", "agent"}, rule.kinds,
		"the contract's kind enum is the closed set this rule is written against")

	named := func(p addressing.Pair) string {
		if p.AgentID == "ray" {
			return "Ray the Researcher"
		}
		return ""
	}
	senders := []struct {
		name string
		s    addressing.Sender
	}{
		{"agent with a configured name", addressing.Sender{Agent: addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}}},
		{"agent falling back to its id", addressing.Sender{Agent: addressing.Pair{WorkspaceID: "ws-1", AgentID: "zed"}}},
		{"connector human", addressing.Sender{Platform: "telegram", DisplayName: "Alice"}},
		{"connector human with an uppercase platform", addressing.Sender{Platform: "Google-Chat", DisplayName: "Bo"}},
		{"connector human whose platform has symbols", addressing.Sender{Platform: "Slack!", DisplayName: "Cy"}},
		{"connector human with an over-long platform", addressing.Sender{Platform: strings.Repeat("z", 200), DisplayName: "Di"}},
		{"web principal", addressing.Sender{Principal: "dan"}},
		{"web principal with a name at the bound", addressing.Sender{Principal: strings.Repeat("é", rule.maxName)}},
		{"web principal with a name over the bound", addressing.Sender{Principal: strings.Repeat("é", rule.maxName+50)}},
		{"web principal with control characters", addressing.Sender{Principal: "A\u0000li‮ce\n"}},
	}
	produced := 0
	for _, tc := range senders {
		t.Run(tc.name, func(t *testing.T) {
			p := participantFromSender(tc.s, named)
			require.NotNil(t, p, "this sender names a participant")
			produced++

			require.Contains(t, rule.kinds, p.Kind, "kind must be one of the contract's enum values")

			runes := utf8.RuneCountInString(p.DisplayName)
			require.GreaterOrEqual(t, runes, rule.minName, "display_name must be at least %d runes", rule.minName)
			require.LessOrEqual(t, runes, rule.maxName, "display_name must be at most %d runes", rule.maxName)
			require.Equal(t, p.DisplayName, strings.TrimSpace(p.DisplayName),
				"a stamped display_name must not carry surrounding whitespace")

			switch p.Kind {
			case "agent":
				require.NotNil(t, p.Agent, "kind=agent must carry the agent pair (ARCHITECT-ANSWER-PARTICIPANT.md D1)")
				require.Nil(t, p.Source, "kind=agent must carry no source")
				require.NotEmpty(t, p.Agent.AgentId, "the agent pair must name the agent")
			case "human":
				require.NotNil(t, p.Source, "kind=human must carry a source")
				require.Nil(t, p.Agent, "kind=human must carry no agent")
				require.Regexp(t, rule.sourcePat, *p.Source,
					"source must match the contract's pattern: lower-case, no instance id")
			}
		})
	}
	require.Positive(t, produced, "instrument: the table must actually produce records")
}

// TestParticipant_NoAuthenticatedFactIsNeverAHuman pins the nil branch, which is
// the one case where no contract conformance can be checked at all.
func TestParticipant_NoAuthenticatedFactIsNeverAHuman(t *testing.T) {
	for _, s := range []addressing.Sender{
		{},
		{Principal: "   "},
		{Platform: "telegram"},
		{Platform: "telegram", DisplayName: "\u0007​"},
		{DisplayName: "a name with no authenticated fact behind it"},
	} {
		require.Nil(t, participantFromSender(s, nil),
			"sender %+v must produce no label: an empty principal is never turned into a human", s)
	}
}
