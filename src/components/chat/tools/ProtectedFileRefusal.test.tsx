// Unit 5 / #1284, strengthened for CHECK2 B5-copy (F3).
// Independent oracle: L1 amendment §2 D6 says protected paths are "refused
// outright (never prompted, never grantable)"; D7 says "God Mode never lifts
// the protection". Never import the production explanation as the expectation.
// Existing shell/file producer, result/error and ordinary-failure controls
// from the pinned feature test are retained; only the coupled copy oracle changes.
import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import type { MessagePartStatus } from '@assistant-ui/react'
import { GenericToolCall } from './GenericToolCall'
import { resolveToolResult } from './toolResultDisplay'

// Producer fixtures from the feature's existing regression test, not an
// expected UI constant. Their classification assertions stay unchanged.
const SHELL_REFUSAL = 'Command blocked: "/home/u/.omnipus/sessions/a.jsonl" is inside Omnipus\'s protected internal files, which no agent command may read or change in any mode — God Mode included. This is never grantable; work inside the workspace and use the dedicated internal tools where one exists.'
const FILE_REFUSAL = 'failed to read file: access denied: path is a protected carve-out ($OMNIPUS_HOME-anchored)'
const DONE: MessagePartStatus = { type: 'complete' }
// not-wire-format: additive public resolver signature at the feature head.
// The base accepts only result; this type adapter keeps its missing error-path
// behavior as a runtime RED, not a collection/typecheck failure. No mock.
const resolveResultOrError = resolveToolResult as unknown as (result: unknown, error?: unknown) => { kind: string }

// Independently bounded equivalents approved by team-lead: D6 specifies
// never-grantable meaning, not one production-owned sentence. D7 still has
// its own required God-Mode clause, and approval/retry advice is forbidden.
const NEVER_GRANTABLE = /\b(?:never\s+grantable|nothing\s+can\s+approve|cannot\s+be\s+approved)\b/i
const GOD_MODE_INCLUDED = /\bGod\s+Mode\s+included\b/i
function refusalMeaning(text: string) {
  const normalized = text.replace(/[‘’]/g, "'")
  const withoutExplicitProhibitions = normalized
    .replace(/\b(?:do\s+not|don't|never)\s+(?:retry|try\s+again|ask\s+for\s+approval)\b/gi, '')
    .replace(new RegExp(NEVER_GRANTABLE.source, 'gi'), '')
  return {
    neverGrantable: NEVER_GRANTABLE.test(normalized),
    godModeIncluded: GOD_MODE_INCLUDED.test(normalized),
    approvalOrRetryAdvice: /\b(?:ask|request|seek|get)\s+(?:for\s+)?(?:approval|permission)\b|\b(?:approve|retry|try\s+again|always\s+allow)\b|\bapproval\s+(?:can|will|may)\b/i.test(withoutExplicitProhibitions),
  }
}
const REQUIRED_MEANING = { neverGrantable: true, godModeIncluded: true, approvalOrRetryAdvice: false }

describe('#1284 protected-file refusal', () => {
  it('D6/D7 instrument: semantic oracle detects approval/retry advice and a missing God-Mode clause', () => {
    // Test the assertion instrument with literal counterexamples; do NOT
    // mutate any production code or replace the real rendered component.
    expect(refusalMeaning('Never grantable; God Mode included.')).toEqual(REQUIRED_MEANING)
    expect(refusalMeaning('Nothing can approve this. God Mode included.')).toEqual(REQUIRED_MEANING)
    expect(refusalMeaning('Cannot be approved. God Mode included. Do not retry.')).toEqual(REQUIRED_MEANING)
    expect(refusalMeaning('Ask for approval and retry. God Mode included.')).toEqual({
      neverGrantable: false, godModeIncluded: true, approvalOrRetryAdvice: true,
    })
    expect(refusalMeaning('Never grantable, but ask for approval and retry. God Mode included.')).toEqual({
      neverGrantable: true, godModeIncluded: true, approvalOrRetryAdvice: true,
    })
    expect(refusalMeaning('Nothing can approve this. Try again. God Mode included.')).toEqual({
      neverGrantable: true, godModeIncluded: true, approvalOrRetryAdvice: true,
    })
    expect(refusalMeaning('Never grantable. Always allow this path. God Mode included.')).toEqual({
      neverGrantable: true, godModeIncluded: true, approvalOrRetryAdvice: true,
    })
    expect(refusalMeaning('Nothing can approve this.')).toEqual({
      neverGrantable: true, godModeIncluded: false, approvalOrRetryAdvice: false,
    })
  })
  it.each([SHELL_REFUSAL, FILE_REFUSAL])('resolves the refusal as its own kind from the result or the error: %s', text => {
    expect(resolveToolResult(text).kind).toBe('protectedFile')
    expect(resolveResultOrError(undefined, text).kind).toBe('protectedFile')
  })

  it('an ordinary failure is not mistaken for it', () => {
    expect(resolveToolResult('failed to read file: access denied: permission denied').kind).toBe('text')
  })

  it('the tool row explains it is never grantable, including in God Mode, and keeps the server text', () => {
    // This producer text contains no never-grantable/God-Mode words: those
    // promises must come from the rendered explanation, not the raw result.
    render(<GenericToolCall toolName="read_file" error={FILE_REFUSAL} status={DONE} isError />)
    fireEvent.click(screen.getByRole('button'))
    const block = screen.getByTestId('result-protected-file')
    expect(block).toHaveTextContent('Protected Omnipus file')
    expect(refusalMeaning(block.textContent!)).toEqual(REQUIRED_MEANING)
    expect(block).toHaveTextContent(GOD_MODE_INCLUDED)
    expect(block).not.toHaveTextContent(/ask for approval and retry/i)
    expect(within(block).queryByRole('button', { name: /approve|allow|retry|try again/i })).not.toBeInTheDocument()
    expect(block).toHaveTextContent(FILE_REFUSAL)
  })
})
