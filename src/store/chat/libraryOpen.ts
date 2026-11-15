// libraryOpen.ts — page-local state behind the `open_in_library` frame
// (chat-links spec "Replay guarantee" step 3, UI States, Accessibility).
//
//   - notices: at most ONE "Open in Library" notice per session; a newer one
//     replaces the older. Never persisted, so a reload shows none.
//   - announcement: the polite live-region text ("Opened plan.md in Library",
//     or the notice's own sentence). `id` changes on every announcement so the
//     same sentence twice is announced twice.
//   - handled-set: the last 256 `session_id` + `request_id` keys this page
//     acted on. A repeat still in the set is dropped silently; the 257th
//     distinct key forgets the oldest, which may then act again. `call_id` is
//     never part of the key.

import { create } from 'zustand'

export interface LibraryOpenNotice { // not-wire-format: page-local notice, built from the generated frame's fields
  workspaceId: string
  path: string
  requestId: string
}

interface LibraryOpenState {
  notices: Record<string, LibraryOpenNotice>
  announcement: { id: number; text: string } | null
}

export const useLibraryOpenStore = create<LibraryOpenState>(() => ({ notices: {}, announcement: null }))

/** The file name shown to the user for a Library path. */
export function libraryFileName(path: string): string {
  return path.slice(path.lastIndexOf('/') + 1) || path
}

export function showLibraryOpenNotice(sessionId: string, notice: LibraryOpenNotice): void {
  useLibraryOpenStore.setState((state) => ({ notices: { ...state.notices, [sessionId]: notice } }))
}

export function dismissLibraryOpenNotice(sessionId: string): void {
  useLibraryOpenStore.setState((state) => {
    if (!(sessionId in state.notices)) return {}
    const notices = { ...state.notices }
    delete notices[sessionId]
    return { notices }
  })
}

let announcementId = 0

export function announceLibraryOpen(text: string): void {
  announcementId += 1
  useLibraryOpenStore.setState({ announcement: { id: announcementId, text } })
}

// ── handled-set ──────────────────────────────────────────────────────────────
export const HANDLED_REQUEST_LIMIT = 256
const handledRequests = new Set<string>() // insertion order = age

function requestKey(sessionId: string, requestId: string): string {
  return `${sessionId}\u0000${requestId}`
}

export function wasLibraryOpenRequestHandled(sessionId: string, requestId: string): boolean {
  return handledRequests.has(requestKey(sessionId, requestId))
}

export function rememberLibraryOpenRequest(sessionId: string, requestId: string): void {
  const key = requestKey(sessionId, requestId)
  if (handledRequests.has(key)) return
  handledRequests.add(key)
  if (handledRequests.size > HANDLED_REQUEST_LIMIT) {
    const oldest = handledRequests.values().next().value
    if (oldest !== undefined) handledRequests.delete(oldest)
  }
}

/** Test seam: forget every handled request, notice and announcement. */
export function resetLibraryOpenState(): void {
  handledRequests.clear()
  useLibraryOpenStore.setState({ notices: {}, announcement: null })
}
