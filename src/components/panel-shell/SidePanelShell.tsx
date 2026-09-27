// SidePanelShell.tsx — the shared side-panel shell (side-panel-shell-spec.md
// §8.1): a docked [chat | separator | panel] split measured on the shell's
// OWN row (MIN-001 — row width via ResizeObserver, not the window), the
// SP-25 phone takeover below 680px (overlay deleted; the panel takes the
// full row, the chat hides), the SP-17 width geometry with MAJ-009's
// transient re-clamp (the applied width is re-derived at render from
// (stored, geometry) — geometry changes never write back), SP-13 width
// memory read on open / written on settle / deleted on reset, CRIT-001's
// beforeLeave gate run BEFORE store/URL/content move, and SP-26's takeover
// affordances: header Back, the header X, and swipe-to-close.
//
// Wave 0 (this demo): exercised through Storybook stories only — no route
// wiring, no integration with the real app store (wave 1).

import { useEffect, useMemo, useRef, useState } from 'react'
import type { CSSProperties, KeyboardEvent as ReactKeyboardEvent, ReactNode } from 'react'
import { ArrowsOutSimple, ArrowLeft, X } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { IconButton } from '@/components/ui/icon-button'
import { ResizeSeparator } from '@/components/ui/resize-separator'
import { getDiscardConfirmDialogOpen } from '@/components/library/preview/unsavedGuard'
import { usePanelShell, usePanelShellHistory } from './usePanelShell'
import { useSwipeToClose } from './useSwipeToClose'
import { usePanelShellStore, PANEL_WIDTH_UNSET } from './panelShellStore'
import { PANEL_MIN_PX, clampPanelWidth, panelDefaultWidth, panelWidthCeiling, isPhoneTakeover } from './panelWidth'
import type { PanelDefinition } from './types'

export interface SidePanelShellProps {
  /** The registered panels (§8.1 registry — adding a panel is one entry). */
  panels: PanelDefinition[]
  /**
   * Width-memory user bucket (SP-13's per-user key). The demo passes a
   * fixed demo user; wave 1 passes the signed-in user's identity.
   */
  username: string
  /**
   * A pinned sidebar's width INSIDE this shell's row, if any. The demo's
   * row IS the story stage, so 0; wave 1 passes the real sidebar width.
   */
  sidebarWidth?: number
  /** The chat column — the shell's other half of the row. */
  chat: ReactNode
}

export function SidePanelShell({
  panels,
  username,
  sidebarWidth = 0,
  chat,
}: SidePanelShellProps) {
  const shell = usePanelShell(panels, username)
  const activePanel = shell.activePanel
  const storedWidth = usePanelShellStore((s) => s.panelWidth)
  const rowRef = useRef<HTMLDivElement>(null)
  const [rowWidth, setRowWidth] = useState(0)

  // MIN-001: geometry is measured on the shell's own row via
  // ResizeObserver — truthful in the real app AND inside a Storybook stage.
  // A window-driven re-clamp must never write back (MAJ-009), so the
  // observer only re-renders; the applied width re-derives below.
  useEffect(() => {
    const row = rowRef.current
    if (row === null) return
    const ro = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect.width ?? 0
      setRowWidth(Math.round(w))
    })
    ro.observe(row)
    return () => ro.disconnect()
  }, [])

  const takeover = rowWidth > 0 && isPhoneTakeover(rowWidth)
  usePanelShellHistory({
    enabled: takeover && activePanel !== null,
    guardThenClose: shell.guardThenClose,
  })

  // US-6/US-7 Escape closes the panel — EXCEPT the Browser panel (SP-19:
  // Escape never closes it; only the header X does) and while the Library
  // discard-confirm dialog is up (it owns its own Escape).
  const onShellKeyDown = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    if (e.key !== 'Escape') return
    if (getDiscardConfirmDialogOpen()) return
    if (activePanel?.id === 'browser') return
    shell.requestClose()
  }

  // SP-17 applied width: re-derived at render from (stored, geometry).
  const defaultWidth = useMemo(
    () => panelDefaultWidth(rowWidth, sidebarWidth),
    [rowWidth, sidebarWidth],
  )
  const ceiling = useMemo(
    () => panelWidthCeiling(rowWidth, sidebarWidth),
    [rowWidth, sidebarWidth],
  )
  const appliedWidth =
    storedWidth === PANEL_WIDTH_UNSET
      ? defaultWidth
      : clampPanelWidth(storedWidth, rowWidth, sidebarWidth)

  // Live width moves (drag + keyboard) go to a CSS custom property on the
  // row — bypassing React state so a 60fps drag never re-renders the panel
  // content (the real Library explorer is heavy). Updates ride
  // requestAnimationFrame (MIN-006): at most one style write per frame, no
  // visible jank on a long chat transcript. The React-rendered value is the
  // SETTLED width; commit writes the store, which re-renders.
  const livePxRef = useRef<number | null>(null)
  const liveRafRef = useRef<number | null>(null)
  const applyLiveWidth = (px: number) => {
    livePxRef.current = px
    if (liveRafRef.current !== null) return
    liveRafRef.current = requestAnimationFrame(() => {
      liveRafRef.current = null
      const pending = livePxRef.current
      if (pending !== null) {
        rowRef.current?.style.setProperty('--panel-width', `${Math.round(pending)}px`)
      }
    })
  }
  useEffect(
    () => () => {
      if (liveRafRef.current !== null) cancelAnimationFrame(liveRafRef.current)
    },
    [],
  )

  // SP-26 swipe-to-close: recognizer attached to the takeover panel root.
  const swipeRef = useSwipeToClose({
    enabled: takeover && activePanel !== null,
    onClose: shell.requestClose,
  })

  const def = activePanel === null ? undefined : panels.find((p) => p.id === activePanel.id)
  const panelOpen = activePanel !== null && def !== undefined

  // SP-12 expand with fail-visible popup-block handling.
  const [expandBlocked, setExpandBlocked] = useState(false)
  const handleExpand = () => {
    setExpandBlocked(false)
    if (!shell.requestExpand()) setExpandBlocked(true)
  }

  // Plain JSX builder (NOT a nested component — a nested component type
  // would remount the panel content on every shell render).
  const panelBody = (takeoverMode: boolean) => (
    <div
      ref={swipeRef}
      id={def === undefined ? undefined : `side-panel-${def.id}`}
      data-testid="side-panel"
      data-takeover={takeoverMode ? 'true' : undefined}
      className={cn(
        'flex h-full min-h-0 min-w-0 flex-1 flex-col overflow-hidden bg-[var(--color-surface-2)]',
        takeoverMode && 'absolute inset-y-0 left-0 w-full shadow-[var(--elevation-overlay)]',
      )}
    >
      {def !== undefined && activePanel !== null && (
        <>
          <div
            data-testid="side-panel-header"
            className="flex h-10 shrink-0 items-center gap-[var(--space-1)] border-b border-[var(--color-border)] px-[var(--space-2)]"
          >
            {takeoverMode && (
              <IconButton size="sm" aria-label="Back" data-testid="panel-back" onClick={() => shell.requestBack()}>
                <ArrowLeft weight="bold" className="h-4 w-4" />
              </IconButton>
            )}
            <span className="truncate text-[length:var(--type-body-compact-size)] font-medium">{def.title}</span>
            <span className="grow" />
            {expandBlocked && (
              <span data-testid="panel-expand-error" className="text-[length:var(--type-utility-xs-size)] text-[var(--color-error)]" role="status">
                Pop-up blocked — allow pop-ups to expand.
              </span>
            )}
            <IconButton
              size="sm"
              aria-label={`Expand ${def.title} panel`}
              data-testid="panel-expand"
              onClick={handleExpand}
            >
              <ArrowsOutSimple weight="bold" className="h-4 w-4" />
            </IconButton>
            <IconButton
              size="sm"
              aria-label={`Close ${def.title}`}
              data-testid="panel-close"
              onClick={() => shell.requestClose()}
            >
              <X weight="bold" className="h-4 w-4" />
            </IconButton>
          </div>
          <div className="min-h-0 flex-1 overflow-hidden">
            <def.content context={activePanel.context} close={shell.requestClose} expand={handleExpand} />
          </div>
        </>
      )}
    </div>
  )

  const rowStyle = {
    '--panel-width': `${Math.round(appliedWidth)}px`,
  } as CSSProperties

  return (
    <div
      ref={rowRef}
      data-testid="panel-shell-row"
      data-takeover={takeover ? 'true' : undefined}
      onKeyDown={onShellKeyDown}
      className="relative flex h-full w-full overflow-hidden bg-[var(--color-surface-1)]"
      style={rowStyle}
    >
      <div
        data-testid="chat-column"
        className={cn('flex h-full min-w-0 flex-1 flex-col', takeover && panelOpen && 'hidden')}
      >
        {chat}
      </div>

      {panelOpen && def !== undefined && activePanel !== null && (
        <>
          {takeover ? (
            panelBody(true)
          ) : (
            // Docked: the separator lives INSIDE the panel's width footprint
            // so the chat floor is exact — at the 680px boundary the chat is
            // precisely 360px and the panel (separator included) precisely
            // 320px, with no separator pixel borrowed from the chat.
            <div
              data-testid="side-panel-container"
              className="flex h-full min-h-0 shrink-0"
              style={{ width: 'var(--panel-width)' } as CSSProperties}
            >
              <ResizeSeparator
                label={`Resize ${def.title} panel`}
                value={appliedWidth}
                min={PANEL_MIN_PX}
                max={Math.max(PANEL_MIN_PX, Math.round(ceiling))}
                controls={`side-panel-${def.id}`}
                panelSide="right"
                testId="panel-resize-separator"
                onValueChange={(px, source) => {
                  applyLiveWidth(px)
                  // Keyboard moves preview through the store (a11y: aria-
                  // valuenow stays honest between settles); drags do not.
                  if (source !== 'drag') {
                    usePanelShellStore.getState().setPanelWidth(px)
                  }
                }}
                onCommit={(px) => shell.settleWidth(px)}
                onReset={shell.resetWidth}
              />
              {panelBody(false)}
            </div>
          )}
        </>
      )}
      </div>
  )
}

