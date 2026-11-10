// ComposerEffortChip.tsx — the chat composer's effort chip and its model
// switch transaction (effort-levels-spec-r10.md US-2, FR-019, FR-047,
// FR-052, FR-053, FR-055; S-07 to S-14, S-41, S-72, S-73, S-86).
//
// The composer's model button and this chip are one shared picker: a model
// pick goes through useEffortSwitch (map, note, or the blocked step), then the
// pair and its effort are committed together and saved with the per-chat
// model choice. Not set inherits the agent's setting ("Agent's setting: …").

import { useCallback, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { EffortControl } from '@/components/ui/model-effort-picker'
import { fetchConfig, type Agent } from '@/lib/api'
import { providersCatalogQueryOptions } from '@/lib/providersCatalogQuery'
import { chatInheritedEffort } from '@/lib/effort/chatEffort'
import { findServedRow } from '@/lib/effort/servedReasoning'
import { useEffortSwitch, type EffortPair, type EffortSwitch } from '@/lib/effort/useEffortSwitch'
import { useChatStore } from '@/store/chat'

interface ComposerEffortOptions {
  agent: Agent | undefined
  /** The composer's model and the provider row it shows. */
  model: string
  provider: string
  /** Commits the picked pair (model button bookkeeping, choice persistence). */
  commitPair: (pair: EffortPair) => void
}

export interface ComposerEffort {
  switcher: EffortSwitch
  inherit: string
  note: string | null
  setNote: (note: string | null) => void
}

/** The chat's effort state and its model-switch transaction. */
export function useComposerEffort({ agent, model, provider, commitPair }: ComposerEffortOptions): ComposerEffort {
  const nextEffort = useChatStore((s) => s.nextEffort)
  const nextEffortByCommand = useChatStore((s) => s.nextEffortByCommand)
  const setNextEffort = useChatStore((s) => s.setNextEffort)
  const catalogQuery = useQuery(providersCatalogQueryOptions())
  // The configuration is read only when the agent rides the default model (its effort is then inherited).
  const ridesDefault = agent !== undefined && (agent.model ?? '').trim() === ''
  const { data: config } = useQuery({ queryKey: ['config'], queryFn: fetchConfig, enabled: ridesDefault })
  const catalog = catalogQuery.data
  const inherit = chatInheritedEffort(agent, config?.agents?.defaults?.default_model?.reasoning_effort)
  const [note, setNote] = useState<string | null>(null)
  const lookup = useCallback((pair: EffortPair) => findServedRow(catalog, pair.provider, pair.model), [catalog])
  const onCommit = useCallback(
    (pair: EffortPair, effort: string, mappingNote: string | null) => {
      commitPair(pair)
      setNextEffort(effort, effort === nextEffort && nextEffortByCommand, 'user')
      setNote(mappingNote)
    },
    [commitPair, setNextEffort, nextEffort, nextEffortByCommand],
  )
  const value = useMemo(() => ({ provider, model, effort: nextEffort }), [provider, model, nextEffort])
  const switcher = useEffortSwitch({ value, inherit, lookup, onCommit, ready: catalogQuery.status === 'success' })
  return { switcher, inherit, note, setNote }
}

interface ComposerEffortChipProps {
  effort: ComposerEffort
  model: string
  provider: string
  disabled?: boolean
  tabIndex?: number
  /** Persists the chat's choice after a direct chip pick. */
  onSaved: () => void
}

/** The chip next to the composer's model button (data-testid effort-chip). */
export function ComposerEffortChip({ effort, model, provider, disabled = false, tabIndex, onSaved }: ComposerEffortChipProps) {
  const nextEffort = useChatStore((s) => s.nextEffort)
  const nextEffortByCommand = useChatStore((s) => s.nextEffortByCommand)
  const setNextEffort = useChatStore((s) => s.setNextEffort)
  if (!model && !effort.switcher.pending) return null
  return (
    <EffortControl
      scope="chat"
      value={{ provider, model, effort: nextEffort }}
      inherit={effort.inherit}
      setByCommand={nextEffortByCommand}
      pendingSwitch={effort.switcher.pending}
      onSwitchComplete={effort.switcher.completePending}
      onSwitchCancel={effort.switcher.cancelPending}
      cancelFocusTarget={() => document.querySelector<HTMLElement>('[data-testid="composer-model-selector"]')}
      note={effort.note}
      announcement={effort.switcher.announcement}
      onChange={(value) => {
        setNextEffort(value, false, 'user')
        effort.setNote(null)
        onSaved()
      }}
      disabled={disabled}
      tabIndex={tabIndex}
      data-testid="effort-chip"
      className="shrink-0 flex-nowrap"
    />
  )
}
