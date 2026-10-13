// Test infrastructure only. Real UI/stores/transport; doubles at fetch/socket/output edges.
import { vi, expect } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { createElement, type ComponentType } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { render, cleanup, waitFor, screen } from '@testing-library/react'
import { queryClient } from '@/lib/queryClient'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useToolApprovalStore } from '@/store/toolApproval'
import { useAuthStore } from '@/store/auth'
import { isForceLoggingOut } from '@/lib/authLogout'
import type { StoreApi, UseBoundStore } from 'zustand'
import type { SecretRequestFrame, SecretRequestResolvedFrame, SessionStateSecretRequest } from '@/lib/api/generated/asyncapi-types'
import type { SecretRequestState, SecretSubmitRequest, SecretNameEntry, SecretOptionsUpdateRequest, ToolApprovalActionRequest } from '@/lib/api/generated/openapi-types'

export interface CapturedRequest { // not-wire-format: observation of browser fetch arguments
  path: string
  method: string
  body: string | undefined
  credentials: RequestCredentials | undefined
  headers: Headers
}
export const requests: CapturedRequest[] = []
export const socketSends: string[] = []
export function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}
export function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
export function installNetwork(handler: (call: CapturedRequest) => Response | Promise<Response>) {
  requests.length = 0
  socketSends.length = 0
  document.cookie = 'csrf=l1-red-csrf; path=/'
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const raw = input instanceof Request ? input.url : String(input)
    const call: CapturedRequest = {
      path: new URL(raw, 'http://localhost').pathname,
      method: init?.method ?? 'GET',
      body: typeof init?.body === 'string' ? init.body : undefined,
      credentials: init?.credentials,
      headers: new Headers(init?.headers),
    }
    requests.push(call)
    return await handler(call)
  }))
  class BoundarySocket {
    static OPEN = 1
    static CLOSED = 3
    readyState = 1
    onopen: ((event: Event) => void) | null = null
    constructor(_url: string) { queueMicrotask(() => this.onopen?.(new Event('open'))) }
    send(data: string) { socketSends.push(data) }
    close() { this.readyState = 3 }
    addEventListener() {}
    removeEventListener() {}
  }
  vi.stubGlobal('WebSocket', BoundarySocket)
}

// import.meta.glob is deliberate: absent implementation yields named fatal tests,
// not a module-resolution/collection error. It never substitutes a fake component.
const optionalModules = import.meta.glob<Record<string, unknown>>([
  '/src/components/chat/SecretRequestCard.tsx',
  '/src/components/agents/AgentPrivateSecrets.tsx',
  '/src/store/secretRequests.ts',
  '/src/lib/api/secrets.ts',
  '/src/components/agents/ToolApprovalPanel.tsx',
])
async function requireModule(path: string, spec: string) {
  expect(Object.keys(optionalModules), `BLOCKED: ${path} not implemented — required by ${spec}`).toContain(path)
  return await optionalModules[path]()
}
export interface SecretEntry { // not-wire-format: public component-local entry shape
  request: SessionStateSecretRequest
  localPhase?: 'submitting' | 'unknown' | 'rejected'
  localMessage?: string
  checkRequired?: boolean
  dismissed?: boolean
}
interface SecretStorePort { // not-wire-format: public Zustand actions extracted before implementation runs
  byId: Record<string, SecretEntry>
  upsertFromFrame(frame: SecretRequestFrame): void
  markResolved(frame: SecretRequestResolvedFrame): void
  applyState(state: SecretRequestState): void
  reconcileSession(sessionId: string, entries: readonly SessionStateSecretRequest[]): void
  reset(): void
}
export async function loadSecretSurface() {
  const card = await requireModule('/src/components/chat/SecretRequestCard.tsx', 'WU-4 / FR-061, FR-075')
  const state = await requireModule('/src/store/secretRequests.ts', 'K1–K4 / FR-079')
  expect(typeof card.SecretRequestCard).toBe('function')
  expect(typeof card.SecretRequestThreadTail).toBe('function')
  expect(typeof state.useSecretRequestStore).toBe('function')
  return {
    Card: card.SecretRequestCard as ComponentType<{ entry: SecretEntry }>,
    Tail: card.SecretRequestThreadTail as ComponentType,
    store: state.useSecretRequestStore as UseBoundStore<StoreApi<SecretStorePort>>,
  }
}
export async function loadPrivateSecrets() {
  const mod = await requireModule('/src/components/agents/AgentPrivateSecrets.tsx', 'WU-5 / FR-241 / §6.3')
  expect(typeof mod.AgentPrivateSecrets).toBe('function')
  return mod.AgentPrivateSecrets as ComponentType<{ agentId: string }>
}
export async function loadSecretApi() {
  const mod = await requireModule('/src/lib/api/secrets.ts', 'K5, K6, K49 / FR-062, FR-079')
  return mod as unknown as {
    submitSecretRequest(id: string, body: SecretSubmitRequest): Promise<{ kind: 'accepted' | 'not-pending'; state: SecretRequestState }>
    getSecretRequest(id: string): Promise<SecretRequestState>
    listSecrets(): Promise<SecretNameEntry[]>
    updateSecretOptions(reference: string, body: SecretOptionsUpdateRequest): Promise<SecretNameEntry>
  }
}
export async function loadApprovalPanel() {
  const mod = await requireModule('/src/components/agents/ToolApprovalPanel.tsx', 'L1 amendment §1.2 R6 / §1.5')
  expect(typeof mod.ToolApprovalPanel, 'BLOCKED: ToolApprovalPanel public export').toBe('function')
  return mod.ToolApprovalPanel as ComponentType
}
export async function loadApprovalThreadTail() {
  const mod = await requireModule('/src/components/agents/ToolApprovalPanel.tsx', 'L1 amendment §1.2 R6 dock above composer')
  expect(typeof mod.ToolApprovalThreadTail, 'BLOCKED: ToolApprovalThreadTail public export').toBe('function')
  return mod.ToolApprovalThreadTail as ComponentType
}
export function renderWithClient(ui: Parameters<typeof render>[0]) {
  return render(createElement(QueryClientProvider, { client: queryClient }, ui))
}
export function renderReal(Component: ComponentType, props?: Record<string, unknown>) {
  return render(createElement(QueryClientProvider, { client: queryClient }, createElement(Component, props)))
}
export function resetSharedState(sessionId = 'sess-001') {
  queryClient.clear()
  useUiStore.setState({ toasts: [] })
  useSessionStore.setState({ activeSessionId: sessionId, activeAgentId: 'agent-main', workspaceEntry: null })
  useWorkspacesStore.setState({ activeWorkspaceId: null })
  useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
  useAuthStore.getState().setUsername('l1-red-user')
  window.location.hash = ''
}
export function toasts() {
  return useUiStore.getState().toasts.map(({ id: _id, ...toast }) => toast)
}
export function approvalPosts() {
  return requests.filter(c => c.method === 'POST' && c.path.startsWith('/api/v1/tool-approvals/'))
    .map(c => ({ id: decodeURIComponent(c.path.split('/').at(-1)!), body: JSON.parse(c.body!) as unknown }))
}
let approvalReply: unknown | undefined
let approvalStatus = 200
let approvalOffline = false
export function setApprovalReply(body: unknown, status = 200) {
  approvalReply = body
  approvalStatus = status
  approvalOffline = false
}
export function setApprovalOffline() { approvalOffline = true }
export function installApprovalNetwork() {
  approvalReply = undefined
  approvalStatus = 200
  approvalOffline = false
  installNetwork(call => {
    if (call.path.startsWith('/api/v1/tool-approvals/') && call.method === 'POST') {
      if (approvalOffline) return Promise.reject(new TypeError('network disconnected'))
      const id = decodeURIComponent(call.path.split('/').at(-1)!)
      const action = (JSON.parse(call.body!) as ToolApprovalActionRequest).action
      return jsonResponse(approvalReply ?? { approval_id: id, action, status: 'ok', grant_recorded: true }, approvalStatus)
    }
    if (call.method === 'GET' && ['/api/v1/workspaces', '/api/v1/agents', '/api/v1/sessions'].includes(call.path)) return jsonResponse([])
    throw new Error(`Unexpected network boundary: ${call.method} ${call.path}`)
  })
}
function fieldLabel(label: string): RegExp {
  const escaped = label.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  return new RegExp(`^${escaped}(?: \\*)?$`)
}
export function labelledControl(label: string) {
  const control = screen.getByLabelText(fieldLabel(label))
  expect(control).toHaveAccessibleName(label)
  return control
}
export function queryLabelledControl(label: string) {
  return screen.queryByLabelText(fieldLabel(label))
}
export function observableText(value: unknown): string {
  const seen = new WeakSet<object>()
  return JSON.stringify(value, (_key, item: unknown) => {
    if (item !== null && typeof item === 'object') {
      // Real connection stores contain clock handles with cycles. Every object
      // is inspected at its first occurrence; only the repeated edge is marked.
      if (seen.has(item)) return '[already inspected]'
      seen.add(item)
    }
    if (item instanceof Map) return Array.from(item.entries())
    if (item instanceof Set) return Array.from(item.values())
    return item instanceof Error
      ? { ...item, name: item.name, message: item.message, stack: item.stack, cause: item.cause }
      : item
  }) ?? ''
}
export function containsSentinel(value: unknown, sentinel: string): boolean {
  return observableText(value).includes(sentinel)
}
export async function cleanupApprovalState() {
  cleanup()
  for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id)
  queryClient.clear()
  // Real forced-logout has a documented two-second debounce. Wait for its
  // actual flag transition between tests, rather than mocking the helper.
  if (isForceLoggingOut()) await waitFor(() => expect(isForceLoggingOut()).toBe(false))
  vi.unstubAllGlobals()
  vi.useRealTimers()
}
