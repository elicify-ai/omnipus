// Shared types for the CreateAgentWizard sub-components.
//
// The wizard is a controlled form — each step receives the current payload
// plus a `setField` callback that updates a single key. The parent owns the
// wizard state (via useReducer) and the submit handler.
//
// The props are intentionally NOT generic over a per-step key set: the
// steps need to read fields from across the payload (e.g. Step 3 may
// need to display name/description for confirmation), and the parent's
// `setField` is intentionally wider than any single step's writes.
//
// W4 + W5: extended with `initialCli` plumbing, an optional
// `useDefaultProvider` helper for the fallback editor's per-entry
// provider default, and the `AdvancedFields` shape that the
// `<Advanced />` disclosure reads/writes.

import type { WizardSubmitPayload, WizardType, WizardCli } from '../CreateAgentWizard'

export type { WizardType, WizardCli, WizardSubmitPayload }

export interface StepProps {
  payload: WizardSubmitPayload
  setField: <L extends keyof WizardSubmitPayload>(field: L, value: WizardSubmitPayload[L]) => void
  initialType: WizardType
  initialCli?: WizardCli
  /**
   * Returns the default provider id for a freshly-added fallback entry.
   * Optional — the fallback editor falls back to a safe empty string when
   * not provided (so the editor renders in test environments without the
   * provider query wiring).
   */
  useDefaultProvider?: () => string | undefined
  /**
   * Connected providers (filtered to status === 'connected' by the
   * parent). The model picker on Step 1 and the FallbackEditor on Step 3
   * both consume this. The wizard does NOT call `useQuery` itself so the
   * step components stay query-client-free and unit-testable.
   */
  connectedProviders?: ReadonlyArray<import('@/lib/api/generated/openapi-types').Provider>
  /** Registry tools for the ToolPolicyEditor. */
  registryTools?: ReadonlyArray<import('@/lib/api').RegistryTool>
  /** Installed skills for the skills picker on Step 3. */
  skills?: ReadonlyArray<import('@/lib/api/generated/openapi-types').Skill>
}

// ── Advanced disclosure payload ────────────────────────────────────────────

export interface AdvancedFields {
  // Model runtime knobs (Main + Subagent only).
  model_params?: {
    temperature?: number
    max_tokens?: number
    top_p?: number
  }
  // Sandbox + shell hardening (Main + Subagent only).
  sandbox_profile?: 'none' | 'workspace' | 'workspace+net' | 'host' | 'off'
  shell_policy?: { deny?: string[] }
  rate_limits?: {
    per_minute?: number
    per_hour?: number
    per_day?: number
  }
  // Delegation / steering — Main only.
  delegation_policy?: string
  steering_mode?: 'auto' | 'manual' | 'hybrid'
  // Runtime knobs (Main + Subagent only).
  timeout_seconds?: number
  max_tool_iterations?: number
  // External-cli executor block (subagent_3p only). Mirrors the three
  // inputs rendered in Step 1 — kept here so the disclosure can edit them
  // without dragging in the wider payload shape.
  executor?: {
    cli_path?: string
    env_overrides?: Record<string, string>
    cli_args?: string
  }
}

// Step props for the Advanced disclosure — same payload/setField plumbing
// as the wizard steps, but `initialType` drives the type-branched field
// set and `useDefaultProvider` is unused.
export interface AdvancedProps extends StepProps {}

// Re-exported so consumers can `import type { AdvancedFields } from
// './types'` without going through CreateAgentWizard.