// agentMutationRefresh — GitHub #215 (founder: joins WU-10). A newly created,
// changed or deleted agent must show everywhere at once — the Team list,
// the sidebar rows, the pickers — without navigating away. When an agent
// management tool call (create_agent, update_agent, delete_agent) resolves
// SUCCESSFULLY, the agents list and every cached workspace record (lists,
// details, delegation graphs — all under the 'workspaces' key prefix) are
// invalidated so their observers refetch. A failed call changes nothing and
// invalidates nothing; a read tool (list_agents, get_agent) is not a change.
import type { ToolCallResultFrame } from '@/lib/api/generated/asyncapi-types'
import { queryClient } from '@/lib/queryClient'

const AGENT_MUTATION_TOOLS = new Set(['create_agent', 'update_agent', 'delete_agent'])

export function refreshRosterAfterAgentMutation(frame: Pick<ToolCallResultFrame, 'tool' | 'status'>): void {
  if (!frame.tool || !AGENT_MUTATION_TOOLS.has(frame.tool)) return
  if (frame.status !== 'success') return
  void queryClient.invalidateQueries({ queryKey: ['agents'] })
  void queryClient.invalidateQueries({ queryKey: ['workspaces'] })
}
