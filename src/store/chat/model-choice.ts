// Per-session, per-browser model-choice memory (#1436).
//
// The latest model the user explicitly picked in a session's composer is kept
// here so returning to the session — by navigation or by a full reload —
// restores it instead of re-seeding from the transcript or the agent default.
// The agent default applies only to a session with NO explicit choice, and an
// explicit choice of the default itself is stored like any other choice (the
// session keeps it until the user picks something else).
//
// Chief ruling 2026-10-24: this applies to EVERY chat — workspace chats and
// unfiled chats (no bound workspace) alike; the key is the session id alone.
//
// Same-browser only: this is a restore convenience keyed in localStorage, not
// a server-side preference. The wire already carries the model actually used
// per message (metadata.model_name); no contract change is involved here.
// not-wire-format: browser-local UI preference; never sent to the gateway.

import { useUiStore } from '@/store/ui'
import { logDiagnostic } from '@/lib/telemetry'

const STORAGE_KEY = 'omnipus.sessionModelChoice.v1'

/** An explicit user pick: the (provider, model) pair the row was chosen under.
 *  provider null = recorded from a pick with no provider context (rare — the
 *  chat picker is catalogue-constrained and always emits a pair). */
export interface SessionModelChoice { // not-wire-format: browser-local UI preference; never sent to the gateway
  model: string
  provider: string | null
}

type ChoiceMap = Record<string, SessionModelChoice>

export interface ModelChoiceStorageFailure { // not-wire-format: browser persistence outcome only
  operation: 'read' | 'write'
  reason: 'unavailable' | 'invalid'
  errorName: string
}

/** Shared by picker actions and background first-message receipts. */
export function warnModelChoiceStorageFailure(failure: ModelChoiceStorageFailure | null | undefined): void {
  if (!failure) return
  console.warn('[chat] model choice storage failed', failure)
  logDiagnostic('sessionModelChoiceStorageFailed', { ...failure })
  const ui = useUiStore.getState()
  const message = failure.operation === 'write'
    ? 'Could not save your model choice. It is kept in this page, but may be lost on reload. Check browser storage space and permissions.'
    : failure.reason === 'invalid'
      ? 'Saved model choices could not be read because browser storage is invalid. Choices made in this page are kept until reload. Select a model to save it again.'
      : 'Could not read saved model choices. Choices made in this page are kept until reload. Check browser storage permissions.'
  if (!ui.toasts.some((toast) => toast.testId === 'model-choice-storage-warning')) {
    ui.addToast({ message, variant: 'warning', testId: 'model-choice-storage-warning', duration: 0 })
  }
}

// Saved choices are cached for failed reads; unsaved choices also outrank stale
// storage during ordinary navigation. Healthy reads still see other tabs' writes.
const pageChoices = new Map<string, SessionModelChoice | null>()
const unsavedChoices = new Set<string>()

type ChoiceRead = { choices: ChoiceMap; failure?: ModelChoiceStorageFailure }

function storageFailure(operation: 'read' | 'write', reason: ModelChoiceStorageFailure['reason'], error: unknown): ModelChoiceStorageFailure {
  return { operation, reason, errorName: error instanceof Error ? error.name : 'UnknownError' }
}

function isChoice(value: unknown): value is SessionModelChoice {
  if (!value || typeof value !== 'object') return false
  const choice = value as Partial<SessionModelChoice>
  return typeof choice.model === 'string' && choice.model.trim() !== ''
    && (choice.provider === null || (typeof choice.provider === 'string' && choice.provider.trim() !== ''))
}

function readAll(): ChoiceRead {
  let raw: string | null
  try {
    raw = localStorage.getItem(STORAGE_KEY)
  } catch (error) {
    return { choices: {}, failure: storageFailure('read', 'unavailable', error) }
  }
  if (raw === null) return { choices: {} }
  try {
    const parsed: unknown = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed) || !Object.values(parsed).every(isChoice)) {
      throw new TypeError('Invalid saved model choices')
    }
    return { choices: parsed as ChoiceMap }
  } catch (error) {
    return { choices: {}, failure: storageFailure('read', 'invalid', error) }
  }
}

/** A missing choice and an unreadable preference are different outcomes.
 *  The current page's cache preserves choices if persistence fails. */
export function readSessionModelChoice(sessionId: string | null | undefined): {
  choice: SessionModelChoice | null
  failure?: ModelChoiceStorageFailure
} {
  if (!sessionId || sessionId === '__pending') return { choice: null }
  const loaded = readAll()
  const choice = loaded.failure || unsavedChoices.has(sessionId)
    ? pageChoices.get(sessionId) ?? null
    : Object.hasOwn(loaded.choices, sessionId) ? loaded.choices[sessionId] : null
  pageChoices.set(sessionId, choice)
  return { choice, failure: loaded.failure }
}

/** Cache before saving so a failed write cannot discard the choice on A→B→A.
 *  A new chat's choice is saved by its correlated first-message receipt. */
export function writeSessionModelChoice(
  sessionId: string | null | undefined,
  choice: SessionModelChoice | null,
): ModelChoiceStorageFailure | null {
  if (!sessionId || sessionId === '__pending') return null
  pageChoices.set(sessionId, choice)
  unsavedChoices.add(sessionId)
  const loaded = readAll()
  const all = { ...loaded.choices }
  for (const [id, cached] of pageChoices) {
    if (!loaded.failure && !unsavedChoices.has(id)) continue
    if (cached) all[id] = cached
    else delete all[id]
  }
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(all))
    unsavedChoices.clear()
    return loaded.failure ?? null
  } catch (error) {
    return storageFailure('write', 'unavailable', error)
  }
}
