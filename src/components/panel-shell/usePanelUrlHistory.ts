import { useEffect } from 'react'
import type { ActivePanel } from './types'

function projectedHref(activePanel: ActivePanel | null): string | null {
  const url = new URL(window.location.href)
  const panel = activePanel?.id === 'library' ? 'library' : undefined

  // Hash URLs belong to TanStack Router. Writing them directly caused the
  // shell's null mount state to erase a hard-load deep link before the chat
  // route could adopt it. usePanelDeepLink is the one production writer.
  if (url.hash.startsWith('#/')) return null

  // Production uses hash history. Supporting a plain chat URL keeps this
  // hook correct under memory/browser-history harnesses; the root fallback
  // is limited to tests so Storybook controls never acquire a panel query.
  if (import.meta.env.MODE !== 'test') return null
  if (panel) url.searchParams.set('panel', panel)
  else url.searchParams.delete('panel')
  return url.toString()
}

/**
 * Test-harness compatibility for legacy plain-URL shell tests. Production
 * hash routing is projected only through usePanelDeepLink/TanStack Router.
 */
export function usePanelUrlHistory(activePanel: ActivePanel | null): void {
  useEffect(() => {
    const href = projectedHref(activePanel)
    if (href !== null && href !== window.location.href) {
      window.history.replaceState(window.history.state, '', href)
    }
  }, [activePanel])

  useEffect(() => {
    const onPopState = () => {
      const href = projectedHref(activePanel)
      if (href !== null) window.history.replaceState(window.history.state, '', href)
    }
    window.addEventListener('popstate', onPopState)
    return () => window.removeEventListener('popstate', onPopState)
  }, [activePanel])
}
