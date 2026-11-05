package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Q2-B §1 / §6.1: the schema remains backwards compatible, but the model no
// longer needs a read permission to invent or discover the conflict token.
func TestChannelsQ2B01_RevisionPropertyIsOptional(t *testing.T) {
	f := q7Fixture(t, "telegram", "telegram", "jim")
	tool, ok := f.api.builtinRegistry.Get("set_channel_outbound")
	require.True(t, ok)
	t.Run("schema", func(t *testing.T) {
		params, paramsOK := cq2JSON(t, tool.Parameters()).(map[string]any)
		require.True(t, paramsOK, "instrument: tool parameters are a JSON object")
		properties, propertiesOK := params["properties"].(map[string]any)
		require.True(t, propertiesOK, "instrument: schema properties are an object")
		require.Contains(t, properties, "expected_revision")
		require.ElementsMatch(t, []any{"id", "policy", "targets"}, params["required"], "Q2-B: expected_revision is no longer required")
		revision, revisionOK := properties["expected_revision"].(map[string]any)
		require.True(t, revisionOK, "instrument: revision property is a schema object")
		require.Equal(t, "Leave it out; the tool reads the current settings itself. Pass it only if you read it with list_channels.", revision["description"], "Q2-B §1 exact schema guidance")
		require.Equal(t, false, params["additionalProperties"], "Q2-B: optional does not accept unknown arguments")
	})
	t.Run("provider_golden", func(t *testing.T) {
		_, file, _, ok := runtime.Caller(0)
		require.True(t, ok)
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "tools", "testdata", "provider_defs.golden.json"))
		require.NoError(t, err)
		var entries []tools.BuiltinEntry
		require.NoError(t, json.Unmarshal(raw, &entries))
		matches := []tools.BuiltinEntry{}
		for _, entry := range entries {
			if entry.Name == "set_channel_outbound" {
				matches = append(matches, entry)
			}
		}
		require.Len(t, matches, 1, "instrument: golden reader sees the real registered entry")
		require.Contains(t, strings.ToLower(matches[0].Description), "optional", "Q2-B: provider golden must no longer tell chat agents a channel read is required")
		require.Equal(t, matches[0].Description, tool.Description(), "Q2-B: independently revised golden and tool guidance agree")
	})
}

// Q2-B §6.2: this is the real-loop UAT regression, not a direct tool unit test.
func TestChannelsQ2B02_MiaWithoutReadPermissionApprovesOnceAndSaves(t *testing.T) {
	for _, revision := range []string{"omitted", "", " \t "} {
		t.Run(fmt.Sprintf("revision_%q", revision), func(t *testing.T) {
			f := q7Fixture(t, "telegram", "telegram", "jim")
			sid := cq2Caller(t, f, "mia", false, false)
			args := cq2Args("telegram", "agent_may_start", []string{" 222 ", "telegram:222", " -100/45 "})
			if revision != "omitted" {
				args["expected_revision"] = revision
			}
			const callID = "cq2-mia-no-read"
			events := cq2Start(t, f, sid, "mia", callID, args)
			entry := q7PendingOrFailOnTurnEnd(t, f, events, sid, callID)
			require.Equal(t, "mia", entry.AgentID)
			require.Equal(t, 1, cq2RequestCount(f, sid, callID), "Q2-B: exactly one approval request")
			assert.Equal(t, cq2JSON(t, map[string]any{"policy": "reply_only", "targets": []string{}}), cq2JSON(t, entry.Args["current_outbound"]), "Q2-B UAT: person sees the current setting")
			assert.Equal(t, cq2JSON(t, map[string]any{"policy": "agent_may_start", "targets": []string{"telegram:222", "telegram:-100/45"}}), cq2JSON(t, entry.Args["new_outbound"]), "Q2-B UAT: person sees exactly the canonical replacement")
			cq2Action(t, f, entry, "allow_once")
			q7WaitTurn(t, f, events, sid)
			q7Saved(t, &tools.ToolResult{ForLLM: f.provider.lastToolResult()}, "agent_may_start", []string{"telegram:222", "telegram:-100/45"})
			t13AssertOutbound(t, f, "telegram", "agent_may_start", []string{"telegram:222", "telegram:-100/45"})
			require.Equal(t, 1, cq2RequestCount(f, sid, callID), "Q2-B: no second card after approve-once")
			require.False(t, f.al.ApprovalGrants().IsAllowed(sid, "mia", "set_channel_outbound", args))
		})
	}
}

func TestChannelsQ2B03_CardShowsServerReadCurrentAndCanonicalNewValues(t *testing.T) {
	f := q7Fixture(t, "telegram", "telegram", "jim")
	sid := cq2Caller(t, f, "mia", false, false)
	cq2Stored(t, f, "telegram", "reply_only", []string{"telegram:777", "telegram:888"})
	revision := t14Read(t, f, "telegram").Revision
	// A matching explicit token isolates card enrichment from optional parsing.
	args := q7Args("telegram", "agent_may_start", revision, []string{" 888 ", "222", "telegram:222"})
	before := t13Before(t, f)
	events := cq2Start(t, f, sid, "mia", "cq2-card", args)
	entry := q7PendingOrFailOnTurnEnd(t, f, events, sid, "cq2-card")
	for key, want := range map[string]any{
		"connection":       map[string]any{"id": "telegram", "platform": "telegram"},
		"current_outbound": map[string]any{"policy": "reply_only", "targets": []string{"telegram:777", "telegram:888"}},
		"new_outbound":     map[string]any{"policy": "agent_may_start", "targets": []string{"telegram:888", "telegram:222"}},
		"added":            []string{"telegram:222"}, "removed": []string{"telegram:777"}, "revision": revision,
	} {
		assert.Equal(t, cq2JSON(t, want), cq2JSON(t, entry.Args[key]), "Q2-B §4: server-read card field %s", key)
	}
	for _, key := range []string{"id", "policy", "targets", "expected_revision"} {
		assert.Equal(t, args[key], entry.Args[key], "Q2-B: facts never collide with argument %s", key)
	}
	t13Unchanged(t, f, before) // Preview itself is read-only.
	cq2Action(t, f, entry, "allow_once")
	q7WaitTurn(t, f, events, sid)
	t13AssertOutbound(t, f, "telegram", "agent_may_start", []string{"telegram:888", "telegram:222"})
}

func TestChannelsQ2B04_ConcurrentRESTChangeAfterCardRefusesWithoutWriting(t *testing.T) {
	for _, mode := range []string{"server_pin", "explicit_revision"} {
		t.Run(mode, func(t *testing.T) {
			f := q7Fixture(t, "telegram", "telegram", "jim")
			sid := cq2Caller(t, f, "mia", false, false)
			revision := t14Read(t, f, "telegram").Revision
			args := cq2Args("telegram", "agent_may_start", []string{"222"})
			if mode == "explicit_revision" {
				args["expected_revision"] = revision
			}
			const callID = "cq2-after-card-conflict"
			events := cq2Start(t, f, sid, "mia", callID, args)
			entry := q7PendingOrFailOnTurnEnd(t, f, events, sid, callID)
			rec := t14Put(t, f, "telegram", "agent_may_start", []string{"999"}, revision)
			require.Equal(t, http.StatusOK, rec.Code, "instrument: competing real REST writer saved")
			require.NotEqual(t, revision, t14Read(t, f, "telegram").Revision)
			before := t13Before(t, f)
			cq2Action(t, f, entry, "allow_once")
			q7WaitTurn(t, f, events, sid)
			t13Unchanged(t, f, before)
			failure := cq2Failure(t, f.provider.lastToolResult(), "CONFLICT", "stale_revision")
			assert.Equal(t, cq2AfterCard, failure["message"], "Q2-B: only legitimate post-card state refusal explains that nothing was saved")
			assert.NotContains(t, fmt.Sprint(failure), "list_channels", "Q2-B: do not instruct a denied chat agent to read settings")
			rows := cq2OutcomeRows(t, f, sid, callID)
			require.Len(t, rows, 1)
			require.Equal(t, "refused", rows[0]["outcome"])
			require.Equal(t, "stale_revision", rows[0]["reason"])
			require.Equal(t, entry.ApprovalID, rows[0]["approval_id"])
		})
	}
}

func TestChannelsQ2B05_MatchingExplicitRevisionRemainsCompatible(t *testing.T) {
	f := q7Fixture(t, "telegram", "telegram", "mia")
	sid := cq2Caller(t, f, "omnipus", false, false)
	args := q7Args("telegram", "agent_may_start", t14Read(t, f, "telegram").Revision, []string{"222"})
	events := cq2Start(t, f, sid, "omnipus", "cq2-compat", args)
	entry := q7PendingOrFailOnTurnEnd(t, f, events, sid, "cq2-compat")
	require.Equal(t, 1, cq2RequestCount(f, sid, "cq2-compat"))
	cq2Action(t, f, entry, "allow_once")
	q7WaitTurn(t, f, events, sid)
	q7Saved(t, &tools.ToolResult{ForLLM: f.provider.lastToolResult()}, "agent_may_start", []string{"telegram:222"})
	t13AssertOutbound(t, f, "telegram", "agent_may_start", []string{"telegram:222"})
}

func TestChannelsQ2B06_OmnipusStaleRevisionIsRefusedBeforeCard(t *testing.T) {
	cq2StaleBeforeCard(t, "omnipus", "cq2-omnipus-stale")
}

func TestChannelsQ2B07_MiaMadeUpRevisionIsRefusedBeforeCard(t *testing.T) {
	cq2StaleBeforeCard(t, "mia", "cq2-mia-made-up")
}

func cq2StaleBeforeCard(t *testing.T, caller, callID string) {
	t.Helper()
	f := q7Fixture(t, "telegram", "telegram", "jim")
	sid := cq2Caller(t, f, caller, false, false)
	require.NotEqual(t, "invented-by-model", t14Read(t, f, "telegram").Revision, "instrument: supplied revision really is stale")
	args := q7Args("telegram", "agent_may_start", "invented-by-model", []string{"222"})
	before := t13Before(t, f)
	events := cq2Start(t, f, sid, caller, callID, args)
	cq2NoCard(t, f, events, sid, callID)
	t13Unchanged(t, f, before)
	cq2RefusedAudit(t, f, sid, caller, callID, "telegram", "stale_revision")
	cq2Failure(t, f.provider.lastToolResult(), "CONFLICT", "stale_revision")
}

func TestChannelsQ2B09_StoppedDiscordRemovalNeedsNoLookupAndStillAsks(t *testing.T) {
	f := q7Fixture(t, "discord", "discord", "mia")
	sid := cq2Caller(t, f, "omnipus", false, false)
	cq2Stored(t, f, "discord", "agent_may_start", []string{"discord:222", "discord:333"})
	adapter := cq2DiscordEdge(f, false, "lookup_unavailable")
	args := q7Args("discord", "reply_only", t14Read(t, f, "discord").Revision, []string{"333"})
	events := cq2Start(t, f, sid, "omnipus", "cq2-remove", args)
	entry := q7PendingOrFailOnTurnEnd(t, f, events, sid, "cq2-remove")
	require.Empty(t, adapter.lookupSnapshot(), "Q2-B/T-9: stopped removal has no pre-card lookup")
	cq2Action(t, f, entry, "allow_once")
	q7WaitTurn(t, f, events, sid)
	require.Empty(t, adapter.lookupSnapshot(), "Q2-B/T-9: removal has no post-card lookup either")
	t13AssertOutbound(t, f, "discord", "reply_only", []string{"discord:333"})
	q7Saved(t, &tools.ToolResult{ForLLM: f.provider.lastToolResult()}, "reply_only", []string{"discord:333"})
}

func TestChannelsQ2B11_DirectExecutionWithoutPinOrRevisionFailsClosed(t *testing.T) {
	f := q7Fixture(t, "telegram", "telegram", "jim")
	before := t13Before(t, f)
	failure := q7Error(t, q7ExecuteAs(t, f, "mia", cq2Args("telegram", "agent_may_start", []string{"222"})), "INVALID_INPUT")
	require.Equal(t, "no approved revision; nothing was saved", failure["message"], "Q2-B §2: execution must not read latest as a substitute for approval")
	t13Unchanged(t, f, before)
}
