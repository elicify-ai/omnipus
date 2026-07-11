/**
 * toolPolicyPresets.test.ts — Issue #315 ACs.
 *
 * Verifies that the preset table exactly matches the §2.1 table from the spec,
 * with real tool ids (no delete_file), and that applyRolePreset expands a
 * preset into a COMPLETE per-tool policy map over whatever tool list it is
 * given — there is no `default_policy` field on the wire anymore, so a preset
 * must batch-author an explicit entry for every known tool.
 */

import { describe, it, expect } from 'vitest'
import {
  POLICY_PRESETS,
  applyRolePreset,
  findOrphanedPresetOverrideKeys,
  resolveToolsCfg,
  type RolePreset,
} from './toolPolicyPresets'
import type { AgentToolsCfg, RegistryTool } from '@/lib/api'

function makeTool(name: string): RegistryTool {
  return { name, description: `${name} description`, scope: 'general', category: 'core', source: 'builtin' }
}

const SOME_TOOLS: RegistryTool[] = [
  makeTool('read_file'),
  makeTool('exec'),
  makeTool('web_search'),
]

const TOOLS_WITH_OVERRIDES: RegistryTool[] = [
  makeTool('bash'),
  makeTool('browser_navigate'),
  makeTool('browser_click'),
  makeTool('browser_type'),
  makeTool('browser_evaluate'),
  makeTool('write_file'),
  makeTool('read_file'),
]

describe('POLICY_PRESETS', () => {
  it('defines exactly three roles: cautious, balanced, full_access', () => {
    expect(Object.keys(POLICY_PRESETS).sort()).toEqual(['balanced', 'cautious', 'full_access'])
  })

  describe('Cautious', () => {
    const preset = POLICY_PRESETS.cautious

    it('has defaultPolicy = ask', () => {
      expect(preset.defaultPolicy).toBe('ask')
    })

    it('has zero overrides', () => {
      expect(Object.keys(preset.overrides)).toHaveLength(0)
    })

    it('does not reference delete_file', () => {
      expect(Object.keys(preset.overrides)).not.toContain('delete_file')
    })
  })

  describe('Balanced', () => {
    const preset = POLICY_PRESETS.balanced

    it('has defaultPolicy = allow', () => {
      expect(preset.defaultPolicy).toBe('allow')
    })

    it('has exactly 6 overrides (spec §2.1)', () => {
      expect(Object.keys(preset.overrides)).toHaveLength(6)
    })

    it('sets bash = ask', () => {
      expect(preset.overrides['bash']).toBe('ask')
    })

    it('sets browser_navigate = ask', () => {
      expect(preset.overrides['browser_navigate']).toBe('ask')
    })

    it('sets browser_click = ask', () => {
      expect(preset.overrides['browser_click']).toBe('ask')
    })

    it('sets browser_type = ask', () => {
      expect(preset.overrides['browser_type']).toBe('ask')
    })

    it('sets browser_evaluate = deny', () => {
      expect(preset.overrides['browser_evaluate']).toBe('deny')
    })

    it('sets write_file = ask', () => {
      expect(preset.overrides['write_file']).toBe('ask')
    })

    it('does not reference delete_file', () => {
      expect(Object.keys(preset.overrides)).not.toContain('delete_file')
    })

    it('matches the §2.1 table exactly (full snapshot)', () => {
      expect(preset.overrides).toStrictEqual({
        bash: 'ask',
        'browser_navigate': 'ask',
        'browser_click': 'ask',
        'browser_type': 'ask',
        'browser_evaluate': 'deny',
        write_file: 'ask',
      })
    })
  })

  describe('Full access', () => {
    const preset = POLICY_PRESETS.full_access

    it('has defaultPolicy = allow', () => {
      expect(preset.defaultPolicy).toBe('allow')
    })

    it('has zero overrides', () => {
      expect(Object.keys(preset.overrides)).toHaveLength(0)
    })

    it('does not reference delete_file', () => {
      expect(Object.keys(preset.overrides)).not.toContain('delete_file')
    })
  })
})

describe('applyRolePreset — expands to a complete map over the given tool list', () => {
  it('Cautious → every known tool explicitly set to "ask", no default_policy field', () => {
    expect(applyRolePreset('cautious', SOME_TOOLS)).toStrictEqual({
      policies: { read_file: 'ask', exec: 'ask', web_search: 'ask' },
    })
  })

  it('Full access → every known tool explicitly set to "allow"', () => {
    expect(applyRolePreset('full_access', SOME_TOOLS)).toStrictEqual({
      policies: { read_file: 'allow', exec: 'allow', web_search: 'allow' },
    })
  })

  it('Balanced → tools with a §2.1 override get it; everything else gets "allow"', () => {
    expect(applyRolePreset('balanced', SOME_TOOLS)).toStrictEqual({
      // None of read_file/exec/web_search are in the §2.1 override table.
      policies: { read_file: 'allow', exec: 'allow', web_search: 'allow' },
    })
  })

  it('Balanced applies the exact §2.1 overrides when those tools are present', () => {
    expect(applyRolePreset('balanced', TOOLS_WITH_OVERRIDES)).toStrictEqual({
      policies: {
        bash: 'ask',
        'browser_navigate': 'ask',
        'browser_click': 'ask',
        'browser_type': 'ask',
        'browser_evaluate': 'deny',
        write_file: 'ask',
        read_file: 'allow', // not in the override table → falls back to defaultPolicy
      },
    })
  })

  it('an empty tool list produces an empty (but still valid) policies map', () => {
    expect(applyRolePreset('cautious', [])).toStrictEqual({ policies: {} })
  })

  it('returns a new object each call (mutations do not affect the preset definition or a prior result)', () => {
    const result = applyRolePreset('balanced', TOOLS_WITH_OVERRIDES)
    const presetOverridesBefore = { ...POLICY_PRESETS.balanced.overrides }
    // Mutate the returned policies — should not affect the preset or a fresh call.
    result.policies['bash'] = 'allow'
    expect(POLICY_PRESETS.balanced.overrides).toStrictEqual(presetOverridesBefore)
    expect(applyRolePreset('balanced', TOOLS_WITH_OVERRIDES).policies['bash']).toBe('ask')
  })

  // Verify each role produces its exact §2.1 shape over TOOLS_WITH_OVERRIDES.
  const cases: [RolePreset, Record<string, string>][] = [
    [
      'cautious',
      {
        bash: 'ask',
        'browser_navigate': 'ask',
        'browser_click': 'ask',
        'browser_type': 'ask',
        'browser_evaluate': 'ask',
        write_file: 'ask',
        read_file: 'ask',
      },
    ],
    [
      'balanced',
      {
        bash: 'ask',
        'browser_navigate': 'ask',
        'browser_click': 'ask',
        'browser_type': 'ask',
        'browser_evaluate': 'deny',
        write_file: 'ask',
        read_file: 'allow',
      },
    ],
    [
      'full_access',
      {
        bash: 'allow',
        'browser_navigate': 'allow',
        'browser_click': 'allow',
        'browser_type': 'allow',
        'browser_evaluate': 'allow',
        write_file: 'allow',
        read_file: 'allow',
      },
    ],
  ]

  it.each(cases)('%s matches the expected complete map over TOOLS_WITH_OVERRIDES', (role, expected) => {
    expect(applyRolePreset(role, TOOLS_WITH_OVERRIDES).policies).toStrictEqual(expected)
  })
})

describe('ToolPolicyValue — HC#8 wire-type alignment', () => {
  it('applyRolePreset returns an object with only a `policies` field (no default_policy)', () => {
    const result = applyRolePreset('balanced', SOME_TOOLS)
    expect(Object.keys(result)).toEqual(['policies'])
    expect(typeof result.policies).toBe('object')
    expect(result.policies).not.toBeNull()
  })

  it('every value in the returned policies map is a valid ToolPolicy', () => {
    const result = applyRolePreset('balanced', TOOLS_WITH_OVERRIDES)
    for (const v of Object.values(result.policies)) {
      expect(['allow', 'ask', 'deny']).toContain(v)
    }
  })

  it('the returned policies map covers every tool name passed in, exactly once', () => {
    const result = applyRolePreset('cautious', TOOLS_WITH_OVERRIDES)
    expect(Object.keys(result.policies).sort()).toEqual(TOOLS_WITH_OVERRIDES.map((t) => t.name).sort())
  })
})

// ── Regression: preset override keys must match the LIVE registry's real ──────
// tool names (underscored), not a legacy/invented dotted namespace ─────────────
//
// Bug (fixed 2026-07-11): the Balanced preset's browser-tool overrides were
// keyed with dotted names (`browser.navigate` etc.) that never matched
// anything in the real GET /api/v1/tools registry (confirmed underscored:
// `browser_navigate`, `browser_click`, `browser_type`, `browser_evaluate` —
// see humanizeToolName.ts's "New canonical browser tool names" vs. "Legacy
// dotted names" split). The mismatch meant `applyRolePreset('balanced', ...)`
// silently fell through to defaultPolicy ('allow') for all four browser
// tools instead of asking/denying them — verified live via a wizard-created
// agent's persisted config.json. These tests pin the fix and would have
// caught the original bug directly (unlike the pre-fix suite, which used
// the same wrong dotted names in its own fixtures and therefore couldn't
// detect the mismatch).
describe('Balanced preset — browser-tool override keys match the real registry naming', () => {
  const REAL_BROWSER_TOOLS: RegistryTool[] = [
    makeTool('browser_navigate'),
    makeTool('browser_click'),
    makeTool('browser_type'),
    makeTool('browser_evaluate'),
    makeTool('browser_screenshot'),
    makeTool('browser_get_text'),
    makeTool('browser_wait'),
  ]

  it('applies ask/deny to the real underscored tool names, not "allow"', () => {
    const result = applyRolePreset('balanced', REAL_BROWSER_TOOLS)
    expect(result.policies.browser_navigate).toBe('ask')
    expect(result.policies.browser_click).toBe('ask')
    expect(result.policies.browser_type).toBe('ask')
    expect(result.policies.browser_evaluate).toBe('deny')
    // No override for these three → defaultPolicy ('allow') is correct.
    expect(result.policies.browser_screenshot).toBe('allow')
    expect(result.policies.browser_get_text).toBe('allow')
    expect(result.policies.browser_wait).toBe('allow')
  })

  it('none of the Balanced preset overrides contain a dot (no legacy/invented namespacing)', () => {
    for (const key of Object.keys(POLICY_PRESETS.balanced.overrides)) {
      expect(key).not.toContain('.')
    }
  })
})

describe('findOrphanedPresetOverrideKeys', () => {
  it('returns an empty array when every override key matches a real tool name', () => {
    const fullCatalog: RegistryTool[] = [
      makeTool('bash'),
      makeTool('browser_navigate'),
      makeTool('browser_click'),
      makeTool('browser_type'),
      makeTool('browser_evaluate'),
      makeTool('write_file'),
    ]
    expect(findOrphanedPresetOverrideKeys(fullCatalog)).toEqual([])
  })

  it('flags an override key that does not match any tool in the given registry', () => {
    // Simulates the original bug: dotted keys that never match a real tool.
    const registryMissingBrowserTools: RegistryTool[] = [makeTool('bash'), makeTool('write_file')]
    const orphaned = findOrphanedPresetOverrideKeys(registryMissingBrowserTools)
    expect(orphaned).toEqual(
      expect.arrayContaining(['browser_navigate', 'browser_click', 'browser_type', 'browser_evaluate']),
    )
  })

  it('an empty registry flags every override key as orphaned (nothing to match against)', () => {
    const orphaned = findOrphanedPresetOverrideKeys([])
    expect(orphaned.length).toBeGreaterThan(0)
  })
})

// ── resolveToolsCfg — single source of truth for display default + commit default ──

describe('resolveToolsCfg', () => {
  const TOOLS: RegistryTool[] = [
    makeTool('bash'),
    makeTool('read_file'),
    makeTool('write_file'),
  ]

  it('returns undefined when cfg is undefined and the tool list is empty (registry not resolved yet)', () => {
    expect(resolveToolsCfg(undefined, [])).toBeUndefined()
  })

  it('computes the Balanced default when cfg is undefined and tools are available', () => {
    const result = resolveToolsCfg(undefined, TOOLS)
    expect(result).toEqual({ builtin: applyRolePreset('balanced', TOOLS) })
  })

  it('returns cfg unchanged when it already carries a genuine builtin.policies object', () => {
    const cfg: AgentToolsCfg = { builtin: { policies: { bash: 'deny' } } }
    expect(resolveToolsCfg(cfg, TOOLS)).toBe(cfg)
  })

  it('treats a cfg of {} as incomplete — falls through to the Balanced default, not passed through unchanged', () => {
    const result = resolveToolsCfg({}, TOOLS)
    expect(result).toEqual({ builtin: applyRolePreset('balanced', TOOLS) })
  })

  it('treats a cfg carrying only .mcp (no .builtin) as incomplete — computes builtin and preserves .mcp', () => {
    const cfg: AgentToolsCfg = { mcp: { servers: [{ id: 'srv1' }] } }
    const result = resolveToolsCfg(cfg, TOOLS)
    expect(result).toEqual({
      mcp: { servers: [{ id: 'srv1' }] },
      builtin: applyRolePreset('balanced', TOOLS),
    })
  })

  it('returns undefined (omit tools_cfg) when tools is empty even if cfg is a non-complete object', () => {
    // Degenerate race: registry query hasn't resolved, and no prior explicit
    // config exists either. Must NOT commit an explicit-but-empty policies map.
    expect(resolveToolsCfg({}, [])).toBeUndefined()
  })
})
