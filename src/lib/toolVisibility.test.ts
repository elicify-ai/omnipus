// toolVisibility.test.ts — coverage for the tool-call render filter
// (src/lib/toolVisibility.ts). Ground truth for `delegate`/`bash` defaults
// verified against pkg/tools/delegate.go and pkg/tools/shell.go on
// origin/hotfix/v0.1.1 — see the header comment in toolVisibility.ts.

import { describe, it, expect } from 'vitest'
import { shouldRenderToolCall } from './toolVisibility'

describe('shouldRenderToolCall — load_tool', () => {
  it.each([
    [undefined, false, false],
    [{}, false, false],
    [undefined, true, true],
    [{}, true, true],
  ])('params=%o verbose=%s → %s', (params, verbose, expected) => {
    expect(shouldRenderToolCall('load_tool', params as Record<string, unknown> | undefined, verbose)).toBe(
      expected,
    )
  })
})

describe('shouldRenderToolCall — delegate', () => {
  it.each<[Record<string, unknown> | undefined, boolean]>([
    [undefined, false], // defaults: action=run, async=true → hidden
    [{}, false],
    [{ async: true }, false], // explicit async=true, same as default → hidden
    [{ async: false }, true], // explicit await (blocking) → visible
    [{ action: 'status' }, false], // status polling → hidden
    [{ action: 'status', async: false }, false], // status wins over async
    [{ action: 'run', async: false }, true], // explicit run + await → visible
  ])('params=%o → %s', (params, expected) => {
    expect(shouldRenderToolCall('delegate', params, false)).toBe(expected)
  })
})

describe('shouldRenderToolCall — bash', () => {
  it.each<[Record<string, unknown> | undefined, boolean]>([
    [undefined, true], // defaults: action=run, run_in_background=false → visible
    [{}, true],
    [{ run_in_background: true }, false], // background run → hidden
    [{ run_in_background: false }, true], // explicit foreground → visible
    [{ action: 'poll' }, false],
    [{ action: 'read' }, false],
    [{ action: 'kill' }, true], // regression guard: kill must NOT be lumped with poll/read
    [{ action: 'kill', run_in_background: true }, true], // kill wins even against a background session
  ])('params=%o → %s', (params, expected) => {
    expect(shouldRenderToolCall('bash', params, false)).toBe(expected)
  })
})

describe('shouldRenderToolCall — always-visible tools', () => {
  it.each([
    'hand_off',
    'return_to_default',
    'remember',
    'write_agent_metadata',
    'get_usage',
    'run_doctor',
  ])('%s is visible, non-verbose', (tool) => {
    expect(shouldRenderToolCall(tool, undefined, false)).toBe(true)
  })
})

describe('shouldRenderToolCall — under-review set (not hidden by this change)', () => {
  it.each(['recall_memory', 'read_agent_metadata'])('%s is visible, non-verbose', (tool) => {
    expect(shouldRenderToolCall(tool, undefined, false)).toBe(true)
  })
})

describe('shouldRenderToolCall — mcp_* and unknown tools (default fallthrough, no wildcard match)', () => {
  it.each(['mcp_github_create_issue', 'mcp_some_other_tool', 'some_future_unknown_tool'])(
    '%s is visible, non-verbose',
    (tool) => {
      expect(shouldRenderToolCall(tool, { anything: 'goes' }, false)).toBe(true)
    },
  )
})

describe('shouldRenderToolCall — verbose override', () => {
  it('load_tool becomes visible when verbose', () => {
    expect(shouldRenderToolCall('load_tool', undefined, true)).toBe(true)
  })

  it('a hidden background bash call becomes visible when verbose', () => {
    expect(shouldRenderToolCall('bash', { run_in_background: true }, true)).toBe(true)
  })

  it('a hidden background delegate call becomes visible when verbose', () => {
    expect(shouldRenderToolCall('delegate', undefined, true)).toBe(true)
  })
})

// ── isError override: an error/denial/failure outcome must always render,
// regardless of what the tool+params classification alone would decide. ──

describe('shouldRenderToolCall — isError override forces visibility', () => {
  it.each<[string, Record<string, unknown> | undefined]>([
    ['load_tool', undefined],
    ['load_tool', {}],
    ['delegate', undefined], // default action=run, async=true — normally hidden
    ['delegate', {}],
    ['delegate', { async: true }],
    ['delegate', { action: 'status' }],
    ['bash', { run_in_background: true }], // normally hidden background dispatch
    ['bash', { action: 'poll' }],
    ['bash', { action: 'read' }],
  ])('tool=%s params=%o is visible when isError=true even though normally hidden', (tool, params) => {
    expect(shouldRenderToolCall(tool, params, false, true)).toBe(true)
  })
})

describe('shouldRenderToolCall — isError=false explicit is a no-op (regression guard)', () => {
  // Confirms passing isError=false explicitly reproduces the exact same
  // classifications as omitting the parameter entirely — this fix must not
  // accidentally make everything always-visible.
  it.each<[string, Record<string, unknown> | undefined, boolean]>([
    ['load_tool', undefined, false],
    ['delegate', undefined, false],
    ['delegate', { async: false }, true],
    ['bash', { run_in_background: true }, false],
    ['bash', { action: 'kill' }, true],
    ['remember', undefined, true],
  ])('tool=%s params=%o → %s (unchanged from isError-omitted behavior)', (tool, params, expected) => {
    expect(shouldRenderToolCall(tool, params, false, false)).toBe(expected)
    // Same result whether the 4th arg is omitted or explicitly false.
    expect(shouldRenderToolCall(tool, params, false)).toBe(expected)
  })
})
