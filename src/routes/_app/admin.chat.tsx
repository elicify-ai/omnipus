import { useCallback, useEffect, useRef, useState } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { createSession } from '@/lib/api'
import { useWorkspacesStore } from '@/store/workspacesStore'

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
      <div className="flex h-full flex-col items-center justify-center gap-3 px-4 text-center">
        <p className="text-sm font-medium text-[var(--color-secondary)]">Could not start Admin chat.</p>
        <p className="max-w-sm text-xs text-[var(--color-muted)]">{error}</p>
        <button tabIndex={0}
          type="button"
          onClick={() => {
            startedRef.current = false
            void start()
          }}
          className="text-xs text-[var(--color-accent)] underline underline-offset-2"
        >
          Try again
        </button>
      </div>
    )
  }

  return (
    <div className="flex h-full items-center justify-center" aria-live="polite">
      <p className="text-sm text-[var(--color-muted)]">Starting Admin chat…</p>
    </div>
  )
}

export const Route = createFileRoute('/_app/admin/chat')({
  component: AdminChatRoute,
})
