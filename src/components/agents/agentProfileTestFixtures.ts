// agentProfileTestFixtures.ts — fixtures shared by AgentProfile.test.tsx and
// AgentProfile.providerPair.test.tsx (H6 R2 file-budget split, 2026-10-14:
// AgentProfile.test.tsx is grandfathered in scripts/budgets/ and may only
// shrink, so the provider-pair tests moved to their own file and the fixture
// both files need moved here). Plain data only — no module mocks (those are
// per-test-file by design).
import type { Agent } from '@/lib/api'

export const editable = (...names: string[]) => names.map((name) => ({ name, editable: true }))

export const COMMON_EDITABLE_FIELDS = editable(
  // No 'default' — the field is off the wire (omnipus-system-agent K11/K12,
  // bundle B1) and the server no longer describes it as editable.
  'name', 'description', 'color', 'icon', 'figure', 'role', 'model', 'provider',
  'fallback_models', 'model_params', 'soul', 'memory_enabled', 'voice',
  'max_tool_iterations', 'context_window_override', 'skills',
  'tool_policy_changes', 'executor',
)

export const BUILTIN_EDITABLE_FIELDS = COMMON_EDITABLE_FIELDS.map((field) =>
  ['name', 'description', 'color', 'icon', 'figure', 'role', 'soul', 'executor'].includes(field.name)
    ? { ...field, editable: false, reason: 'Built-in identity and runtime are fixed.' }
    : field,
)

export const mockCoreAgent: Agent = {
  revision: '0'.repeat(64),
  id: 'general-assistant',
  name: 'General Assistant',
  type: 'core',
  locked: false,
  needs_model: false,
  status: 'active',
  model: 'claude-sonnet-4-6',
  description: 'General purpose assistant',
  soul: '',
  max_tool_iterations: 20,
  max_tool_iterations_source: 'global',
  max_tool_iterations_override_ignored: false,
  stats: { total_sessions: 5, total_tokens: 12000, total_cost: 0.05 },
  // ADR-052 FR-039: memory_enabled is required on the wire Agent type.
  memory_enabled: true,
  figure: 'Omnipus',
  role: 'general',
  editable_fields: COMMON_EDITABLE_FIELDS,
}
