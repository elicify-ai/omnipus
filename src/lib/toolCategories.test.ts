/**
 * toolCategories.test.ts — Blocker 4 (glob-aware resolvePolicy) + removal of the
 * default-policy fallback.
 *
 * The backend seeds custom agents with {"system.*":"deny"} as the privilege
 * rail. resolvePolicy must handle this glob key correctly so the per-agent
 * consumer resolves system.foo as 'deny' rather than falling through to a
 * default policy.
 *
 * There is no third "default policy" fallback step anymore — the wire
 * contract guarantees (server-validated) that every static builtin tool has
 * an explicit entry, so a genuinely-missing tool resolves to `undefined`
 * ("unconfigured / needs attention"), never silently to 'allow'.
 */

import { describe, it, expect } from 'vitest'
import { resolvePolicy } from './toolCategories'

describe('resolvePolicy — exact match', () => {
  it('returns the exact override when present', () => {
    expect(resolvePolicy('browser.evaluate', { 'browser.evaluate': 'deny' })).toBe('deny')
  })

  it('returns undefined when no policies provided', () => {
    expect(resolvePolicy('exec', undefined)).toBeUndefined()
  })

  it('returns undefined when policies is empty', () => {
    expect(resolvePolicy('exec', {})).toBeUndefined()
  })

  it('returns undefined when tool not in policies (no default to fall back to)', () => {
    expect(resolvePolicy('write_file', { read_file: 'deny' })).toBeUndefined()
  })
})

describe('resolvePolicy — glob match (Blocker 4)', () => {
  it('system.* glob applies to system.config_read', () => {
    expect(resolvePolicy('system.config_read', { 'system.*': 'deny' })).toBe('deny')
  })

  it('system.* glob applies to system.policy_list', () => {
    expect(resolvePolicy('system.policy_list', { 'system.*': 'deny' })).toBe('deny')
  })

  it('system.* glob applies to any system.* tool', () => {
    const policies = { 'system.*': 'ask' } as const
    expect(resolvePolicy('system.anything', policies)).toBe('ask')
    expect(resolvePolicy('system.config_write', policies)).toBe('ask')
  })

  it('system.* glob does NOT apply to a tool named just "system" (no dot)', () => {
    expect(resolvePolicy('system', { 'system.*': 'deny' })).toBeUndefined()
  })

  it('system.* glob does NOT apply to "systemx.foo" (prefix must match exactly)', () => {
    expect(resolvePolicy('systemx.foo', { 'system.*': 'deny' })).toBeUndefined()
  })

  it('exact match takes precedence over a matching glob', () => {
    // system.shell has an explicit allow; system.* would otherwise deny it
    const policies = {
      'system.*': 'deny',
      'system.shell': 'allow',
    } as const
    expect(resolvePolicy('system.shell', policies)).toBe('allow')
  })

  it('workspace.* glob applies to workspace.shell', () => {
    expect(resolvePolicy('workspace.shell', { 'workspace.*': 'ask' })).toBe('ask')
  })

  it('workspace.* glob does not apply to system.shell', () => {
    expect(resolvePolicy('system.shell', { 'workspace.*': 'ask' })).toBeUndefined()
  })

  it('value from glob match is returned (all three policy values)', () => {
    expect(resolvePolicy('system.foo', { 'system.*': 'allow' })).toBe('allow')
    expect(resolvePolicy('system.foo', { 'system.*': 'ask' })).toBe('ask')
    expect(resolvePolicy('system.foo', { 'system.*': 'deny' })).toBe('deny')
  })
})

describe('resolvePolicy — the seeded privilege-rail scenario', () => {
  /**
   * Custom agents are seeded with an explicit allow entry per tool plus a
   * system.*=deny glob to enforce the privilege rail. Verify that system.*
   * tools resolve to 'deny' via the glob, and a tool with its own explicit
   * entry resolves to that entry (not a phantom default).
   */
  const privilegeRailPolicies = { 'system.*': 'deny', write_file: 'allow', 'browser.navigate': 'allow' } as const

  it('system.config_read resolves to deny under the seeded privilege rail', () => {
    expect(resolvePolicy('system.config_read', privilegeRailPolicies)).toBe('deny')
  })

  it('write_file resolves to its explicit allow entry under the seeded privilege rail', () => {
    expect(resolvePolicy('write_file', privilegeRailPolicies)).toBe('allow')
  })

  it('browser.navigate resolves to its explicit allow entry under the seeded privilege rail', () => {
    expect(resolvePolicy('browser.navigate', privilegeRailPolicies)).toBe('allow')
  })

  it('a tool with neither an exact nor a glob entry resolves to undefined (needs attention)', () => {
    // Realistically this never happens — the server validates full coverage —
    // but the frontend must not silently coerce a genuine gap to 'allow'.
    expect(resolvePolicy('web_search', privilegeRailPolicies)).toBeUndefined()
  })
})
