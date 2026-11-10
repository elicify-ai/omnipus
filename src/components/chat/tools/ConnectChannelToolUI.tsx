// ConnectChannelToolUI — the chat rows for a connect_channel call: the usual
// tool card plus ConnectChannelTool (the WhatsApp pairing QR, BDD-8.4). The
// live row is a makeAssistantToolUI registration (OmnipusRuntimeProvider);
// the completed-transcript row is rendered by HistoricalToolCallBlock through
// ConnectChannelReplay. Kept apart from ConnectChannelTool.tsx so the QR card
// itself carries no chat-store dependency.

import { makeAssistantToolUI, type MessagePartStatus, type ToolCallMessagePartStatus } from '@assistant-ui/react'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { GenericToolCall } from './GenericToolCall'
import { ConnectChannelTool, markConnectChannelLive, wasConnectChannelLive } from './ConnectChannelTool'

interface ConnectChannelArgs {
  id?: string
}

function channelIdOf(args: unknown): string {
  const id = (args as ConnectChannelArgs | undefined)?.id
  return typeof id === 'string' ? id : ''
}

/** The pairing card under a connect_channel row of the completed transcript. */
export function ConnectChannelReplay({ toolCallId, args, sessionId }: { toolCallId: string; args: unknown; sessionId: string }) {
  return (
    <ConnectChannelTool
      toolCallId={toolCallId}
      sessionId={sessionId}
      channelId={channelIdOf(args)}
      live={wasConnectChannelLive(toolCallId)}
    />
  )
}

/** The live part adds `requires-action` while approval is pending; the card shows it as running. */
function toMessagePartStatus(status: ToolCallMessagePartStatus): MessagePartStatus {
  return status.type === 'requires-action' ? { type: 'running' } : status
}

function ConnectChannelLiveBlock(props: {
  toolCallId: string
  args: unknown
  result: unknown
  status: ToolCallMessagePartStatus
  isError?: boolean
}) {
  markConnectChannelLive(props.toolCallId)
  const liveCall = useChatStore((s) => s.toolCalls[props.toolCallId])
  const activeSessionId = useSessionStore((s) => s.activeSessionId) ?? ''
  return (
    <>
      <GenericToolCall
        toolName="connect_channel"
        args={props.args}
        result={liveCall?.result ?? props.result}
        status={toMessagePartStatus(props.status)}
        isError={props.isError ?? liveCall?.status === 'error'}
        error={liveCall?.error}
        durationMs={liveCall?.duration_ms}
        sessionId={activeSessionId}
      />
      <ConnectChannelTool toolCallId={props.toolCallId} sessionId={activeSessionId} channelId={channelIdOf(props.args)} live />
    </>
  )
}

export const ConnectChannelToolUI = makeAssistantToolUI<ConnectChannelArgs, unknown>({
  toolName: 'connect_channel',
  render: ({ toolCallId, args, result, status, isError }) => (
    <ConnectChannelLiveBlock toolCallId={toolCallId} args={args} result={result} status={status} isError={isError} />
  ),
})
