// SectionSaveScope — one Save bar for a section built from several
// components (Settings redesign #1519, FR-019). Providers holds the default
// model (ProvidersSection) and the Active transcriber (IntegrationsSection);
// both are drafts of the same section, so they share one Save bar, one
// password prompt and one leave prompt.
//
// Each component registers its part with useSectionSavePart. The scope keeps
// the latest part of every component and hands the Save bar thin wrappers
// that call the latest part when Save runs, so a Save never sends a value
// from an earlier render. Outside a scope (a component rendered alone) the
// component gets its own Save bar back from the hook.

import { createContext, useCallback, useContext, useEffect, useId, useMemo, useRef, useState } from 'react'
import { SaveBarPanel, SaveOutcomeLine } from './SaveBar'
import { useFormSaveBar, type FormSavePart } from './useFormSaveBar'
import type { FieldMessage } from './useSaveBar'

interface Registry { // not-wire-format: React context contract inside the SPA
  set: (key: string, part: FormSavePart) => void
  remove: (key: string) => void
  bump: () => void
}

// The registry never changes identity; the outcome lines change only after a Save.
const SectionSaveContext = createContext<Registry | null>(null)
const SectionMessagesContext = createContext<Record<string, FieldMessage | null>>({})

function summaryOf(p: FormSavePart): string {
  return JSON.stringify([p.id, p.count, p.invalidCount, p.restartLabels ?? [], p.passwordLabels ?? [], p.blocked?.() ?? false])
}

function wrap(key: string, latest: React.MutableRefObject<Map<string, FormSavePart>>, snapshot: FormSavePart): FormSavePart {
  const now = () => latest.current.get(key) ?? snapshot
  return {
    id: snapshot.id,
    count: snapshot.count,
    blocked: snapshot.blocked ? () => now().blocked?.() ?? false : undefined,
    invalidCount: snapshot.invalidCount,
    restartLabels: snapshot.restartLabels,
    passwordLabels: snapshot.passwordLabels,
    preSave: snapshot.preSave ? () => now().preSave?.() ?? Promise.resolve(true) : undefined,
    write: (ctx) => now().write(ctx),
    stopsSave: snapshot.stopsSave ? (o) => now().stopsSave?.(o) ?? false : undefined,
    check: snapshot.check
      ? () => {
          const c = now().check
          if (!c) throw new Error('the saved-state check is no longer available')
          return c()
        }
      : undefined,
    onOutcome: (o) => now().onOutcome?.(o),
    focusInvalid: () => now().focusInvalid?.(),
    discard: () => now().discard(),
  }
}

export function SectionSaveScope({ children }: { children: React.ReactNode }) {
  const latest = useRef(new Map<string, FormSavePart>())
  const [version, setVersion] = useState(0)
  const bump = useCallback(() => setVersion((v) => v + 1), [])

  // Rebuilt only when a registered part's summary changed (version).
  const parts = useMemo(
    () => [...latest.current.entries()].map(([key, p]) => wrap(key, latest, p)),
    // `version` is the change signal for the ref.
    [version],
  )
  const { bar, message } = useFormSaveBar(parts)

  const registry = useMemo<Registry>(
    () => ({
      set: (key, part) => latest.current.set(key, part),
      remove: (key) => {
        latest.current.delete(key)
        bump()
      },
      bump,
    }),
    [bump],
  )

  const messageList = parts.map((p) => [p.id, message(p.id)] as const)
  const messagesKey = JSON.stringify(messageList)
  const messages = useMemo(
    () => Object.fromEntries(JSON.parse(messagesKey) as [string, FieldMessage | null][]),
    [messagesKey],
  )

  return (
    <SectionSaveContext.Provider value={registry}>
      <SectionMessagesContext.Provider value={messages}>
        {children}
        <SaveBarPanel {...bar} />
      </SectionMessagesContext.Provider>
    </SectionSaveContext.Provider>
  )
}

/**
 * Registers this component's part with the section's Save bar. Returns the
 * part's outcome line, and — only when no SectionSaveScope is around — the
 * component's own Save bar to render.
 */
export function useSectionSavePart(part: FormSavePart): { outcome: React.ReactNode; ownBar: React.ReactNode } {
  const registry = useContext(SectionSaveContext)
  const messages = useContext(SectionMessagesContext)
  const key = useId()
  const summary = summaryOf(part)

  useEffect(() => {
    registry?.set(key, part)
  })
  useEffect(() => {
    registry?.bump()
  }, [registry, summary])
  useEffect(() => () => registry?.remove(key), [registry, key])

  const own = useFormSaveBar(registry ? [] : [part])
  const message = registry ? (messages[part.id] ?? null) : own.message(part.id)
  return {
    outcome: <SaveOutcomeLine message={message} />,
    ownBar: registry ? null : <SaveBarPanel {...own.bar} />,
  }
}
