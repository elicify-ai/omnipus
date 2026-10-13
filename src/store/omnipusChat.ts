/**
 * Opening Omnipus's one chat (omnipus-system-agent FR-100, FR-045, FR-103;
 * BDD-4.1, BDD-5.2, BDD-12.1; ADR-20261011 OSA-10 U1, OSA-11).
 *
 * The dedicated attach path: Omnipus's main lives in the default workspace,
 * and opening it never switches the active workspace (today's
 * useSelectSession switches it for a foreign session, so it is not used
 * here). The attach is recorded for no workspace, so returning to the active
 * workspace restores that workspace's own conversation.
 *
 * A screen button ("Add agent", "Create with Omnipus", "Set up a team")
 * additionally sends one ordinary user message into the main with
 * `workflow_start` target data (K8): the workflow and the workspace the user
 * was looking at. The frame carries no `metadata.workspace_id` — see
 * sendMessage (outbound-lifecycle.ts).
 */
import type { Session, Workspace } from '@/lib/api'
import type { WorkflowStart } from '@/lib/api/generated/asyncapi-types'
import { OMNIPUS_AGENT_ID, omnipusMainSessionId } from '@/lib/omnipus'
import { logDiagnostic } from '@/lib/telemetry'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'

export type OmnipusWorkflowRequest = { // not-wire-format: what a screen button asks for; turned into the generated WorkflowStart below
  workflow: WorkflowStart['workflow']
  workspaceId: string
  workspaceName: string
}

export type OpenOmnipusOutcome = 'opened' | 'unavailable' | 'attach-failed' // not-wire-format: in-browser result of the entry click

type WorkflowSender = (content: string, workflowStart: WorkflowStart) => void // not-wire-format: in-browser callback into the chat store

// The chat store registers its send here (src/store/chat.ts), the same
// import-cycle break the session store uses: this module is imported by the
// sidebar, which must not pull the whole chat store in on its own.
let workflowSender: WorkflowSender | null = null

export function registerOmnipusWorkflowSender(send: WorkflowSender): void {
  workflowSender = send
}

/** The visible message text a workflow button sends (the server renders the fixed instruction from the target data, FR-012). */
export function workflowRequestText(request: OmnipusWorkflowRequest): string {
  return request.workflow === 'W18'
    ? `Set up a team for ${request.workspaceName}`
    : `Create an agent in ${request.workspaceName}`
}

/**
 * Attach Omnipus's main (and send a workflow start when asked). The caller
 * navigates to `/omnipus` on 'opened'. 'unavailable' means the main id does
 * not resolve — nothing is attached or sent, the caller shows Retry.
 */
export function openOmnipusChat(input: {
  workspaces: readonly Workspace[]
  sessions: readonly Session[]
  workflow?: OmnipusWorkflowRequest
}): OpenOmnipusOutcome {
  const mainId = omnipusMainSessionId(input.workspaces)
  if (!mainId) {
    logDiagnostic('omnipusEntryMainUnresolved', { workspaceCount: input.workspaces.length })
    return 'unavailable'
  }
  const sessionStore = useSessionStore.getState()
  if (sessionStore.activeSessionId !== mainId) {
    const session = input.sessions.find((candidate) => candidate.id === mainId)
    const attached = sessionStore.attachToSession(
      mainId,
      session?.type ?? 'main',
      session?.title ?? undefined,
      OMNIPUS_AGENT_ID,
      { recordForWorkspace: false },
    )
    if (!attached) {
      useUiStore.getState().addToast({
        message: "Connection lost — couldn't open the Omnipus chat. Try again once you are reconnected.",
        variant: 'error',
      })
      return 'attach-failed'
    }
    useSessionStore.getState().setActiveAgentType('core')
  }
  if (input.workflow) sendWorkflowStart(input.workflow)
  return 'opened'
}

function sendWorkflowStart(request: OmnipusWorkflowRequest): void {
  const { connection, isConnected } = useConnectionStore.getState()
  if (!connection || !isConnected) {
    // An offline send would be queued WITHOUT its target data; never send a
    // workflow message that has lost which workspace it is for.
    useUiStore.getState().addToast({
      message: `Not connected — "${workflowRequestText(request)}" was not sent. Press the button again once you are reconnected.`,
      variant: 'warning',
    })
    return
  }
  if (!workflowSender) {
    // Never silent: the chat store is not loaded, so the message cannot go out.
    logDiagnostic('omnipusWorkflowSenderMissing', { workflow: request.workflow })
    useUiStore.getState().addToast({
      message: `"${workflowRequestText(request)}" could not be sent. Type it in the Omnipus chat instead.`,
      variant: 'error',
    })
    return
  }
  workflowSender(workflowRequestText(request), { workflow: request.workflow, target_workspace_id: request.workspaceId })
}
