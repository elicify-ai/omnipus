import { queryClient } from '@/lib/queryClient'
import { Session, SessionDetail } from '@/lib/api/generated/schemas'
import type { components } from '@/lib/api/generated/openapi-types'
import { availableDetail, page } from '../../chat-history-paging/fixtures'

/** Server-boundary fixture: the same generated Session contract used by saved chat/list responses. */
export function seedCanonicalSession(
  id: string,
  agentId: string,
  workspaceId?: string,
  source: 'detail' | 'list' = 'detail',
) {
  const session = Session.parse({
    id, type: 'chat', agent_id: agentId, title: id, status: 'active',
    created_at: '2026-10-22T00:00:00Z', updated_at: '2026-10-22T00:00:00Z',
    channel: 'webchat', partitions: [],
    stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 },
    ...(workspaceId === undefined ? {} : { workspace_id: workspaceId }),
  })
  if (source === 'detail') {
    const detail = availableDetail(page([], { sessionId: id }), session)
    SessionDetail.parse(detail)
    queryClient.setQueryData<components['schemas']['SessionDetail']>(['session-detail', id], detail)
  } else {
    const previous = queryClient.getQueryData<components['schemas']['Session'][]>(['sessions']) ?? []
    queryClient.setQueryData<components['schemas']['Session'][]>(['sessions'], [
      ...previous.filter((entry) => entry.id !== id), session,
    ])
  }
  return session
}
