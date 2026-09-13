/**
 * goalCommandMessage.test.ts
 *
 * UAT defect B, parser half. The oracle for every case is the SERVER's own
 * command parsing, not this module's implementation:
 *
 *   - `pkg/commands/request.go::CommandArgs` — `strings.Fields` on the trimmed
 *     input, drop the command word, rejoin with single spaces. So runs of
 *     whitespace collapse and a bare command yields empty args.
 *   - `pkg/commands/request.go::normalizeCommandName` — lowercases, so the
 *     command word is case-insensitive.
 *   - `pkg/agent/goal_loop.go::applyGoalCommandPrompt` — empty args print
 *     status, `isGoalClearVerb(args)` clears, `goalConfirmNoOpArg`
 *     ("confirm") is an ADR-081 no-op reply. Only what falls through all
 *     three actually SETS a goal.
 *   - `pkg/commands/cmd_goal.go::GoalClearAliases` — clear, stop, off, reset,
 *     cancel, none. The command itself has no aliases (`Name: "goal"`).
 */

import { describe, it, expect } from 'vitest'
import { goalCommandStatement } from './goalCommandMessage'

describe('goalCommandStatement — what the server would treat as setting a goal', () => {
  it('returns the intent for an ordinary goal command', () => {
    expect(goalCommandStatement('/goal ship the release')).toBe('ship the release')
  })

  it('collapses whitespace runs exactly as CommandArgs does', () => {
    expect(goalCommandStatement('  /goal   ship   the  release  ')).toBe('ship the release')
  })

  it('accepts the command word case-insensitively', () => {
    expect(goalCommandStatement('/GOAL ship the release')).toBe('ship the release')
    expect(goalCommandStatement('/Goal ship the release')).toBe('ship the release')
  })

  it('preserves the intent\'s own casing — only the command word is normalised', () => {
    expect(goalCommandStatement('/goal Ship The Release')).toBe('Ship The Release')
  })

  it('returns null for a bare /goal — that prints status, it sets nothing', () => {
    expect(goalCommandStatement('/goal')).toBeNull()
    expect(goalCommandStatement('  /goal  ')).toBeNull()
  })

  it.each(['clear', 'stop', 'off', 'reset', 'cancel', 'none'])(
    'returns null for the clear verb "%s"',
    (verb) => {
      expect(goalCommandStatement(`/goal ${verb}`)).toBeNull()
      expect(goalCommandStatement(`/goal ${verb.toUpperCase()}`)).toBeNull()
    },
  )

  it('returns null for /goal confirm — an ADR-081 no-op reply, not a goal named "confirm"', () => {
    expect(goalCommandStatement('/goal confirm')).toBeNull()
  })

  it('still treats a clear verb WITH extra words as a real goal, matching isGoalClearVerb\'s whole-args match', () => {
    // `isGoalClearVerb` compares the ENTIRE args string, so "stop the build"
    // is an intent, not a clear.
    expect(goalCommandStatement('/goal stop the build')).toBe('stop the build')
  })

  it('returns null for ordinary chat and for other slash commands', () => {
    expect(goalCommandStatement('ship the release')).toBeNull()
    expect(goalCommandStatement('/plan ship the release')).toBeNull()
    expect(goalCommandStatement('/goals ship the release')).toBeNull()
    expect(goalCommandStatement('please /goal ship it')).toBeNull()
  })

  it('returns null for empty, whitespace-only, null and undefined content', () => {
    expect(goalCommandStatement('')).toBeNull()
    expect(goalCommandStatement('   ')).toBeNull()
    expect(goalCommandStatement(null)).toBeNull()
    expect(goalCommandStatement(undefined)).toBeNull()
  })
})
