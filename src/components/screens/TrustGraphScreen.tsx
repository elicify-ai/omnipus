import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { ArrowLeft, ShareNetwork } from '@phosphor-icons/react'
import { TrustGraph } from '@/components/agents/TrustGraph'
import { Button } from '@/components/ui/button'
import { fetchAgents } from '@/lib/api'

// Trust-graph screen (Spec-3 FR-6.2 / US-3). Reuses the shared ['agents'] query
// — no parallel fetch — and renders the read-only delegation map. Surfaces
// loading / error / empty states like the rest of the Agents area.
export function TrustGraphScreen() {
  const {
    data: agents = [],
    isLoading,
    isError,
    refetch,
  } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
  })

  return (
    <div className="absolute inset-0 overflow-y-auto pb-[env(safe-area-inset-bottom)]">
      <div className="mx-auto max-w-4xl px-4 py-6">
        {/* Header */}
        <div className="mb-2">
          <Link
            to="/agents"
            className="inline-flex items-center gap-1 text-xs text-[var(--color-muted)] transition-colors hover:text-[var(--color-accent)]"
          >
            <ArrowLeft size={12} weight="bold" /> Agents
          </Link>
        </div>
        <div className="mb-6 flex items-center gap-2">
          <ShareNetwork
            size={22}
            weight="bold"
            className="text-[var(--color-accent)]"
          />
          <div>
            <h1 className="font-headline text-2xl font-bold text-[var(--color-secondary)]">
              Trust Graph
            </h1>
            <p className="mt-0.5 text-sm text-[var(--color-muted)]">
              Who may delegate to whom, and in which modes (await / background /
              task).
            </p>
          </div>
        </div>

        {/* Content */}
        {isLoading ? (
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            {[1, 2, 3, 4].map((i) => (
              <div
                key={i}
                className="h-36 animate-pulse rounded-xl border border-[var(--color-border)] bg-[var(--color-surface-1)]"
              />
            ))}
          </div>
        ) : isError ? (
          <div className="flex flex-col items-center justify-center gap-3 py-16">
            <p className="text-sm text-[var(--color-muted)]">
              Could not load the trust graph.
            </p>
            <Button variant="outline" size="sm" onClick={() => refetch()}>
              Retry
            </Button>
          </div>
        ) : (
          <TrustGraph agents={agents} />
        )}
      </div>
    </div>
  )
}
