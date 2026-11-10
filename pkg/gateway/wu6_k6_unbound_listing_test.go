// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// WU-6 K6 kind ruling RED pack — the architect ruling
// (coordination/designs/WU6-K6-SECRET-KIND-RULING-20261018.md, BINDING):
// Settings/boot-stored plain credential rows are listed with kind "unbound"
// and NEVER an inferred kind; an unused one is visible and removable; a ref
// used by two kinds still shows unbound plus both consumers; a deletion
// clears the value row AND the card metadata row in one publication.
//
// Expected values derive from the ruling text (§2.1 listing rule, §3.2 rules
// 1–6, §4 oracles O1–O11), never from the current implementation — the
// current one implements the REJECTED scheme (listing.go::NameEntry infers
// the kind from consumers via consumerKind and omits unused plain rows), so
// these tests are RED on a1536fba0 for exactly that reason.
//
// Oracle map: O1 TestWU6K6_UnboundUsedPlainRow_Shape · O2
// TestWU6K6_UnusedPlainRow_ListedStoredWithEmptyConsumers · O3
// TestWU6K6_PlainRowUsedByTwoKinds_StaysUnbound · O4
// TestWU6K6_CardEntries_RecordedShapeUnchanged · O5
// TestWU6K6_ToolAndRest_ListingParity · O6
// TestWU6K6_UnboundScoping_OrdinaryAgentSeesNoneOmnipusSeesAll · O7 = the
// repaired TestWU6RemoveSecret_UnusedDeletedAfterApproval (wu6_secret_removal_test.go;
// same oracle, not duplicated) · O8
// TestWU6Removal_CardRefViaPlainDelete_NoGhostEntry · O9
// TestWU6K6_UnboundState_FlipsWithConfigNotStore · O10
// TestWU6K6_Contract_UnboundEnumValues + src/test/secrets-unbound-contract.test.ts ·
// O11 TestWU6K6_MaskingSet_CoversPlainValues.
package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// wu6UnboundFixedLine is §2.4 #2 / §3.2 rule 1's exact, single recipient line
// for an unbound entry: "for unbound, lines are exactly the single fixed line
// Stored in Settings; no recipient recorded".
const wu6UnboundFixedLine = "Stored in Settings; no recipient recorded"

// wu6K6List is the K6 REST listing as generated entries.
func (w *wu6Fixture) wu6K6List() []gen.SecretNameEntry {
	w.t.Helper()
	rec := w.do(http.MethodGet, "/api/v1/secrets", "")
	require.Equal(w.t, http.StatusOK, rec.Code, "got %d %s", rec.Code, rec.Body.String())
	var entries []gen.SecretNameEntry
	require.NoError(w.t, json.Unmarshal(rec.Body.Bytes(), &entries), "body must decode: %s", rec.Body.String())
	return entries
}

// wu6K6Find returns the entry for one reference, failing when absent.
func wu6K6Find(t *testing.T, entries []gen.SecretNameEntry, ref string) *gen.SecretNameEntry {
	t.Helper()
	for i := range entries {
		if entries[i].Reference == ref {
			return &entries[i]
		}
	}
	t.Fatalf("BLOCKED: reference %q is not in the K6 listing (%d entries) — required by the WU-6 K6 kind ruling §2.1 (every stored name appears)", ref, len(entries))
	return nil
}

// wu6K6OmnipusToolList runs the real list_secrets tool as Omnipus and
// decodes its K6 rows.
func (w *wu6Fixture) wu6K6OmnipusToolList(sid string) []gen.SecretNameEntry {
	w.t.Helper()
	ctx := tools.WithAgentID(
		tools.WithTranscriptSessionID(
			tools.WithToolContext(context.Background(), "webchat", sid),
			sid),
		"omnipus")
	tool := w.loopTool("omnipus", "list_secrets")
	require.NotNil(w.t, tool, "BLOCKED: list_secrets is not registered — required by FR-073")
	result := tool.Execute(ctx, map[string]any{})
	require.False(w.t, result.IsError, "list_secrets must run for Omnipus: %s", result.ForLLM)
	var decoded struct {
		Secrets []gen.SecretNameEntry `json:"secrets"`
	}
	require.NoError(w.t, json.Unmarshal([]byte(result.ForLLM), &decoded), "tool result must decode: %s", result.ForLLM)
	return decoded.Secrets
}

// wu6K6MiaToolList runs the real list_secrets tool as Mia scoped to her
// sales-workspace session (FR-073 scoping input).
func (w *wu6Fixture) wu6K6MiaToolList(sid string) []gen.SecretNameEntry {
	w.t.Helper()
	ctx := tools.WithAgentID(
		tools.WithTranscriptSessionID(
			tools.WithToolContext(context.Background(), "webchat", sid),
			sid),
		"mia")
	tool := w.loopTool("mia", "list_secrets")
	require.NotNil(w.t, tool, "BLOCKED: list_secrets is not registered — required by FR-073")
	result := tool.Execute(ctx, map[string]any{})
	require.False(w.t, result.IsError, "list_secrets must run for Mia: %s", result.ForLLM)
	var decoded struct {
		Secrets []gen.SecretNameEntry `json:"secrets"`
	}
	require.NoError(w.t, json.Unmarshal([]byte(result.ForLLM), &decoded), "tool result must decode: %s", result.ForLLM)
	return decoded.Secrets
}

// TestWU6K6_UnboundUsedPlainRow_Shape is ruling O1 / §3.2 rules 1+2: the
// plain row consumed only by the enabled channel's access token is listed
// with kind unbound, the exact single fixed line, version 1, state selected,
// the consumer label, and no owner / channel-turns fields — never a value.
func TestWU6K6_UnboundUsedPlainRow_Shape(t *testing.T) {
	w := newWU6Fixture(t)
	entries := w.wu6K6List()
	entry := wu6K6Find(t, entries, "QA1277_HEALTHY_LINE_ACCESS")

	require.Equal(t, gen.SecretNameEntryKind("unbound"), entry.Kind,
		"the store records no recipient for a plain row; the listing must say unbound, never an inferred kind (ruling rule 3); got kind %q", entry.Kind)
	require.Equal(t, gen.SecretNameEntryRecipientKind("unbound"), entry.Recipient.Kind,
		"the recipient display carries the same unbound kind (ruling rule 1)")
	require.Equal(t, []string{wu6UnboundFixedLine}, entry.Recipient.Lines,
		"lines are EXACTLY the single fixed line (ruling §2.4 #2); got %q", entry.Recipient.Lines)
	require.EqualValues(t, 1, entry.Version, "a plain row is exactly one version (ruling rule 1)")
	require.Equal(t, gen.SecretNameEntryStateSelected, entry.State,
		"the row has a consumer, so its derived state is selected (ruling rule 2)")
	// The consumer label is the value-free config path as the walk reports
	// it — the marshalled config's JSON keys (ReferenceConsumers walks the
	// JSON tree, so the field leg is the wire spelling `*_ref`), the
	// instance middle, and nothing else (ruling §2.2 mechanism, O1's "the
	// channels.line.healthy.ChannelAccessTokenRef label").
	require.Len(t, entry.Consumers, 1,
		"exactly one config field references this row (fixture wiring: line.healthy's access token)")
	require.Contains(t, entry.Consumers[0], "channels.line.healthy.",
		"the label names the consuming instance's config path (ruling O1)")
	require.True(t, strings.HasSuffix(entry.Consumers[0], "_ref"),
		"the label's field leg is the *_ref config key, value-free (ruling §2.2); got %q", entry.Consumers[0])
	require.Nil(t, entry.Owner, "an unbound entry never carries an owner pair (ruling rule 1)")
	require.Nil(t, entry.AvailableInChannelTurns, "an unbound entry never carries the private channel-turns option (ruling rule 1)")
	require.NotContains(t, "qa1277-healthy-access", entry.Recipient.Lines[0],
		"the listing is value-free (FR-073)")
}

// TestWU6K6_UnusedPlainRow_ListedStoredWithEmptyConsumers is ruling O2: an
// unused plain row is VISIBLE (BDD-15.4's premise — an operator must find a
// dormant credential to remove it), with consumers present and empty (never
// null) and derived state stored (ruling rule 2).
func TestWU6K6_UnusedPlainRow_ListedStoredWithEmptyConsumers(t *testing.T) {
	w := newWU6Fixture(t)
	require.NoError(t, w.store.Set(wu6UnusedRef, wu6UnusedValue))

	entries := w.wu6K6List()
	entry := wu6K6Find(t, entries, wu6UnusedRef)
	require.Equal(t, gen.SecretNameEntryKind("unbound"), entry.Kind, "ruling rule 1")
	require.NotNil(t, entry.Consumers, "consumers is present and empty, never null (ruling O2)")
	require.Empty(t, entry.Consumers, "an unused row has no consumers (ruling O2)")
	require.Equal(t, gen.SecretNameEntryStateStored, entry.State,
		"no consumers means derived state stored, never selected or superseded (ruling rule 2)")
	require.NotContains(t, rec(t, w), wu6UnusedValue, "the listing is value-free (FR-073)")
}

// rec is the raw K6 response body (value sweep helper).
func rec(t *testing.T, w *wu6Fixture) string {
	t.Helper()
	return w.do(http.MethodGet, "/api/v1/secrets", "").Body.String()
}

// TestWU6K6_PlainRowUsedByTwoKinds_StaysUnbound is ruling O3 — the
// no-inference mutation probe: the ref consumed by BOTH the enabled channel
// and a provider row stays kind unbound with both consumer labels. Any
// change that derives kind from consumers must fail this test.
func TestWU6K6_PlainRowUsedByTwoKinds_StaysUnbound(t *testing.T) {
	w := newWU6Fixture(t)
	// Second real consumer: a provider row holding the same ref (the wiring
	// TestWU6RemoveSecret_InUse_Approved_DegradedNextBoot uses).
	live := w.api.agentLoop.GetConfig()
	live.Providers = append(live.Providers, &config.ModelConfig{
		Provider: "qa1277-second", Model: "health-probe", Models: []string{"health-probe"}, Custom: true,
		Protocol: "openai-compatible", APIBase: "http://127.0.0.1:1/v1", APIKeyRef: issue1277HealthyRef,
	})

	entries := w.wu6K6List()
	entry := wu6K6Find(t, entries, issue1277HealthyRef)
	require.Equal(t, gen.SecretNameEntryKind("unbound"), entry.Kind,
		"a two-kind ref must stay unbound — never channel_credential, never provider_key (ruling rule 3); got %q", entry.Kind)
	require.NotEqual(t, gen.SecretNameEntryKindChannelCredential, entry.Kind,
		"the channel consumer must not win kind inference (ruling §2.2)")
	require.NotEqual(t, gen.SecretNameEntryKindProviderKey, entry.Kind,
		"the provider consumer must not win kind inference (ruling §2.2)")
	joined := strings.Join(entry.Consumers, "\n")
	require.Contains(t, joined, "line.healthy", "the channel consumer label is listed (ruling O3)")
	require.Contains(t, joined, "qa1277-second", "the provider consumer label is listed (ruling O3)")
	require.Equal(t, gen.SecretNameEntryStateSelected, entry.State, "consumed, so selected (ruling rule 2)")
}

// TestWU6K6_CardEntries_RecordedShapeUnchanged is ruling O4: card-stored
// entries keep exactly their recorded shape — kind, recipient display, owner
// pair, version, selected/superseded state — through the plain-row merge
// (regression guard for rule 4: metadata-row presence decides).
func TestWU6K6_CardEntries_RecordedShapeUnchanged(t *testing.T) {
	w := newWU6Fixture(t)
	sid := w.mainSession("sales", "mia")
	v1Refs := w.commitPrivateEntry(t, sid, "sales", "mia", wu6VariableName, wu6PrivateValue)
	v2Refs := w.commitPrivateEntry(t, sid, "sales", "mia", wu6VariableName, wu6PrivateAlt)

	entries := w.wu6K6List()
	v1 := wu6K6Find(t, entries, v1Refs[0])
	v2 := wu6K6Find(t, entries, v2Refs[0])

	for _, row := range []*gen.SecretNameEntry{v1, v2} {
		require.Equal(t, gen.SecretNameEntryKindAgentPrivate, row.Kind,
			"a card row keeps its recorded kind (ruling rule 4)")
		require.Equal(t, gen.SecretNameEntryRecipientKindAgentPrivate, row.Recipient.Kind, "K2 display kind")
		require.NotNil(t, row.Owner, "a private row carries its owner pair (K6)")
		require.Equal(t, "mia", row.Owner.AgentId, "the recorded owner agent")
		require.Equal(t, "sales", row.Owner.WorkspaceId, "the recorded owner workspace")
		require.NotNil(t, row.AvailableInChannelTurns, "a private row carries the channel-turns option field (K6/FR-241)")
		// K2's agent_private lines are "workspace + agent + variable" — the
		// display renders the recorded names; the oracle is the semantics
		// (names the pair and the variable), not the renderer's casing.
		display := strings.ToLower(strings.Join(row.Recipient.Lines, " "))
		require.Contains(t, display, "sales", "K2's workspace line")
		require.Contains(t, display, "mia", "K2's agent line")
		require.Contains(t, display, strings.ToLower(wu6VariableName), "K2's variable line")
	}
	require.EqualValues(t, 1, v1.Version, "the first stored version is version 1 (FR-072)")
	require.EqualValues(t, 2, v2.Version, "the replacement is version 2 (FR-072)")
	require.Equal(t, gen.SecretNameEntryStateSuperseded, v1.State,
		"the replaced version is superseded (R2-MAJ-001)")
	require.Equal(t, gen.SecretNameEntryStateSelected, v2.State,
		"the replacement is selected on submit (R2-MAJ-001, FR-086)")
}

// TestWU6K6_ToolAndRest_ListingParity is ruling O5 (§2.1 parity): the tool
// result for Omnipus and the REST body carry the same name set (plain + card)
// with equal kind, state and consumers per name.
func TestWU6K6_ToolAndRest_ListingParity(t *testing.T) {
	w := newWU6Fixture(t)
	sid := w.mainSession("sales", "omnipus")
	require.NoError(t, w.store.Set(wu6UnusedRef, wu6UnusedValue))
	privateRefs := w.commitPrivateEntry(t, sid, "sales", "omnipus", wu6VariableName, wu6PrivateValue)

	rest := w.wu6K6List()
	toolList := w.wu6K6OmnipusToolList(sid)

	byRef := func(entries []gen.SecretNameEntry) map[string]gen.SecretNameEntry {
		out := make(map[string]gen.SecretNameEntry, len(entries))
		for _, e := range entries {
			out[e.Reference] = e
		}
		return out
	}
	restByID, toolByID := byRef(rest), byRef(toolList)
	require.Len(t, toolByID, len(restByID),
		"both surfaces list the same merged inventory (ruling §2.1); REST=%d tool=%d", len(restByID), len(toolByID))
	for ref, restEntry := range restByID {
		toolEntry, ok := toolByID[ref]
		require.True(t, ok, "tool listing is missing %q (parity, ruling O5)", ref)
		require.Equal(t, restEntry.Kind, toolEntry.Kind, "kind parity for %q (ruling O5)", ref)
		require.Equal(t, restEntry.State, toolEntry.State, "state parity for %q (ruling O5)", ref)
		require.ElementsMatch(t, restEntry.Consumers, toolEntry.Consumers, "consumer parity for %q (ruling O5)", ref)
	}
	// The name set spans both worlds: a plain row and a card row.
	wu6K6Find(t, toolList, wu6UnusedRef)
	wu6K6Find(t, toolList, privateRefs[0])
}

// TestWU6K6_UnboundScoping_OrdinaryAgentSeesNoneOmnipusSeesAll is ruling O6
// (FR-073 scoping): Mia sees only her own private names — no unbound entry —
// while Omnipus's listing contains them.
func TestWU6K6_UnboundScoping_OrdinaryAgentSeesNoneOmnipusSeesAll(t *testing.T) {
	w := newWU6Fixture(t)
	miaSid := w.mainSession("sales", "mia")
	omniSid := w.mainSession("sales", "omnipus")
	require.NoError(t, w.store.Set(wu6UnusedRef, wu6UnusedValue))
	miaRefs := w.commitPrivateEntry(t, miaSid, "sales", "mia", wu6VariableName, wu6PrivateValue)

	miaList := w.wu6K6MiaToolList(miaSid)
	for _, entry := range miaList {
		require.NotEqual(t, wu6UnusedRef, entry.Reference,
			"an ordinary agent sees no unbound entry (FR-073 scoping, ruling O6)")
		require.NotEqual(t, issue1277HealthyRef, entry.Reference,
			"an ordinary agent sees no unbound entry (FR-073 scoping, ruling O6)")
	}
	wu6K6Find(t, miaList, miaRefs[0]) // positive control: her own private row IS listed

	omniList := w.wu6K6OmnipusToolList(omniSid)
	wu6K6Find(t, omniList, wu6UnusedRef)        // Omnipus sees the unused plain row (ruling O6)
	wu6K6Find(t, omniList, issue1277HealthyRef) // Omnipus sees the used plain row (ruling O6)
}

// TestWU6Removal_CardRefViaPlainDelete_NoGhostEntry is ruling O8 (§2.5
// ghost-row co-change): a card reference deleted through ANY deletion path —
// here the Settings→Security plain-name path (Store.Delete,
// rest_settings.go::HandleCredentials) — must leave NO entry in the listing:
// value and metadata rows clear together in one publication.
func TestWU6Removal_CardRefViaPlainDelete_NoGhostEntry(t *testing.T) {
	w := newWU6Fixture(t)
	sid := w.mainSession("sales", "mia")
	refs := w.commitPrivateEntry(t, sid, "sales", "mia", wu6VariableName, wu6PrivateValue)
	ref := refs[0]

	require.NoError(t, w.store.Delete(ref), "the plain-name deletion path accepts a card reference (Settings DELETE)")
	_, getErr := w.store.Get(ref)
	var notFound *credentials.NotFoundError
	require.ErrorAs(t, getErr, &notFound, "instrument: the value row is gone after the plain-path delete")

	for _, entry := range w.wu6K6List() {
		require.NotEqual(t, ref, entry.Reference,
			"a deleted card reference must not keep listing as a valueless ghost (ruling §2.5, rule 6)")
	}
}

// TestWU6K6_UnboundState_FlipsWithConfigNotStore is ruling O9 (rule 2's
// derived-state property): when the referencing config field is cleared with
// NO store change, the state flips selected → stored while kind stays
// unbound. Falsifies any caching of state or any store-write coupling.
func TestWU6K6_UnboundState_FlipsWithConfigNotStore(t *testing.T) {
	w := newWU6Fixture(t)
	require.NoError(t, w.store.Set(wu6UnusedRef, wu6UnusedValue))
	live := w.api.agentLoop.GetConfig()
	live.Providers = append(live.Providers, &config.ModelConfig{
		Provider: "qa1277-flip", Model: "health-probe", Models: []string{"health-probe"}, Custom: true,
		Protocol: "openai-compatible", APIBase: "http://127.0.0.1:1/v1", APIKeyRef: wu6UnusedRef,
	})

	selected := wu6K6Find(t, w.wu6K6List(), wu6UnusedRef)
	require.Equal(t, gen.SecretNameEntryStateSelected, selected.State, "consumed via the provider row (rule 2)")

	// Clear the referencing field. No store write of any kind.
	live.Providers = live.Providers[:len(live.Providers)-1]
	_, getErr := w.store.Get(wu6UnusedRef)
	require.NoError(t, getErr, "instrument: the store still holds the value — only configuration changed")

	stored := wu6K6Find(t, w.wu6K6List(), wu6UnusedRef)
	require.Equal(t, gen.SecretNameEntryStateStored, stored.State,
		"the state flips selected → stored from configuration alone (ruling O9, rule 2)")
	require.Equal(t, gen.SecretNameEntryKind("unbound"), stored.Kind, "kind never moves (ruling rule 3)")
	require.Empty(t, stored.Consumers, "the cleared reference leaves no consumer label")
}

// TestWU6K6_Contract_UnboundEnumValues is ruling O10's Go half (§2.4 #1–2):
// the generated SecretNameEntry.kind and SecretRecipientDisplay.kind enums
// carry "unbound". RED until backend-lead edits the two schema files and
// regenerates (Constraint #8's 5 steps); the requiredness leg
// (provider_key without recipient rejected) is pinned by
// src/test/secrets-unbound-contract.test.ts — a Go struct cannot express
// requiredness.
func TestWU6K6_Contract_UnboundEnumValues(t *testing.T) {
	require.True(t, gen.SecretNameEntryKind("unbound").Valid(),
		"BLOCKED: SecretNameEntry.kind has no \"unbound\" value — the WU-6 K6 kind ruling (§2.4 #1) widens the enum; edit contracts/components/schemas/SecretNameEntry.yaml and regenerate")
	require.True(t, gen.SecretNameEntryRecipientKind("unbound").Valid(),
		"BLOCKED: SecretRecipientDisplay.kind has no \"unbound\" value — the WU-6 K6 kind ruling (§2.4 #2/#3) widens both copies of the schema")
}

// TestWU6K6_MaskingSet_CoversPlainValues is ruling O11 (§6.7 / FR-069
// positive control): the boot-computed masking set still covers every plain
// value of §1.3 — the mechanism is unchanged by the listing merge, and this
// pin keeps it that way. GREEN by design (coverage pin, not a RED oracle).
// Scope note: the oracle is the boot-present plain rows, which is what the
// ruling names; a row stored AFTER boot through the direct store API is
// covered on the next RegisterSensitiveValues (reload/Settings write) — the
// direct store.Set is not a production write path, so it is not the oracle.
func TestWU6K6_MaskingSet_CoversPlainValues(t *testing.T) {
	w := newWU6Fixture(t)
	cfg := w.api.agentLoop.GetConfig()
	for name, value := range map[string]string{
		issue1277HealthyRef:          "qa1277-healthy-secret",
		issue1277ProviderRef:         issue1277ProviderValue,
		"QA1277_HEALTHY_LINE_ACCESS": "qa1277-healthy-access",
		issue1277BrokenAccessRef:     "qa1277-broken-access",
	} {
		content := "list_secrets output for " + name + ": the stored value " + value +
			" must never reach a model or a log line; this padding exists only to clear the filter's minimum content length."
		filtered := cfg.FilterSensitiveData(content)
		require.NotContains(t, filtered, value,
			"the masking set must cover plain row %q (ruling O11, §6.7 positive control)", name)
	}
}
