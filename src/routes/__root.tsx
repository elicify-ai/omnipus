import { createRootRoute, Outlet, Link } from '@tanstack/react-router'
import { House } from '@phosphor-icons/react'
import OmnipusAvatar from '@/assets/logo/omnipus-avatar.svg?url'

// US-4: Branded 404 empty state with mascot and back-to-chat link
function NotFoundPage() {
  return (
    <div className="flex flex-col items-center justify-center h-full min-h-screen gap-6 p-8 text-center bg-[var(--color-primary)]">
      <img src={OmnipusAvatar} alt="omnipus.ai" className="h-20 w-20 opacity-50" />
      <div>
        <h1 className="font-headline text-5xl font-bold text-[var(--color-secondary)] mb-2">
          404
        </h1>
        <p className="text-[var(--color-muted)] text-lg">
          This page drifted into the deep.
        </p>
      </div>
      <Link
        to="/"
        tabIndex={0}
        className="inline-flex items-center gap-2 px-4 py-2 rounded-md bg-[var(--color-accent)] text-[var(--color-primary)] font-semibold text-sm hover:bg-[var(--color-accent-hover)] transition-colors"
      >
        <House size={16} />
        Back to Chat
      </Link>
    </div>
  )
}

export const Route = createRootRoute({
  component: () => <Outlet />,
  notFoundComponent: NotFoundPage,
})
