import { Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, createFileRoute, useNavigate, useRouter } from '@tanstack/react-router'
import { useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, SpinnerGap } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { ErrorBoundary } from '@/components/shared/ErrorBoundary'
import { focusChatInputWhenReady } from '@/components/panel-shell/panelFocus'
import { isStandaloneWindow } from '@/lib/browserDisplayMode'
import { leaveGateThen } from '@/components/panel-shell/leaveGate'
import { shouldClosePanelOnEscape } from '@/components/panel-shell/panelEscape'
import { getPanelDefinition } from '@/components/panel-shell/registry'
import { isWorkspaceScopedPanel } from '@/components/panel-shell/types'
import type { PanelContext, PanelId } from '@/components/panel-shell/types'
import { fetchWorkspaces, workspacesQueryKeys } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { announcePanelTabPresence, panelIdentityFromContext } from '@/lib/panelTabPresence'
import {
  announcePanelPopoutClosed,
  announcePanelPopoutContext,
  announcePanelPopoutDeparture,
} from '@/lib/panelPopoutLifecycle'

export const Route = createFileRoute('/_fullscreen/panel/$panelId')({
  validateSearch: (search) => search as Record<string, unknown>,
  component: FullScreenPanelRoute,
})

function InvalidPanel() {
  return (
    <main className="flex h-dvh items-center justify-center bg-[var(--color-surface-0)] p-[var(--space-4)] text-center">
      <div className="grid gap-[var(--space-3)]">
        <h1 className="text-[length:var(--type-body-compact-size)] font-semibold">Can&apos;t open this panel</h1>
        <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
          The panel link is incomplete or no longer available.
        </p>
        <Button asChild variant="outline">
          <Link to="/">Back to Omnipus</Link>
        </Button>
      </div>
    </main>
  )
}

function FullScreenPanelRoute() {
  const { panelId } = Route.useParams()
  const search = Route.useSearch()
  const navigate = useNavigate()
  const router = useRouter()
  const queryClient = useQueryClient()
  const [exitError, setExitError] = useState<string | null>(null)
  const definition = getPanelDefinition(panelId as PanelId)
  const initialContext = useMemo(
    () => definition?.fullScreen.fromSearch(search) ?? null,
    [definition, search],
  )
  const popoutId = typeof search.popout === 'string' && search.popout.length > 0 ? search.popout : null
  const contextRef = useRef<PanelContext | null>(initialContext)
  const expandContextRef = useRef<(() => PanelContext) | null>(null)
  const announcementRef = useRef<ReturnType<typeof announcePanelTabPresence> | null>(null)
  const closedRef = useRef(false)

  const reportContext = useCallback((context: PanelContext) => {
    if (!definition) return
    contextRef.current = context
    const identity = panelIdentityFromContext(definition.id, context)
    if (identity) announcementRef.current?.update(identity)
    if (popoutId) announcePanelPopoutContext(definition.id, popoutId, context)
    void navigate({
      to: '/panel/$panelId',
      params: { panelId: definition.id },
      search: {
        ...definition.fullScreen.toSearch(context),
        ...(popoutId ? { popout: popoutId } : {}),
      },
      replace: true,
    })
  }, [definition, navigate, popoutId])

  const registerExpandContext = useCallback((getter: (() => PanelContext) | null) => {
    expandContextRef.current = getter
    if (getter) reportContext(getter())
  }, [reportContext])

  const announceClosed = useCallback(() => {
    if (closedRef.current || !definition || !popoutId || contextRef.current === null) return
    // Release exclusive tab presence before the opener tries to re-dock this panel.
    announcementRef.current?.stop()
    closedRef.current = true
    announcePanelPopoutClosed(definition.id, popoutId, contextRef.current)
  }, [definition, popoutId])

  const returnToChat = useCallback(async (context: PanelContext) => {
    if (!definition) return
    try {
      let workspaceId = context.workspaceId ?? useWorkspacesStore.getState().activeWorkspaceId
      if (!workspaceId) {
        const workspaces = await queryClient.fetchQuery({
          queryKey: workspacesQueryKeys.list({ status: 'active' }),
          queryFn: () => fetchWorkspaces({ status: 'active' }),
          staleTime: 30_000,
        })
        workspaceId = (workspaces.find((workspace) => workspace.is_default) ?? workspaces[0])?.id ?? null
      }
      if (!workspaceId) {
        setExitError('Could not open chat. Try again.')
        return
      }
      leaveGateThen(useUiStore.getState().activePanel?.id ?? null, () => {
        const store = useUiStore.getState()
        ;(store.openPanel as (id: PanelId, context: PanelContext) => void)(definition.id, context)
        void navigate({
          to: '/workspaces/$workspaceId/chat',
          params: { workspaceId },
          search: isWorkspaceScopedPanel(definition.id) ? { panel: definition.id } : {},
          replace: true,
        }).then(() => {
          focusChatInputWhenReady(() => router.state.location.pathname === `/workspaces/${workspaceId}/chat`)
        }).catch((error: unknown) => {
          console.error('[side-panel] Could not return to workspace chat.', error)
          setExitError('Could not open chat. Try again.')
        })
      })
    } catch (error) {
      console.error('[side-panel] Could not resolve workspace chat.', error)
      setExitError('Could not open chat. Try again.')
    }
  }, [definition, navigate, queryClient, router])

  const requestClose = useCallback(() => {
    if (!definition) return
    setExitError(null)
    void (async () => {
      if (definition.beforeLeave) {
        try {
          if (!(await definition.beforeLeave())) return
        } catch (error) {
          console.error('[side-panel] Full-screen leave guard failed; close cancelled.', error)
          setExitError('Could not leave this panel. Try again.')
          return
        }
      }
      // Resolve the live selection after the leave guard accepts.
      const context = expandContextRef.current?.() ?? contextRef.current
      if (context === null) return
      contextRef.current = context
      announceClosed()
      if (isStandaloneWindow()) {
        // This is the chat's installed-app window, not a disposable tab.
        // Reuse the same guarded context-preserving re-dock path directly.
        void returnToChat(context)
        return
      }
      try {
        window.close()
      } catch (error) {
        console.error('[side-panel] Browser refused to close the tab.', error)
      }
      // A pasted URL has no script-opened tab to close. Read the browser's
      // actual closed flag, rather than guessing from a timeout.
      if (!window.closed) void returnToChat(context)
    })()
  }, [announceClosed, definition, returnToChat])

  useEffect(() => {
    if (!definition || initialContext === null) return undefined
    const identity = panelIdentityFromContext(definition.id, initialContext)
    if (!identity) return undefined
    const announcement = announcePanelTabPresence(identity, popoutId ?? undefined)
    announcementRef.current = announcement
    const onPageHide = () => {
      const context = expandContextRef.current?.() ?? contextRef.current
      if (popoutId && context !== null) announcePanelPopoutDeparture(definition.id, popoutId, context)
    }
    window.addEventListener('pagehide', onPageHide)
    return () => {
      window.removeEventListener('pagehide', onPageHide)
      if (announcementRef.current === announcement) announcementRef.current = null
      announcement.stop()
    }
  }, [definition, initialContext, popoutId])

  useEffect(() => {
    if (!definition?.beforeLeaveRequired) return undefined
    const promptBeforeUnload = (event: BeforeUnloadEvent) => {
      if (!definition.beforeLeaveRequired?.()) return
      event.preventDefault()
      event.returnValue = ''
    }
    window.addEventListener('beforeunload', promptBeforeUnload)
    return () => window.removeEventListener('beforeunload', promptBeforeUnload)
  }, [definition])

  useEffect(() => {
    if (!definition || initialContext === null) return undefined
    const onKeyDown = (event: KeyboardEvent) => {
      if (shouldClosePanelOnEscape(definition.id, event)) requestClose()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [definition, initialContext, requestClose])

  if (!definition || initialContext === null) return <InvalidPanel />

  const Content = definition.content
  return (
    <main data-testid="fullscreen-panel" className="flex h-dvh w-full flex-col overflow-hidden bg-[var(--color-surface-0)]">
      <div className="flex shrink-0 items-center gap-[var(--space-2)] self-start p-[var(--space-1)]">
        <Button variant="outline" size="sm" onClick={requestClose}>
          <ArrowLeft size={16} weight="bold" aria-hidden="true" />
          Back to chat
        </Button>
        {exitError && (
          <span role="alert" className="text-[length:var(--type-utility-xs-size)] text-[var(--color-error)]">
            {exitError}
          </span>
        )}
      </div>
      <div className="min-h-0 w-full flex-1">
        <Suspense
          fallback={
            <div role="status" className="flex h-full items-center justify-center gap-[var(--space-2)] text-[var(--color-muted)]">
              <SpinnerGap className="h-4 w-4 animate-spin" aria-hidden="true" />
              <span>Loading {definition.title}…</span>
            </div>
          }
        >
          <ErrorBoundary>
            <Content
              context={initialContext}
              presentation="fullscreen"
              close={requestClose}
              expand={() => {}}
              registerExpandContext={registerExpandContext}
              onWidthSettle={() => {}}
            />
          </ErrorBoundary>
        </Suspense>
      </div>
    </main>
  )
}
