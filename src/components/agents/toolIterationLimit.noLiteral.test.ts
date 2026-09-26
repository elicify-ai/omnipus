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
]

const BANNED: { name: string; re: RegExp }[] = [
  { name: 'useState(200)', re: /useState\(\s*['"]?200['"]?\s*\)/ },
  { name: '?? 200', re: /\?\?\s*200\b/ },
  { name: 'max_tool_iterations: 200', re: /max_tool_iterations\s*:\s*200\b/ },
  { name: 'Default: 200 / Default 200', re: /Default:?\s*200\b/ },
  { name: 'max_tool_iterations ?? 0', re: /max_tool_iterations\s*\?\?\s*0\b/ },
]

describe('FR-004: no literal tool-iteration default in the SPA', () => {
  it.each(FILES)('$path has no hidden limit default', ({ path, known }) => {
    const src = readFileSync(resolve(ROOT, path), 'utf8')
    expect(src, `instrument check: ${path} must contain "${known}"`).toContain(known)
    const hits = BANNED.filter((b) => b.re.test(src)).map((b) => b.name)
    expect(hits, `${path} still carries a literal limit default`).toEqual([])
  })
})
