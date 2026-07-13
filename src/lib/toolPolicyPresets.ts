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

import type { AgentToolsCfg, RegistryTool } from '@/lib/api'
import type { ToolPolicy } from '@/components/shared/PolicyBadge'

/**
 * The policy value shape used by ToolPolicyEditor and returned by applyRolePreset.
 *
 * HC#8: derived from the generated wire type AgentToolsCfg['builtin'] with the
 * `policies` field required (the editor always has a definite, complete policy
 * map — there is no `default_policy` field anymore; every static builtin tool
 * name must be present as an explicit, literal key). This is the ONLY
 * definition of this shape — do not create parallel types.
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
      // ADR-036: `bash` replaces exec / workspace_shell / workspace_shell_bg.
      bash: 'ask',
      // Canonical (underscored) browser tool names — see humanizeToolName.ts's
      // "New canonical browser tool names" vs. "Legacy dotted names (backward
      // compat with old transcripts)" split. These MUST match the live
      // GET /api/v1/tools registry's real tool names exactly, or the override
      // silently never applies (falls through to defaultPolicy = 'allow') —
      // that was this table's own bug until 2026-07-11: the dotted forms
      // below (`browser.navigate` etc.) never matched anything in the real
      // registry, so Balanced quietly allowed all four browser tools by
      // default instead of asking/denying them. Confirmed live via
      // GET /api/v1/tools and the persisted config.json of a wizard-created
      // agent. Do NOT revert to the dotted forms.
      browser_navigate: 'ask',
      browser_click: 'ask',
      browser_type: 'ask',
      browser_evaluate: 'deny',
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
 * Apply a role preset, returning a ToolPolicyValue (= AgentToolsCfg['builtin']
 * with `policies` required) ready for the backend's AgentToolsCfg.builtin field.
 *
 * The wire contract requires a COMPLETE per-tool policy map — every static
 * builtin tool name must be present as an explicit, literal key (there is no
 * `default_policy` fallback anymore). A preset therefore batch-authors that
 * complete map by walking the full known tool catalog (`tools`, as returned by
 * GET /api/v1/tools via fetchRegistryTools/fetchBuiltinTools — reused here,
 * never re-invented) and assigning each tool the preset's per-tool override
 * when one exists, otherwise the preset's default policy.
 *
 * The returned `policies` object is a fresh map — mutations to it do not
 * affect the preset definition.
 */
export function applyRolePreset(role: RolePreset, tools: RegistryTool[]): ToolPolicyValue {
  const preset = POLICY_PRESETS[role]
  const policies: Record<string, ToolPolicy> = {}
  for (const tool of tools) {
    policies[tool.name] = preset.overrides[tool.name] ?? preset.defaultPolicy
  }
  return { policies }
}

/**
 * Resolve a (possibly partial or absent) `tools_cfg` into a complete,
 * wire-ready `AgentToolsCfg` — the ONE piece of logic behind both the create
 * wizard's uncommitted-default render (Step3Tools.tsx's tools-policy display)
 * and its commit-on-submit default (CreateAgentModal.tsx's create-request
 * payload). Sharing this function is deliberate: two independently
 * maintained copies of "what counts as already-set vs. needs-a-default" is
 * exactly how this codebase's original commit-on-submit bug happened —
 * the display path and the submit path silently disagreed.
 *
 * "Already set" requires a genuine `builtin.policies` object — matching the
 * wire contract's invariant that `policies`, when present, is the COMPLETE
 * per-tool map (no `default_policy` fallback exists on the wire). A
 * `tools_cfg` that is `{}` or carries only `.mcp` does NOT count as
 * complete and falls through to the Balanced-preset default exactly like
 * `undefined` does — a bare `!== undefined` check would let a "defined but
 * incomplete" config pass through unchanged, silently reproducing the same
 * class of bug.
 *
 * When `tools` is empty — the registry query hasn't resolved yet, or,
 * degenerately, an install whose catalog has been trimmed to nothing — a
 * Balanced default cannot be safely computed: `applyRolePreset('balanced',
 * [])` would produce an explicit-but-empty `{ policies: {} }`, which fails
 * the backend's full-coverage validation with a confusing 400 instead of
 * falling back to the server's own seed. Returns `undefined` in that case
 * so the caller can omit `tools_cfg` entirely — the same behavior as before
 * any default was ever committed, preserved for this one degenerate case.
 */
export function resolveToolsCfg(
  cfg: AgentToolsCfg | undefined,
  tools: RegistryTool[],
): AgentToolsCfg | undefined {
  if (cfg?.builtin && typeof cfg.builtin.policies === 'object') {
    return cfg
  }
  if (tools.length === 0) return undefined
  return { ...cfg, builtin: applyRolePreset('balanced', tools) }
}

/**
 * Dev-only guard: returns every role-preset override key (across all three
 * presets) that does not match any tool name in `tools` — intended to be
 * called with the FULL registry from GET /api/v1/tools. A non-empty result
 * means a preset override has drifted from the live tool registry (e.g. a
 * tool was renamed and this file's override table wasn't updated), so the
 * override silently never applies and falls through to `defaultPolicy`
 * instead — exactly the class of bug that shipped with the dotted
 * `browser.navigate` etc. keys until 2026-07-11 (see the Balanced preset's
 * comment above). This is the frontend analogue of the backend's
 * `validateOverrideKeys` guard (pkg/coreagent/core.go).
 *
 * Pure function, no side effects — callers decide how/whether to surface
 * the result (e.g. gated on `import.meta.env.DEV`).
 */
export function findOrphanedPresetOverrideKeys(tools: RegistryTool[]): string[] {
  const names = new Set(tools.map((tool) => tool.name))
  const orphaned = new Set<string>()
  for (const preset of Object.values(POLICY_PRESETS)) {
    for (const key of Object.keys(preset.overrides)) {
      if (!names.has(key)) orphaned.add(key)
    }
  }
  return [...orphaned]
}
