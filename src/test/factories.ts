// factories.ts — typed fixture builders for tests.
//
// Traces to: gap 12 (test-quality review, bugfixes3 QA hardening wave) —
// hand-rolled agent fixtures across useSlashMenu.test.ts,
// ChatScreen.agent-mention.test.tsx, and useChatAgents.test.ts drifted from
// the real `Agent` wire type (contracts/components/schemas/Agent.yaml) in
// two ways: (1) some used the legacy persisted-config value `type: 'worker'`
// instead of the real wire enum value `Subagent`/`subagent_3p`
// (contracts/components/schemas/Agent.yaml's `type` enum never emits
// "worker" — `ToWireType` normalizes it before the value ever reaches the
// wire), and (2) none satisfied the schema's `required` field list, relying
// on partial object literals (often silently cast `as Agent`) that would
// happily typecheck even if a genuinely-required field were missing.
//
// `makeAgent` fills every schema-required field (id, name, type, locked,
// status, soul, timeout_seconds, max_tool_iterations, memory_enabled — the
// last added by ADR-052 FR-039) with a realistic default so a test only has
// to specify what actually varies for its scenario — the rest can't
// silently go missing from the fixture.
//
// Deliberately NOT a counter-based id/name generator: every call site in
// this codebase's tests passes an explicit `id`/`name` (needed for the
// assertions that key off them), so a fixed default keeps output
// deterministic without adding hidden cross-test-order coupling.

import type { Agent } from '@/lib/api'

export function makeAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: 'agent-fixture',
    name: 'Agent Fixture',
    type: 'Main',
    locked: false,
    needs_model: false,
    status: 'active',
    soul: '',
    timeout_seconds: 300,
    max_tool_iterations: 50,
    // ADR-052 FR-039: memory_enabled is required on the wire Agent type.
    memory_enabled: true,
    ...overrides,
  }
}
