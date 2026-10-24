// R8 guard: every producer of the active-workspace-list query must use the
// shared activeWorkspacesQueryOptions (which forwards the abort signal). A
// queryKey for that list followed by its own queryFn is a signal-less
// producer that a Retry could never abort.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const ROOT = join(__dirname, '..')
function sources(dir: string): string[] {
  return readdirSync(dir).flatMap(name => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return sources(path)
    return /\.(ts|tsx)$/.test(name) && !/\.(test|spec|stories)\.tsx?$/.test(name) ? [path] : []
  })
}
const KEY = String.raw`(?:workspacesQueryKeys\.list\(\{\s*status:\s*'active'\s*\}\)|\[\s*'workspaces'\s*,\s*\{\s*status:\s*'active'\s*\}\s*\])`
const OWN_PRODUCER = new RegExp(String.raw`queryKey:\s*${KEY}\s*,[^{}]*?queryFn\s*:`, 'g')

describe('active workspace list query producers', () => {
  it('positive control: the scanner sees a hand-built producer', () => {
    expect(`useQuery({ queryKey: workspacesQueryKeys.list({ status: 'active' }), queryFn: () => fetchWorkspaces({ status: 'active' }) })`).toMatch(OWN_PRODUCER)
    expect(`useQuery({ queryKey: ['workspaces', { status: 'active' }],\n  queryFn: () => x() })`).toMatch(new RegExp(OWN_PRODUCER.source))
  })
  it('no source file builds that query with its own queryFn', () => {
    const files = sources(ROOT)
    expect(files.length, 'positive control: the scan reads the source tree').toBeGreaterThan(100)
    // The shared definition itself is the one legitimate producer.
    const offenders = files.map(file => file.slice(ROOT.length + 1))
      .filter(file => file !== join('lib', 'workspaceQueries.ts') && new RegExp(OWN_PRODUCER.source).test(readFileSync(join(ROOT, file), 'utf8')))
    expect(offenders, 'use activeWorkspacesQueryOptions from @/lib/workspaceQueries').toStrictEqual([])
  })
})
