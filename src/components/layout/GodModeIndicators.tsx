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
 *   amber suggestion). A god-mode fetch failure shows NOTHING anywhere —
 *   that is the accepted trade of the ruling, not an oversight; only an
 *   app-state fetch failure has its own banner in AppShell.
 *
 *   GodModeSidebarPill — a small red pill in the sidebar brand row, next to
 *     the omnipus.ai wordmark, while god-mode is live. Clicking it navigates
 *     to Settings → Gateway at the God Mode control (the pill itself never
 *     toggles anything — flipping god-mode keeps its step-up gate in
 *     GodModeControl).
 *
 *   GodModeCornerDot — rendered ONCE from the AppShell SHELL ROOT (not
 *     inside <main>), so every route is covered — including Library, the
 *     live-browser view and admin chat, which have no sidebar-open button
 *     to carry a dot — and phone-width takeover panels cannot clip or inert
 *     it. It shows only while god-mode is on AND the sidebar (and its pill)
 *     is off screen: a small red dot at the left edge of the screen, just
 *     below the 44px chrome-header band (clear of the sidebar-open
 *     hamburger's hit area — see the geometry note in the component). It is
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
      tabIndex={0}
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
    // Anchored wrapper (the Tooltip's own root span is inline-flex and
    // cannot carry the placement), mounted by AppShell at the SHELL ROOT —
    // see the call site in AppShell.tsx for why not inside <main>. Two
    // geometry decisions live here (review round 2, findings 3–4):
    //
    //  • BELOW the chrome-header band, not in the top-left corner. Every
    //    screen that has a sidebar-open hamburger fills the top-left 44px
    //    band with it (the workspace hamburger is flush at x=0, 44×44;
    //    ScreenHeader's spans x=8..48 at the same height) — no corner-
    //    anchored hit area of ANY size can avoid eating part of it, and the
    //    later sibling wins an overlap (touch-target rule, design-system
    //    skill §12). Anchoring one token below the band — the very token
    //    the hamburger rows take their height from (--spacing-chrome-header
    //    backs h-chrome-header) — makes the disjointness structural: the
    //    dot's hit area starts at y=44 where every hamburger ends.
    //  • z-40, above the docked takeover panels (static flex siblings), so
    //    the dot stays visible and clickable through phone-width takeovers.
    <span
      data-testid="god-mode-corner-dot-anchor"
      className="absolute left-0 top-[var(--spacing-chrome-header)] z-40"
    >
      <Tooltip
        interactive
        side="bottom"
        content="God Mode is on — open settings to turn it off"
      >
        {/* The Link is the sole tab stop (Tooltip interactive mode): a 24px
            hit area (= --target-pointer-minimum, WCAG 2.5.8) with the 8px
            visual dot centered in it. Below the chrome-header band nothing
            else owns this corner, so the full pointer minimum is safe —
            unlike the old top-left seat, where the same 24px stole the
            workspace hamburger's icon centre. */}
        <Link
          to="/settings"
          search={{ tab: 'gateway', focus: 'god-mode' }}
          tabIndex={0}
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
