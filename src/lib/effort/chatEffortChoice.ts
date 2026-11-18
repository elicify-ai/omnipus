// chatEffortChoice.ts — the chat's effort outside React: saving it with the
// per-chat model choice (FR-055) and the web `/effort` command (FR-023,
// S-41: delivery client, handled in the browser, no server message, no
// agent write).

import { queryClient } from '@/lib/queryClient'
import { generateId } from '@/lib/constants'
import { PROVIDERS_CATALOG_QUERY_KEY } from '@/lib/providersCatalogQuery'
import type { Agent, Config, ProvidersCatalog } from '@/lib/api'
import { chatInheritedEffort, decideWebEffort } from '@/lib/effort/chatEffort'
import { findServedRow } from '@/lib/effort/servedReasoning'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { warnModelChoiceStorageFailure, writeSessionModelChoice } from '@/store/chat/model-choice'
import type { ChatMessage } from '@/store/chat/types'

/** Saves the session's current pair together with its effort (same lifetime as the model choice). */
export function saveChatChoice(sessionId: string | null | undefined): void {
  const { nextModel, nextModelProvider, nextEffort, nextEffortByCommand } = useChatStore.getState()
  if (!nextModel) return
  warnModelChoiceStorageFailure(
    writeSessionModelChoice(sessionId, {
      model: nextModel,
      provider: nextModelProvider,
      ...(nextEffort ? { effort: nextEffort, ...(nextEffortByCommand ? { effortByCommand: true } : {}) } : {}),
    }),
  )
}

/** The agent setting a Not set chat inherits, from the cached agents and configuration. */
export function cachedChatInheritedEffort(agentId: string | null): string {
  const agents = queryClient.getQueryData<Agent[]>(['agents']) ?? []
  const config = queryClient.getQueryData<Config>(['config'])
  const agent = agents.find((a) => a.id === agentId)
  return chatInheritedEffort(agent, config?.agents?.defaults?.default_model?.reasoning_effort)
}

function reply(appendMessage: (message: ChatMessage) => void, content: string): void {
  appendMessage({ id: generateId(), role: 'system', content, timestamp: new Date().toISOString(), status: 'done' })
}

/**
 * `/effort [level|on|off|default|reset]` typed in the web composer. Replies
 * locally; a valid value becomes this conversation's effort ("set for this
 * conversation", gold dot). Nothing goes to the server.
 */
export function runWebEffortCommand(argument: string, appendMessage: (message: ChatMessage) => void): void {
  const { nextModel, nextModelProvider, nextEffort, setNextEffort } = useChatStore.getState()
  const { activeAgentId, activeSessionId } = useSessionStore.getState()
  if (!nextModel) {
    reply(appendMessage, 'Pick a model for this chat first; effort belongs to a model.')
    return
  }
  const catalog = queryClient.getQueryData<ProvidersCatalog>(PROVIDERS_CATALOG_QUERY_KEY)
  if (!catalog) {
    reply(appendMessage, "This model's effort options are still loading. Try again in a moment.")
    return
  }
  const decision = decideWebEffort(
    {
      model: nextModel,
      row: findServedRow(catalog, nextModelProvider ?? '', nextModel),
      current: nextEffort,
      agentSetting: cachedChatInheritedEffort(activeAgentId),
    },
    argument,
  )
  if (decision.change) {
    setNextEffort(decision.change.value, decision.change.value !== '', 'user')
    saveChatChoice(activeSessionId)
  }
  reply(appendMessage, decision.reply)
}
