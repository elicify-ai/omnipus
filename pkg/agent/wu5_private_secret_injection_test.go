// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// WU-5 scoped-injection RED tests (US-7 AS-1…AS-4; BDD-7.1, 7.2, 7.3, 7.5,
// 7.6; FR-076; CRIT-001 scope = (workspace, agent, variable)).
//
// Observation channel: every injection assertion reads the value's LENGTH
// computed inside the real child (`${#WEATHER_TOKEN}`), never a raw print —
// BDD-7.3 makes a raw exact-value print masked in returned output, so a
// length read is the masking-independent observable the spec itself uses for
// BDD-7.1. This is stated in tests/plans/train2-wu5-2026-10-18.md.
package agent

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
)

// requireWU5VariableAbsentFromHost is the instrument check: the child would
// inherit a host-set WEATHER_TOKEN through the scrubbed baseline and every
// assertion below would be blind to injection.
func requireWU5VariableAbsentFromHost(t *testing.T) {
	t.Helper()
	if v := os.Getenv(wu5Variable); v != "" {
		t.Fatalf("instrument broken: %s is set in the test host environment; every injection assertion below would be blind", wu5Variable)
	}
}

// ownerLenCommand prints the value's length from inside the child (BDD-7.1's
// own oracle form: "printing the variable's length — the length matches").
const ownerLenCommand = `printf 'len=%s' "${#WEATHER_TOKEN}"`

// TestWU5PrivateSecret_OwnerCommandSeesVariable is BDD-7.1 / US-7 AS-1: Mia
// stores WEATHER_TOKEN in Sales on her own card; her next Sales command sees
// it (length 16). The card must name her own (Sales, mia) pair (K1/K4 owner
// pair; CRIT-001 scope).
func TestWU5PrivateSecret_OwnerCommandSeesVariable(t *testing.T) {
	requireWU5VariableAbsentFromHost(t)
	f := newWU5Fixture(t)
	sales := f.mainSession("sales", "mia")
	f.provider.setScript(sales, []providers.LLMResponse{
		wu5RequestPrivateSecret(wu5Variable),
		wu5Bash(ownerLenCommand),
		wu5Stop("stored and checked"),
	})

	f.webTurn(sales, "store my weather token")
	f.waitForCalls(sales, 1, "request_secret raised the private card")
	card := f.pendingPrivateCard(sales, "mia", "sales")
	require.Equal(t, wu5Purpose, card.Purpose, "the card shows the purpose it was raised with (FR-061)")

	submitted := f.submitPrivate(cardToRecord(f, card), wu5SalesValue)
	require.Equal(t, "submitted", submitted.Card.State)
	require.NotEmpty(f.t, submitted.Card.ReferenceNames, "a submitted card stores under a versioned reference (FR-072)")
	f.resumeAfterSubmit(submitted.Card.RequestId)
	f.waitForCalls(sales, 2, "the continuation ran the length check")

	require.Contains(t, strings.Join(f.provider.toolResultsFor(sales), "\n"), "len=16",
		"the owner pair's own command must see the selected value (BDD-7.1: the length matches; FR-076: only the selected version is injected)")
}

// cardToRecord re-reads the full registry record for a state card so the test
// can submit against the real K5 entry point. Owner is the session ancestry's
// owner (the same value Snapshot filtered on).
func cardToRecord(f *wu5Fixture, card gen.SessionStateSecretRequest) secretrequest.Record {
	rec, err := f.al.SecretRequests().Get(card.RequestId, wu5Owner)
	require.NoError(f.t, err, "the pending card must be readable by its owner (K5 GET)")
	return rec
}

// TestWU5PrivateSecret_OtherAgentSameWorkspace_Excluded is BDD-7.2 / US-7
// AS-2: Ray, another Sales agent whose commands run, runs
// `echo RAY_RAN; printenv WEATHER_TOKEN` — the command runs (exit 0,
// RAY_RAN printed) and the variable is unset, while Mia's own command sees
// the value. Ray's first leg is the run-control: it must pass on its own,
// before any WU-5 feature exists.
func TestWU5PrivateSecret_OtherAgentSameWorkspace_Excluded(t *testing.T) {
	requireWU5VariableAbsentFromHost(t)
	f := newWU5Fixture(t)
	ray := f.mainSession("sales", "ray")
	rayLen := `echo RAY_RAN; printf 'raylen=%s' "${#WEATHER_TOKEN}"`

	// --- Ray control leg: the command provably runs (BDD-7.2's marker). ---
	f.provider.setScript(ray, []providers.LLMResponse{wu5Bash(rayLen), wu5Stop("ray done")})
	f.webTurn(ray, "print the weather token length")
	f.waitForCalls(ray, 2, "Ray's control command ran")
	rayResults := strings.Join(f.provider.toolResultsFor(ray), "\n")
	require.Contains(t, rayResults, "RAY_RAN", "control: Ray's command must run (exit 0, marker printed) — BDD-7.2")
	require.Contains(t, rayResults, "raylen=0", "control: WEATHER_TOKEN is unset before anything is stored")

	// --- Mia stores and sees the value (the positive that gives Ray's
	// exclusion its power; without it "unset" is vacuous). ---
	sales := f.mainSession("sales", "mia")
	f.provider.setScript(sales, []providers.LLMResponse{
		wu5RequestPrivateSecret(wu5Variable), wu5Bash(ownerLenCommand), wu5Stop("mia done"),
	})
	f.webTurn(sales, "store my weather token")
	f.waitForCalls(sales, 1, "Mia's private card raised")
	card := f.pendingPrivateCard(sales, "mia", "sales")
	submitted := f.submitPrivate(cardToRecord(f, card), wu5SalesValue)
	f.resumeAfterSubmit(submitted.Card.RequestId)
	f.waitForCalls(sales, 2, "Mia's continuation ran")
	require.Contains(t, strings.Join(f.provider.toolResultsFor(sales), "\n"), "len=16",
		"instrument: the owner sees the value, so the exclusion assertion below is not vacuous")

	// --- Ray again, now that Mia's secret is selected: still unset. ---
	f.provider.setScript(ray, []providers.LLMResponse{wu5Bash(rayLen), wu5Stop("ray done 2")})
	f.webTurn(ray, "print the weather token length again")
	f.waitForCalls(ray, 2, "Ray's post-store command ran")
	rayResults = strings.Join(f.provider.toolResultsFor(ray), "\n")
	require.Contains(t, rayResults, "RAY_RAN", "Ray's command must still run after the store (BDD-7.2: exit 0)")
	require.Contains(t, rayResults, "raylen=0",
		"WEATHER_TOKEN must be unset in Ray's child while it is selected for Mia (BDD-7.2; a control where injection ignored the agent id reads raylen=16 and fails)")
}

// TestWU5PrivateSecret_WorkspaceScope_TwoWorkspaces is BDD-7.5 + BDD-7.6 /
// US-7 AS-4: Mia@Sales WEATHER_TOKEN=A and Mia@Marketing WEATHER_TOKEN=B —
// each workspace's command matches only its own value, including under
// concurrent turns.
func TestWU5PrivateSecret_WorkspaceScope_TwoWorkspaces(t *testing.T) {
	requireWU5VariableAbsentFromHost(t)
	f := newWU5Fixture(t)
	sales := f.mainSession("sales", "mia")
	mkt := f.mainSession("marketing", "mia")

	f.provider.setScript(sales, []providers.LLMResponse{
		wu5RequestPrivateSecret(wu5Variable), wu5Bash(ownerLenCommand), wu5Stop("sales stored"),
	})
	f.provider.setScript(mkt, []providers.LLMResponse{
		wu5RequestPrivateSecret(wu5Variable), wu5Bash(ownerLenCommand), wu5Stop("marketing stored"),
	})

	// Store both (sequential submits; the scopes must not disturb each other).
	f.webTurn(sales, "store my weather token")
	f.waitForCalls(sales, 1, "Sales card raised")
	salesCard := f.pendingPrivateCard(sales, "mia", "sales")
	salesSubmitted := f.submitPrivate(cardToRecord(f, salesCard), wu5SalesValue)
	f.resumeAfterSubmit(salesSubmitted.Card.RequestId)
	f.waitForCalls(sales, 2, "Sales continuation ran")

	f.webTurn(mkt, "store my weather token")
	f.waitForCalls(mkt, 1, "Marketing card raised")
	mktCard := f.pendingPrivateCard(mkt, "mia", "marketing")
	mktSubmitted := f.submitPrivate(cardToRecord(f, mktCard), wu5MktValue)
	f.resumeAfterSubmit(mktSubmitted.Card.RequestId)
	f.waitForCalls(mkt, 2, "Marketing continuation ran")

	// Concurrent turns (BDD-7.6): both workspaces' commands run at the same
	// time; each matches its own workspace's value.
	f.provider.setScript(sales, []providers.LLMResponse{wu5Bash(ownerLenCommand), wu5Stop("sales check")})
	f.provider.setScript(mkt, []providers.LLMResponse{wu5Bash(ownerLenCommand), wu5Stop("mkt check")})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, sess := range []string{sales, mkt} {
		wg.Add(1)
		go func(sid string) {
			defer wg.Done()
			msg := bus.InboundMessage{Channel: "webchat", ChatID: sid, SessionID: sid, Content: "check the token"}
			_, _, err := f.al.processMessage(context.Background(), msg)
			errs <- err
		}(sess)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err, "both concurrent turns must process")
	}
	f.waitForCalls(sales, 2, "concurrent Sales check")
	f.waitForCalls(mkt, 2, "concurrent Marketing check")

	salesOut := strings.Join(f.provider.toolResultsFor(sales), "\n")
	mktOut := strings.Join(f.provider.toolResultsFor(mkt), "\n")
	require.Contains(t, salesOut, "len=16", "Sales sees Sales's value (16 bytes); BDD-7.5")
	require.NotContains(t, salesOut, "len=14", "Sales must never see Marketing's value; BDD-7.5")
	require.Contains(t, mktOut, "len=14", "Marketing sees Marketing's value (14 bytes); BDD-7.5")
	require.NotContains(t, mktOut, "len=16", "Marketing must never see Sales's value; BDD-7.5")
}

// TestWU5PrivateSecret_OutputMasking is BDD-7.3 / US-7 AS-3: a command that
// prints the exact value gets it masked in the returned output. The base64
// rendering is deliberately NOT asserted — the spec says it is "not
// guaranteed (documented)".
func TestWU5PrivateSecret_OutputMasking(t *testing.T) {
	requireWU5VariableAbsentFromHost(t)
	f := newWU5Fixture(t)
	sales := f.mainSession("sales", "mia")
	printValue := `printf '%s\n' "$WEATHER_TOKEN"; printf '%s' "$WEATHER_TOKEN" | base64`
	f.provider.setScript(sales, []providers.LLMResponse{
		wu5RequestPrivateSecret(wu5Variable), wu5Bash(printValue), wu5Stop("printed"),
	})

	f.webTurn(sales, "store my weather token")
	f.waitForCalls(sales, 1, "card raised")
	card := f.pendingPrivateCard(sales, "mia", "sales")
	submitted := f.submitPrivate(cardToRecord(f, card), wu5SalesValue)
	f.resumeAfterSubmit(submitted.Card.RequestId)
	f.waitForCalls(sales, 2, "print command ran")

	out := strings.Join(f.provider.toolResultsFor(sales), "\n")
	require.NotEmpty(t, strings.TrimSpace(out), "the command must have produced output (instrument: the print ran)")
	require.NotContains(t, out, wu5SalesValue,
		"the exact value must be masked in returned output (BDD-7.3; promise wording A: hidden in output where possible)")
}
