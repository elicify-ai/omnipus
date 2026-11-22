// ToolPolicyEditor.noPresets.test.tsx — Skills & Tools redesign (#1963,
// epic #1519, page F), order 21 (S-18; FR-022; T-Q6 A).
//
// Oracle: skills-tools-redesign-spec.md FR-022 ("No agent's Tools tab and no
// other screen MUST show Cautious / Balanced / Full access; the presets
// module MUST be removed") and the Regression Test Requirements row
// "Presets | toolPolicyPresets.test.ts, preset cases in ToolPolicyEditor
// | Deleted with the feature (T-Q6 A) | the deletion is the expected
// change, not a weakened test".
//
// RED today: src/lib/toolPolicyPresets.ts still exists and the editor still
// offers its presets, so both guards fail for that missing deletion.

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { RegistryTool } from '@/lib/api'
import { ToolPolicyEditor } from './ToolPolicyEditor'

const tools: RegistryTool[] = [
  {
    name: 'write_file',
    description: 'Write a file.',
    scope: 'core',
    category: 'file',
    source: 'builtin',
    visibility: 'full',
    visibility_locked: false,
    permission_fixed: false,
  },
  {
    name: 'execute_plan',
    description: 'Run a plan.',
    scope: 'core',
    category: 'plans',
    source: 'builtin',
    visibility: 'full',
    visibility_locked: false,
    permission_fixed: false,
  },
]

describe('ToolPolicyEditor — presets removed (order 21)', () => {
  it('S-18: the editor renders no preset text anywhere', () => {
    render(
      <ToolPolicyEditor
        tools={tools}
        value={{ policies: { write_file: 'ask', execute_plan: 'ask' } }}
        onChange={() => {}}
      />,
    )
    for (const preset of [/cautious/i, /balanced/i, /full access/i]) {
      expect(screen.queryByText(preset), `no preset text matching ${preset} (FR-022)`).toBeNull()
    }
  })

  it('FR-022: the presets module is removed from the codebase', async () => {
    let gone = false
    try {
      await import('@/lib/toolPolicyPresets')
    } catch {
      gone = true
    }
    // The spec's Regression row calls the deletion the expected change:
    // importing the module must fail once FR-022 lands.
    expect(gone, 'BLOCKED: @/lib/toolPolicyPresets still exists — the presets module must be removed (FR-022, T-Q6 A)').toBe(true)
  })
})
