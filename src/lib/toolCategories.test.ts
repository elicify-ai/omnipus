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

  it('system.* glob DOES apply to a tool named exactly "system" (bare-prefix equality)', () => {
    // Matches the backend's authoritative semantics: pkg/tools/compositor.go's
    // resolveFromMap treats a wildcard as matching when the tool name starts with
    // "prefix+delimiter" OR equals the bare prefix itself ("matches tools whose name
    // starts with 'system.' or equals 'system'" — buildWildcardIndex doc comment).
    // An earlier version of this test asserted the opposite (no bare-prefix match),
    // which was an undetected drift from the backend contract — resolvePolicy was
    // re-implemented as a faithful mirror of resolveFromMap, which surfaced and
    // fixed this discrepancy.
    expect(resolvePolicy('system', { 'system.*': 'deny' })).toBe('deny')
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

describe('resolvePolicy — underscore wildcard match', () => {
  it('mcp_github_* glob applies to mcp_github_search', () => {
    expect(resolvePolicy('mcp_github_search', { 'mcp_github_*': 'deny' })).toBe('deny')
  })

  it('mcp_github_* glob applies to a tool named exactly "mcp_github" (equality, no trailing delimiter)', () => {
    expect(resolvePolicy('mcp_github', { 'mcp_github_*': 'ask' })).toBe('ask')
  })

  it('mcp_github_* glob does NOT apply to mcp_gitlab_search (prefix must match exactly)', () => {
    expect(resolvePolicy('mcp_gitlab_search', { 'mcp_github_*': 'deny' })).toBeUndefined()
  })

  it('exact match takes precedence over a matching underscore wildcard', () => {
    const policies = {
      'mcp_github_*': 'deny',
      mcp_github_search: 'allow',
    } as const
    expect(resolvePolicy('mcp_github_search', policies)).toBe('allow')
  })

  it('longer _* prefix wins over a shorter _* prefix', () => {
    // Both 'mcp_github_*' and 'mcp_github_mcp_*' match 'mcp_github_mcp_search' —
    // the longer, more specific prefix must win.
    const policies = {
      'mcp_github_*': 'deny',
      'mcp_github_mcp_*': 'allow',
    } as const
    expect(resolvePolicy('mcp_github_mcp_search', policies)).toBe('allow')
    // Order of keys must not matter.
    const reordered = {
      'mcp_github_mcp_*': 'allow',
      'mcp_github_*': 'deny',
    } as const
    expect(resolvePolicy('mcp_github_mcp_search', reordered)).toBe('allow')
  })

  it('a shorter _* prefix still applies when the longer one does not match', () => {
    const policies = {
      'mcp_github_*': 'deny',
      'mcp_github_mcp_*': 'allow',
    } as const
    // 'mcp_github_search' is not under the 'mcp_github_mcp_' sub-prefix.
    expect(resolvePolicy('mcp_github_search', policies)).toBe('deny')
  })

  it('a dot wildcard and an underscore wildcard participate in the same longest-prefix contest', () => {
    // 'system.*' (dot form, prefix 'system') and 'system.admin_*' (underscore
    // form, prefix 'system.admin') both match 'system.admin' — the dot form
    // via startsWith('system.'), the underscore form via bare-prefix equality.
    // The longer prefix ('system.admin') must win regardless of which
    // delimiter it uses — both forms compete in ONE contest, not two separate
    // ones keyed by delimiter.
    const policies = {
      'system.*': 'deny',
      'system.admin_*': 'allow',
    } as const
    expect(resolvePolicy('system.admin', policies)).toBe('allow')
  })

  it('underscore wildcard value is returned (all three policy values)', () => {
    expect(resolvePolicy('mcp_github_search', { 'mcp_github_*': 'allow' })).toBe('allow')
    expect(resolvePolicy('mcp_github_search', { 'mcp_github_*': 'ask' })).toBe('ask')
    expect(resolvePolicy('mcp_github_search', { 'mcp_github_*': 'deny' })).toBe('deny')
  })

  it('no match with only underscore wildcards present resolves to undefined', () => {
    expect(resolvePolicy('browser.evaluate', { 'mcp_github_*': 'deny' })).toBeUndefined()
  })
})

describe('resolvePolicy — segments-first wildcard ordering (mirrors compositor.go exactly)', () => {
  it('a 2-segment dot wildcard beats a longer-but-1-segment underscore wildcard (segment count is primary)', () => {
    // 'a.b.*' has 2 segments (dot-count of 'a.b' is 1, +1); 'a.b.c_*' has only
    // 1 segment (underscore wildcards are always single-segment) even though
    // its literal prefix ('a.b.c', 5 chars) is longer than 'a.b.*'s ('a.b',
    // 3 chars). Both match 'a.b.c_d'. Ranking by raw prefix length (the old,
    // pre-fix behaviour) would have picked 'a.b.c_*' → 'allow', which
    // diverges from the backend's segment-primary pick of 'a.b.*' → 'deny'.
    const policies = { 'a.b.*': 'deny', 'a.b.c_*': 'allow' } as const
    expect(resolvePolicy('a.b.c_d', policies)).toBe('deny')
    // Order of keys must not matter.
    const reordered = { 'a.b.c_*': 'allow', 'a.b.*': 'deny' } as const
    expect(resolvePolicy('a.b.c_d', reordered)).toBe('deny')
  })

  it('equal-prefix cross-delimiter tie resolves to the dot form (2 segments beats 1)', () => {
    // 'x.y.*' (prefix 'x.y', 2 segments) and 'x.y_*' (prefix 'x.y', always
    // 1 segment) both match the tool name 'x.y' via bare-prefix equality —
    // same literal prefix, same length, so a length-only tie-break could not
    // distinguish them. Segment count breaks the tie: the dot form wins.
    const policies = { 'x.y.*': 'allow', 'x.y_*': 'deny' } as const
    expect(resolvePolicy('x.y', policies)).toBe('allow')
    // Order of keys must not matter.
    const reordered = { 'x.y_*': 'deny', 'x.y.*': 'allow' } as const
    expect(resolvePolicy('x.y', reordered)).toBe('allow')
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
