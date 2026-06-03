/**
 * toolCategories.test.ts — Blocker 4 (glob-aware resolvePolicy).
 *
 * The backend seeds custom agents with {"system.*":"deny"} as the privilege
 * rail. resolvePolicy must handle this glob key correctly so the per-agent
 * consumer resolves system.foo as 'deny' rather than falling through to the
 * default policy.
 */

import { describe, it, expect } from 'vitest'
import { resolvePolicy } from './toolCategories'

describe('resolvePolicy — exact match', () => {
  it('returns the exact override when present', () => {
    expect(resolvePolicy('browser.evaluate', { 'browser.evaluate': 'deny' }, 'allow')).toBe('deny')
  })

  it('returns default when no policies provided', () => {
    expect(resolvePolicy('exec', undefined, 'ask')).toBe('ask')
  })

  it('returns default when policies is empty', () => {
    expect(resolvePolicy('exec', {}, 'allow')).toBe('allow')
  })

  it('returns default when tool not in policies', () => {
    expect(resolvePolicy('write_file', { read_file: 'deny' }, 'allow')).toBe('allow')
  })
})

describe('resolvePolicy — glob match (Blocker 4)', () => {
  it('system.* glob applies to system.config_read', () => {
    expect(resolvePolicy('system.config_read', { 'system.*': 'deny' }, 'allow')).toBe('deny')
  })

  it('system.* glob applies to system.policy_list', () => {
    expect(resolvePolicy('system.policy_list', { 'system.*': 'deny' }, 'allow')).toBe('deny')
  })

  it('system.* glob applies to any system.* tool', () => {
    const policies = { 'system.*': 'ask' } as const
    expect(resolvePolicy('system.anything', policies, 'allow')).toBe('ask')
    expect(resolvePolicy('system.config_write', policies, 'allow')).toBe('ask')
  })

  it('system.* glob does NOT apply to a tool named just "system" (no dot)', () => {
    expect(resolvePolicy('system', { 'system.*': 'deny' }, 'allow')).toBe('allow')
  })

  it('system.* glob does NOT apply to "systemx.foo" (prefix must match exactly)', () => {
    expect(resolvePolicy('systemx.foo', { 'system.*': 'deny' }, 'allow')).toBe('allow')
  })

  it('exact match takes precedence over a matching glob', () => {
    // system.shell has an explicit allow; system.* would otherwise deny it
    const policies = {
      'system.*': 'deny',
      'system.shell': 'allow',
    } as const
    expect(resolvePolicy('system.shell', policies, 'allow')).toBe('allow')
  })

  it('workspace.* glob applies to workspace.shell', () => {
    expect(resolvePolicy('workspace.shell', { 'workspace.*': 'ask' }, 'allow')).toBe('ask')
  })

  it('workspace.* glob does not apply to system.shell', () => {
    expect(resolvePolicy('system.shell', { 'workspace.*': 'ask' }, 'allow')).toBe('allow')
  })

  it('value from glob match is returned (all three policy values)', () => {
    expect(resolvePolicy('system.foo', { 'system.*': 'allow' }, 'deny')).toBe('allow')
    expect(resolvePolicy('system.foo', { 'system.*': 'ask' }, 'deny')).toBe('ask')
    expect(resolvePolicy('system.foo', { 'system.*': 'deny' }, 'allow')).toBe('deny')
  })
})

describe('resolvePolicy — the seeded privilege-rail scenario', () => {
  /**
   * Custom agents are seeded with default_policy=allow and system.*=deny.
   * Verify that system.* tools resolve to 'deny' and non-system tools
   * resolve to 'allow' (the default).
   */
  const privilegeRailPolicies = { 'system.*': 'deny' } as const

  it('system.config_read resolves to deny under the seeded privilege rail', () => {
    expect(resolvePolicy('system.config_read', privilegeRailPolicies, 'allow')).toBe('deny')
  })

  it('write_file resolves to allow (default) under the seeded privilege rail', () => {
    expect(resolvePolicy('write_file', privilegeRailPolicies, 'allow')).toBe('allow')
  })

  it('browser.navigate resolves to allow (default) under the seeded privilege rail', () => {
    expect(resolvePolicy('browser.navigate', privilegeRailPolicies, 'allow')).toBe('allow')
  })
})
