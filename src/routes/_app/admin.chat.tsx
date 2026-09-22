import { useCallback, useEffect, useRef, useState } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { createSession } from '@/lib/api'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { Button } from '@/components/ui/button'

function AdminChatRoute() {
  const navigate = useNavigate()
  const startedRef = useRef(false)
  const [error, setError] = useState<string | null>(null)

  const start = useCallback(async () => {
    if (startedRef.current) return
    startedRef.current = true
    setError(null)

    // Admin is a standalone operator. Clear the prior workspace before the
    // session route attaches so this session is never remembered under it.
    useWorkspacesStore.getState().setActiveWorkspaceId(null)

    try {
      const session = await createSession('admin')
      await navigate({
        to: '/sessions/$sessionId',
        params: { sessionId: session.id },
        replace: true,
      })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'The server could not start Admin chat.')
    }
  }, [navigate])

  useEffect(() => {
    void start()
  }, [start])

  if (error) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-[var(--space-2-5)] px-[var(--space-3)] text-center">
        <p className="text-[length:var(--type-body-compact-size)] font-medium text-[var(--color-secondary)]">Could not start Admin chat.</p>
        <p className="max-w-sm text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">{error}</p>
        <Button
          type="button"
          variant="link"
          size="sm"
          onClick={() => {
            startedRef.current = false
            void start()
          }}
          className="text-[length:var(--type-utility-xs-size)]"
        >
          Try again
        </Button>
      </div>
    )
  }

  return (
    <div className="flex h-full items-center justify-center" aria-live="polite">
      <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">Starting Admin chat…</p>
    </div>
  )
}

export const Route = createFileRoute('/_app/admin/chat')({
  component: AdminChatRoute,
})
