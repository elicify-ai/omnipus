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

/** The Library content shared by the docked shell and its chrome-less route. */
export function LibraryPanel({ shellProps }: LibraryPanelProps = {}) {
  const activePanel = useUiStore((state) => state.activePanel)
  const suppliedContext = asWorkspaceContext(
    shellProps?.context ?? (activePanel?.id === 'library' ? activePanel.context : null),
  )
  const [currentContext, setCurrentContext] = useState<WorkspacePanelContext>(() =>
    suppliedContext ?? {},
  )
  const suppliedKey = suppliedContext
    ? JSON.stringify([suppliedContext.workspaceId, suppliedContext.path, suppliedContext.folder])
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

  const updateAddress = (next: LibraryAddress) => setCurrentContext(next)
  const explorer = shellProps?.presentation === 'fullscreen'
    ? (
        <LibraryExplorer
          address={currentContext}
          onAddressChange={updateAddress}
          layout="split"
          className="h-full"
        />
      )
    : (
        <LibraryExplorer
          key={suppliedContext.workspaceId ?? 'root'}
          initialWorkspaceId={suppliedContext.workspaceId}
          onWorkspaceChange={(workspaceId) => {
            setCurrentContext((current) => ({
              ...current,
              workspaceId: workspaceId ?? undefined,
            }))
          }}
          onSelectionChange={({ path, folder }) => {
            setCurrentContext((current) => ({
              ...current,
              path: path ?? undefined,
              folder: path ? undefined : folder || undefined,
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
