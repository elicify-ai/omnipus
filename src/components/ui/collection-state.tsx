import { Fragment, useEffect, useRef, useState, type ReactNode } from 'react'

import { useLoadingVisibility } from '@/design-system/use-loading-visibility'
import { cn } from '@/lib/utils'

export type CollectionStatus = 'initial-loading' | 'refreshing' | 'empty' | 'partial' | 'ready' | 'error'

export interface CollectionStateProps {
  state: CollectionStatus
  loading: ReactNode
  empty: ReactNode
  error: ReactNode
  children?: ReactNode
  className?: string
}

const announcements: Record<CollectionStatus, string> = {
  'initial-loading': 'Loading',
  refreshing: 'Refreshing',
  empty: 'Empty',
  partial: 'Partially loaded',
  ready: 'Loaded',
  error: 'Unable to load',
}

export function CollectionState({ state, loading, empty, error, children, className }: CollectionStateProps) {
  const [announcement, setAnnouncement] = useState('')
  const pending = state === 'initial-loading' || state === 'refreshing'
  const loadingVisible = useLoadingVisibility(pending)
  const retainLoading = pending || loadingVisible
  const pendingPhase = state === 'initial-loading' || state === 'refreshing' ? state : null
  const lastCommittedPendingPhase = useRef<typeof pendingPhase>(null)

  useEffect(() => {
    if (pendingPhase !== null) lastCommittedPendingPhase.current = pendingPhase
    else if (!loadingVisible) lastCommittedPendingPhase.current = null
  }, [loadingVisible, pendingPhase])

  useEffect(() => setAnnouncement(announcements[state]), [state])

  const retainingInitialPlaceholder = !pending && loadingVisible && lastCommittedPendingPhase.current === 'initial-loading'

  const content = state === 'initial-loading' || retainingInitialPlaceholder
    ? null
    : state === 'empty'
      ? empty
      : state === 'error'
        ? error
        : children

  return (
    <Fragment>
      <span data-collection-announcement="" className="sr-only" role="status" aria-live="polite" aria-atomic="true">
        {announcement}
      </span>
      <div className={className} aria-busy={pending}>
      {content}
      {retainLoading ? (
        <div
          data-collection-loading=""
          data-collection-refresh={state === 'refreshing' ? '' : undefined}
          data-visible={loadingVisible}
          aria-hidden={loadingVisible ? undefined : true}
          inert={!loadingVisible}
          className={cn(
            'transition-opacity duration-150 motion-reduce:transition-none',
            loadingVisible ? 'opacity-100' : 'opacity-0',
          )}
        >
          {loading}
        </div>
      ) : null}
      </div>
    </Fragment>
  )
}
