// Omnipus — channel allow-list / canonical channel-type drift regression tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"sort"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// knownChannelToolIDs returns the sorted set of channel ids the sysagent
// channel-management tools (enable_channel/configure_channel/etc.) accept,
// observed indirectly via list_channels — channelEntry/knownChannels are
// unexported, so this test goes through the same public surface a caller
// (an LLM agent) does.
func knownChannelToolIDs(t *testing.T) []string {
	t.Helper()
	deps, _ := newTestDeps()
	res := systools.NewChannelListTool(deps).Execute(context.Background(), nil)
	if res.IsError {
		t.Fatalf("list_channels returned an error: %s", res.ForLLM)
	}
	out := parseToolJSON(t, res.ForLLM)
	rawChannels, ok := out["channels"].([]any)
	if !ok {
		t.Fatalf("list_channels response missing \"channels\" array: %s", res.ForLLM)
	}
	ids := make([]string, 0, len(rawChannels))
	for _, rc := range rawChannels {
		ch, ok := rc.(map[string]any)
		if !ok {
			t.Fatalf("list_channels entry has unexpected shape: %#v", rc)
		}
		id, _ := ch["id"].(string)
		if id == "" {
			t.Fatalf("list_channels entry missing \"id\": %#v", rc)
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// TestKnownChannels_MatchesCanonicalChannelTypes is the drift regression test
// for the bug where pkg/sysagent/tools's hand-written channel allow-list had
// drifted out of sync with pkg/config's canonical knownChannelTypes in both
// directions: "signal" was accepted by enable_channel/configure_channel/
// test_channel/disable_channel but is NOT a real supported channel type (any
// config entry under an id outside knownChannelTypes is dropped on the next
// config reload — config.normalizeChannelMap); several real, supported
// channels (feishu, qq, dingtalk, matrix, wecom, weixin, google-chat) were
// rejected with CHANNEL_NOT_FOUND despite being genuinely supported.
//
// Since the fix derives the sysagent tool's id set directly from
// config.KnownChannelTypes() (buildKnownChannels in channel.go), this test
// is true by construction today — its job is to keep failing loudly if a
// future refactor reintroduces a second, independently-maintained id list.
func TestKnownChannels_MatchesCanonicalChannelTypes(t *testing.T) {
	got := knownChannelToolIDs(t)
	want := config.KnownChannelTypes()

	if len(got) != len(want) {
		t.Fatalf("sysagent channel tool ids = %v (len %d), want %v (len %d) — the two lists have drifted apart again",
			got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sysagent channel tool ids[%d] = %q, want %q (config.KnownChannelTypes()) — the two lists have drifted apart again",
				i, got[i], want[i])
		}
	}
}

// TestKnownChannels_SignalNotAccepted is a direct regression test for the
// specific reported symptom: "signal" is not (yet) a canonical supported
// channel type in this codebase (WIP, unpushed sibling clone per CLAUDE.md;
// pkg/channels has no signal/ package here), so it must be rejected with
// CHANNEL_NOT_FOUND rather than silently accepted and then dropped on the
// next config reload.
func TestKnownChannels_SignalNotAccepted(t *testing.T) {
	deps, _ := newTestDeps()
	res := systools.NewChannelEnableTool(deps).Execute(context.Background(), map[string]any{"id": "signal"})
	if !res.IsError {
		t.Fatalf("enable_channel(\"signal\") should be rejected (signal is not a canonical channel type "+
			"in this codebase yet — see config.KnownChannelTypes), got success: %s", res.ForLLM)
	}
}

// TestKnownChannels_PreviouslyMissingChannelsAreAccepted is a direct
// regression test for the other half of the drift: real, supported channels
// that the old hand-written list omitted must now be accepted by
// enable_channel (i.e. resolve via findChannel, not CHANNEL_NOT_FOUND).
func TestKnownChannels_PreviouslyMissingChannelsAreAccepted(t *testing.T) {
	previouslyMissing := []string{"feishu", "qq", "dingtalk", "matrix", "wecom", "weixin", "google-chat"}
	for _, id := range previouslyMissing {
		t.Run(id, func(t *testing.T) {
			deps, cfg := newTestDeps()
			if cfg.Channels == nil {
				cfg.Channels = map[string]config.ChannelInstanceConfig{}
			}
			res := systools.NewChannelEnableTool(deps).Execute(context.Background(), map[string]any{"id": id})
			if res.IsError {
				t.Errorf("enable_channel(%q) should succeed (it is a real, supported channel type), got error: %s",
					id, res.ForLLM)
			}
		})
	}
}
