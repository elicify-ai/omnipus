import { useMemo } from 'react'
import { ArrowRight, Star, ShareNetwork, Stack } from '@phosphor-icons/react'
import type { Agent } from '@/lib/api'
import { cn } from '@/lib/utils'

// ── Trust-graph visualization (Spec-3 FR-6.2 / US-3 / NFR-7) ─────────────────
//
// Read-only delegation map. Each agent is a NODE; each entry in the agent's
// `delegation_policy.to` is an outgoing EDGE (A → B means "A may delegate to B").
// Edges are labeled with the policy's allowed `modes` (await / background / task)
// and, when present, the `depth` cap.
//
// HARD CONSTRAINT (NFR-7 / FR-6.2 / M-6): `accept_from` and `budget` are inert in
// v0.1.0 and MUST NOT be surfaced — rendering them would falsely imply an active
// authorization boundary. This component reads ONLY `to`, `modes`, and `depth`
// from `delegation_policy`. It never reads `accept_from` or `budget`.

type DelegationMode = 'await' | 'background' | 'task'

const MODE_LABEL: Record<DelegationMode, string> = {
  await: 'await',
  background: 'background',
  task: 'task',
}

// Per-mode chip accent. Forge Gold for the synchronous `await` (the primary
// delegation), cooler tones for the async modes — all on the dark surface.
const MODE_CHIP_CLASS: Record<DelegationMode, string> = {
  await:
    'border-[var(--color-accent)]/40 bg-[var(--color-accent)]/10 text-[var(--color-accent)]',
  background:
    'border-[var(--color-info,#5B8DEF)]/40 bg-[var(--color-info,#5B8DEF)]/10 text-[var(--color-info,#9DBEFF)]',
  task:
    'border-[var(--color-success)]/40 bg-[var(--color-success)]/10 text-[var(--color-success)]',
}

interface TrustGraphProps {
  agents: Agent[]
}

interface ResolvedEdge {
  /** The raw target id from `delegation_policy.to[].id`. */
  targetId: string
  /** The resolved target agent, if it exists in the roster. */
  target?: Agent
  /** Reference kind — local now, remote-a2a reserved. */
  kind: 'local' | 'remote-a2a'
}

interface NodeRow {
  agent: Agent
  edges: ResolvedEdge[]
  modes: DelegationMode[]
  /** Depth cap from the policy, when set (>= 0). */
  depth?: number
}

/**
 * Build the directed adjacency model from the roster. Reads ONLY `to`, `modes`,
 * and `depth` — `accept_from` and `budget` are intentionally never read.
 */
function buildGraph(agents: Agent[]): NodeRow[] {
  const byId = new Map(agents.map((a) => [a.id, a]))
  return agents.map((agent) => {
    const policy = agent.delegation_policy
    const to = policy?.to ?? []
    const edges: ResolvedEdge[] = to.map((ref) => ({
      targetId: ref.id,
      target: byId.get(ref.id),
      kind: ref.kind,
    }))
    const modes = (policy?.modes ?? []) as DelegationMode[]
    const depth =
      typeof policy?.depth === 'number' && policy.depth >= 0
        ? policy.depth
        : undefined
    return { agent, edges, modes, depth }
  })
}

function AgentChip({ agent, label }: { agent?: Agent; label: string }) {
  const initial = (agent?.name ?? label).charAt(0).toUpperCase()
  return (
    <span className="inline-flex items-center gap-1.5 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-2 py-1">
      <span
        className="flex h-4 w-4 shrink-0 items-center justify-center rounded-full text-[9px] font-bold text-[var(--color-secondary)]"
        style={{ backgroundColor: agent?.color ?? 'var(--color-surface-3)' }}
        aria-hidden="true"
      >
        {initial}
      </span>
      <span className="text-xs font-medium text-[var(--color-secondary)]">
        {agent?.name ?? label}
      </span>
      {agent?.default && (
        <Star
          size={10}
          weight="fill"
          className="text-[var(--color-accent)]"
          aria-label="Default agent"
        />
      )}
    </span>
  )
}

function ModeChips({ modes }: { modes: DelegationMode[] }) {
  if (modes.length === 0) {
    return (
      <span className="text-[10px] italic text-[var(--color-muted)]">
        no modes set
      </span>
    )
  }
  return (
    <span className="flex flex-wrap items-center gap-1">
      {modes.map((m) => (
        <span
          key={m}
          data-testid={`mode-chip-${m}`}
          className={cn(
            'rounded border px-1.5 py-0.5 text-[10px] font-mono lowercase',
            MODE_CHIP_CLASS[m] ??
              'border-[var(--color-border)] bg-[var(--color-surface-2)] text-[var(--color-muted)]',
          )}
        >
          {MODE_LABEL[m] ?? m}
        </span>
      ))}
    </span>
  )
}

function NodeCard({ row }: { row: NodeRow }) {
  const { agent, edges, modes, depth } = row
  const initial = agent.name.charAt(0).toUpperCase()
  return (
    <div
      data-testid={`trust-node-${agent.id}`}
      className="rounded-xl border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4"
    >
      {/* Node header: the source agent */}
      <div className="flex items-start gap-3">
        <div
          className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full text-sm font-bold text-[var(--color-secondary)]"
          style={{ backgroundColor: agent.color ?? 'var(--color-surface-3)' }}
          aria-hidden="true"
        >
          {initial}
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="truncate font-headline text-sm font-bold text-[var(--color-secondary)]">
              {agent.name}
            </span>
            {agent.default && (
              <Star
                size={12}
                weight="fill"
                className="shrink-0 text-[var(--color-accent)]"
                aria-label="Default agent"
              />
            )}
            <span className="rounded bg-[var(--color-surface-2)] px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-[var(--color-muted)]">
              {agent.type}
            </span>
          </div>
          {agent.description && (
            <p className="mt-0.5 line-clamp-1 text-xs text-[var(--color-muted)]">
              {agent.description}
            </p>
          )}
        </div>
        {typeof depth === 'number' && (
          <span
            data-testid={`trust-depth-${agent.id}`}
            className="inline-flex shrink-0 items-center gap-1 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-2 py-1 text-[10px] text-[var(--color-muted)]"
            title="Maximum delegation depth"
          >
            <Stack size={11} weight="bold" />
            depth {depth}
          </span>
        )}
      </div>

      {/* Outgoing edges */}
      <div className="mt-3 border-t border-[var(--color-border)]/60 pt-3">
        {edges.length === 0 ? (
          <p className="text-xs italic text-[var(--color-muted)]">
            No outgoing delegation — this agent may not delegate to anyone.
          </p>
        ) : (
          <ul className="flex flex-col gap-2">
            {edges.map((edge) => (
              <li
                key={`${agent.id}->${edge.targetId}`}
                data-testid={`trust-edge-${agent.id}-${edge.targetId}`}
                className="flex flex-wrap items-center gap-2"
              >
                <ArrowRight
                  size={13}
                  weight="bold"
                  className="shrink-0 text-[var(--color-accent)]"
                  aria-hidden="true"
                />
                <AgentChip agent={edge.target} label={edge.targetId} />
                {edge.kind === 'remote-a2a' && (
                  <span className="rounded border border-[var(--color-border)] px-1 py-0.5 text-[9px] uppercase text-[var(--color-muted)]">
                    remote
                  </span>
                )}
                {!edge.target && (
                  <span className="text-[10px] italic text-[var(--color-warning)]">
                    unknown target
                  </span>
                )}
                <span className="text-[10px] text-[var(--color-muted)]">via</span>
                <ModeChips modes={modes} />
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}

export function TrustGraph({ agents }: TrustGraphProps) {
  const rows = useMemo(() => buildGraph(agents), [agents])
  const totalEdges = useMemo(
    () => rows.reduce((sum, r) => sum + r.edges.length, 0),
    [rows],
  )

  if (agents.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center gap-3 py-16 text-center">
        <ShareNetwork
          size={48}
          weight="thin"
          className="text-[var(--color-border)]"
        />
        <p className="text-sm text-[var(--color-muted)]">
          No agents to graph yet.
        </p>
      </div>
    )
  }

  return (
    <div data-testid="trust-graph">
      <div className="mb-4 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-[var(--color-muted)]">
        <span className="inline-flex items-center gap-1.5">
          <ShareNetwork
            size={14}
            weight="bold"
            className="text-[var(--color-accent)]"
          />
          {agents.length} {agents.length === 1 ? 'agent' : 'agents'}
        </span>
        <span>
          {totalEdges} delegation {totalEdges === 1 ? 'edge' : 'edges'}
        </span>
        <span className="ml-auto inline-flex items-center gap-2">
          <span className="text-[10px] uppercase tracking-wide text-[var(--color-muted)]/70">
            modes
          </span>
          {(['await', 'background', 'task'] as DelegationMode[]).map((m) => (
            <span
              key={m}
              className={cn(
                'rounded border px-1.5 py-0.5 text-[10px] font-mono lowercase',
                MODE_CHIP_CLASS[m],
              )}
            >
              {m}
            </span>
          ))}
        </span>
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {rows.map((row) => (
          <NodeCard key={row.agent.id} row={row} />
        ))}
      </div>
    </div>
  )
}
