type PanelIdentity = {
  panelId: string
  workspaceId?: string
  sessionId?: string
  agentId?: string
}

type PanelWindowHandle = Pick<Window, 'closed' | 'focus'>

type MutableHandleRegistry = ReadonlyMap<string, PanelWindowHandle> & {
  delete?: (key: string) => boolean
  set?: (key: string, handle: PanelWindowHandle) => unknown
}

type PanelOpenOutcome =
  | { kind: 'focused' }
  | { kind: 'affordance' }
  | { kind: 'opened' }
  | { kind: 'blocked' }

const WORKSPACE_SCOPED_PANELS = new Set([
  'library',
  'mail',
  'tasks',
  'team',
  'calendar',
])

export function panelIdentityKey(identity: PanelIdentity): string {
  if (identity.panelId === 'browser') {
    return `browser:${identity.sessionId ?? ''}:${identity.agentId ?? ''}`
  }
  return `${identity.panelId}:${identity.workspaceId ?? 'app'}`
}

export function resolvePanelOpen(input: {
  identity: PanelIdentity
  handles: ReadonlyMap<string, PanelWindowHandle>
  presence: PanelIdentity[]
  open: () => Window | null
}): PanelOpenOutcome {
  const key = panelIdentityKey(input.identity)
  const registry = input.handles as MutableHandleRegistry
  const existing = input.handles.get(key)

  if (existing) {
    if (!existing.closed) {
      try {
        existing.focus()
      } catch {
        // Window focus is best-effort. Do not create a duplicate merely
        // because the browser declined to bring the existing tab forward.
      }
      return { kind: 'focused' }
    }
    registry.delete?.(key)
  }

  if (input.presence.some((identity) => panelIdentityKey(identity) === key)) {
    return { kind: 'affordance' }
  }

  let opened: Window | null
  try {
    opened = input.open()
  } catch {
    return { kind: 'blocked' }
  }
  if (!opened || opened.closed) return { kind: 'blocked' }

  registry.set?.(key, opened)
  return { kind: 'opened' }
}

export function acceptPresence(message: unknown): boolean {
  if (!isRecord(message)) return false

  const keys = Object.keys(message)
  if (keys.some((key) => !['panelId', 'workspaceId', 'sessionId', 'agentId'].includes(key))) {
    return false
  }

  const panelId = message.panelId
  if (typeof panelId !== 'string') return false

  if (panelId === 'browser') {
    return (
      keys.length === 3 &&
      isNonEmptyString(message.sessionId) &&
      isNonEmptyString(message.agentId)
    )
  }

  if (!WORKSPACE_SCOPED_PANELS.has(panelId)) return false
  if ('sessionId' in message || 'agentId' in message) return false
  return message.workspaceId === undefined || isNonEmptyString(message.workspaceId)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0
}
