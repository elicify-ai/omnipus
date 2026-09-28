import { Suspense, useCallback, useEffect, useMemo, useRef } from 'react'
import { Link, createFileRoute, useNavigate } from '@tanstack/react-router'
import { SpinnerGap } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { ErrorBoundary } from '@/components/shared/ErrorBoundary'
import { getPanelDefinition } from '@/components/panel-shell/registry'
import type { PanelContext, PanelId } from '@/components/panel-shell/types'
import { announcePanelTabPresence, panelIdentityFromContext } from '@/lib/panelTabPresence'
import {
  announcePanelPopoutClosed,
  announcePanelPopoutContext,
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
  const definition = getPanelDefinition(panelId as PanelId)
  const initialContext = useMemo(
    () => definition?.fullScreen.fromSearch(search) ?? null,
    [definition, search],
  )
  const popoutId = typeof search.popout === 'string' && search.popout.length > 0 ? search.popout : null
  const contextRef = useRef<PanelContext | null>(initialContext)
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
    if (getter) reportContext(getter())
  }, [reportContext])

  const announceClosed = useCallback(() => {
    if (closedRef.current || !definition || !popoutId || contextRef.current === null) return
    closedRef.current = true
    announcePanelPopoutClosed(definition.id, popoutId, contextRef.current)
  }, [definition, popoutId])

  useEffect(() => {
    if (!definition || initialContext === null) return undefined
    const identity = panelIdentityFromContext(definition.id, initialContext)
    if (!identity) return undefined
    const announcement = announcePanelTabPresence(identity)
    announcementRef.current = announcement
    window.addEventListener('pagehide', announceClosed)
    return () => {
      window.removeEventListener('pagehide', announceClosed)
      if (announcementRef.current === announcement) announcementRef.current = null
      announcement.stop()
    }
  }, [announceClosed, definition, initialContext])

  if (!definition || initialContext === null) return <InvalidPanel />

  const Content = definition.content
  return (
    <main data-testid="fullscreen-panel" className="h-dvh w-full overflow-hidden bg-[var(--color-surface-0)]">
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
            close={() => {
              announceClosed()
              window.close()
              void navigate({ to: '/' })
            }}
            expand={() => {}}
            registerExpandContext={registerExpandContext}
            onWidthSettle={() => {}}
          />
        </ErrorBoundary>
      </Suspense>
    </main>
  )
}
