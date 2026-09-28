// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WP-C RED tests for the FR-002 visibility gate predicate (spec FR-002,
// dataset "gated boundary matrix" rows 1-7, section 16 items 8/9/10).
// The pinned production symbol is pkg/gateway/thinking_gate.go::thinkingVisible
// (new file in backend-lead's GREEN).
//
// The predicate, verbatim: show = !isCLIToken && userID != "" && row found
// && row.show_thinking && no read error. isCLIToken comes from the
// AUTHENTICATION METHOD, never the username string: a CLI token resolving
// userID "cli" stays gated off even when a real account named "cli" exists
// with its toggle ON (dataset row 3 / BDD "A CLI token is gated off even
// when a real account named cli exists").
//
// RED status: compile-fail on thinkingVisible and
// config.UserConfig.ShowThinking - the sanctioned RED for new-API tests.
//
// Disclosure: the predicate's "no read error" row (dataset row 7) is
// structurally unreachable against an in-memory config snapshot; fail-closed
// on a missing row is covered by the lookup-miss case (row 6).

func TestThinkingVisible_GatePredicate(t *testing.T) {
	cliRowOn := func() *config.Config {
		return &config.Config{Gateway: config.GatewayConfig{Users: []config.UserConfig{
			{Username: "cli", ShowThinking: true},
		}}}
	}

	t.Run("real login toggle on shows", func(t *testing.T) {
		cfg := &config.Config{Gateway: config.GatewayConfig{Users: []config.UserConfig{
			{Username: "alice", ShowThinking: true},
		}}}
		assert.True(t, thinkingVisible(cfg, "alice", false),
			"dataset row 1: a real login with its toggle on sees thinking")
	})

	t.Run("real login toggle off hides (default off)", func(t *testing.T) {
		cfg := &config.Config{Gateway: config.GatewayConfig{Users: []config.UserConfig{
			{Username: "alice", ShowThinking: false},
		}}}
		assert.False(t, thinkingVisible(cfg, "alice", false),
			"dataset row 2: a toggle-off login receives zero thinking bytes (FR-003 default off)")
	})

	t.Run("lookup miss hides fail-closed", func(t *testing.T) {
		cfg := &config.Config{Gateway: config.GatewayConfig{Users: []config.UserConfig{
			{Username: "alice", ShowThinking: true},
		}}}
		assert.False(t, thinkingVisible(cfg, "nobody", false),
			"dataset row 6: a deleted row (lookup miss) is toggle-off, fail closed")
	})

	t.Run("empty identity hides", func(t *testing.T) {
		cfg := cliRowOn()
		assert.False(t, thinkingVisible(cfg, "", false),
			"dataset row 4: an empty identity (dev bypass) is hidden")
	})

	t.Run("cli token hidden even when a real cli account row is on", func(t *testing.T) {
		cfg := cliRowOn()
		assert.False(t, thinkingVisible(cfg, "cli", true),
			"dataset row 3: the predicate keys on the AUTHENTICATION METHOD, never the "+
				"username string - a CLI token with userID \"cli\" stays hidden even when a real "+
				"account named \"cli\" has its toggle ON")
	})

	t.Run("real cli-named account row with toggle on shows", func(t *testing.T) {
		cfg := cliRowOn()
		assert.True(t, thinkingVisible(cfg, "cli", false),
			"the username is not the key: a REAL login named \"cli\" with its row ON sees thinking - "+
				"this is the row that kills an isCLIToken-by-username mutation")
	})

	t.Run("live flip takes effect without restart", func(t *testing.T) {
		cfg := &config.Config{Gateway: config.GatewayConfig{Users: []config.UserConfig{
			{Username: "alice", ShowThinking: false},
		}}}
		require.False(t, thinkingVisible(cfg, "alice", false), "starts off (FR-003)")
		cfg.Gateway.Users[0].ShowThinking = true
		assert.True(t, thinkingVisible(cfg, "alice", false),
			"the gate reads the config row live - a flip takes effect on the next read, no restart (FR-004)")
	})
}
