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
// CHECK round 1 F26/W3: the earlier absence proof caught ANY import error,
// so a present-but-broken module would have passed as "removed". The probe
// now (a) checks the spec-named file's presence in the tree, (b) classifies
// the import failure — only a module-resolution failure counts as removed,
// anything else fails loudly naming the error, and (c) runs a
// planted-present control: the same probe against a module that exists must
// report presence, proving the check could have seen the presets module had
// it still been there.
//
// RED today: src/lib/toolPolicyPresets.ts still exists and the editor still
// offers its presets, so both guards fail for that missing deletion.

import { describe, it, expect } from 'vitest'
import { existsSync } from 'node:fs'
import { join } from 'node:path'
import { render, screen } from '@testing-library/react'
import type { RegistryTool } from '@/lib/api'
import { ToolPolicyEditor } from './ToolPolicyEditor'

// vitest runs with cwd = the project root; import.meta.url is not a file://
// URL under the jsdom environment, so paths derive from cwd.
function repoFile(rel: string): string {
  return join(process.cwd(), rel)
}

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

// CHECK round 1 F26: only a failed module RESOLUTION counts as "the module
// was removed". A present-but-broken module throws for a different reason
// and must never pass this guard.
function isModuleResolutionFailure(err: unknown): boolean {
  const msg = err instanceof Error ? err.message : String(err)
  return /failed to resolve import|cannot find module|err_module_not_found/i.test(msg)
}

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
    // The spec's Regression row names the module: the file must be gone
    // from the tree.
    const modulePath = repoFile('src/lib/toolPolicyPresets.ts')
    expect(
      existsSync(modulePath),
      `BLOCKED: ${modulePath} still exists — the presets module must be removed (FR-022, T-Q6 A)`,
    ).toBe(false)

    // And importing it must fail because it cannot RESOLVE — not merely
    // with any error (a present-but-broken module is a finding, not a
    // removal).
    let error: unknown
    let resolved = false
    try {
      await import('@/lib/toolPolicyPresets')
      resolved = true
    } catch (err) {
      error = err
    }
    if (resolved) {
      throw new Error(
        'BLOCKED: @/lib/toolPolicyPresets still imports — the presets module must be removed (FR-022, T-Q6 A)',
      )
    }
    expect(
      isModuleResolutionFailure(error),
      `the presets import failed for a reason other than removal — investigate: ${String(error)}`,
    ).toBe(true)
  })

  it('instrument control: the absence probe reports a present module as present', async () => {
    // Planted-present control (CHECK round 1 F26): the same probe against a
    // module that exists must resolve and be found in the tree — proving
    // the guard could have seen the presets module had it still been there.
    try {
      await import('@/lib/toolCategories')
    } catch (err) {
      throw new Error('instrument: the control module failed to import', { cause: err })
    }
    // Reaching this line proves the module resolved; the tree probe repeats
    // the check through the filesystem.
    const controlPath = repoFile('src/lib/toolCategories.ts')
    expect(existsSync(controlPath)).toBe(true)
  })
})
