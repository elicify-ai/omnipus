// #1452 D2 A2.4 "Storage and coherence": fetched full content of one saved
// history occurrence lives only in the query cache, keyed by session,
// position and a client-local read generation. It is never written into the
// chat store. The generation advances whenever an authoritative page,
// snapshot or window-refresh commit delivers that position (even when its
// preview, sizes and status compare equal) and when the session is left;
// advancing drops the cached body and aborts any read of an older generation,
// so a late response can never be cached or shown.
import { useQueries, useQuery, type UseQueryOptions, type UseQueryResult } from '@tanstack/react-query'
import { create } from 'zustand'
import { queryClient } from '@/lib/queryClient'
import { fetchSessionHistoryEntry, type SessionHistoryEntry } from '@/lib/api/sessions'
import { isApiError } from '@/lib/api-error'

interface HistoryEntryReadState { // not-wire-format: client-local read generations and refusals; never persisted or sent
  /** Read generation per `${sessionId}\u0000${position}`; absent means 0. */
  generations: Record<string, number>
  /**
   * Reads refused with 401. The existing sign-in handling takes over and
   * clears the query cache; a refused read must not silently start again.
   */
  refused: Record<string, true>
}

export const useHistoryEntryReadStore = create<HistoryEntryReadState>(() => ({ generations: {}, refused: {} }))

const QUERY_ROOT = 'history-entry'
const entryKey = (sessionId: string, position: string) => `${sessionId}\u0000${position}`

function dropReads(sessionId: string, position?: string): void {
  const filters = { queryKey: position === undefined ? [QUERY_ROOT, sessionId] : [QUERY_ROOT, sessionId, position] }
  // Removing a query destroys it, which aborts its in-flight request; a late
  // response then lands on a detached query and never reaches the cache.
  void queryClient.cancelQueries(filters)
  queryClient.removeQueries(filters)
}

/** A commit delivered these positions: older reads and cached bodies are void. */
export function advanceHistoryEntryGenerations(sessionId: string, positions: readonly string[]): void {
  if (!positions.length) return
  for (const position of positions) dropReads(sessionId, position)
  useHistoryEntryReadStore.setState(state => {
    const generations = { ...state.generations }
    const refused = { ...state.refused }
    for (const position of positions) {
      generations[entryKey(sessionId, position)] = (generations[entryKey(sessionId, position)] ?? 0) + 1
      delete refused[entryKey(sessionId, position)]
    }
    return { generations, refused }
  })
}

/** Leaving or switching the session releases every read and cached body it holds (text expansion flags live on the chat bucket and are released with it). */
export function releaseHistoryEntryReads(sessionId: string): void {
  dropReads(sessionId)
  useHistoryEntryReadStore.setState(state => {
    const prefix = `${sessionId}\u0000`
    const generations = { ...state.generations }
    for (const key of Object.keys(generations)) if (key.startsWith(prefix)) generations[key] += 1
    const keep = (map: Record<string, true>) => Object.fromEntries(Object.entries(map).filter(([key]) => !key.startsWith(prefix)))
    return { generations, refused: keep(state.refused) }
  })
}

function readGeneration(sessionId: string, position: string): number {
  return useHistoryEntryReadStore.getState().generations[entryKey(sessionId, position)] ?? 0
}

function readOptions(sessionId: string, position: string, generation: number, enabled: boolean): UseQueryOptions<SessionHistoryEntry, Error> {
  return {
    queryKey: [QUERY_ROOT, sessionId, position, generation],
    queryFn: async ({ signal }) => {
      let entry: SessionHistoryEntry
      try {
        entry = await fetchSessionHistoryEntry(sessionId, position, signal)
      } catch (error) {
        if (isApiError(error) && error.status === 401) useHistoryEntryReadStore.setState(state => ({ refused: { ...state.refused, [entryKey(sessionId, position)]: true } }))
        throw error
      }
      // A response is accepted only for the generation that started it.
      if (readGeneration(sessionId, position) !== generation) throw new DOMException('Superseded history entry read.', 'AbortError')
      return entry
    },
    enabled: enabled && sessionId !== '' && position !== '' && !useHistoryEntryReadStore.getState().refused[entryKey(sessionId, position)],
    // Retry is the user's explicit action (A2.4); nothing repeats silently.
    retry: false,
    staleTime: Infinity,
    refetchOnMount: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  }
}

/** One occurrence's full public projection, read only while `enabled`. Same position, same read. */
/**
 * A full read answered with the D4 per-item placeholder (spec D4; A2.2, #1452
 * SEC-3): the server kept the occurrence's identity and position but cannot
 * serve its saved body. Only ordinary user/assistant occurrences are ever
 * shortened, so a system-role answer is that placeholder. It is a permanent
 * state of this occurrence, not a failed read: the same read returns it again.
 */
export function isHistoryEntryPlaceholder(entry: SessionHistoryEntry | undefined): boolean {
  // The Message type carries no tool calls on a system-role entry.
  return entry?.role === 'system'
}

export function useHistoryEntryRead(sessionId: string, position: string, enabled: boolean): UseQueryResult<SessionHistoryEntry, Error> {
  const generation = useHistoryEntryReadStore(state => state.generations[entryKey(sessionId, position)] ?? 0)
  const refused = useHistoryEntryReadStore(state => state.refused[entryKey(sessionId, position)] === true)
  // The app's one query client is named explicitly: message rows also render
  // outside a QueryClientProvider (plain lists, isolated row tests).
  return useQuery(readOptions(sessionId, position, generation, enabled && !refused), queryClient)
}

/** Several occurrences at once (the text parts of one combined reply). */
export function useHistoryEntryReads(sessionId: string, reads: readonly { position: string; enabled: boolean }[]): UseQueryResult<SessionHistoryEntry, Error>[] {
  const generations = useHistoryEntryReadStore(state => reads.map(read => `${state.generations[entryKey(sessionId, read.position)] ?? 0}`).join(','))
  // Subscribed so a 401 refusal re-renders and disables the read (readOptions reads it).
  useHistoryEntryReadStore(state => reads.map(read => state.refused[entryKey(sessionId, read.position)] ? '1' : '0').join(''))
  const values = generations ? generations.split(',').map(Number) : []
  return useQueries({ queries: reads.map((read, i) => readOptions(sessionId, read.position, values[i] ?? 0, read.enabled)) }, queryClient)
}
