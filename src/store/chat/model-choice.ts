// Per-session, per-browser model-choice memory (#1436).
//
// The latest model the user explicitly picked in a session's composer is kept
// here so returning to the session — by navigation or by a full reload —
// restores it instead of re-seeding from the transcript or the agent default.
// The agent default applies only to a session with NO explicit choice, and an
// explicit choice of the default itself is stored like any other choice (the
// session keeps it until the user picks something else).
//
// Scope: workspace chat sessions only. The chat composer always runs inside a
// bound workspace; the unfiled inline chat path (no active workspace) keeps
// the historical in-memory behaviour — its pick lives in nextModel for the
// page life and a reload re-seeds from the transcript/agent default.
//
// Same-browser only: this is a restore convenience keyed in localStorage, not
// a server-side preference. The wire already carries the model actually used
// per message (metadata.model_name); no contract change is involved here.
// not-wire-format: browser-local UI preference; never sent to the gateway.
import { useWorkspacesStore } from '@/store/workspacesStore'

const STORAGE_KEY = 'omnipus.sessionModelChoice.v1'

/** An explicit user pick: the (provider, model) pair the row was chosen under.
 *  provider null = recorded from a pick with no provider context (rare — the
 *  chat picker is catalogue-constrained and always emits a pair). */
export interface SessionModelChoice { // not-wire-format: browser-local UI preference; never sent to the gateway
  model: string
  provider: string | null
}

type ChoiceMap = Record<string, SessionModelChoice>

function readAll(): ChoiceMap {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return {}
    const parsed: unknown = JSON.parse(raw)
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as ChoiceMap
    }
    return {}
  } catch {
    // Corrupt JSON or storage unavailable (private mode, quota) — degrade to
    // "no session has an explicit choice"; the picker falls back to today's
    // transcript/agent-default seeding. Never crash the composer over a
    // restore convenience.
    return {}
  }
}

/** The stored explicit choice for a session, or null when it has none (or
 *  when no workspace is bound, or storage is unreadable). */
export function readSessionModelChoice(sessionId: string | null | undefined): SessionModelChoice | null {
  if (!sessionId || sessionId === '__pending') return null
  if (!useWorkspacesStore.getState().activeWorkspaceId) return null
  const choice = readAll()[sessionId]
  if (!choice || typeof choice.model !== 'string' || choice.model.trim() === '') return null
  return choice
}

/** Record (choice) or clear (null) a session's explicit model choice.
 *  No-op for a pending/absent session id or a session with no bound
 *  workspace — see the scope note in the header comment. */
export function writeSessionModelChoice(
  sessionId: string | null | undefined,
  choice: SessionModelChoice | null,
): void {
  if (!sessionId || sessionId === '__pending') return
  if (!useWorkspacesStore.getState().activeWorkspaceId) return
  try {
    const all = readAll()
    if (choice) {
      all[sessionId] = choice
    } else {
      delete all[sessionId]
    }
    localStorage.setItem(STORAGE_KEY, JSON.stringify(all))
  } catch {
    // localStorage write failed (quota/private mode) — the in-memory
    // nextModel the caller just set is still correct for this page life;
    // only the NEXT reload's restore degrades to transcript/default seeding.
  }
}
