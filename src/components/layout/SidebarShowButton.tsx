import { List } from '@phosphor-icons/react'
import { IconButton } from '@/components/ui/icon-button'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { SIDEBAR_PIN_BREAKPOINT, useSidebarStore } from '@/store/sidebar'

/**
 * The only header control for the sidebar: the hamburger. It is rendered
 * only while the sidebar is off screen — hidden while it is docked on a
 * wide window, and hidden while it is open over the page. Showing never
 * pins: a click calls open() (an overlay on any width); keeping the sidebar
 * open is the separate pin control inside the sidebar.
 */
export function SidebarShowButton({
  className,
  testId,
}: {
  className?: string
  testId?: string
}) {
  const isOpen = useSidebarStore((s) => s.isOpen)
  const isPinned = useSidebarStore((s) => s.isPinned)
  const open = useSidebarStore((s) => s.open)
  const canPin = useMediaQuery(`(min-width: ${SIDEBAR_PIN_BREAKPOINT}px)`)
  const visible = (isPinned && canPin) || isOpen
  if (visible) return null

  return (
    <IconButton
      id="sidebar-hamburger"
      type="button"
      variant="ghost"
      aria-label="Show sidebar"
      aria-expanded={false}
      data-testid={testId}
      onClick={open}
      className={className}
    >
      <List size={20} />
    </IconButton>
  )
}
