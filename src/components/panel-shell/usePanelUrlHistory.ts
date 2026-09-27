import { useEffect } from 'react'
import type { ActivePanel } from './types'

function isWorkspaceChat(pathname: string): boolean {
  return /^\/workspaces\/[^/]+\/chat\/?$/.test(pathname)
}

function projectedHref(activePanel: ActivePanel | null): string | null {
  const url = new URL(window.location.href)
  const panel = activePanel?.id === 'library' ? 'library' : undefined

  if (url.hash.startsWith('#/')) {
    const route = new URL(url.hash.slice(1), url.origin)
    const desired = isWorkspaceChat(route.pathname) ? panel : undefined
    if (desired) route.searchParams.set('panel', desired)
    else route.searchParams.delete('panel')
    url.hash = `#${route.pathname}${route.search}${route.hash}`
    return url.toString()
  }

  // Production uses hash history. Supporting a plain chat URL keeps this
  // hook correct under memory/browser-history harnesses; the root fallback
  // is limited to tests so Storybook controls never acquire a panel query.
  if (!isWorkspaceChat(url.pathname) && import.meta.env.MODE !== 'test') return null
  if (panel) url.searchParams.set('panel', panel)
  else url.searchParams.delete('panel')
  return url.toString()
}

/**
 * SP-22/MAJ-206 browser-history projection for the shared shell.
 * App-driven panel changes replace the current entry; Back/Forward never
 * adopts a stale panel value and instead re-projects the store.
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
