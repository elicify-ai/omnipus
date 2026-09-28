/**
 * Guard test — issue #898 (toolui-analysis, 2026-09-26).
 *
 * The #898 gap class: the SPA's registration comment CLAIMED `list_directory`
 * and `search_web` were registered while the registrations did not exist — a
 * live or replayed call fell to the generic badge. This guard makes that
 * drift fail a test instead of silently degrading at runtime:
 *
 *   1. Captures every makeAssistantToolUI registration name the SPA's tool-UI
 *      modules actually register (via a makeAssistantToolUI mock — factory
 *      registrations like makeBashUI('exec') are invisible to a static grep).
 *   2. Extracts the backend builtin tool names from pkg/tools' `Name()`
 *      methods on the Go side.
 *   3. Asserts every SPA registration name is a real backend builtin name or
 *      a documented legacy alias.
 *
 * WHY pkg/tools' Name() methods are the source: pkg/tools/builtin_registry.go
 * boot-freezes the builtin catalog from exactly these Name() methods, so a
 * Name() return value is by construction what the backend can emit on the
 * wire. The test reaches them from vitest via the filesystem (precedent:
 * src/test/canonicalToolNames.test.ts, which also walks the repo). Dynamic
 * names (mcp_* from MCP servers, compositor wrappers) have no static source
 * and are out of reach — a registered name resolving to NEITHER set fails
 * this test, which is the safe direction for a guard.
 */

import { describe, it, expect, vi } from 'vitest'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'

// vi.hoisted so the capture array exists before the vi.mock factory runs.
const captured = vi.hoisted(() => ({ names: [] as string[] }))

vi.mock('@assistant-ui/react', async (importOriginal) => {
  const original = await importOriginal<typeof import('@assistant-ui/react')>()
  return {
    ...original,
    makeAssistantToolUI: (config: { toolName: string; render: unknown }) => {
      captured.names.push(config.toolName)
      // Dummy component; nothing renders it here.
      return () => null
    },
  }
})

// Static imports — vi.mock intercepts makeAssistantToolUI before these run.
// One import per module that calls makeAssistantToolUI; a NEW dedicated
// tool-UI module must be added here, and its registrations are then covered
// automatically.
import '@/components/chat/tools/BashOutput'
import '@/components/chat/tools/FileReadPreview'
import '@/components/chat/tools/FileTreeView'
import '@/components/chat/tools/FileWriteConfirm'
import '@/components/chat/tools/WebSearchResult'
import '@/components/chat/tools/WebFetchPreview'
import '@/components/chat/tools/BrowserNavigate'
import '@/components/chat/tools/BrowserTool'
import '@/components/chat/tools/WebServeUI'
import '@/components/chat/tools/ServeWorkspaceUI'
import '@/components/chat/tools/RunInWorkspaceUI'
import '@/components/chat/tools/SetGoalToolUI'

// ── Backend extraction ──────────────────────────────────────────────────────
// Package-scoped consts:  const X = "name"
const CONST_DECL = /const\s+([A-Za-z0-9_]+)\s*=\s*"([A-Za-z0-9_.]+)"/g
// Name() bodies on one line:  func (r *T) Name() string { return "lit" }
const NAME_LITERAL = /func\s+\([^)]*\)\s+Name\(\)\s+string\s*\{\s*return\s+"([A-Za-z0-9_.]+)"\s*\}/g
// Name() bodies returning a package const:  func (r *T) Name() string { return SomeConst }
const NAME_CONST = /func\s+\([^)]*\)\s+Name\(\)\s+string\s*\{\s*return\s+([A-Za-z0-9_]+)\s*\}/g

function collectGoFiles(dir: string): string[] {
  const out: string[] = []
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) out.push(...collectGoFiles(full))
    else if (entry.endsWith('.go') && !entry.endsWith('_test.go')) out.push(full)
  }
  return out
}

function extractBackendToolNames(projectRoot: string): Set<string> {
  const constants = new Map<string, string>()
  const names = new Set<string>()
  for (const file of collectGoFiles(join(projectRoot, 'pkg/tools'))) {
    const src = readFileSync(file, 'utf8')
    for (const m of src.matchAll(CONST_DECL)) constants.set(m[1], m[2])
    for (const m of src.matchAll(NAME_LITERAL)) names.add(m[1])
    for (const m of src.matchAll(NAME_CONST)) {
      const constName = m[1]
      const value = constants.get(constName)
      if (value) names.add(value)
    }
  }
  return names
}

const projectRoot = join(__dirname, '..', '..')

// ── Legacy aliases — SPA registrations that are NOT backend builtin names ───
// Every entry states its reason. These render old, already-persisted session
// transcripts (historical JSONL is never migrated) or the dotted internal-
// name form of an underscore Name() literal (the backend registry normalizes
// underscore Name() literals to dotted internal names —
// pkg/tools/registry.go::UnsanitizeToolName — transcripts may carry either).
const LEGACY_ALIASES: Record<string, string> = {
  exec: 'legacy alias of bash (ADR-036 consolidation)',
  workspace_shell: 'legacy alias of bash (ADR-036 consolidation)',
  'workspace.shell': 'dotted form of workspace_shell',
  workspace_shell_bg: 'legacy alias of bash background dispatch',
  'workspace.shell_bg': 'dotted form of workspace_shell_bg',
  'file.read': 'BRD C.6.1.4 dot-notation alias of read_file',
  'file.list': 'BRD C.6.1.4 dot-notation alias of list_directory',
  'file.write': 'BRD C.6.1.4 dot-notation alias of write_file',
  list_dir: 'legacy alias of list_directory (pre-rename transcripts)',
  web_search: 'legacy alias of search_web (pre-rename transcripts)',
  web_fetch: 'legacy alias of fetch_url (pre-rename transcripts)',
  web_serve: 'legacy form; backend canonical serve_web is unregistered (reported gap)',
  serve_workspace: 'back-compat alias of web_serve',
  run_in_workspace: 'back-compat alias of web_serve',
  'browser.navigate': 'dotted form of browser_navigate',
  'browser.click': 'dotted form of browser_click',
  'browser.type': 'dotted form of browser_type',
  'browser.screenshot': 'dotted form of browser_screenshot',
  'browser.get_text': 'dotted form of browser_get_text',
  'browser.wait': 'dotted form of browser_wait',
  'browser.evaluate': 'dotted form of browser_evaluate',
}


// ── Tests ───────────────────────────────────────────────────────────────────

describe('backend Name() extraction sanity', () => {
  it('finds the pinned backend builtin names', () => {
    const backend = extractBackendToolNames(projectRoot)
    const pinned = ['search_web', 'list_directory', 'bash', 'read_file', 'fetch_url', 'set_goal', 'serve_web', 'browser_navigate', 'write_file']
    for (const name of pinned) {
      expect(backend, `backend Name() extraction missed "${name}"`).toContain(name)
    }
  })
})

describe('SPA registrations subset backend U aliases (issue #898 guard)', () => {
  it('every captured registration name is a backend builtin name or a documented legacy alias', () => {
    const backend = extractBackendToolNames(projectRoot)
    const allowed = new Set([...backend, ...Object.keys(LEGACY_ALIASES)])
    const unknown = captured.names.filter((n) => !allowed.has(n))
    expect(
      unknown,
      `SPA registers names the backend builtin catalog does not emit: ${unknown.join(', ')}`,
    ).toEqual([])
  })

  it('issue #898 pins: search_web and list_directory ARE registered', () => {
    expect(captured.names, 'search_web registration missing (#898)').toContain('search_web')
    expect(captured.names, 'list_directory registration missing (#898)').toContain('list_directory')
  })

  it('sanity: the capture saw registrations at all (the instrument could have failed red)', () => {
    expect(captured.names.length).toBeGreaterThanOrEqual(30)
    expect(captured.names).toContain('bash')
    expect(captured.names).toContain('web_serve')
    expect(captured.names).toContain('browser_navigate')
  })
})

describe('legacy alias map hygiene', () => {
  it('every LEGACY_ALIASES key is actually captured from the imported modules', () => {
    const capturedSet = new Set(captured.names)
    for (const alias of Object.keys(LEGACY_ALIASES)) {
      expect(capturedSet, `LEGACY_ALIASES key "${alias}" is not registered by any imported module`).toContain(alias)
    }
  })

  it('no duplicate registrations of the same tool name', () => {
    const dupes = captured.names.filter((n, i) => captured.names.indexOf(n) !== i)
    expect(dupes, `duplicate tool-UI registrations: ${dupes.join(', ')}`).toEqual([])
  })
})
