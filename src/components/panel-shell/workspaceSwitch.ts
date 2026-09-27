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
  if (input.panelId === 'browser' || input.panelId === 'team') {
    return { action: 'stay' }
  }

  if (input.panelId === 'library' && input.openedWorkspaceId === undefined) {
    return { action: 'stay' }
  }

  if (input.beforeLeave && !(await input.beforeLeave())) {
    return { action: 'cancel' }
  }

  return { action: 'follow', workspaceId: input.nextWorkspaceId }
}
