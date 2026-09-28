// panelShell.registry.test.ts — side-panel-shell-spec.md §12 test #1: the
// panel registry resolves registered ids (library, browser — wave 1's
// registrations, SC-002) and resolves NOTHING for unregistered ids (the
// wave-2/3 panels ship later; §9 wave-1 scope: "skip anything tagged wave
// 2/3 except as 'unregistered id is dropped'").
//
// WHY A NEW MODULE (and why dynamic import): the spec §8.1 registry contract
// ("Adding a panel is one entry — the shell changes nothing", SP-4) needs ONE
// production registry the shell consumes. Wave 0 shipped only the DEMO
// registry (src/components/panel-shell/demo/demoPanelRegistry.tsx::
// DEMO_PANELS — six entries incl. mail/tasks/team/calendar, demo-only expand
// targets, not consumed by the app). The production registry does not exist
// anywhere in this tree — verified before writing this file (rg for a
// production registry export: only demoPanelRegistry and the type file).
// Per the RED discipline (never a silent skip, never a bare refusal): every
// test below loads the module dynamically and, if the module is missing,
// fails LOUDLY with a BLOCKED message naming the spec reference. When
// frontend-lead's GREEN lands the registry at this path, these tests become
// the acceptance gate; if GREEN lands it elsewhere, updating the single
// MODULE_PATH constant is the whole re-point.
//
// Every expected value is spec-derived: titles and expand targets from §1's
// panel inventory (/library pop-out route; /browser-live?session=&agent=),
// the beforeLeave rule from §8.1 ("beforeLeave is supplied ONLY by panels
// with unsaved-edit risk — Library, in wave 1"), the exactly-two rule from
// SC-002 + §9 wave-1 scope.

import { describe, expect, it } from 'vitest'
import type { PanelDefinition } from '@/components/panel-shell/types'

// The wave-1 production registry module the shell consumes. Spec §8.1 frames
// it as the registry the shell reads; this tree has no such module yet (the
// only registry-shaped file is the demo one), so the path below is the
// conventional one — see the header note.
const MODULE_PATH = '@/components/panel-shell/registry'

async function loadRegistry(): Promise<{ panels: PanelDefinition[] }> {
  let mod: unknown
  try {
    mod = await import(/* @vite-ignore */ MODULE_PATH)
  } catch (importErr) {
    throw new Error(
      `BLOCKED: production panel registry module not implemented — required by side-panel-shell-spec.md §8.1/§12 test #1 (import of ${MODULE_PATH} failed: ${importErr instanceof Error ? importErr.message : String(importErr)})`,
      { cause: importErr },
    )
  }
  const panels = (mod as { panels?: PanelDefinition[] }).panels ??
    (mod as { PANEL_REGISTRY?: PanelDefinition[] }).PANEL_REGISTRY
  if (!Array.isArray(panels)) {
    throw new Error(
      `BLOCKED: ${MODULE_PATH} exists but exports no panels/PANEL_REGISTRY array — required by side-panel-shell-spec.md §8.1/§12 test #1`,
    )
  }
  return { panels }
}

describe('panel registry — wave-1 registrations (§12 #1, SC-002)', () => {
  it('exposes a production registry module the shell can consume (spec §8.1; missing module fails loudly, never skips)', async () => {
    let panels: PanelDefinition[] | null = null
    let blocked = ''
    try {
      panels = (await loadRegistry()).panels
    } catch (err) {
      blocked = err instanceof Error ? err.message : String(err)
    }
    expect(blocked, blocked || 'registry module must exist').toBe('')
    expect(panels).not.toBeNull()
  })

  it('registers exactly wave-1 panels: library and browser (SC-002: exactly two real registrations in production wave-1 code)', async () => {
    const { panels } = await loadRegistry()
    expect(panels.map((p) => p.id).sort()).toEqual(['browser', 'library'])
  })

  it('library resolves: title "Library", and its expand target names the /library pop-out route (§1 panel inventory)', async () => {
    const { panels } = await loadRegistry()
    const library = panels.find((p) => p.id === 'library')
    expect(library).toBeDefined()
    expect(library?.title).toBe('Library')
    const target = library?.expandTarget({ workspaceId: 'ws-1' })
    expect(typeof target === 'string' || typeof (target as unknown as { to?: string })?.to === 'string').toBe(
      true,
    )
    const s = typeof target === 'string' ? target : String((target as unknown as { to?: string }).to)
    expect(s).toContain('/library')
    expect(s).toContain('ws-1')
  })

  it('browser resolves: title "Browser", expand target names /browser-live with session AND agent (§1: /browser-live?session=…&agent=…)', async () => {
    const { panels } = await loadRegistry()
    const browser = panels.find((p) => p.id === 'browser')
    expect(browser).toBeDefined()
    expect(browser?.title).toBe('Browser')
    const target = browser?.expandTarget({ sessionId: 's1', agentId: 'a1' })
    const s = typeof target === 'string' ? target : String((target as unknown as { to?: string }).to)
    expect(s).toContain('/browser-live')
    expect(s).toContain('session=')
    expect(s).contains('agent=')
  })

  it('library carries beforeLeave (the CRIT-001 unsaved-edits guard — §8.1); browser carries NONE (transitions replace it freely)', async () => {
    const { panels } = await loadRegistry()
    const library = panels.find((p) => p.id === 'library')
    const browser = panels.find((p) => p.id === 'browser')
    expect(typeof library?.beforeLeave).toBe('function')
    expect(browser?.beforeLeave).toBeUndefined()
  })

  it('unregistered ids resolve NOTHING — mail/tasks/team/calendar are wave 2/3 (§9 wave-1 scope; an unregistered id is dropped, MAJ-012)', async () => {
    const { panels } = await loadRegistry()
    for (const id of ['mail', 'tasks', 'team', 'calendar']) {
      expect(panels.find((p) => p.id === id)).toBeUndefined()
    }
  })
})
