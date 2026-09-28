import { PANEL_POLICIES } from './types'
import type { PanelId } from './types'

type WorkspaceSwitchDecision =
  | { action: 'stay' }
  | { action: 'follow'; workspaceId: string }
  | { action: 'cancel' }

export async function resolveWorkspaceSwitch(input: {
  panelId: PanelId
  openedWorkspaceId?: string
  nextWorkspaceId: string
  beforeLeave?: () => Promise<boolean>
}): Promise<WorkspaceSwitchDecision> {
  const follows = PANEL_POLICIES[input.panelId].switchFollows
  if (follows === 'never') {
    return { action: 'stay' }
  }

  if (follows === 'when-scoped' && input.openedWorkspaceId === undefined) {
    return { action: 'stay' }
  }

  if (input.beforeLeave && !(await input.beforeLeave())) {
    return { action: 'cancel' }
  }

  return { action: 'follow', workspaceId: input.nextWorkspaceId }
}
