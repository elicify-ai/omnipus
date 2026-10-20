/**
 * The clickable parent part of a child chat's breadcrumb (founder decision
 * 2026-10-20). Opens the parent through the same session-selection path the
 * sidebar and the search modal use (useSelectSession) — no navigation of its
 * own. Split from FeedKindLabel so the router-bound hook only mounts when a
 * parent was actually resolved.
 */
import { useQuery } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { fetchAgents, fetchWorkspaces, workspacesQueryKeys } from '@/lib/api'
import type { Session, Workspace } from '@/lib/api'
import { useSelectSession } from './useSelectSession'

export function ParentCrumbLink({ parent, label }: { parent: Session; label: string }) {
  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const { data: workspaces = [] } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'all' }),
    queryFn: () => fetchWorkspaces({ status: 'all' }),
    staleTime: 30_000,
  })
  const selectSession = useSelectSession({ agents, workspaces: workspaces as Workspace[], onClose: () => {} })
  return (
    <Button
      variant="link"
      className="inline h-auto p-0 align-baseline text-[length:var(--type-utility-xs-size)]"
      onClick={() => selectSession(parent)}
    >
      {label}
    </Button>
  )
}
