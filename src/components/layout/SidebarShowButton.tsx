import { SidebarSimple } from '@phosphor-icons/react'
import { IconButton } from '@/components/ui/icon-button'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { SIDEBAR_PIN_BREAKPOINT, useSidebarStore } from '@/store/sidebar'

/**
 * The only header control for the sidebar. It is rendered only while the
 * sidebar is hidden. While the sidebar is on screen — docked on a wide
 * window, or open over the page on a narrow one — this button is absent.
 * The matching Hide control lives inside the sidebar.
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
  const pin = useSidebarStore((s) => s.pin)
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
      onClick={() => {
        if (canPin) pin()
        else open()
      }}
      className={className}
    >
      <SidebarSimple size={20} />
    </IconButton>
  )
}
