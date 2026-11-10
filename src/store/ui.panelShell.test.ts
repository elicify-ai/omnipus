// ui.panelShell.test.ts — RED pack for side-panel-shell-spec.md §8.1 (wave 1,
// TDD plan test #4's store-level half: "Single-panel reducer: open replaces,
// toggle closes, close clears URL param").
//
// Oracle: §8.1's Shell state contract, verbatim —
//
//   Shell state (store, single slice replacing browserPanel/libraryPanel):
//     activePanel: { id, context } | null           // at most one (SP-7)
//     openPanel(id, context) / closePanel() / setPanelWidth(px)
//
// — and SC-005 ("No simultaneous Library+Browser open exists anywhere in the
// app after wave 1 — proven by grep + store shape"), and §3.1's impact row:
// "src/store/ui.ts::UiStore (browserPanel, libraryPanel) — modifies — The
// two slices collapse into one at-most-one-open panel state."
//
// Out of scope here (covered elsewhere): the `beforeLeave`/CRIT-001 guard
// threading (component-level, since the guard belongs to a PanelDefinition a
// real panel supplies — see ChatControls.panelGuard.test.tsx and
// WorkspaceTabBar.toggle.test.tsx), URL projection (history REPLACE, the
// `panel` search param — see workspaces.$workspaceId.chat.panel.test.tsx),
// and width-memory keying by real signed-in user (SP-20 — see
// panelShellWidthMemory.wave1.test.ts).
//
// RED evidence (2026-09-27, read src/store/ui.ts::UiStore in full): the
// store still declares the OLD two independent slices —
// `browserPanel: {sessionId, agentId} | null` / `libraryPanel: {workspaceId?}
// | null` with `openBrowserPanel`/`openLibraryPanel`/`closeBrowserPanel`/
// `closeLibraryPanel` — and has no `activePanel`, `openPanel`, `closePanel`
// or `setPanelWidth` at all. Every assertion below fails at runtime (vitest
// transpiles via esbuild with no type-check gate, so the missing properties
// resolve to `undefined` rather than a compile error) — this is the intended
// RED-for-the-right-reason failure: "expected function, got undefined" / a
// wrongly-shaped object, not an ImportError.

import { describe, it, expect, beforeEach } from 'vitest'
import { useUiStore } from '@/store/ui'

describe('ui store — single-panel shell slice (side-panel-shell-spec.md §8.1, wave 1)', () => {
  beforeEach(() => {
    // Full reset between tests — the real store keeps growing slices
    // (toasts, modals, ...) this file must not disturb; only null out what
    // this test cares about via the API under test itself once it exists.
    // For now, closePanel() is asserted to exist by the first test; later
    // tests call it directly to reset.
    const state = useUiStore.getState() as unknown as { closePanel?: () => void }
    state.closePanel?.()
  })

  it('exposes activePanel/openPanel/closePanel/setPanelWidth — the §8.1 shape replacing browserPanel/libraryPanel', () => {
    const state = useUiStore.getState() as unknown as Record<string, unknown>
    expect(typeof state.openPanel).toBe('function')
    expect(typeof state.closePanel).toBe('function')
    expect(typeof state.setPanelWidth).toBe('function')
    expect(state.activePanel).toBeNull()
  })

  it('opening the Library panel sets activePanel to {id:"library", context} (§8.1)', () => {
    const state = useUiStore.getState() as unknown as {
      openPanel: (id: string, context?: Record<string, unknown>) => void
    }
    state.openPanel('library', { workspaceId: 'ws-1' })
    expect((useUiStore.getState() as unknown as { activePanel: unknown }).activePanel).toEqual({
      id: 'library',
      context: { workspaceId: 'ws-1' },
    })
  })

  it('opening a second panel REPLACES the first — never both open at once (SP-7, FR-006, SC-005)', () => {
    const state = useUiStore.getState() as unknown as {
      openPanel: (id: string, context?: Record<string, unknown>) => void
    }
    state.openPanel('library', { workspaceId: 'ws-1' })
    state.openPanel('browser', { sessionId: 's1', agentId: 'a1' })

    const after = useUiStore.getState() as unknown as Record<string, unknown>
    expect(after.activePanel).toEqual({
      id: 'browser',
      context: { sessionId: 's1', agentId: 'a1' },
    })
    // SC-005's "store shape proves it": the OLD two-slice shape (where both
    // could hold a value simultaneously) must be GONE, not merely unused —
    // otherwise a stray call site could still open both at once.
    expect('browserPanel' in after).toBe(false)
    expect('libraryPanel' in after).toBe(false)
  })

  it('closePanel() clears activePanel to null', () => {
    const state = useUiStore.getState() as unknown as {
      openPanel: (id: string, context?: Record<string, unknown>) => void
      closePanel: () => void
    }
    state.openPanel('browser', { sessionId: 's1', agentId: 'a1' })
    state.closePanel()
    expect((useUiStore.getState() as unknown as { activePanel: unknown }).activePanel).toBeNull()
  })

  it('setPanelWidth(px) is readable back off the store (the shell\'s applied-width source, MAJ-009\'s basis)', () => {
    const state = useUiStore.getState() as unknown as {
      openPanel: (id: string, context?: Record<string, unknown>) => void
      setPanelWidth: (px: number) => void
    }
    state.openPanel('library', {})
    state.setPanelWidth(555)
    expect((useUiStore.getState() as unknown as { panelWidth: unknown }).panelWidth).toBe(555)
  })
})
