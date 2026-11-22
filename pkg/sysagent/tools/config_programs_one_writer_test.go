// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: scenario S-45's first clause (TestConfigSet_ChannelProgramsAllowed_OneWriter
// — restored by CHECK R02 after the round-1 repair wrongly claimed order 26
// covered it): FR-009 — the per-connection programs setting has its own
// person-only writer, and `set_config` cannot change it. The spec's Given is
// the key path `channels.telegram.programs_allowed`; the Then is "refused;
// the setting is unchanged".
//
// The refusal must be the one-writer refusal, never the generic
// KEY_NOT_FOUND class: the rule must hold when the setting EXISTS, so a
// correct GREEN never answers "Config key not found" here (the same shape as
// the existing channels.*.outbound refusal, FR-024's precedent). The
// unchanged half reads the setting back through get_config: at RED the field
// does not exist in config at all, so the read-back BLOCKs loudly naming the
// missing setting — never a silent pass.

package systools_test

import (
	"context"
	"strings"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/require"
)

func TestConfigSet_ChannelProgramsAllowed_OneWriter(t *testing.T) {
	deps, _ := newTestDeps(t)
	ctx := context.Background()

	t.Run("S45_set_config_refused_by_the_one_writer_rule", func(t *testing.T) {
		result := systools.NewConfigSetTool(deps).Execute(ctx, map[string]any{
			"key":   "channels.telegram.programs_allowed",
			"value": true,
		})
		require.True(t, result.IsError,
			"S-45: set_config must refuse channels.telegram.programs_allowed: %s", result.ForLLM)
		require.Contains(t, result.ForLLM, "programs_allowed",
			"the refusal is about this key: %s", result.ForLLM)
		// The refusal is the ONE-WRITER rule, never a generic class: the rule
		// must hold when the setting exists, so a correct GREEN never answers
		// "names no setting"/"not found" here, and — like the established
		// channels.*.outbound refusal — it names where the setting actually
		// belongs (the Connectors page / its own programs writer).
		require.NotContains(t, result.ForLLM, "names no setting",
			"the refusal is the one-writer rule (FR-009: the setting has its own person-only writer), "+
				"never the landing-nowhere class: %s", result.ForLLM)
		require.NotContains(t, result.ForLLM, "not found",
			"never the generic KEY_NOT_FOUND class: %s", result.ForLLM)
		require.True(t, strings.Contains(result.ForLLM, "Connectors") ||
			strings.Contains(result.ForLLM, "/programs"),
			"the refusal names where the setting actually belongs (the person-only writer), "+
				"as every blocklist refusal does: %s", result.ForLLM)
	})

	t.Run("S45_the_setting_is_unchanged_after_the_refused_write", func(t *testing.T) {
		get := systools.NewConfigGetTool(deps).Execute(ctx, map[string]any{
			"key": "channels.telegram.programs_allowed",
		})
		if get.IsError {
			t.Fatalf("BLOCKED: the per-connection programs setting is not readable back — "+
				"get_config refused the read (%s) — required by S-45's 'the setting is unchanged' "+
				"(the channel config carries no programs_allowed field at RED; FR-009)", get.ForLLM)
		}
		require.NotContains(t, get.ForLLM, "true",
			"the refused set_config left the setting unchanged (not enabled): %s", get.ForLLM)
	})
}
