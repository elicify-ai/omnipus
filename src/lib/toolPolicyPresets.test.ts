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
import { POLICY_PRESETS, applyRolePreset, type RolePreset } from './toolPolicyPresets'
import type { RegistryTool } from '@/lib/api'

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
  makeTool('browser.navigate'),
  makeTool('browser.click'),
  makeTool('browser.type'),
  makeTool('browser.evaluate'),
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

    it('sets browser.navigate = ask', () => {
      expect(preset.overrides['browser.navigate']).toBe('ask')
    })

    it('sets browser.click = ask', () => {
      expect(preset.overrides['browser.click']).toBe('ask')
    })

    it('sets browser.type = ask', () => {
      expect(preset.overrides['browser.type']).toBe('ask')
    })

    it('sets browser.evaluate = deny', () => {
      expect(preset.overrides['browser.evaluate']).toBe('deny')
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
        'browser.navigate': 'ask',
        'browser.click': 'ask',
        'browser.type': 'ask',
        'browser.evaluate': 'deny',
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
        'browser.navigate': 'ask',
        'browser.click': 'ask',
        'browser.type': 'ask',
        'browser.evaluate': 'deny',
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
        'browser.navigate': 'ask',
        'browser.click': 'ask',
        'browser.type': 'ask',
        'browser.evaluate': 'ask',
        write_file: 'ask',
        read_file: 'ask',
      },
    ],
    [
      'balanced',
      {
        bash: 'ask',
        'browser.navigate': 'ask',
        'browser.click': 'ask',
        'browser.type': 'ask',
        'browser.evaluate': 'deny',
        write_file: 'ask',
        read_file: 'allow',
      },
    ],
    [
      'full_access',
      {
        bash: 'allow',
        'browser.navigate': 'allow',
        'browser.click': 'allow',
        'browser.type': 'allow',
        'browser.evaluate': 'allow',
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
