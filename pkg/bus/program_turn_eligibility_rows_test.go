// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: TestProgramTurnEligibility (order 26, Unit) — the per-turn
// eligibility decision the S-39–S-45 runtime scenarios execute: the origin
// set of a turn's inputs evaluated against the origin settings (FR-010,
// FR-012, founder Q1 = A). The rows are the spec's settings table in unit
// form: connector default off, channel switched on, web default on, web
// off, mixed connections, settings consulted per evaluation (S-44's new
// command), the withheld reasons naming the blocked origin (S-39/S-43's
// notice), and independent work joined with the web setting (S-124's
// founder Q1 = A core).
//
// Oracle: FR-010's eligibility table and FR-012's notice naming. Expected
// values come from the spec, never from the implementation. The runtime
// rows of S-39–S-45 (printenv outputs, notices in chat, the running
// command's kept environment) ride the env integration slice (order 22's
// harness); this file pins the decision itself.
//
// RED expectation: the Provenance value type is the RED stub — every
// construction resolves to the unresolved zero value, so the eligible rows
// report withheld and the withheld rows report empty reasons.

package bus

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// s1AllowFromSettings builds the `allowed` predicate over the spec's two
// origin settings: the web-chat setting covers web AND independent (founder
// Q1 = A), each connection's `programs_allowed` covers its connector origin.
func s1AllowFromSettings(webChat bool, connectionAllowed map[string]bool) func(Origin) bool {
	return func(o Origin) bool {
		if IsConnectorOrigin(o) {
			return connectionAllowed[string(o[len(OriginConnectorPrefix):])]
		}
		return webChat
	}
}

func TestProgramTurnEligibility(t *testing.T) {
	const telegram = "telegram"
	const discord = "discord"

	t.Run("S39_connector_default_off_withheld_naming_it", func(t *testing.T) {
		p := NewProvenance(ConnectorOrigin(telegram))
		require.False(t, p.Eligible(s1AllowFromSettings(true, map[string]bool{telegram: false})),
			"a connection never changed is off by default: the turn is withheld")
		require.Equal(t, []Origin{ConnectorOrigin(telegram)},
			p.WithheldOrigins(s1AllowFromSettings(true, map[string]bool{telegram: false})),
			"the withheld reason names telegram")
	})

	t.Run("S40_channel_switched_on_eligible", func(t *testing.T) {
		p := NewProvenance(ConnectorOrigin(telegram))
		require.True(t, p.Eligible(s1AllowFromSettings(true, map[string]bool{telegram: true})),
			"telegram allowed: the turn may use connected programs")
		require.Empty(t, p.WithheldOrigins(s1AllowFromSettings(true, map[string]bool{telegram: true})))
	})

	t.Run("S41_web_default_on_eligible", func(t *testing.T) {
		p := NewProvenance(OriginWeb)
		require.True(t, p.Eligible(s1AllowFromSettings(true, nil)),
			"the web-chat setting never changed (default on): the turn is eligible")
	})

	t.Run("S42_web_off_withheld", func(t *testing.T) {
		p := NewProvenance(OriginWeb)
		require.False(t, p.Eligible(s1AllowFromSettings(false, nil)),
			"web_chat_allowed=false withholds the web turn")
		require.Equal(t, []Origin{OriginWeb}, p.WithheldOrigins(s1AllowFromSettings(false, nil)))
	})

	t.Run("S43_mixed_connections_withheld_naming_the_blocked_one", func(t *testing.T) {
		p := NewProvenance(ConnectorOrigin(telegram), ConnectorOrigin(discord))
		allow := s1AllowFromSettings(true, map[string]bool{telegram: true, discord: false})
		require.False(t, p.Eligible(allow),
			"one blocked origin withholds the whole turn (monotone restriction)")
		require.Equal(t, []Origin{ConnectorOrigin(discord)}, p.WithheldOrigins(allow),
			"the notice names discord, not telegram")
	})

	t.Run("S44_settings_are_consulted_per_evaluation", func(t *testing.T) {
		// S-44's unit core: the running command keeps its environment
		// (runtime), and a NEW command is evaluated against the CURRENT
		// settings — the decision is per evaluation, never cached in the
		// provenance value.
		p := NewProvenance(ConnectorOrigin(telegram))
		allowOn := s1AllowFromSettings(true, map[string]bool{telegram: true})
		allowOff := s1AllowFromSettings(true, map[string]bool{telegram: false})
		require.True(t, p.Eligible(allowOn), "allowed while the setting is on")
		require.False(t, p.Eligible(allowOff), "the same turn shape is withheld after the setting is turned off")
	})

	t.Run("S124_independent_follows_the_web_setting_founder_Q1_A", func(t *testing.T) {
		p := NewProvenance(OriginIndependent)
		require.False(t, p.Eligible(s1AllowFromSettings(false, nil)),
			"with web off, independent work is withheld (founder Q1 = A)")
		require.True(t, p.Eligible(s1AllowFromSettings(true, nil)),
			"with web on, independent work is eligible")
	})

	t.Run("unresolved_stays_ineligible_with_the_fail_closed_reason", func(t *testing.T) {
		// S-111's And-clause in unit form: a producer without provenance is
		// withheld, never permissive, whatever the settings.
		var zero Provenance
		require.False(t, zero.Eligible(s1AllowFromSettings(true, map[string]bool{telegram: true, discord: true})),
			"unresolved withholds even when every setting allows")
		require.True(t, zero.IsUnresolved())
	})
}
