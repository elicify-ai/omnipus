/**
 * toolResultDisplay.test.ts — unit-level pinning for resolveToolResult's
 * result-shape resolution, added with the text-envelope unwrap fix
 * (code-reviewer + silent-failure-hunter finding on 893c45f2c, both
 * reviewers corroborating the same root cause: pkg/agent/loop_run_turn_tools
 * .go::finishCall persists every successful plain-text result as the
 * single-key { "text": <string> } envelope — the "nothing richer" default
 * for read_file/list_directory/search_web/fetch_url/bash — and nothing in
 * the replay chain unwraps it, pkg/gateway/replay.go::truncateResult
 * returns it unchanged inline. resolveToolResult classified that envelope
 * as the generic 'json' kind; four of the five dedicated blocks read body
 * content only from the 'text' kind, so a NORMAL successful reload showed
 * a false "0 lines" / "0 entries" / "0 results" next to a green success
 * dot. The replay-parity suite only seeded plain strings, which is why the
 * gap shipped past 12 green suites.)
 */

import { describe, it, expect } from 'vitest'
import {
  isTextEnvelopeResult,
  resolveToolResult,
} from './toolResultDisplay'

describe('resolveToolResult — the finishCall { text } success envelope', () => {
  it('unwraps the single-key { text: <string> } envelope to the text kind', () => {
    const resolved = resolveToolResult({ text: 'package tools\n\nfunc Name() string' })
    expect(resolved).toEqual({ kind: 'text', text: 'package tools\n\nfunc Name() string' })
  })

  it('an envelope and its equivalent plain string resolve identically (live/reload parity)', () => {
    const content = 'M file.go\nM other.go\n'
    expect(resolveToolResult({ text: content })).toEqual(resolveToolResult(content))
  })

  it('a multi-key object that happens to carry a text field stays on the json kind — unwrapping would drop keys', () => {
    expect(resolveToolResult({ text: 'x', count: 2 })).toEqual({
      kind: 'json',
      text: JSON.stringify({ text: 'x', count: 2 }, null, 2),
    })
  })

  it('a genuinely structured object with no text key stays on the json kind', () => {
    expect(resolveToolResult({ results: [1, 2] })).toEqual({
      kind: 'json',
      text: JSON.stringify({ results: [1, 2] }, null, 2),
    })
  })

  it('a plain string still resolves to the text kind — the live path shape is untouched', () =>
    expect(resolveToolResult('M file.go\n')).toEqual({ kind: 'text', text: 'M file.go\n' }))

  it('undefined / null / empty string resolve to the empty kind', () => {
    expect(resolveToolResult(undefined).kind).toBe('empty')
    expect(resolveToolResult(null).kind).toBe('empty')
    expect(resolveToolResult('').kind).toBe('empty')
  })

  it('a structured failure sentinel with an extra text key still wins over the envelope', () => {
    const resolved = resolveToolResult({
      error: 'delegation_denied',
      reason: 'Scout is not in your trust set for delegation.',
      policy: 'trust_set',
      tool: 'delegate',
      text: 'x',
    })
    expect(resolved.kind).toBe('delegationDenied')
  })
})

describe('isTextEnvelopeResult — the envelope detector itself', () => {
  it('accepts the exact persisted shape', () => {
    expect(isTextEnvelopeResult({ text: 'out' })).toBe(true)
  })

  it('rejects multi-key objects, arrays, strings, null and keyless objects', () => {
    expect(isTextEnvelopeResult({ text: 'x', n: 1 })).toBe(false)
    expect(isTextEnvelopeResult(['text'])).toBe(false)
    expect(isTextEnvelopeResult('plain')).toBe(false)
    expect(isTextEnvelopeResult(null)).toBe(false)
    expect(isTextEnvelopeResult({})).toBe(false)
  })
})
