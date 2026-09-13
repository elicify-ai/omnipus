/**
 * goalCommandMessage.test.ts
 *
 * UAT defect B, parser half. The oracle for every case is the SERVER's own
 * command parsing, not this module's implementation:
 *
 *   - `pkg/commands/request.go::parseCommandName` — the first token, with
 *     EITHER prefix in `commandPrefixes = []string{"/", "!"}` stripped, an
 *     `@…` suffix (Telegram's `/goal@bot`) cut, then lowercased. Every one of
 *     those spellings reaches `applyGoalCommandPrompt`'s `cmdName != "goal"`
 *     gate as the same command, so every one of them sets a real goal.
 *   - `pkg/commands/request.go::CommandArgs` — `strings.Fields` on the
 *     trimmed input, drop the command token, rejoin with single spaces. So
 *     runs of whitespace collapse and a bare command yields empty args.
 *   - `pkg/agent/goal_loop.go::applyGoalCommandPrompt` — empty args print
 *     status, `isGoalClearVerb(args)` clears, `goalConfirmNoOpArg`
 *     ("confirm") is an ADR-081 no-op reply. Only what falls through all
 *     three actually SETS a goal.
 *   - `pkg/commands/cmd_goal.go::GoalClearAliases` — clear, stop, off, reset,
 *     cancel, none. The command itself has no aliases (`Name: "goal"`).
 *
 * The function answers one question — did this message set a goal? — because
 * that is the only question its caller asks; see `messageSetsGoal`'s own doc
 * comment for why returning the intent string was worse than useless.
 */

import { describe, it, expect } from 'vitest'
import { messageSetsGoal } from './goalCommandMessage'

describe('messageSetsGoal — what the server would treat as setting a goal', () => {
  it('recognises an ordinary goal command', () => {
    expect(messageSetsGoal('/goal ship the release')).toBe(true)
  })

  it('is unmoved by whitespace runs, exactly as CommandArgs is', () => {
    expect(messageSetsGoal('  /goal   ship   the  release  ')).toBe(true)
  })

  it('accepts the command word case-insensitively', () => {
    expect(messageSetsGoal('/GOAL ship the release')).toBe(true)
    expect(messageSetsGoal('/Goal ship the release')).toBe(true)
  })

  it('does not care about the intent\'s own casing', () => {
    expect(messageSetsGoal('/goal Ship The Release')).toBe(true)
  })

  it('returns false for a bare /goal — that prints status, it sets nothing', () => {
    expect(messageSetsGoal('/goal')).toBe(false)
    expect(messageSetsGoal('  /goal  ')).toBe(false)
  })

  it.each(['clear', 'stop', 'off', 'reset', 'cancel', 'none'])(
    'returns false for the clear verb "%s"',
    (verb) => {
      expect(messageSetsGoal(`/goal ${verb}`)).toBe(false)
      expect(messageSetsGoal(`/goal ${verb.toUpperCase()}`)).toBe(false)
    },
  )

  it('returns false for /goal confirm — an ADR-081 no-op reply, not a goal named "confirm"', () => {
    expect(messageSetsGoal('/goal confirm')).toBe(false)
    expect(messageSetsGoal('/goal CONFIRM')).toBe(false)
  })

  it('still treats a clear verb WITH extra words as a real goal, matching isGoalClearVerb\'s whole-args match', () => {
    // `isGoalClearVerb` compares the ENTIRE args string, so "stop the build"
    // is an intent, not a clear.
    expect(messageSetsGoal('/goal stop the build')).toBe(true)
    expect(messageSetsGoal('/goal confirm the booking with the venue')).toBe(true)
  })

  it('returns false for ordinary chat and for other slash commands', () => {
    expect(messageSetsGoal('ship the release')).toBe(false)
    expect(messageSetsGoal('/plan ship the release')).toBe(false)
    expect(messageSetsGoal('/goals ship the release')).toBe(false)
    expect(messageSetsGoal('please /goal ship it')).toBe(false)
  })

  it('returns false for empty, whitespace-only, null and undefined content', () => {
    expect(messageSetsGoal('')).toBe(false)
    expect(messageSetsGoal('   ')).toBe(false)
    expect(messageSetsGoal(null)).toBe(false)
    expect(messageSetsGoal(undefined)).toBe(false)
  })
})

// ── Every spelling the SERVER accepts (pkg/commands/request.go) ──────────────
// A goal set with one of these spellings is a real, active goal server-side;
// a parser that only knew "/" left every one of them replaying as an
// anonymous bubble — the exact defect this module exists to fix.

describe('messageSetsGoal — the prefixes and suffixes parseCommandName accepts', () => {
  it('recognises the "!" prefix — commandPrefixes carries BOTH "/" and "!"', () => {
    expect(messageSetsGoal('!goal ship the release')).toBe(true)
    expect(messageSetsGoal('  !GOAL   ship the release ')).toBe(true)
  })

  it('recognises the "@bot" suffix parseCommandName cuts before comparing', () => {
    expect(messageSetsGoal('/goal@omnipus ship the release')).toBe(true)
    expect(messageSetsGoal('/GOAL@Omnipus_Bot ship the release')).toBe(true)
  })

  it('recognises both together', () => {
    expect(messageSetsGoal('!goal@omnipus ship the release')).toBe(true)
  })

  it('applies the same non-setting rules to those spellings', () => {
    expect(messageSetsGoal('!goal')).toBe(false)
    expect(messageSetsGoal('/goal@omnipus')).toBe(false)
    expect(messageSetsGoal('!goal clear')).toBe(false)
    expect(messageSetsGoal('!goal@omnipus confirm')).toBe(false)
  })

  it('does not widen past what the server accepts', () => {
    // Not a command prefix at all.
    expect(messageSetsGoal('#goal ship it')).toBe(false)
    expect(messageSetsGoal('-goal ship it')).toBe(false)
    expect(messageSetsGoal('goal ship it')).toBe(false)
    // A different command under an accepted prefix.
    expect(messageSetsGoal('!plan ship it')).toBe(false)
    expect(messageSetsGoal('!goals ship it')).toBe(false)
    // The name is what precedes "@" — here, nothing.
    expect(messageSetsGoal('/@goal ship it')).toBe(false)
    // A prefix with no command word at all.
    expect(messageSetsGoal('/ goal ship it')).toBe(false)
    expect(messageSetsGoal('!')).toBe(false)
  })
})

// ── Large inputs ────────────────────────────────────────────────────────────
// This runs once per user row per render of a virtualized transcript, so the
// input can be a pasted log. Correctness must not depend on the size, and the
// non-command answer must be settled from the first character.

describe('messageSetsGoal — large pasted content', () => {
  const bigBody = 'ERROR could not bind port '.repeat(4000) // ~104 KB

  it('returns false for a big paste that is not a command', () => {
    expect(messageSetsGoal(bigBody)).toBe(false)
    expect(bigBody.length).toBeGreaterThan(100_000)
  })

  it('still recognises a goal command whose intent is a big paste', () => {
    expect(messageSetsGoal(`/goal fix this: ${bigBody}`)).toBe(true)
  })

  it('returns false for a big paste that merely contains a goal command later on', () => {
    expect(messageSetsGoal(`${bigBody} /goal ship it`)).toBe(false)
  })
})
