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
// (a) checks the spec-named file's presence in the tree, (b) asks Vite's
// module graph whether ANY resolvable form of the module exists, and (c)
// runs a planted-present control: the same probe shape against a module that
// exists must report presence, proving the check could have seen the presets
// module had it still been there.
//
// CHECK round 4, candidate 9: leg (b) used to be a literal
// `await import('@/lib/toolPolicyPresets')`. Vite resolves a literal
// specifier while it transforms the FILE, so once the module is deleted —
// the very state FR-022 orders — the whole file failed at collection
// ("Failed to resolve import") and no guard in it could ever pass. The probe
// now never imports the module: a module-graph glob lists every resolvable
// form (any extension, or a directory's index file) whether or not it would
// evaluate, so a present-but-broken module is still reported as present and
// a removed one as absent. The guard keeps its strength: the file must be
// gone from the tree AND from the module graph, and the control proves the
// glob shape can see a module that is there.
//
// RED on the pre-change tree: src/lib/toolPolicyPresets.ts still exists and
// the editor still offers its presets, so both guards fail for that missing
// deletion.

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

// Every form the retired specifier `@/lib/toolPolicyPresets` could resolve
// to: the spec-named file in any extension Vite resolves, and a directory
// module's index file. Patterns must be literal for Vite to expand them, so
// the control below repeats the same shape on a module that exists.
const retiredPresetModules = import.meta.glob<Record<string, unknown>>([
  '/src/lib/toolPolicyPresets.{ts,tsx,js,jsx,mjs,cjs,json}',
  '/src/lib/toolPolicyPresets/index.{ts,tsx,js,jsx,mjs,cjs,json}',
])
const controlModules = import.meta.glob<Record<string, unknown>>([
  '/src/lib/toolCategories.{ts,tsx,js,jsx,mjs,cjs,json}',
  '/src/lib/toolCategories/index.{ts,tsx,js,jsx,mjs,cjs,json}',
])

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

  it('FR-022: the presets module is removed from the codebase', () => {
    // The spec's Regression row names the module: the file must be gone
    // from the tree...
    const modulePath = repoFile('src/lib/toolPolicyPresets.ts')
    expect(
      existsSync(modulePath),
      `BLOCKED: ${modulePath} still exists — the presets module must be removed (FR-022, T-Q6 A)`,
    ).toBe(false)

    // ...and from the module graph in every form the old specifier resolved
    // to (a present-but-broken module is a finding, not a removal).
    expect(
      Object.keys(retiredPresetModules),
      'BLOCKED: @/lib/toolPolicyPresets still resolves — the presets module must be removed (FR-022, T-Q6 A)',
    ).toEqual([])
  })

  it('instrument control: the absence probes report a present module as present', async () => {
    // Planted-present control (CHECK round 1 F26): the same probes against a
    // module that exists must find it — proving the guard could have seen the
    // presets module had it still been there.
    try {
      await import('@/lib/toolCategories')
    } catch (err) {
      throw new Error('instrument: the control module failed to import', { cause: err })
    }
    // Reaching this line proves the module resolved; the tree probe repeats
    // the check through the filesystem.
    const controlPath = repoFile('src/lib/toolCategories.ts')
    expect(existsSync(controlPath)).toBe(true)
    // The module-graph probe's own control: the identical glob shape (same
    // brace list, same index-file form) lists the module that is there, and
    // only it. A glob that expanded to nothing would fail here instead of
    // silently passing the absence guard above.
    expect(Object.keys(controlModules)).toEqual(['/src/lib/toolCategories.ts'])
  })
})
