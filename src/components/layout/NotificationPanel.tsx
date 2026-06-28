import { useNavigate } from '@tanstack/react-router'
import { Tray, Circle } from '@phosphor-icons/react'

import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { EmptyState } from '@/components/shared/ListStates'
import { useUiStore } from '@/store/ui'
import { useNotificationsStore, type NotifItem } from '@/store/notifications'
import { markNotificationRead, markAllNotificationsRead } from '@/lib/api'

// #264 — header notification center (right-side Sheet). Lists notifications
// newest-first; unread items are visually distinct (surface-2 background + a
// severity-colored dot). Clicking an item marks it read and navigates to its
// click-through target (session id → that session; else schedule id → Command Center).

const SEVERITY_COLOR: Record<NotifItem['severity'], string> = {
  info: 'var(--color-accent)',
  warning: 'var(--color-warning)',
  error: 'var(--color-error)',
}

function relativeTime(ms: number): string {
  const diff = Date.now() - ms
  if (diff < 0) return 'just now'
  const sec = Math.floor(diff / 1000)
  if (sec < 60) return 'just now'
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}m ago`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr}h ago`
  const day = Math.floor(hr / 24)
  if (day < 7) return `${day}d ago`
  return new Date(ms).toLocaleDateString()
}

export function NotificationPanel() {
  const open = useUiStore((s) => s.notificationPanelOpen)
  const closePanel = useUiStore((s) => s.closeNotificationPanel)
  const navigate = useNavigate()

  const order = useNotificationsStore((s) => s.order)
  const byId = useNotificationsStore((s) => s.byId)
  const storeMarkRead = useNotificationsStore((s) => s.markRead)
  const storeMarkAllRead = useNotificationsStore((s) => s.markAllRead)
  const unreadCount = useNotificationsStore((s) => s.unreadCount)

  const items = order.map((id) => byId[id]).filter((n): n is NotifItem => Boolean(n))

  function handleClick(item: NotifItem) {
    if (!item.read) {
      storeMarkRead(item.id)
      void markNotificationRead(item.id).catch(() => {
        // Best-effort: the optimistic local read state stays; a failed server
        // write reconciles on the next hydrate. No error UI clutter.
      })
    }
    closePanel()
    if (item.sessionId) {
      void navigate({ to: '/sessions/$sessionId', params: { sessionId: item.sessionId } })
    } else if (item.scheduleId) {
      void navigate({ to: '/command-center' })
    }
  }

  function handleMarkAllRead() {
    storeMarkAllRead()
    void markAllNotificationsRead().catch(() => {
      // Best-effort; local state already cleared.
    })
  }

  return (
    <Sheet open={open} onOpenChange={(v) => (v ? undefined : closePanel())}>
      <SheetContent
        side="right"
        className="sm:w-[360px] bg-[var(--color-surface-1)] border-[var(--color-border)] overflow-y-auto p-0"
      >
        <SheetHeader className="flex flex-row items-center justify-between gap-2 px-4 py-3 border-b border-[var(--color-border)] space-y-0">
          <SheetTitle className="font-headline text-base font-semibold text-[var(--color-secondary)]">
            Notifications
          </SheetTitle>
          {unreadCount > 0 && (
            <button
              type="button"
              onClick={handleMarkAllRead}
              className="text-xs text-[var(--color-accent)] hover:underline"
            >
              Mark all read
            </button>
          )}
        </SheetHeader>

        {items.length === 0 ? (
          <EmptyState icon={<Tray size={32} />} message="No notifications yet" />
        ) : (
          <ul className="divide-y divide-[var(--color-border)]">
            {items.map((item) => (
              <li key={item.id}>
                <button
                  type="button"
                  onClick={() => handleClick(item)}
                  className={`flex w-full items-start gap-2.5 px-4 py-3 text-left transition-colors hover:bg-[var(--color-surface-2)] ${
                    item.read ? '' : 'bg-[var(--color-surface-2)]'
                  }`}
                >
                  <Circle
                    size={10}
                    weight="fill"
                    className="mt-1.5 shrink-0"
                    style={{
                      color: item.read
                        ? 'var(--color-border)'
                        : SEVERITY_COLOR[item.severity],
                    }}
                  />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-baseline justify-between gap-2">
                      <span
                        className={`truncate text-sm ${
                          item.read
                            ? 'text-[var(--color-secondary)]'
                            : 'font-medium text-[var(--color-secondary)]'
                        }`}
                      >
                        {item.title}
                      </span>
                      <span className="shrink-0 text-[10px] text-[var(--color-muted)]">
                        {relativeTime(item.createdAtMs)}
                      </span>
                    </div>
                    {item.body && (
                      <p className="mt-0.5 line-clamp-2 text-xs text-[var(--color-muted)]">
                        {item.body}
                      </p>
                    )}
                  </div>
                </button>
              </li>
            ))}
          </ul>
        )}
      </SheetContent>
    </Sheet>
  )
}
