/**
 * toolPolicyPresets.test.ts — Issue #315 ACs.
 *
 * Verifies that the preset map exactly matches the §2.1 table from the spec,
 * with real tool ids (no delete_file).
 */

import { describe, it, expect } from 'vitest'
import { POLICY_PRESETS, applyRolePreset, type RolePreset } from './toolPolicyPresets'

describe('POLICY_PRESETS', () => {
  it('defines exactly three roles: cautious, balanced, full_access', () => {
    expect(Object.keys(POLICY_PRESETS).sort()).toEqual(['balanced', 'cautious', 'full_access'])
  })

  describe('Cautious', () => {
    const preset = POLICY_PRESETS.cautious

    it('has default_policy = ask', () => {
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

    it('has default_policy = allow', () => {
      expect(preset.defaultPolicy).toBe('allow')
    })

    it('has exactly 6 overrides (spec §2.1)', () => {
      expect(Object.keys(preset.overrides)).toHaveLength(6)
    })

    it('sets exec = ask', () => {
      expect(preset.overrides['exec']).toBe('ask')
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
        exec: 'ask',
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

    it('has default_policy = allow', () => {
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

describe('applyRolePreset', () => {
  it('Cautious → {default_policy:ask, policies:{}}', () => {
    expect(applyRolePreset('cautious')).toStrictEqual({
      default_policy: 'ask',
      policies: {},
    })
  })

  it('Balanced → {default_policy:allow, policies:§2.1 overrides}', () => {
    expect(applyRolePreset('balanced')).toStrictEqual({
      default_policy: 'allow',
      policies: {
        exec: 'ask',
        'browser.navigate': 'ask',
        'browser.click': 'ask',
        'browser.type': 'ask',
        'browser.evaluate': 'deny',
        write_file: 'ask',
      },
    })
  })

  it('Full access → {default_policy:allow, policies:{}}', () => {
    expect(applyRolePreset('full_access')).toStrictEqual({
      default_policy: 'allow',
      policies: {},
    })
  })

  it('returns a new object (mutations do not affect the preset definition)', () => {
    const result = applyRolePreset('balanced')
    const presetOverridesBefore = { ...POLICY_PRESETS.balanced.overrides }
    // Mutate the returned policies — should not affect the preset.
    result.policies['new_tool'] = 'deny'
    expect(POLICY_PRESETS.balanced.overrides).toStrictEqual(presetOverridesBefore)
  })

  // Verify each role produces its exact §2.1 shape
  const cases: [RolePreset, { default_policy: string; policies: Record<string, string> }][] = [
    ['cautious', { default_policy: 'ask', policies: {} }],
    [
      'balanced',
      {
        default_policy: 'allow',
        policies: {
          exec: 'ask',
          'browser.navigate': 'ask',
          'browser.click': 'ask',
          'browser.type': 'ask',
          'browser.evaluate': 'deny',
          write_file: 'ask',
        },
      },
    ],
    ['full_access', { default_policy: 'allow', policies: {} }],
  ]

  it.each(cases)('%s matches §2.1 table exactly', (role, expected) => {
    expect(applyRolePreset(role)).toStrictEqual(expected)
  })
})
