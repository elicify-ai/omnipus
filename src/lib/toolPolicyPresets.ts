/**
 * toolPolicyPresets.ts — role→policy mapping (§2.1, nontech-ux-hardening-spec).
 *
 * Single source of truth for the three user-facing roles. Tool ids are verified
 * against the live GET /api/v1/tools registry (§0 as-is verification log).
 * There is NO 'delete_file' tool — do not add it.
 *
 * Consumed by:
 *   - ToolPolicyEditor (shared)
 *   - B6 (Security / global policies)
 *   - D12 (per-agent Tools & Permissions consolidation)
 */

import type { ToolPolicy } from '@/components/shared/PolicyBadge'

export type RolePreset = 'cautious' | 'balanced' | 'full_access'

export interface PresetDefinition {
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

export interface AppliedPolicy {
  default_policy: ToolPolicy
  /** Sparse override map — only keys that differ from default_policy. */
  policies: Record<string, ToolPolicy>
}

/**
 * Apply a role preset, returning the `{default_policy, policies}` shape
 * expected by the backend's AgentToolsCfg.builtin field.
 *
 * The returned `policies` object is a shallow copy of the preset's overrides —
 * mutations to it do not affect the preset definition.
 */
export function applyRolePreset(_role: RolePreset): AppliedPolicy {
  const preset = POLICY_PRESETS[_role]
  return {
    default_policy: preset.defaultPolicy,
    policies: { ...preset.overrides },
  }
}
