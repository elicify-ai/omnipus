import { expect, type Page } from '@playwright/test'
import { SessionDetail, Workspace } from '../../../src/lib/api/generated/schemas'
import type { WorkspaceCreateRequest } from '../../../src/lib/api/generated/openapi-types'

/** CR-1: team membership makes the agent executable without filing the conversation. */
export async function provisionHansWorkspaceTeam(
  page: Page, baseURL: string, agentId: string, headers: Record<string, string>,
) {
  const response = await page.request.post(`${baseURL}/api/v1/workspaces`, {
    headers,
    data: { name: `Hans execution ${agentId}`, core_team: [agentId] } satisfies WorkspaceCreateRequest,
  })
  expect(response.ok(), `Hans workspace creation failed: ${response.status()} ${await response.text()}`).toBe(true)
  const workspace = Workspace.parse(await response.json())
  expect(workspace.core_team, 'Hans belongs to the real workspace team before the follow-up').toEqual([agentId])
}

export async function assertHansSessionUnfiled(page: Page, baseURL: string, sessionId: string) {
  const response = await page.request.get(`${baseURL}/api/v1/sessions/${sessionId}`)
  expect(response.ok(), `Hans session detail failed: ${response.status()} ${await response.text()}`).toBe(true)
  const detail = SessionDetail.parse(await response.json())
  expect(detail.session.id, 'check the same tested Hans conversation').toBe(sessionId)
  expect(detail.session, 'team membership must never file or rebind this unfiled conversation').not.toHaveProperty('workspace_id')
}
