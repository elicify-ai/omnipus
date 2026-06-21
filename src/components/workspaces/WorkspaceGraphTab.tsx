import { Graph } from '@phosphor-icons/react'
import { WorkspaceTabEmptyState } from './WorkspaceTabEmptyState'

// ── F2 COMPONENT BOUNDARY ────────────────────────────────────────────────────
// This is the Graph (Task DAG) tab. The F2 wave replaces the body of this
// component with the real DAG view: tasks as nodes, `blocked_by` as dependency
// edges, left→right by order, live per-node status colour, pan/zoom.
//
// Contract for the F2 agent:
//   - Keep the export name `WorkspaceGraphTab` and the props shape below.
//   - The resolved workspace is available via `useActiveWorkspace()` from
//     WorkspaceTabContainer; tasks via `fetchTasks({ workspace_id })`.
//   - The route file (workspaces.$workspaceId.graph.tsx) is already wired.
// ─────────────────────────────────────────────────────────────────────────────

interface WorkspaceGraphTabProps {
  workspaceId: string
}

export function WorkspaceGraphTab(_props: WorkspaceGraphTabProps) {
  return (
    <WorkspaceTabEmptyState
      Icon={Graph}
      title="Task dependency graph"
      description="The marquee new view: every task as a node, blocked-by relationships as edges, laid out left to right with live status colour. Pan, zoom, and trace the critical path at a glance."
      bullets={[
        'Auto-layout DAG with clear edge routing',
        'Node = title + status chip + agent avatar',
        'Live status colour per node, pan / zoom',
      ]}
    />
  )
}
