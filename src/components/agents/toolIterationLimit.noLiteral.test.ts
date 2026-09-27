// toolIterationLimit.noLiteral.test.ts — #904 RED, FR-004 / SC-002 (SPA half
// of test plan row 4, TestNoHiddenLiteral_Guard): the SPA contains NO literal
// default for the tool iteration limit — it renders the server's values.
//
// The files and patterns are exactly the ones the spec's "Existing Codebase
// Context" table lists as carrying a hard-coded default today:
//   AgentProfile.tsx      useState(200), useState('200'), `?? 200`, "Default: 200"
//   CreateAgentWizard.tsx seeds `max_tool_iterations: 200`
//   wizard/Advanced.tsx   caption "Default 200."
//   useCommandPreview.ts  sends `max_tool_iterations ?? 0` (the old "0 = server default 50")
//
// Instrument check: each file must be readable AND contain a string known to
// be there after the change too (the file's own component/export name), so
// an empty-match result cannot come from reading the wrong path.

import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const ROOT = resolve(__dirname, '..', '..')

const FILES: { path: string; known: string }[] = [
  { path: 'components/agents/AgentProfile.tsx', known: 'export function AgentProfile' },
  { path: 'components/agents/CreateAgentWizard.tsx', known: 'export function CreateAgentWizard' },
  { path: 'components/agents/wizard/Advanced.tsx', known: 'export function Advanced' },
  { path: 'hooks/useCommandPreview.ts', known: 'useCommandPreview' },
  // The #904 surfaces added since RED (8-reviewer gate: the guard must cover them).
  { path: 'components/agents/ToolIterationLimitField.tsx', known: 'export function ToolIterationLimitField' },
  { path: 'components/settings/MaxToolIterationsCard.tsx', known: 'export function MaxToolIterationsCard' },
  { path: 'components/settings/MaxToolIterationsLoweringDialog.tsx', known: 'export function MaxToolIterationsLoweringDialog' },
  { path: 'components/settings/PerformanceSection.tsx', known: 'export function PerformanceSection' },
  { path: 'hooks/useGlobalToolIterationLimit.ts', known: 'export function useGlobalToolIterationLimit' },
  // Gate round 2: the refusal hook and the API layer that carries the limit.
  { path: 'components/agents/useToolIterationLimitRefusal.ts', known: 'export function useToolIterationLimitRefusal' },
  { path: 'lib/api/config.ts', known: 'export function fetchPerformanceSettings' },
]

const BANNED: { name: string; re: RegExp }[] = [
  { name: 'useState(200)', re: /useState\(\s*['"]?200['"]?\s*\)/ },
  { name: '?? 200', re: /\?\?\s*200\b/ },
  { name: 'max_tool_iterations: 200', re: /max_tool_iterations\s*:\s*200\b/ },
  { name: 'Default: 200 / Default 200', re: /Default:?\s*200\b/ },
  { name: 'max_tool_iterations ?? 0', re: /max_tool_iterations\s*\?\?\s*0\b/ },
  // Broadened: ANY numeric fallback or default on a limit-named value
  // (`?? n`, `|| n`, `= n`, `: n`), and the old limit numbers as bare code
  // literals (comments stripped first — prose may cite them).
  {
    name: 'numeric default on a limit-named value',
    re: /(max_?tool_?iterations|toolIter\w*|globalLimit|globalToolIterationLimit|maxTurns|max_turns)\s*(\?\?|\|\||=(?!=)|:)\s*\d+\b/i,
  },
  { name: 'bare 200 literal', re: /(?<![\w.$-])200(?![\w.])/ },
  { name: 'bare 50 literal', re: /(?<![\w.$-])50(?![\w.])/ },
]

/** Strips block and line comments so prose citing the old numbers is not a hit. */
function codeOnly(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1')
}

describe('FR-004: no literal tool-iteration default in the SPA', () => {
  it('instrument: every broadened pattern fires on a known-bad snippet', () => {
    const bad: Record<string, string> = {
      'numeric default on a limit-named value': 'const v = globalLimit ?? 150',
      'bare 200 literal': 'setValue(200)',
      'bare 50 literal': 'return 50',
    }
    for (const [name, snippet] of Object.entries(bad)) {
      const b = BANNED.find((x) => x.name === name)
      expect(b, name).toBeDefined()
      expect(b!.re.test(codeOnly(snippet)), `${name} must fire on: ${snippet}`).toBe(true)
    }
    expect(codeOnly('// Default 200\n/* 50 */ const ok = 1'), 'comments are stripped').not.toMatch(/200|50/)
  })

  it.each(FILES)('$path has no hidden limit default', ({ path, known }) => {
    const src = readFileSync(resolve(ROOT, path), 'utf8')
    expect(src, `instrument check: ${path} must contain "${known}"`).toContain(known)
    const hits = BANNED.filter((b) => b.re.test(codeOnly(src))).map((b) => b.name)
    expect(hits, `${path} still carries a literal limit default`).toEqual([])
  })
})
