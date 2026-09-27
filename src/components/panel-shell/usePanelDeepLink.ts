// usePanelDeepLink — the runtime half of the §8.2 URL contract, mounted ONCE
// on the workspace Chat route (the only route whose search schema declares
// `panel`). validateSearch decides which keys are VALID; it cannot rewrite
// the address bar. This hook does, because the router merges a schema return
// onto the raw search and `router.state.location.search` stays raw until a
// navigation replaces it (US-7 AS-4: the drop must be visible).
//
//   1. ADOPTION (URL -> store), fresh landings only. `?panel=library` opens
//      the Library panel workspace-scoped (US-7 AS-1). Replacing a DIFFERENT
//      open panel goes through the leave gate (CRIT-001 lists deep-link
//      replace). A param that is not library — `browser` (SP-21 + SP-28) or
//      any unknown/unregistered id (US-7 AS-4) — is a "no panel" verdict:
//      the URL is replaced and an open panel closes through the gate, so
//      "just the chat renders" (US-7 AS-5). An ABSENT param does not close:
//      in-app opens project the URL themselves, and closing on absence would
//      undo them.
//   2. EVERY replace sets dropped keys to `undefined` (the encoder omits
//      them). Spreading the previous search keeps `session` and friends,
//      which is exactly the shareable-link leak SP-28 forbids. `agent`
//      stays — it is a declared key (SP-23).
//   3. STORE-WINS ON BACK/FORWARD (SP-22 as amended by MAJ-206). A popstate
//      re-projects the store (replace) and the adoption pass that follows
//      does not adopt or close. Panel toggles are not pages.
//   4. Our own replaces set a suppress flag so the search change they cause
//      is not adopted again (no loop, no fight with projection).
import { useEffect, useRef } from 'react'
import { useNavigate, useRouterState } from '@tanstack/react-router'
import { useUiStore } from '@/store/ui'
import { leaveGateThen } from './leaveGate'
import type { PanelContext } from './types'

type SearchRecord = Record<string, unknown>

function rawPanel(search: SearchRecord): string | undefined {
  return typeof search.panel === 'string' ? search.panel : undefined
}

function hasForeignKey(search: SearchRecord): boolean {
  return Object.keys(search).some((key) => key !== 'panel' && key !== 'agent')
}

/** Keep `agent` (declared, SP-23) and an optional `panel=library`. Every
 * other key is set undefined so the encoder drops it. */
function cleanedSearch(prev: SearchRecord, panel: 'library' | undefined): SearchRecord {
  const next: SearchRecord = {}
  for (const key of Object.keys(prev)) next[key] = undefined
  if (panel) next.panel = panel
  if (typeof prev.agent === 'string') next.agent = prev.agent
  return next
}

function gatedCloseIfOpen(): void {
  if (useUiStore.getState().activePanel === null) return
  leaveGateThen(() => {
    if (useUiStore.getState().activePanel !== null) useUiStore.getState().closePanel()
  })
}

export function usePanelDeepLink(workspaceId: string, panel: string | undefined): void {
  const navigate = useNavigate()
  const rawSearch = useRouterState({ select: (s) => s.location.search }) as SearchRecord
  const activePanel = useUiStore((s) => s.activePanel)
  const backForwardRef = useRef(false)
  const selfWriteRef = useRef(false)

  const replaceSearch = (desired: 'library' | undefined) => {
    selfWriteRef.current = true
    navigate({
      search: ((prev: SearchRecord) => cleanedSearch(prev, desired)) as never,
      replace: true,
    })
  }

  useEffect(() => {
    const onPopState = () => {
      backForwardRef.current = true
      const current = useUiStore.getState().activePanel
      replaceSearch(current?.id === 'library' ? 'library' : undefined)
    }
    window.addEventListener('popstate', onPopState)
    return () => window.removeEventListener('popstate', onPopState)
    // replaceSearch closes over the latest navigate; popstate is re-bound
    // with it. The flag refs are stable.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [navigate])

  useEffect(() => {
    if (backForwardRef.current) {
      backForwardRef.current = false
      selfWriteRef.current = false
      return
    }
    if (selfWriteRef.current) {
      selfWriteRef.current = false
      return
    }
    const named = rawPanel(rawSearch)
    if (named === 'library') {
      if (hasForeignKey(rawSearch)) replaceSearch('library')
      const { activePanel: current, openPanel } = useUiStore.getState()
      if (current?.id === 'library') return
      const context: PanelContext = workspaceId ? { workspaceId } : {}
      if (current !== null) {
        leaveGateThen(() => {
          const { activePanel: still, openPanel: open } = useUiStore.getState()
          if (still?.id !== 'library') open('library', context)
        })
      } else {
        openPanel('library', context)
      }
      return
    }
    // browser, unknown, or a foreign key (session) — rewrite the bar.
    if (named !== undefined || hasForeignKey(rawSearch)) replaceSearch(undefined)
    // A named non-library param is a "no panel" verdict (US-7 AS-4/AS-5).
    // An absent param is not: in-app panel opens must survive projection.
    if (named !== undefined) gatedCloseIfOpen()
  }, [rawSearch, workspaceId, navigate])

  // PROJECTION (store -> URL, SP-22 REPLACE). The mount pass records a
  // baseline and writes nothing — a store a mount STARTS with is not a
  // change this mount may project. Later, only Library is written; a closed
  // or Browser panel scrubs `panel` (SP-28) without spreading leftover keys.
  const mountedRef = useRef(false)
  useEffect(() => {
    if (!mountedRef.current) {
      mountedRef.current = true
      return
    }
    const desired = activePanel?.id === 'library' ? 'library' : undefined
    const validated = panel === 'library' ? 'library' : undefined
    if (desired === validated) return
    replaceSearch(desired)
  }, [activePanel, panel, navigate])
}
