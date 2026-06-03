/**
 * toolPolicyPresets.ts — role→policy mapping (§2.1, nontech-ux-hardening-spec).
 *
 * Single source of truth for the three user-facing roles. Tool ids are verified
 * against the live GET /api/v1/tools registry (§0 as-is verification log).
 * There is NO 'delete_file' tool — do not add it.
 *
 * HC#8 compliance: the policy value shape is derived from the generated wire type
 * AgentToolsCfg['builtin'] — no hand-forked wire types. ToolPolicyValue is the
 * single canonical type used here and in ToolPolicyEditor.
 *
 * Consumed by:
 *   - ToolPolicyEditor (shared)
 *   - B6 (Security / global policies)
 *   - D12 (per-agent Tools & Permissions consolidation)
 */

import type { AgentToolsCfg } from '@/lib/api/generated/openapi-types'
import type { ToolPolicy } from '@/components/shared/PolicyBadge'

/**
 * The policy value shape used by ToolPolicyEditor and returned by applyRolePreset.
 *
 * HC#8: derived from the generated wire type AgentToolsCfg['builtin'] with both
 * fields required (the editor always has a definite default_policy and policies
 * map). This is the ONLY definition of this shape — do not create parallel types.
 */
export type ToolPolicyValue = Required<NonNullable<AgentToolsCfg['builtin']>>

export type RolePreset = 'cautious' | 'balanced' | 'full_access'

export interface PresetDefinition { // not-wire-format: internal UI role-preset descriptor (label/description + defaultPolicy/overrides); compiled into a generated ToolPolicyValue locally and never serialized or sent across any HTTP/WS boundary
  /** Human-readable label shown in the UI. */
  label: string
  /** One-sentence description shown under the preset button. */
  description: string
  /** Policy applied to any tool not listed in `overrides`. */
  defaultPolicy: ToolPolicy
  /** Per-tool overrides (empty object = no overrides). */
  overrides: Record<string, ToolPolicy>
}

/**
 * The three canonical role presets.
 *
 * Balanced is the system default (matches spec §2.1 table exactly).
 * Override keys are real registered tool ids (verified via live /api/v1/tools).
 */
export const POLICY_PRESETS: Record<RolePreset, PresetDefinition> = {
  cautious: {
    label: 'Cautious',
    description: 'Every tool requires your approval before it runs. Best for production or sensitive data.',
    defaultPolicy: 'ask',
    overrides: {},
  },
  balanced: {
    label: 'Balanced',
    description:
      'Safe tools run freely; file writes, code execution, and browser control ask first. ' +
      'Note: scheduled runs auto-deny any "Ask" tool — use Full access if you schedule tasks that need these.',
    defaultPolicy: 'allow',
    overrides: {
      exec: 'ask',
      'browser.navigate': 'ask',
      'browser.click': 'ask',
      'browser.type': 'ask',
      'browser.evaluate': 'deny',
      write_file: 'ask',
    },
  },
  full_access: {
    label: 'Full access',
    description: 'All tools run without asking. Fastest, but gives the agent maximum capability.',
    defaultPolicy: 'allow',
    overrides: {},
  },
}

/**
 * Apply a role preset, returning a ToolPolicyValue (= AgentToolsCfg['builtin'] with
 * both fields required) ready for the backend's AgentToolsCfg.builtin field.
 *
 * The returned `policies` object is a shallow copy of the preset's overrides —
 * mutations to it do not affect the preset definition.
 */
export function applyRolePreset(role: RolePreset): ToolPolicyValue {
  const preset = POLICY_PRESETS[role]
  return {
    default_policy: preset.defaultPolicy,
    policies: { ...preset.overrides },
  }
}
