import { useEffect, useCallback, useRef, useState } from 'react'
import { Link, useLocation, useNavigate } from '@tanstack/react-router'
import { motion, AnimatePresence } from 'framer-motion'
import {
  ChatCircle,
  Gauge,
  Robot,
  PlugsConnected,
  PuzzlePiece,
  Gear,
  PushPin,
  PushPinSlash,
  SignOut,
} from '@phosphor-icons/react'
import { useSidebarStore, SIDEBAR_PIN_BREAKPOINT } from '@/store/sidebar'
import { useChatStore } from '@/store/chat'
import { useAuthStore } from '@/store/auth'
import { cn } from '@/lib/utils'
import avatarUrl from '@/assets/logo/omnipus-avatar.svg?url'

const NAV_ITEMS = [
  { to: '/', label: 'Chat', Icon: ChatCircle },
  { to: '/command-center', label: 'Command Center', Icon: Gauge },
  { to: '/agents', label: 'Agents', Icon: Robot },
  { to: '/channels', label: 'Channels', Icon: PlugsConnected },
  { to: '/skills', label: 'Skills & Tools', Icon: PuzzlePiece },
] as const

// US-5: Sidebar — overlay default, pin option, Framer Motion, Zustand
export function Sidebar() {
  const { isOpen, isPinned, close, toggle, togglePin } = useSidebarStore()
  const pendingCount = useChatStore((s) => s.pendingApprovals.length)
  const location = useLocation()
  const navigate = useNavigate()

  // Track whether the viewport is wide enough to allow pinning (≥1024px).
  const [canPin, setCanPin] = useState<boolean>(
    () => window.matchMedia(`(min-width: ${SIDEBAR_PIN_BREAKPOINT}px)`).matches
  )

  // The sidebar is effectively pinned only when the viewport allows it.
  // Declared early so all hooks below can reference it.
  const effectivelyPinned = isPinned && canPin
  const isVisible = effectivelyPinned || isOpen

  // Track when overlay sidebar was open so we can return focus to the hamburger on close
  const wasOverlayOpenRef = useRef(false)

  const handleLogout = useCallback(() => {
    useAuthStore.getState().clearAuth()
    navigate({ to: '/login' })
  }, [navigate])

  // US-5: Cmd+B / Ctrl+B keyboard shortcut + Escape to close
  const handleKeydown = useCallback(
    (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'b') {
        e.preventDefault()
        toggle()
      }
      // Escape closes the overlay sidebar; the pinned sidebar is never closed by Escape.
      // When viewport < 1024px pin is ignored, so treat isPinned as false there.
      if (e.key === 'Escape' && isOpen && !effectivelyPinned) {
        close()
      }
    },
    [toggle, close, isOpen, effectivelyPinned]
  )

  // Track when the viewport crosses the pin breakpoint.
  // We do NOT call unpin() here — the user's pin preference is preserved.
  // effectivelyPinned = isPinned && canPin already suppresses pinned behaviour
  // at narrow viewports, and the preference is automatically restored on widen.
  useEffect(() => {
    const mq = window.matchMedia(`(min-width: ${SIDEBAR_PIN_BREAKPOINT}px)`)
    function handleChange(e: MediaQueryListEvent) {
      setCanPin(e.matches)
    }
    mq.addEventListener('change', handleChange)
    return () => mq.removeEventListener('change', handleChange)
  }, [setCanPin])

  useEffect(() => {
    window.addEventListener('keydown', handleKeydown)
    return () => window.removeEventListener('keydown', handleKeydown)
  }, [handleKeydown])

  // Return focus to hamburger when overlay sidebar closes (Task 4)
  useEffect(() => {
    const overlayOpen = isOpen && !effectivelyPinned
    if (wasOverlayOpenRef.current && !overlayOpen) {
      const hamburger = document.getElementById('sidebar-hamburger') as HTMLButtonElement | null
      hamburger?.focus()
    }
    wasOverlayOpenRef.current = overlayOpen
  }, [isOpen, effectivelyPinned])

  // Sidebar content shared between pinned and overlay modes
  const sidebarContent = (
    <nav className="flex h-full flex-col" aria-label="Main navigation">
      {/* Brand mark */}
      <div className="flex items-center gap-3 px-4 py-5 border-b border-[var(--color-border)]">
        <img
          src={avatarUrl}
          alt="Omnipus"
          className="h-8 w-8 flex-shrink-0"
        />
        <span className="font-headline text-lg font-bold text-[var(--color-secondary)]">
          Omnipus
        </span>
      </div>

      {/* Primary nav */}
      <div className="flex-1 overflow-y-auto py-3">
        {NAV_ITEMS.map(({ to, label, Icon }) => {
          const isActive = location.pathname === to
          const badge = to === '/command-center' && pendingCount > 0 ? pendingCount : null
          return (
            <Link
              key={to}
              to={to}
              aria-label={badge !== null ? `${label} (${badge} pending)` : label}
              aria-current={isActive ? 'page' : undefined}
              onClick={() => {
                // US-5: overlay mode — close on nav item click
                if (!effectivelyPinned) close()
              }}
              className={cn(
                'flex items-center gap-3 px-4 py-2.5 mx-2 rounded-lg text-sm transition-colors',
                isActive
                  ? 'bg-[var(--color-surface-2)] text-[var(--color-accent)] font-medium'
                  : 'text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]'
              )}
            >
              <Icon
                size={18}
                weight={isActive ? 'fill' : 'regular'}
                className={isActive ? 'text-[var(--color-accent)]' : ''}
              />
              <span className="flex-1">{label}</span>
              {badge !== null && (
                <span className="flex items-center justify-center min-w-[18px] h-[18px] rounded-full bg-[var(--color-error)] text-white text-[10px] font-bold px-1" aria-hidden="true">
                  {badge > 99 ? '99+' : badge}
                </span>
              )}
            </Link>
          )
        })}
      </div>

      {/* Bottom: Settings + Pin toggle */}
      <div className="border-t border-[var(--color-border)] py-3">
        <Link
          to="/settings"
          aria-label="Settings"
          aria-current={location.pathname === '/settings' ? 'page' : undefined}
          onClick={() => { if (!effectivelyPinned) close() }}
          className={cn(
            'flex items-center gap-3 px-4 py-2.5 mx-2 rounded-lg text-sm transition-colors',
            location.pathname === '/settings'
              ? 'bg-[var(--color-surface-2)] text-[var(--color-accent)] font-medium'
              : 'text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]'
          )}
        >
          <Gear
            size={18}
            weight={location.pathname === '/settings' ? 'fill' : 'regular'}
          />
          Settings
        </Link>

        {/* Sign out */}
        <button
          onClick={handleLogout}
          aria-label="Sign out"
          className="flex items-center gap-3 px-4 py-2.5 mx-2 rounded-lg text-sm text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] transition-colors w-[calc(100%-16px)]"
        >
          <SignOut size={18} />
          Sign out
        </button>

        {/* Pin toggle — only shown when the viewport is wide enough to support it (≥1024px) */}
        {canPin && (
          <button
            onClick={togglePin}
            aria-label={isPinned ? 'Unpin sidebar' : 'Pin sidebar'}
            aria-pressed={isPinned}
            title={isPinned ? 'Unpin sidebar' : 'Pin sidebar'}
            className="flex items-center gap-3 px-4 py-2.5 mx-2 rounded-lg text-sm text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] transition-colors w-[calc(100%-16px)]"
          >
            {isPinned ? <PushPinSlash size={18} /> : <PushPin size={18} />}
            {isPinned ? 'Unpin sidebar' : 'Pin sidebar'}
          </button>
        )}
      </div>
    </nav>
  )

  return (
    <>
      {/* FR-015: Pinned mode — permanent panel, only rendered when ≥1024px AND user chose to pin */}
      {effectivelyPinned && (
        <aside
          className="flex flex-col h-full flex-shrink-0 bg-[var(--color-surface-1)] border-r border-[var(--color-border)]"
          style={{ width: 'var(--spacing-sidebar)' }}
          aria-label="Main navigation"
        >
          {sidebarContent}
        </aside>
      )}

      {/* US-5: Overlay mode — slides in from left, no backdrop dim */}
      <AnimatePresence>
        {isVisible && !effectivelyPinned && (
          <motion.aside
            initial={{ x: '-100%' }}
            animate={{ x: 0 }}
            exit={{ x: '-100%' }}
            transition={{ type: 'tween', duration: 0.22, ease: [0.4, 0, 0.2, 1] }}
            className="fixed left-0 top-0 z-40 flex h-full flex-col bg-[var(--color-surface-1)] border-r border-[var(--color-border)] shadow-2xl"
            style={{ width: 'var(--spacing-sidebar)' }}
            role="dialog"
            aria-modal="true"
            aria-label="Main navigation"
          >
            {sidebarContent}
          </motion.aside>
        )}
      </AnimatePresence>

      {/* US-5: Click-outside overlay dismiss — no background dimming */}
      <AnimatePresence>
        {isOpen && !effectivelyPinned && (
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.15 }}
            className="fixed inset-0 z-30"
            onClick={close}
            aria-hidden="true"
          />
        )}
      </AnimatePresence>
    </>
  )
}
