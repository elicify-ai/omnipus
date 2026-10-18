// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// WU-5 channel-turn gate (FR-241, BDD-7.12, OR-MAJ-004): private variables
// are not injected into the children of a channel-triggered turn unless the
// user marked the variable "available in channel conversations" (default
// off), while the same session's web-app turn (the control) sees the value.
package agent

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// lenRe reads every `len=<digits>` observation out of a tool result.
var lenRe = regexp.MustCompile(`len=(\d+)`)

// TestWU5PrivateSecret_ChannelTurn_DefaultOff_WebControlPresent is BDD-7.12's
// rows "off (default)" and "off, turn started by the user in the web app
// (control)": after Mia's Sales WEATHER_TOKEN is stored, a turn triggered by
// Anna's Telegram message runs the command but the value is absent, while the
// web-app turn on the same session sees it.
//
// Both legs ride real carrier shapes: the channel leg publishes the exact
// inbound shape a channel adapter produces (channel + instance + sender), the
// control leg the webchat shape. The flag-on row (K49 PATCH) is realised in
// pkg/gateway::TestWU5SecretOptions_ChannelFlag_GatesInjection where the
// options route lives.
func TestWU5PrivateSecret_ChannelTurn_DefaultOff_WebControlPresent(t *testing.T) {
	requireWU5VariableAbsentFromHost(t)
	f := newWU5Fixture(t)
	sales := f.mainSession("sales", "mia")

	// Store first (a web-app turn: cards cannot be raised on channel turns,
	// request_secret's own gate — out of scope here, already pinned by WU-4).
	f.provider.setScript(sales, []providers.LLMResponse{
		wu5RequestPrivateSecret(wu5Variable), wu5Stop("stored"),
	})
	f.webTurn(sales, "store my weather token")
	f.waitForCalls(sales, 1, "card raised on the web turn")
	card := f.pendingPrivateCard(sales, "mia", "sales")
	submitted := f.submitPrivate(cardToRecord(f, card), wu5SalesValue)
	f.resumeAfterSubmit(submitted.Card.RequestId)

	// Channel-triggered turn: command runs, value unset (flag default off).
	// Web control turn: value present. Same session, same variable; each
	// phase scripts its own segment (setScript restarts the session's call
	// counter).
	f.provider.setScript(sales, []providers.LLMResponse{wu5Bash(ownerLenCommand), wu5Stop("channel check done")})
	f.channelTurn(sales, "check the weather for me")
	f.waitForCalls(sales, 2, "channel-triggered turn ran its command")
	f.provider.setScript(sales, []providers.LLMResponse{wu5Bash(ownerLenCommand), wu5Stop("web check done")})
	f.webTurn(sales, "check the weather in the app")
	f.waitForCalls(sales, 2, "web control turn ran its command")

	// Count each tool call ONCE: every later prompt re-includes earlier
	// results as history, so the raw walk multiplies one read by the number
	// of subsequent model rounds.
	results := f.provider.uniqueToolResultsFor(sales)
	joined := strings.Join(results, "\n")
	// lenRe extracts every `len=<digits>` read, one per origin turn.
	var lenLines []string
	for _, r := range results {
		for _, m := range lenRe.FindAllStringSubmatch(r, -1) {
			lenLines = append(lenLines, m[1])
		}
	}
	require.Len(t, lenLines, 2, "exactly one length read per origin turn, got %q", joined)
	require.Equal(t, "0", lenLines[0],
		"channel-triggered turn: the command ran but the value is unset (BDD-7.12 row 'off (default)'; FR-241)")
	require.Equal(t, "16", lenLines[1],
		"web-app control turn on the same session sees the value (BDD-7.12 control row)")
}
