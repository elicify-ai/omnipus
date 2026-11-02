package gateway

// FR-030/031/034, T-22: real production-wired tool -> dedicated gateway
// writer -> durable/live config. Only the Discord transport edge is doubled.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

func q7ListedChannel(t *testing.T, f *wu6Fixture, id string) map[string]any {
	t.Helper()
	tool, ok := f.api.builtinRegistry.Get("list_channels")
	require.True(t, ok, "instrument: actual production-wired reader")
	result := tool.Execute(tools.WithAgentID(context.Background(), string(coreagent.IDOmnipus)), nil)
	require.False(t, result.IsError, "list_channels: %s", result.ForLLM)
	var body struct {
		Channels []map[string]any `json:"channels"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &body))
	for _, row := range body.Channels {
		if row["id"] == id {
			return row
		}
	}
	t.Fatalf("FR-034: list_channels omitted configured instance %q", id)
	return nil
}

func q7Saved(t *testing.T, result *tools.ToolResult, policy string, targets []string) map[string]any {
	t.Helper()
	require.False(t, result.IsError, "FR-030: expected saved result: %s", result.ForLLM)
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &body))
	require.Equal(t, "saved", body["status"])
	wantTargets := make([]any, len(targets))
	for i, target := range targets {
		wantTargets[i] = target
	}
	require.Equal(t, map[string]any{"policy": policy, "targets": wantTargets}, body["outbound"], "FR-030: canonical whole replacement, not an append")
	return body
}

func q7DiscordFixture(t *testing.T) (*wu6Fixture, *t13Adapter) {
	t.Helper()
	f := q7Fixture(t, "discord", "discord", "mia")
	require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
		inst := cfg.Channels["discord"]
		inst.Enabled = true
		cfg.Channels["discord"] = inst
		return config.SaveConfig(f.f1277.configPath, cfg)
	}))
	adapter := &t13Adapter{BaseChannel: channels.NewBaseChannel("discord", nil, f.bus, nil)}
	adapter.SetRunning(true)
	f.svc.ChannelManager.RegisterChannel("discord", adapter)
	return f, adapter
}

func TestQ7T22_SaveCanonicalWholeListAndShareTheLiveRevision(t *testing.T) {
	f := q7Fixture(t, "telegram", "telegram", "mia")
	before := t14Read(t, f, "telegram").Revision
	result := q7Execute(t, f, q7Args("telegram", "agent_may_start", before, []string{" 222 ", "telegram:222", "-100/45"}))
	body := q7Saved(t, result, "agent_may_start", []string{"telegram:222", "telegram:-100/45"})
	t13AssertOutbound(t, f, "telegram", "agent_may_start", []string{"telegram:222", "telegram:-100/45"})
	current := t14Read(t, f, "telegram").Revision
	require.NotEqual(t, before, current, "FR-030: a changed list advances the conflict token")
	require.Equal(t, current, body["revision"], "FR-030: tool success returns the actual live token")
	listed := q7ListedChannel(t, f, "telegram")
	require.Equal(t, current, listed["revision"], "FR-034: agent and REST use the same token family")
	require.Equal(t, body["outbound"], listed["outbound"])
	second := q7Execute(t, f, q7Args("telegram", "reply_only", current, []string{}))
	q7Saved(t, second, "reply_only", []string{})
	t13AssertOutbound(t, f, "telegram", "reply_only", []string{})
}

func TestQ7T22_AuthorityPrecedesOwnConnectionAndEveryRefusalWritesNothing(t *testing.T) {
	for _, tc := range []struct{ name, caller, bound, code, message string }{
		{"Omnipus_own", "omnipus", "omnipus", "OWN_CONNECTION", "a connection's own agent cannot change its posting list; ask the operator"},
		{"ordinary_other", "mia", "jim", "OMNIPUS_ONLY", "only Omnipus can change a connection's posting list; ask the operator or Omnipus"},
		{"ordinary_own", "mia", "mia", "OMNIPUS_ONLY", "only Omnipus can change a connection's posting list; ask the operator or Omnipus"},
		{"missing_identity", "", "mia", "OMNIPUS_ONLY", "only Omnipus can change a connection's posting list; ask the operator or Omnipus"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := q7Fixture(t, "telegram", "telegram", tc.bound)
			before := t13Before(t, f)
			result := q7ExecuteAs(t, f, tc.caller, q7Args("telegram", "agent_may_start", t14Read(t, f, "telegram").Revision, []string{"222"}))
			failure := q7Error(t, result, tc.code)
			require.Equal(t, tc.message, failure["message"], "FR-031/r9 Q1 A: authority-first exact refusal")
			t13Unchanged(t, f, before)
		})
	}
}

func TestQ7T22_RequestShapeErrorsAreInvalidInputAndWriteNothing(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		value       any
		remove      bool
	}{
		{"missing_id", "id", nil, true}, {"missing_policy", "policy", nil, true},
		{"missing_targets", "targets", nil, true}, {"missing_revision", "expected_revision", nil, true},
		{"null_id", "id", nil, false}, {"null_policy", "policy", nil, false},
		{"null_targets", "targets", nil, false}, {"null_revision", "expected_revision", nil, false},
		{"wrong_id_type", "id", 1, false}, {"wrong_policy_type", "policy", true, false},
		{"targets_not_array", "targets", "222", false}, {"target_not_string", "targets", []any{222}, false},
		{"wrong_revision_type", "expected_revision", 1, false}, {"unknown_policy", "policy", "allow_all", false},
		{"unknown_property", "extra", true, false}, {"empty_id", "id", "", false},
		{"empty_revision", "expected_revision", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := q7Fixture(t, "telegram", "telegram", "mia")
			args := q7Args("telegram", "reply_only", t14Read(t, f, "telegram").Revision, []string{})
			if tc.remove {
				delete(args, tc.field)
			} else {
				args[tc.field] = tc.value
			}
			before := t13Before(t, f)
			q7Error(t, q7Execute(t, f, args), "INVALID_INPUT")
			t13Unchanged(t, f, before)
		})
	}
}

func TestQ7T22_TargetCountBoundariesCountCanonicalDistinctIDs(t *testing.T) {
	// T-2/D1: min=0, max=20; negative length is unrepresentable, null and
	// wrong-type are separate request-shape rows. 19/20/21 pin the upper edge.
	for _, count := range []int{0, 1, 19, 20, 21} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f := q7Fixture(t, "telegram", "telegram", "mia")
			input, want := []string{}, []string{}
			for i := range count {
				id := fmt.Sprint(222 + i)
				input = append(input, id)
				want = append(want, "telegram:"+id)
			}
			before := t13Before(t, f)
			result := q7Execute(t, f, q7Args("telegram", "agent_may_start", t14Read(t, f, "telegram").Revision, input))
			if count == 21 {
				failure := q7Error(t, result, "INVALID_TARGETS")
				raw, err := json.Marshal(failure)
				require.NoError(t, err)
				require.Contains(t, string(raw), "too_many_targets", "FR-030/r9 Q2 A: retain the distinct writer reason without a new tool code")
				t13Unchanged(t, f, before)
				return
			}
			q7Saved(t, result, "agent_may_start", want)
			t13AssertOutbound(t, f, "telegram", "agent_may_start", want)
		})
	}
	t.Run("21_raw_entries_but_20_distinct", func(t *testing.T) {
		f := q7Fixture(t, "telegram", "telegram", "mia")
		input := make([]string, 0, 21)
		want := make([]string, 0, 20)
		for i := range 20 {
			id := fmt.Sprint(222 + i)
			input = append(input, id)
			want = append(want, "telegram:"+id)
		}
		input = append(input, "telegram:222")
		q7Saved(t, q7Execute(t, f, q7Args("telegram", "agent_may_start", t14Read(t, f, "telegram").Revision, input)), "agent_may_start", want)
	})
}

func TestQ7T22_WriterRefusalsKeepTheirDistinctReasons(t *testing.T) {
	for _, tc := range []struct {
		name, platform, id, revision, policy, code, reason string
		targets                                            []string
	}{
		{"not_configured", "telegram", "missing-q7", "absent", "reply_only", "CHANNEL_NOT_FOUND", "", []string{}},
		{"stale", "telegram", "telegram", "q7-stale-token", "reply_only", "CONFLICT", "changed since", []string{}},
		{"username", "telegram", "telegram", "", "agent_may_start", "INVALID_TARGETS", "username_not_allowed", []string{"@alice"}},
		{"wildcard", "telegram", "telegram", "", "agent_may_start", "INVALID_TARGETS", "wildcard_not_allowed", []string{"*"}},
		{"wrong_platform", "telegram", "telegram", "", "agent_may_start", "INVALID_TARGETS", "wrong_platform", []string{"discord:222"}},
		{"unsupported_platform", "slack", "slack", "", "agent_may_start", "PLATFORM_CANNOT_START", "starting messages is not supported yet on Slack", []string{}},
		{"unsupported_person", "dingtalk", "dingtalk", "", "agent_may_start", "PLATFORM_CANNOT_START", "platform_cannot_message_people", []string{"222"}},
		{"offline_discord", "discord", "discord", "", "agent_may_start", "CONNECT_FIRST", "", []string{"222"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := q7Fixture(t, tc.platform, tc.platform, "mia")
			revision := tc.revision
			if revision == "" {
				revision = t14Read(t, f, tc.platform).Revision
			}
			before := t13Before(t, f)
			failure := q7Error(t, q7Execute(t, f, q7Args(tc.id, tc.policy, revision, tc.targets)), tc.code)
			if tc.reason != "" {
				raw, err := json.Marshal(failure)
				require.NoError(t, err)
				require.Contains(t, string(raw), tc.reason, "FR-030: preserve the writer's reason")
			}
			t13Unchanged(t, f, before)
		})
	}
}

func TestQ7T22_DiscordPerTargetReasonsSurviveTheToolAdapter(t *testing.T) {
	for _, tc := range []struct {
		reason, code string
		edge         channels.TargetErrorCode
	}{
		{"unsupported_channel_kind", "INVALID_TARGETS", channels.TargetErrorCode("unsupported_channel_kind")},
		{"not_found", "INVALID_TARGETS", channels.TargetErrorCode("not_found")},
		{"lookup_unavailable", "LOOKUP_UNAVAILABLE", channels.TargetErrorCode("lookup_unavailable")},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			f, adapter := q7DiscordFixture(t)
			adapter.lookup = func(context.Context, string) channels.OutboundTargetResult {
				return channels.OutboundTargetResult{Code: tc.edge}
			}
			before := t13Before(t, f)
			failure := q7Error(t, q7Execute(t, f, q7Args("discord", "agent_may_start", t14Read(t, f, "discord").Revision, []string{"600"})), tc.code)
			raw, err := json.Marshal(failure)
			require.NoError(t, err)
			require.Contains(t, string(raw), tc.reason, "FR-030: distinct per-target reason survives the real writer adapter")
			require.Equal(t, []string{"discord:600"}, adapter.lookupSnapshot(), "instrument: actual writer reached the external lookup boundary")
			t13Unchanged(t, f, before)
		})
	}
}

func TestQ7T22_PreWriteSaveFailureNeverClaimsSaved(t *testing.T) {
	f := q7Fixture(t, "telegram", "telegram", "mia")
	revision := t14Read(t, f, "telegram").Revision
	before := t13Before(t, f)
	path, backup := f.f1277.configPath, f.f1277.configPath+".q7-backup"
	require.NoError(t, os.Rename(path, backup))
	require.NoError(t, os.Mkdir(path, 0o700))
	var once sync.Once
	restore := func() {
		once.Do(func() { require.NoError(t, os.Remove(path)); require.NoError(t, os.Rename(backup, path)) })
	}
	t.Cleanup(restore)
	q7Error(t, q7Execute(t, f, q7Args("telegram", "reply_only", revision, []string{})), "CONFIG_SAVE_FAILED")
	restore()
	t13Unchanged(t, f, before)
}

func TestQ7T22_SavedRefreshFailureReturnsWarningAndTheEnforcedRevision(t *testing.T) {
	q7RequireOutboundTool(t)
	f, _ := t32Fixture(t, "discord", []string{"discord:222"})
	q7WireRealTools(t, f)
	t32FaultRefresh(t, f)
	before := t14Read(t, f, "discord").Revision
	body := q7Saved(t, q7Execute(t, f, q7Args("discord", "reply_only", before, []string{"222"})), "reply_only", []string{"discord:222"})
	warning, ok := body["warning"].(string)
	require.True(t, ok)
	require.True(t, strings.HasPrefix(warning, "Saved and in force for starting messages."), "T-6b: truthful saved-but-not-refreshed result")
	require.Equal(t, t14Read(t, f, "discord").Revision, body["revision"])
	t13AssertOutbound(t, f, "discord", "reply_only", []string{"discord:222"})
}

func TestQ7T22_SavedLiveMismatchIsConflictNotStaleRevision(t *testing.T) {
	q7RequireOutboundTool(t)
	f, _ := t32Fixture(t, "discord", []string{"discord:222"})
	q7WireRealTools(t, f)
	t32FaultRefresh(t, f)
	t32FailedCommonSave(t, f, false, t32RebindDoc(t))
	before := t13Before(t, f)
	failure := q7Error(t, q7Execute(t, f, q7Args("discord", "agent_may_start", t14Read(t, f, "discord").Revision, []string{"222"})), "CONFLICT")
	raw, err := json.Marshal(failure)
	require.NoError(t, err)
	require.Contains(t, string(raw), "The saved and running configurations for this connection differ because an earlier change did not finish applying.")
	require.NotContains(t, string(raw), "changed since they were read", "FR-030: a current token is not stale; saved/live correspondence is the distinct refusal")
	t13Unchanged(t, f, before)
}
