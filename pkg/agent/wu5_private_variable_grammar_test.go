// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// WU-5 variable grammar + protected set (US-7 AS-5, BDD-7.7 outline, dataset
// D6, FR-076, MAJ-010): every name is driven through the real request_secret
// tool — the same Execute the model's call hits — against the real registry
// and real sessions. Valid names must admit a card; invalid names must be
// refused WITH THE RULE (the D6 table's own rule names are the oracle).
package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// wu5GrammarRow is one D6 dataset row: the name and the expected outcome with
// the rule the refusal must name. Rule tokens come from the spec's own table
// (BDD-7.7 / D6 rows), not from any implementation wording.
type wu5GrammarRow struct {
	name     string
	accepted bool
	// ruleTokens: a refusal must name the rule; any one of these (case-
	// insensitive) satisfies "refused with the rule" for the row's class.
	ruleTokens []string
}

func wu5GrammarRows() []wu5GrammarRow {
	long64 := strings.Repeat("A", 64) // D6 row 3: max length, accepted
	long65 := strings.Repeat("A", 65) // D6 row 4: max+1, refused (grammar)
	return []wu5GrammarRow{
		{name: wu5Variable, accepted: true}, // D6 row 1
		{name: "A", accepted: true},         // D6 row 2: min length
		{name: long64, accepted: true},      // D6 row 3: max length
		{name: long65, accepted: false, ruleTokens: []string{"grammar", "malformed", "invalid"}},            // D6 row 4: max+1
		{name: "weather_token", accepted: false, ruleTokens: []string{"uppercase"}},                         // D6 row 5 (BDD-7.7)
		{name: "PATH", accepted: false, ruleTokens: []string{"protected", "reserved"}},                      // D6 row 6 (child allowlist)
		{name: "HOME", accepted: false, ruleTokens: []string{"protected", "reserved"}},                      // D6 row 6 (child allowlist)
		{name: "HTTP_PROXY", accepted: false, ruleTokens: []string{"protected", "reserved"}},                // D6 row 7 (proxy)
		{name: "PIP_CACHE_DIR", accepted: false, ruleTokens: []string{"protected", "reserved"}},             // D6 row 8 (setup env)
		{name: "OMNIPUS_X", accepted: false, ruleTokens: []string{"protected", "reserved", "prefix"}},       // D6 row 9
		{name: "LD_PRELOAD", accepted: false, ruleTokens: []string{"protected", "reserved", "prefix"}},      // D6 row 9
		{name: "LC_ALL", accepted: false, ruleTokens: []string{"protected", "reserved", "prefix"}},          // D6 row 9a (BDD-7.7)
		{name: "XDG_CONFIG_HOME", accepted: false, ruleTokens: []string{"protected", "reserved", "prefix"}}, // D6 row 9a
		{name: "PYTHONPATH", accepted: false, ruleTokens: []string{"document"}},                             // D6 row 9a (document runtime)
		{name: "NODE_PATH", accepted: false, ruleTokens: []string{"document"}},                              // D6 row 9a (document runtime)
		{name: "OSL_SOCKET_PATH", accepted: false, ruleTokens: []string{"document"}},                        // D6 row 9a (document runtime)
		{name: "1TOKEN", accepted: false, ruleTokens: []string{"grammar", "malformed", "invalid"}},          // D6 row 10
		{name: "TO-KEN", accepted: false, ruleTokens: []string{"grammar", "malformed", "invalid"}},          // D6 row 10
	}
}

// TestWU5PrivateVariable_GrammarAndProtectedSet drives every D6 row through
// the real tool. Valid rows must produce a pending private card; invalid rows
// must be refused with the rule named.
func TestWU5PrivateVariable_GrammarAndProtectedSet(t *testing.T) {
	f := newWU5Fixture(t)
	tool := tools.NewRequestSecretTool(func() *secretrequest.Registry { return f.al.SecretRequests() }, nil)
	require.NotNil(t, tool)

	for _, row := range wu5GrammarRows() {
		t.Run(row.name, func(t *testing.T) {
			// A fresh real session per row: an admitted card stays pending,
			// and the registry refuses a second pending card per session.
			ws := fmt.Sprintf("wu5-grammar-%s", strings.ToLower(row.name))
			sid := f.mainSession(ws, "mia")
			ctx := tools.WithTurnOrigin(
				tools.WithAgentID(
					tools.WithTranscriptSessionID(
						tools.WithToolContext(context.Background(), "webchat", sid),
						sid),
					"mia"),
				tools.NewTurnOrigin(),
			)
			result := tool.Execute(ctx, map[string]any{"purpose": wu5Purpose, "variable": row.name})

			if row.accepted {
				require.False(t, result.IsError, "D6 name %q must be accepted (BDD-7.7); tool said: %s", row.name, result.ForLLM)
				card := f.pendingPrivateCard(sid, "mia", ws)
				require.NotEmpty(t, card.Fields, "an accepted private card declares its field (K1)")
				return
			}
			require.True(t, result.IsError, "D6 name %q must be refused (BDD-7.7/D6); tool said: %s", row.name, result.ForLLM)
			lower := strings.ToLower(result.ForLLM)
			matched := false
			for _, tok := range row.ruleTokens {
				if strings.Contains(lower, tok) {
					matched = true
					break
				}
			}
			require.True(t, matched, "refusal for %q must name the rule (got %q; want one of %v) — US-7 AS-5 'refused with the rule', D6", row.name, result.ForLLM, row.ruleTokens)
		})
	}
}
