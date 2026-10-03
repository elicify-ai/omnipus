import { describe, expect, it } from 'vitest'
import { PANEL_POLICIES, WORKSPACE_SCOPED_PANELS } from './types'
import { panelWidthScope } from './panelWidthMemory'
import { resolveWorkspaceSwitch } from './workspaceSwitch'

describe('central panel policies', () => {
  it('derive workspace-scoped ids and width scope from one policy record', () => {
    expect(WORKSPACE_SCOPED_PANELS).toEqual(['library', 'mail', 'tasks', 'team', 'calendar'])
    expect(PANEL_POLICIES.browser.scope).toBe('session')
    expect(panelWidthScope('browser', { sessionId: 's1', agentId: 'a1' })).toBe('app')
    expect(panelWidthScope('library', { workspaceId: 'ws-1' })).toBe('ws-1')
  })

  it('derives workspace-switch following from the same policy record', async () => {
    expect(PANEL_POLICIES.calendar.switchFollows).toBe('always')
    await expect(
      resolveWorkspaceSwitch({
        panelId: 'calendar',
        openedWorkspaceId: 'ws-1',
        nextWorkspaceId: 'ws-2',
      }),
    ).resolves.toEqual({ action: 'follow', workspaceId: 'ws-2' })
  })
})
