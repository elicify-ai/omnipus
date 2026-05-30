package telegram

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/commands"
)

// TestCancel_TelegramCommandExactSet (T12a) — asserts that:
//  1. The set of command names registered by Telegram's startCommandRegistration
//     includes "/cancel" (derived from commands.BuiltinDefinitions()).
//  2. The forbidden aliases /stop, /abort, /kill are NOT in the registered set
//     (FR-5 alias prohibition).
//
// Theater smell: the existing command_registration_test.go only checks that
// retries work correctly — it never asserts on the actual command names passed
// to the Telegram API.
//
// This version uses registerFunc injection to capture the exact definitions
// list that startCommandRegistration passes to the Telegram Bot API, then
// asserts on the name set.
//
// Traces to: pkg/commands/cmd_cancel.go (cancelCommand — no Aliases field).
// Traces to: pkg/channels/telegram/telegram.go:151 — c.startCommandRegistration(ctx, commands.BuiltinDefinitions()).
// Spec ref: FR-2, FR-5.

// TestCancel_TelegramExactCommandSet verifies that the command set registered
// to Telegram includes /cancel and excludes the forbidden aliases.
func TestCancel_TelegramExactCommandSet(t *testing.T) {
	t.Parallel()

	ch := &TelegramChannel{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Capture the definitions list that startCommandRegistration passes to
	// the Telegram Bot API (RegisterCommands).
	var capturedDefs []commands.Definition
	ch.registerFunc = func(_ context.Context, defs []commands.Definition) error {
		capturedDefs = defs
		return nil // success on first attempt
	}

	// startCommandRegistration uses commands.BuiltinDefinitions() via the caller
	// (telegram.go:151). We drive it with the real BuiltinDefinitions so the test
	// reflects what production registers.
	ch.startCommandRegistration(ctx, commands.BuiltinDefinitions())

	// Wait for the registration goroutine to complete (it calls registerFunc once
	// synchronously on success with backoff=0 on first attempt).
	ch.WaitCommandRegistrationDone()

	// The captured defs must not be nil — if they are, registration never fired.
	require.NotNil(t, capturedDefs,
		"registerFunc must be called by startCommandRegistration; "+
			"capturedDefs is nil — check that startCommandRegistration calls registerFunc")

	// Build a set of names for easy lookup.
	nameSet := make(map[string]bool, len(capturedDefs))
	for _, d := range capturedDefs {
		nameSet[d.Name] = true
	}

	// ASSERT 1: /cancel must be registered.
	assert.True(t, nameSet["cancel"],
		"Telegram must register the /cancel command; names in set: %v", nameSet)

	// ASSERT 2: forbidden aliases must NOT be registered (FR-5).
	forbiddenAliases := []string{"stop", "abort", "kill"}
	for _, alias := range forbiddenAliases {
		assert.False(t, nameSet[alias],
			"Telegram must NOT register /%s — it is a forbidden alias for /cancel (FR-5); "+
				"names in set: %v", alias, nameSet)
	}

	// DIFFERENTIATION: if a future developer adds /stop as an alias to
	// cancelCommand(), this test catches it immediately. The exact-set check
	// fails if any forbidden name appears — not just if /cancel is missing.

	// CONTENT ASSERTION: verify cancel definition has non-empty description
	// (Telegram requires non-empty descriptions for bot commands).
	for _, d := range capturedDefs {
		if d.Name == "cancel" {
			assert.NotEmpty(t, d.Description,
				"/cancel definition must have a non-empty description for Telegram BotFather")
			break
		}
	}
}

// TestCancel_TelegramNoStopAbortKillInBuiltins verifies directly that
// commands.BuiltinDefinitions() does not contain /stop, /abort, or /kill as
// either primary names or aliases. This is the compile-time enforcement of FR-5.
//
// If someone adds these as aliases (even if Telegram doesn't register them
// separately), the FR-5 prohibition requires they not exist at all.
func TestCancel_TelegramNoStopAbortKillInBuiltins(t *testing.T) {
	t.Parallel()

	defs := commands.BuiltinDefinitions()
	forbidden := map[string]bool{"stop": true, "abort": true, "kill": true}

	for _, d := range defs {
		// Check primary name.
		assert.False(t, forbidden[d.Name],
			"command name %q is a forbidden alias for /cancel (FR-5 prohibition)", d.Name)
		// Check aliases field if it exists.
		for _, alias := range d.Aliases {
			assert.False(t, forbidden[alias],
				"command %q has forbidden alias %q (FR-5 prohibition)", d.Name, alias)
		}
	}
}
