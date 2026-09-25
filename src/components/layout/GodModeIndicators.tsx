import { Link } from '@tanstack/react-router'
import { ShieldWarning } from '@phosphor-icons/react'
import { Badge } from '@/components/ui/badge'
import { useSidebarStore, SIDEBAR_PIN_BREAKPOINT } from '@/store/sidebar'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { useGodModeLiveStatus } from '@/hooks/useGodModeLiveStatus'

/**
 * GodModeIndicators — the sidebar's God Mode signals (founder decision
 * 2026-09-25), replacing the app-wide GodModeActiveBanner that AppShell used
 * to mount on every screen (deleted outright — no shim, no deprecation
 * comment). Two indicators:
 *
 *   GodModeSidebarPill — a small red pill in the sidebar brand row, next to
 *     the omnipus.ai wordmark, while god-mode is live. Clicking it navigates
 *     to Settings → Gateway, where the GodModeControl switch lives (the pill
 *     itself never toggles anything — flipping god-mode keeps its step-up
 *     gate in GodModeControl). The catalogued Badge carries the styling:
 *     `error` variant when on, `warning` variant when the status is unknown.
 *
 *   GodModeSidebarDot — a small red dot on the sidebar-open hamburger
 *     (ScreenHeader / WorkspaceTabContainer) for the state where the pill is
 *     NOT on screen: sidebar hidden (unpinned and closed). The dot is
 *     decorative (aria-hidden); the mention lives in the button's accessible
 *     name, which callers extend via useGodModeSidebarDot() so a screen
 *     reader hears "Toggle navigation sidebar — God Mode is on".
 *
 * The unknown state keeps the deleted banner's safety property: when the app
 * cannot tell whether god-mode is on (god-mode fetch failed, bypass not
 * active), the pill renders the warning variant reading "God Mode ?" with
 * accessible name "God Mode status unknown" — never silence, which would
 * read exactly like "sandboxing is confirmed on". Under dev_mode_bypass the
 * pill renders nothing (the dedicated dev-mode banner in AppShell already
 * covers that mode), exactly as the banner did.
 */

// Mirrors Sidebar.tsx's effectivelyPinned/isVisible computation: the pin
// preference only takes effect at or above the pin breakpoint, so "visible"
// is pinned-and-wide OR overlay-open. Kept here (not exported from the
// sidebar store) because Sidebar.tsx owns the inline original and this only
// needs the two derived booleans.
function useSidebarLayout() {
  const isOpen = useSidebarStore((s) => s.isOpen)
  const isPinned = useSidebarStore((s) => s.isPinned)
  const canPin = useMediaQuery(`(min-width: ${SIDEBAR_PIN_BREAKPOINT}px)`)
  const effectivelyPinned = isPinned && canPin
  return { visible: effectivelyPinned || isOpen, effectivelyPinned }
}

/** True while god-mode is on AND the sidebar (and its pill) is off screen. */
export function useGodModeSidebarDot(): boolean {
  const status = useGodModeLiveStatus()
  const { visible } = useSidebarLayout()
  return status === 'on' && !visible
}

export function GodModeSidebarPill() {
  const status = useGodModeLiveStatus()
  const { effectivelyPinned } = useSidebarLayout()
  const close = useSidebarStore((s) => s.close)
  if (status === 'off') return null
  const unknown = status === 'unknown'
  return (
    <Link
      to="/settings"
      search={{ tab: 'gateway' }}
      data-testid={unknown ? 'sidebar-god-mode-unknown' : 'sidebar-god-mode-pill'}
      aria-label={unknown ? 'God Mode status unknown' : 'God Mode is on — open settings to turn it off'}
      // Same overlay-close discipline as every other nav affordance in the
      // sidebar: navigating from the overlay drawer closes it; a pinned
      // panel stays put.
      onClick={() => {
        if (!effectivelyPinned) close()
      }}
      className="shrink-0"
    >
      <Badge variant={unknown ? 'warning' : 'error'}>
        <ShieldWarning size={12} weight="fill" aria-hidden="true" />
        {unknown ? 'God Mode ?' : 'God Mode'}
      </Badge>
    </Link>
  )
}

export function GodModeSidebarDot() {
  const show = useGodModeSidebarDot()
  if (!show) return null
  // Size/offset follow the sidebar's existing status-dot precedent
  // (Sidebar.tsx's active-session dot: w-1.5 h-1.5) — a 6px dot, 4px off the
  // button's corner, inside the `relative` hamburger button. Decorative: the
  // God Mode mention lives in the hamburger's accessible name.
  return (
    <span
      data-testid="sidebar-god-mode-dot"
      aria-hidden="true"
      className="absolute right-1 top-1 h-1.5 w-1.5 rounded-full bg-[var(--color-error)]"
    />
  )
}
