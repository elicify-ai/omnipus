import { Link } from '@tanstack/react-router'
import { ShieldWarning } from '@phosphor-icons/react'
import { Badge } from '@/components/ui/badge'
import { Tooltip } from '@/components/ui/tooltip'
import { useSidebarStore, SIDEBAR_PIN_BREAKPOINT } from '@/store/sidebar'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { useGodModeOn } from '@/hooks/useGodModeLiveStatus'

/**
 * GodModeIndicators — the app's God Mode signals (founder decision
 * 2026-09-25, revision 2), replacing the app-wide GodModeActiveBanner that
 * AppShell used to mount on every screen (deleted outright — no shim, no
 * deprecation comment). Two indicators, one rule:
 *
 *   RED when god-mode is on, INVISIBLE in every other state — off, still
 *   loading, fetch error, dev-mode bypass. There is no amber or "unknown"
 *   variant anywhere (founder ruling 2026-09-25, overriding the earlier
 *   amber suggestion); a god-mode fetch failure surfaces through the
 *   gateway-state fetch-error banner in AppShell, not through these.
 *
 *   GodModeSidebarPill — a small red pill in the sidebar brand row, next to
 *     the omnipus.ai wordmark, while god-mode is live. Clicking it navigates
 *     to Settings → Gateway at the God Mode control (the pill itself never
 *     toggles anything — flipping god-mode keeps its step-up gate in
 *     GodModeControl).
 *
 *   GodModeCornerDot — rendered ONCE from AppShell (inside <main>), so every
 *     route is covered — including Library, the live-browser view and admin
 *     chat, which have no sidebar-open button to carry a dot. It shows only
 *     while god-mode is on AND the sidebar (and its pill) is off screen: a
 *     small red dot in the top-left corner of the screen content. It is
 *     keyboard-focusable, and hovering or focusing it explains itself via
 *     the catalogued Tooltip; clicking it navigates to the God Mode control
 *     like the pill does.
 *
 * The per-button hamburger dots these replaced (ScreenHeader /
 * WorkspaceTabContainer) are deleted — no shims.
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

export function GodModeSidebarPill() {
  const on = useGodModeOn()
  const { effectivelyPinned } = useSidebarLayout()
  const close = useSidebarStore((s) => s.close)
  if (!on) return null
  return (
    <Link
      to="/settings"
      search={{ tab: 'gateway', focus: 'god-mode' }}
      data-testid="sidebar-god-mode-pill"
      aria-label="God Mode is on — open settings to turn it off"
      // Same overlay-close discipline as every other nav affordance in the
      // sidebar: navigating from the overlay drawer closes it; a pinned
      // panel stays put.
      onClick={() => {
        if (!effectivelyPinned) close()
      }}
      className="shrink-0"
    >
      <Badge variant="error">
        <ShieldWarning size={12} weight="fill" aria-hidden="true" />
        God Mode
      </Badge>
    </Link>
  )
}

export function GodModeCornerDot() {
  const on = useGodModeOn()
  const { visible } = useSidebarLayout()
  if (!on || visible) return null
  return (
    // Positioned wrapper (the Tooltip's own root span is inline-flex and
    // cannot carry the corner placement): pinned to the top-left corner of
    // <main> — the screen content — which is the screen's left edge exactly
    // when the sidebar is hidden.
    <span className="absolute left-0 top-0 z-40">
      <Tooltip
        interactive
        side="bottom"
        content="God Mode is on — open settings to turn it off"
      >
        {/* The Link is the sole tab stop (Tooltip interactive mode): a 24px
            hit area (= --target-pointer-minimum) with the 8px visual dot
            centered in it. Deliberately under the 44px touch minimum: this
            corner is occupied by the sidebar-open hamburger on every screen
            that has one, the dot renders later in the DOM, and — per the
            touch-target overlap rule (see SegmentedControlItem's comment) —
            the later sibling wins an overlap. A full-size region here would
            swallow taps meant for the hamburger, the primary control in
            this corner; a 24px region keeps the hamburger's bulk and icon
            center reachable. */}
        <Link
          to="/settings"
          search={{ tab: 'gateway', focus: 'god-mode' }}
          data-testid="god-mode-corner-dot"
          aria-label="God Mode is on — open settings to turn it off"
          className="flex h-6 w-6 items-center justify-center rounded-full"
        >
          <span
            aria-hidden="true"
            className="block h-2 w-2 rounded-full bg-[var(--color-error)]"
          />
        </Link>
      </Tooltip>
    </span>
  )
}
