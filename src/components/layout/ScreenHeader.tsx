import { List } from '@phosphor-icons/react'
import { useSidebarStore } from '@/store/sidebar'

interface ScreenHeaderProps {
  title: string
  actions?: React.ReactNode
}

/**
 * ScreenHeader — slim top bar rendered at the top of each non-workspace screen.
 *
 * Hosts the hamburger (sidebar toggle) at the left, the screen title in the
 * centre-left, and an optional right-aligned actions slot for per-screen
 * controls (e.g. buttons, icon triggers).
 *
 * Height is locked to the shared chrome header token (`h-chrome-header` =
 * 44px) and has no bottom border so it lines up with the workspace top bar and
 * open panel headers (flat shell chrome).
 */
export function ScreenHeader({ title, actions }: ScreenHeaderProps) {
  const toggle = useSidebarStore((s) => s.toggle)

  return (
    <header
      role="banner"
      className="flex items-center gap-3 px-4 h-chrome-header min-h-chrome-header bg-[var(--color-surface-0)] flex-shrink-0"
    >
      {/* Hamburger — sidebar toggle (full chrome-row height for Fitts / AA) */}
      <button
        id="sidebar-hamburger"
        type="button"
        onClick={toggle}
        aria-label="Toggle navigation sidebar"
        className="flex items-center justify-center h-11 w-10 -ml-2 rounded-md text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors flex-shrink-0"
      >
        <List size={20} />
      </button>

      {/* Screen title */}
      <h2 className="flex-1 min-w-0 font-headline font-semibold text-sm text-[var(--color-secondary)] truncate">
        {title}
      </h2>

      {/* Optional right-aligned actions slot */}
      {actions && (
        <div className="flex items-center gap-2 flex-shrink-0" data-testid="screen-header-actions">
          {actions}
        </div>
      )}
    </header>
  )
}
