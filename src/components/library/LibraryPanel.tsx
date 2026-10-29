import { useCallback, useEffect, useRef, useState } from 'react'
import { useUiStore } from '@/store/ui'
import type {
  PanelContentProps,
  PanelContext,
  WorkspacePanelContext,
} from '@/components/panel-shell/types'
import { LibraryExplorer, type LibraryAddress } from './LibraryExplorer'

export interface LibraryPanelProps {
  shellProps?: PanelContentProps
}

function asWorkspaceContext(context: PanelContext | null): WorkspacePanelContext | null {
  return context !== null && context.sessionId === undefined ? context : null
}

function asLibraryAddress(context: WorkspacePanelContext): LibraryAddress {
  return {
    workspaceId: context.workspaceId,
    path: context.path,
  }
}

/**
 * The Library content shared by the docked shell and its chrome-less route.
 *
 * Both presentations drive the explorer in its ADDRESSED mode, so the docked
 * Library opens at the path it was given (a chat link, "Open in Library" on a
 * sent file or on a saved mail attachment) instead of the workspace root
 * (chat-links spec FR-007, BS-1.17). Docked, every `openPanel('library', …)`
 * re-addresses the explorer — `panelIntentRevision` is part of the key — so a
 * second click on the same link brings the user back to that file even after
 * they browsed elsewhere. The explorer's own navigation is reported back
 * through `onAddressChange` and stays local until the next supplied context.
 */
export function LibraryPanel({ shellProps }: LibraryPanelProps = {}) {
  const activePanel = useUiStore((state) => state.activePanel)
  const panelIntentRevision = useUiStore((state) => state.panelIntentRevision)
  const fullscreen = shellProps?.presentation === 'fullscreen'
  const suppliedContext = asWorkspaceContext(
    shellProps?.context ?? (activePanel?.id === 'library' ? activePanel.context : null),
  )
  const [currentContext, setCurrentContext] = useState<WorkspacePanelContext>(() =>
    suppliedContext ?? {},
  )
  const suppliedKey = suppliedContext
    ? JSON.stringify([fullscreen ? 0 : panelIntentRevision, suppliedContext.workspaceId, suppliedContext.path])
    : ''
  const lastSuppliedKeyRef = useRef(suppliedKey)

  useEffect(() => {
    if (!suppliedContext) return
    if (lastSuppliedKeyRef.current === suppliedKey) return
    lastSuppliedKeyRef.current = suppliedKey
    setCurrentContext(suppliedContext)
  }, [suppliedContext, suppliedKey])

  const getExpandContext = useCallback(() => currentContext, [currentContext])
  useEffect(() => {
    const register = shellProps?.registerExpandContext
    if (!register) return undefined
    register(getExpandContext)
    return () => register(null)
  }, [getExpandContext, shellProps?.registerExpandContext])

  if (!suppliedContext) return null
  if (shellProps?.presentation === 'docked' && activePanel?.id !== 'library') return null

  const updateAddress = (next: LibraryAddress) => setCurrentContext({
    workspaceId: next.workspaceId,
    path: next.path,
  })
  const explorer = fullscreen
    ? (
        <LibraryExplorer
          address={asLibraryAddress(currentContext)}
          onAddressChange={updateAddress}
          layout="split"
          className="h-full"
        />
      )
    : (
        <LibraryExplorer
          key={suppliedContext.workspaceId ?? 'root'}
          initialWorkspaceId={suppliedContext.workspaceId}
          address={asLibraryAddress(currentContext)}
          onAddressChange={updateAddress}
          onWorkspaceChange={(workspaceId) => {
            setCurrentContext((current) => ({
              ...current,
              workspaceId: workspaceId ?? undefined,
            }))
          }}
          onSelectionChange={({ path }) => {
            setCurrentContext((current) => ({
              ...current,
              path: path ?? undefined,
            }))
          }}
        />
      )
  const Root = shellProps ? 'div' : 'aside'

  return (
    <Root
      data-testid={shellProps?.presentation === 'fullscreen' ? 'library-panel-fullscreen' : 'library-panel-docked'}
      aria-label="Library panel"
      className="flex h-full min-h-0 w-full min-w-0 flex-col overflow-hidden bg-[var(--color-surface-0)]"
    >
      {explorer}
    </Root>
  )
}
