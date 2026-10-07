// usePanelDeepLink — the runtime half of the §8.2 URL contract, mounted ONCE
// on the workspace Chat route (the only route whose search schema declares
// `panel`). validateSearch decides which keys are VALID; it cannot rewrite
// the address bar. This hook does, because the router merges a schema return
// onto the raw search and `router.state.location.search` stays raw until a
// navigation replaces it (US-7 AS-4: the drop must be visible).
//
//   1. ADOPTION (URL -> store), fresh landings only. `?panel=library` opens
//      the Library panel workspace-scoped (US-7 AS-1); `?panel=mail` opens
//      Mail the same way, with the SP-23 mailbox directive in the context
//      (§10 wave 2). Replacing a DIFFERENT open panel goes through the leave
//      gate (CRIT-001 lists deep-link replace). A param that is not
//      adoptable — `browser` (SP-21 + SP-28) or any unknown/unregistered id
//      (US-7 AS-4) — is a "no panel" verdict: the URL is replaced and an open
//      panel closes through the gate, so "just the chat renders" (US-7 AS-5).
//      An ABSENT param does not close: in-app opens project the URL
//      themselves, and closing on absence would undo them.
//   2. EVERY replace sets dropped keys to `undefined` (the encoder omits
//      them). Spreading the previous search keeps `session` and friends,
//      which is exactly the shareable-link leak SP-28 forbids. `agent`
//      stays — it is a declared key (SP-23).
//   3. STORE-WINS ON BACK/FORWARD (SP-22 as amended by MAJ-206). A popstate
//      re-projects the store (replace) and the adoption pass that follows
//      does not adopt or close. Panel toggles are not pages.
//   4. Our own replaces set a suppress flag so the search change they cause
//      is not adopted again (no loop, no fight with projection).
import { useCallback, useEffect, useRef } from 'react'
import { useBlocker, useNavigate, useRouter, useRouterState } from '@tanstack/react-router'
import { useUiStore } from '@/store/ui'
import { leaveGateThen } from './leaveGate'
import { isWorkspaceScopedPanel } from './types'
import type { ActivePanel, WorkspacePanelContext, WorkspacePanelId } from './types'
import { getPanelDefinition } from './registry'
import {
  confirmDiscardLibraryEdits,
  isLibraryEditorDirty,
} from '@/components/library/preview/unsavedGuard'

type SearchRecord = Record<string, unknown>

function rawPanel(search: SearchRecord): WorkspacePanelId | undefined {
  if (typeof search.panel !== 'string') return undefined
  const id = search.panel as WorkspacePanelId
  // §8.2: the valid `?panel=` values are the REGISTERED ids — the same
  // single-source registry the shell and the full-screen route read. An
  // unregistered id (`mail` until wave 2, `bogus` always) is dropped exactly
  // like an unknown one (US-7 AS-4).
  if (!getPanelDefinition(id)) return undefined
  return isWorkspaceScopedPanel(id) ? id : undefined
}

function hasForeignKey(search: SearchRecord): boolean {
  return Object.keys(search).some((key) => key !== 'panel' && key !== 'agent')
}

/** Keep `agent` (declared, SP-23) and an optional `panel=library`. Every
 * other key is set undefined so the encoder drops it. */
function cleanedSearch(prev: SearchRecord, panel: WorkspacePanelId | undefined): SearchRecord {
  const next: SearchRecord = {}
  for (const key of Object.keys(prev)) next[key] = undefined
  if (panel) next.panel = panel
  if (typeof prev.agent === 'string') next.agent = prev.agent
  return next
}

function gatedCloseIfOpen(): void {
  const outgoing = useUiStore.getState().activePanel
  if (outgoing === null) return
  leaveGateThen(outgoing.id, () => {
    if (useUiStore.getState().activePanel !== null) useUiStore.getState().closePanel()
  })
}

function projectedPanel(activePanel: ActivePanel | null): WorkspacePanelId | undefined {
  if (activePanel === null) return undefined
  if (!getPanelDefinition(activePanel.id)) return undefined
  return isWorkspaceScopedPanel(activePanel.id) ? activePanel.id : undefined
}

/** The workspace a currently-open panel is scoped to, or `undefined` for the
 * Browser (session-scoped, never workspace-scoped — its context type has no
 * `workspaceId` at all). */
function activePanelWorkspaceId(activePanel: ActivePanel): string | undefined {
  return activePanel.id === 'browser' ? undefined : activePanel.context.workspaceId
}

/** F3: the panel-TYPE check alone (`current.id === named`) is not enough —
 * it is blind to a same-page hash navigation that keeps `panel=mail` (or any
 * other workspace-scoped panel) but changes `$workspaceId`. Without also
 * comparing the workspace, re-requesting the SAME panel id for a DIFFERENT
 * workspace is wrongly treated as "already adopted" and the panel keeps
 * rendering the OLD workspace's data.
 *
 * For `mail` specifically, the workspace/panel-type match alone is still not
 * enough: the URL's `agent` param (SP-23's mailbox directive, read the same
 * way `adoptionContext` does) can name a DIFFERENT mailbox while `panel=mail`
 * and `$workspaceId` both stay unchanged (a same-page `?agent=` swap). Treat
 * that as NOT already adopted so the effect adopts the newly named mailbox
 * instead of silently keeping the old one on screen.
 *
 * Only compare when the URL carries an EXPLICIT `agent` string. An absent
 * `agent` key is not a "no mailbox" directive here — the chat_link scheme
 * (email-mail-view-spec.md §17) transfers the mailbox into the panel
 * context via `openPanel` and lands on a bare `chat?panel=mail` with no
 * `agent` param at all; treating that absence as "clear the mailbox" would
 * wipe the context the link just set. */
function isAlreadyAdopted(
  activePanel: ActivePanel | null,
  named: WorkspacePanelId,
  workspaceId: string,
  search: SearchRecord,
): boolean {
  if (activePanel === null
    || activePanel.id !== named
    || activePanelWorkspaceId(activePanel) !== workspaceId) {
    return false
  }
  if (named === 'mail' && typeof search.agent === 'string') {
    return activePanel.context.mailboxId === search.agent
  }
  return true
}

/**
 * Adoption context per panel (§8.1: context carries what the panel needs).
 * Mail (SP-23): the URL's `agent` param is the mailbox directive. A named
 * `panel=mail` link WITHOUT `agent` explicitly requests the chooser, even
 * when another workspace's Mail panel is already open. An ordinary workspace
 * switch is different: Sidebar navigates without `panel`, and
 * WorkspaceTabContainer's SP-29 follow moves the existing panel context.
 * Projecting that followed panel into the URL is a self-write, not a new
 * deep-link adoption. Library needs only the workspace.
 */
function adoptionContext(
  id: WorkspacePanelId,
  workspaceId: string,
  search: SearchRecord,
): WorkspacePanelContext {
  const context: WorkspacePanelContext = workspaceId ? { workspaceId } : {}
  if (id === 'mail') {
    context.mailboxId = typeof search.agent === 'string' ? search.agent : null
  }
  return context
}

export function usePanelDeepLink(workspaceId: string, panel: string | undefined): void {
  const router = useRouter()
  const owningChatPath = `/workspaces/${workspaceId}/chat`
  // The address a search-only navigate() builds from is the PENDING/LATEST
  // location, which moves to the destination before `router.state.location`
  // (committed) does and before this chat unmounts. Every writer below
  // (cleanup, popstate re-projection, store->URL projection) goes through
  // replaceSearch, so this one check, on that location, keeps the destination's
  // own query (`/settings?tab=chat`) out of reach of cleanedSearch.
  const isOwningChat = useCallback((): boolean => {
    const { pathname } = router.pendingBuiltLocation ?? router.latestLocation
    return pathname.replace(/\/+$/, '') === owningChatPath
  }, [router, owningChatPath])
  const navigate = useNavigate()
  const rawSearch = useRouterState({ select: (s) => s.location.search }) as SearchRecord
  const activePanel = useUiStore((s) => s.activePanel)
  const backForwardRef = useRef(false)
  const selfWriteRef = useRef(false)
  const freshLinkRef = useRef<{ href: string; workspaceId: string } | null>(null)

  // MAJ-205: URL-started transitions are intercepted before the route,
  // address, or panel can move. App-started close/replace paths clear the
  // dirty flag through leaveGateThen before their URL projection arrives,
  // so they do not prompt twice.
  useBlocker({
    shouldBlockFn: async () => {
      const current = useUiStore.getState().activePanel
      if (current?.id !== 'library' || !isLibraryEditorDirty()) return false
      return !(await confirmDiscardLibraryEdits())
    },
    enableBeforeUnload: false,
  })

  const replaceSearch = useCallback(
    (desired: WorkspacePanelId | undefined): void => {
      // Router location changes before React necessarily unmounts this chat.
      // Never let its pending projection strip another route's context (for
      // example the full-screen panel's workspace/path during Expand).
      if (!isOwningChat()) return
      selfWriteRef.current = true
      navigate({
        search: ((prev: SearchRecord) => cleanedSearch(prev, desired)) as never,
        replace: true,
      })
    },
    [navigate, isOwningChat],
  )

  useEffect(() => {
    // A new same-document hash link emits popstate too. Navigation API's
    // navigationType distinguishes that push from a Back/Forward traversal;
    // the latter must still re-project the store (SP-22). Keep the destination
    // until its workspace route renders: the old route can render the new
    // search first, before the new workspaceId reaches this hook.
    if (!window.navigation) return
    const onNavigate = (event: NavigateEvent) => {
      freshLinkRef.current = null
      if (event.navigationType !== 'push' || !event.hashChange || !event.destination.sameDocument) return
      const url = new URL(event.destination.url)
      const match = /^#\/workspaces\/([^/?#]+)\/chat\?(.+)$/.exec(url.hash)
      if (!match || !new URLSearchParams(match[2]).has('panel')) return
      freshLinkRef.current = { href: url.href, workspaceId: match[1] }
    }
    window.navigation.addEventListener('navigate', onNavigate)
    return () => window.navigation.removeEventListener('navigate', onNavigate)
  }, [])

  useEffect(() => {
    const onPopState = () => {
      if (!isOwningChat()) return
      if (freshLinkRef.current?.href === window.location.href) return
      backForwardRef.current = true
      const current = useUiStore.getState().activePanel
      replaceSearch(projectedPanel(current))
    }
    window.addEventListener('popstate', onPopState)
    return () => window.removeEventListener('popstate', onPopState)
  }, [replaceSearch, isOwningChat])

  useEffect(() => {
    if (!isOwningChat()) return
    const freshLink = freshLinkRef.current?.href === window.location.href
      && freshLinkRef.current.workspaceId === workspaceId
    if (backForwardRef.current) {
      backForwardRef.current = false
      selfWriteRef.current = false
      if (!freshLink) return
    }
    if (selfWriteRef.current) {
      selfWriteRef.current = false
      if (!freshLink) return
    }
    if (freshLink) freshLinkRef.current = null
    const rawNamed = typeof rawSearch.panel === 'string' ? rawSearch.panel : undefined
    const named = rawPanel(rawSearch)
    if (named !== undefined) {
      if (hasForeignKey(rawSearch)) replaceSearch(named)
      const { activePanel: current, openPanel } = useUiStore.getState()
      if (isAlreadyAdopted(current, named, workspaceId, rawSearch)) return
      // Adoption follows the URL directive, never the prior workspace's
      // context. The separate workspace-follow effect retains the mailbox
      // when navigation does not include a new panel directive.
      const context = adoptionContext(named, workspaceId, rawSearch)
      if (current !== null) {
        leaveGateThen(current.id, () => {
          const { activePanel: still, openPanel: open } = useUiStore.getState()
          if (!isAlreadyAdopted(still, named, workspaceId, rawSearch)) open(named, context)
        })
      } else {
        openPanel(named, context)
      }
      return
    }
    // browser, unknown, or a foreign key (session) — rewrite the bar.
    if (rawNamed !== undefined || hasForeignKey(rawSearch)) replaceSearch(undefined)
    // A named non-library param is a "no panel" verdict (US-7 AS-4/AS-5).
    // An absent param is not: in-app panel opens must survive projection.
    if (rawNamed !== undefined) gatedCloseIfOpen()
  }, [rawSearch, workspaceId, replaceSearch, isOwningChat])

  // PROJECTION (store -> URL, SP-22 REPLACE). The mount pass records a
  // baseline and writes nothing — a store a mount STARTS with is not a
  // change this mount may project. Later, only Library is written; a closed
  // or Browser panel scrubs `panel` (SP-28) without spreading leftover keys.
  const mountedRef = useRef(false)
  useEffect(() => {
    if (!isOwningChat()) return
    if (!mountedRef.current) {
      mountedRef.current = true
      return
    }
    const desired = projectedPanel(activePanel)
    const validated = panel === undefined ? undefined : rawPanel({ panel })
    if (desired === validated) return
    replaceSearch(desired)
  }, [activePanel, panel, replaceSearch, isOwningChat])
}
