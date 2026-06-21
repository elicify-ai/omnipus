import { UsersThree } from '@phosphor-icons/react'
import { WorkspaceTabEmptyState } from './WorkspaceTabEmptyState'

// ── F2 COMPONENT BOUNDARY ────────────────────────────────────────────────────
// This is the Team tab — the per-workspace DELEGATION GRAPH editor. The F2 wave
// replaces the body of this component with the real graph:
//   - Nodes = agents on this workspace; [+ Add agent] adds a node (team
//     membership); removing a node removes the agent from the team.
//   - Edges = delegation: drag node→node to connect; click an edge → popover to
//     set modes (await/background/task) + depth.
//   - Click a node → open the existing AgentProfile slide-over (edits the global
//     agent definition; cue: "Editing Mia — applies everywhere she's used").
//   - Backed by the per-workspace delegation contract (Sprint 3 BE), keyed by
//     the workspace's core_team.
//
// Contract for the F2 agent:
//   - Keep the export name `WorkspaceTeamTab` and the props shape below.
//   - The resolved workspace is available via `useActiveWorkspace()` from
//     WorkspaceTabContainer; workspace.core_team is the seed roster.
//   - The route file (workspaces.$workspaceId.team.tsx) is already wired.
// ─────────────────────────────────────────────────────────────────────────────

interface WorkspaceTeamTabProps {
  workspaceId: string
}

export function WorkspaceTeamTab(_props: WorkspaceTeamTabProps) {
  return (
    <WorkspaceTabEmptyState
      Icon={UsersThree}
      title="Team & delegation graph"
      description="Manage this workspace's roster and how its agents delegate — one and the same action. Add an agent to drop a node; draw an edge to grant delegation; click an edge to tune modes and depth."
      bullets={[
        'Nodes = the agents on this workspace',
        'Edges = delegation (await / background / task + depth)',
        'Click a node to edit the global agent definition',
      ]}
    />
  )
}
