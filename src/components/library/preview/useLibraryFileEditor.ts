// useLibraryFileEditor.ts — shared save mechanics for the three editable
// Library preview types (markdown / mermaid / any text — library-spec.md
// section 4). One instance per open file (LibraryTextPreview keys its mount
// to `entry.path`, so a hook instance's whole lifetime is exactly one file).
//
// Deliberately a manual/explicit save (a "Save" button + dirty tracking), NOT
// the repo's existing debounced `useAutoSave` hook: the spec calls for an
// explicit save action with clear saving/saved/error state and a
// navigate-away guard — a guard only makes sense when there's something to
// lose between saves, which autosave-on-every-keystroke would eliminate by
// design (and would fire a PUT per keystroke pause on a potentially large
// text file). `AutoSaveIndicator` (the presentation component) is reused as-is
// since it's driven purely by a `status` prop, not by useAutoSave itself.
//
// ── ADR-083 Step 0 (EMB-001/EMB-004/EMB-006/EMB-007/EMB-007a/EMB-007c) ──────
// THE DEFECT THIS CLOSES: this hook used to call putLibraryContent with no
// version token at all — last-write-wins. If this editor and, say, an agent
// edit the same note, one of them silently loses the work. Founder ruling:
// the token is REQUIRED, no exemption.
//
// This hook is now its own read side, independent of whatever content prop
// it was constructed with. `initialContent` (still required, still the save
// baseline) is NOT re-fetched here — this hook fetches ONLY the version
// token, via fetchLibraryContentVersioned, in an effect keyed on
// (workspaceId, path). That is a second GET beyond whatever query the
// mounting screen already ran for display purposes (LibraryPreviewPane's own
// `contentQuery`) — a deliberate, documented tradeoff: this hook's owned
// surface is exactly {api.ts, this file}, and threading the token through
// LibraryPreviewPane → LibraryMarkdownPreview/LibraryMermaidPreview/
// LibraryCodePreview → LibraryTextPreview → here would touch four files
// outside that boundary. Whoever owns that chain can wire the existing
// query's ETag through instead of this independent fetch as a follow-up;
// until then, this hook still gets a REAL token from a REAL read (EMB-007's
// literal requirement — "every read a write may follow returns the current
// version token") rather than skipping the guarantee for scope reasons.
//
// save() is ALSO the retry mechanism: LibraryTextPreview's Save button
// calls only `save()`, so a conflict's fresh token (from the 409 body,
// stored in `versionRef` by onError below) is used the next time the SAME
// button is pressed — no separate "Retry" affordance needed, and EMB-004's
// "MUST NOT resend a refused save on its own" holds because nothing but a
// deliberate user click ever calls save() again.

import { useCallback, useEffect, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  ApiError,
  putLibraryContent,
  fetchLibraryContentVersioned,
  isLibraryVersionConflict,
  libraryQueryKeys,
} from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'
import type { AutoSaveStatus } from '@/hooks/useAutoSave'
import { useUiStore } from '@/store/ui'
import { setLibraryEditorDirty } from './unsavedGuard'
import { getLibraryErrorMessage } from '../libraryErrorMessage'

interface UseLibraryFileEditorOptions {
  workspaceId: string
  path: string
  initialContent: string
  /** Invoked after a successful save with the server's echoed LibraryEntry
   * (updated size/modified_at) — callers use this to refresh their own
   * cached entry metadata (e.g. the header strip's size/modified display). */
  onSaved?: (entry: LibraryEntry) => void
}

/** Detail of a save refused with a 409 (ADR-083 EMB-004) — surfaced
 * separately from `error` so a caller can render something more specific
 * than "save failed" ("someone else changed this file"), and so a retry can
 * be proven (in tests) to use `actualVersion`, never the token the refused
 * attempt sent. */
export interface LibraryConflictInfo {
  message: string
  path: string
  expectedVersion: string | undefined
  /** The fresh token from the 409 body — what the NEXT save() call sends. */
  actualVersion: string | undefined
}

export interface UseLibraryFileEditorResult {
  draft: string
  setDraft: (value: string) => void
  isDirty: boolean
  save: () => void
  status: AutoSaveStatus
  error: string | undefined
  lastSavedAt: Date | undefined
  /** Set when the last save attempt was refused because the file changed
   * since this editor last read it — distinct from a generic `error`. Clears
   * on the next successful save. */
  conflict: LibraryConflictInfo | undefined
}

export function useLibraryFileEditor({
  workspaceId,
  path,
  initialContent,
  onSaved,
}: UseLibraryFileEditorOptions): UseLibraryFileEditorResult {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const [draft, setDraft] = useState(initialContent)
  const [status, setStatus] = useState<AutoSaveStatus>('idle')
  const [error, setError] = useState<string>()
  const [lastSavedAt, setLastSavedAt] = useState<Date>()
  const [conflict, setConflict] = useState<LibraryConflictInfo>()
  // The last successfully-persisted text — NOT re-derived from `initialContent`
  // after mount, so a background refetch of the content query can never
  // silently overwrite an in-progress edit's dirty baseline (same
  // draft-ownership principle useAutoSave.ts documents at its own top).
  const savedRef = useRef(initialContent)
  const fadeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // ADR-083 EMB-007 — the version token to send as expect_version on the
  // NEXT save. Populated by the read effect below on mount/file-change, and
  // REPLACED on every subsequent successful save (from that save's own
  // response) or refused save (from the 409 body's actual_version) — never
  // left pointing at the token that was just proven stale or the one that
  // was proven correct-but-now-superseded.
  const versionRef = useRef<string | null>(null)
  // In-flight (or settled) promise for the current read, so save() can
  // AWAIT it rather than race it — a save triggered before the read settles
  // still gets a real token instead of failing on a timing accident.
  const versionPromiseRef = useRef<Promise<string | null> | null>(null)

  const isDirty = draft !== savedRef.current

  useEffect(() => {
    setLibraryEditorDirty(isDirty)
  }, [isDirty])

  // Unmount: this exact file's editor is going away (user closed the pane,
  // switched files, or navigated) — clear the global flag so it can't outlive
  // this instance. Safe even if a NEWER editor's effect already ran first,
  // since only one editor is ever mounted at a time.
  useEffect(() => {
    return () => setLibraryEditorDirty(false)
  }, [])

  useEffect(() => {
    return () => {
      if (fadeTimerRef.current) clearTimeout(fadeTimerRef.current)
    }
  }, [])

  // ADR-083 EMB-007 — the read side of the save guard. Fires once per
  // (workspaceId, path): the version this editor started from is stale the
  // moment either changes, so a fresh read is required rather than carrying
  // the old file's token forward.
  useEffect(() => {
    let cancelled = false
    versionRef.current = null
    const promise = fetchLibraryContentVersioned(workspaceId, path)
      .then((res) => {
        if (!cancelled) versionRef.current = res.version
        return res.version
      })
      .catch((err) => {
        // Not surfaced here directly — a save attempted before this
        // resolves discovers the failure itself (see ensureVersion below)
        // and reports it through the normal error channel. Logged so a
        // silent version-read failure isn't invisible in the console.
        console.warn('[useLibraryFileEditor] could not read version token', err)
        return null
      })
    versionPromiseRef.current = promise
    return () => {
      cancelled = true
    }
  }, [workspaceId, path])

  /** Resolves the token to send on THIS save attempt: the last-known value
   * if already read, otherwise waits for the in-flight read. Never invents
   * one and never returns a stale value silently. */
  const ensureVersion = useCallback(async (): Promise<string | null> => {
    if (versionRef.current) return versionRef.current
    if (versionPromiseRef.current) return versionPromiseRef.current
    return null
  }, [])

  const mutation = useMutation({
    mutationFn: async (content: string) => {
      const expectVersion = await ensureVersion()
      if (!expectVersion) {
        throw new ApiError(
          400,
          'Could not confirm this file’s current version — reload the file and try again.',
          { code: 'expect_version_unavailable' },
        )
      }
      return putLibraryContent(workspaceId, { path, content, expect_version: expectVersion })
    },
    onMutate: () => {
      setStatus('saving')
      setError(undefined)
    },
    onSuccess: ({ data: entry, version }, content) => {
      savedRef.current = content
      // EMB-007 — the save's OWN response carries the file's new token; a
      // second save in the same session must send THIS, not the token the
      // original read returned.
      versionRef.current = version
      setConflict(undefined)
      setLibraryEditorDirty(false)
      setStatus('saved')
      setLastSavedAt(new Date())
      void queryClient.invalidateQueries({ queryKey: libraryQueryKeys.content(workspaceId, path) })
      void queryClient.invalidateQueries({ queryKey: ['library', workspaceId, 'entries'] })
      addToast({ message: 'Saved.', variant: 'success' })
      onSaved?.(entry)
      if (fadeTimerRef.current) clearTimeout(fadeTimerRef.current)
      fadeTimerRef.current = setTimeout(() => setStatus((s) => (s === 'saved' ? 'idle' : s)), 2000)
    },
    onError: (err) => {
      // Never silently swallowed: surfaced both in the persistent status
      // indicator (stays 'error'/'conflict' until the next save attempt) AND
      // a toast.
      if (isLibraryVersionConflict(err)) {
        // EMB-004/EMB-007 — a refused save because someone else changed the
        // file is a CONFLICT, distinguishable from a generic error, and the
        // fresh token it carries is what the NEXT save() (the same "Save"
        // button, pressed again — see module doc) sends. EMB-004 also
        // requires the refused save is never resent automatically: nothing
        // here triggers another mutate() call, only a user's own click does.
        versionRef.current = err.actualVersion ?? null
        const info: LibraryConflictInfo = {
          message: err.userMessage,
          path: err.path,
          expectedVersion: err.expectedVersion,
          actualVersion: err.actualVersion,
        }
        setConflict(info)
        setStatus('conflict')
        setError(err.userMessage)
        addToast({ message: err.userMessage, variant: 'error' })
        return
      }
      setConflict(undefined)
      const message = getLibraryErrorMessage(err, 'Save failed')
      setStatus('error')
      setError(message)
      addToast({ message, variant: 'error' })
    },
  })

  const save = useCallback(() => {
    if (draft === savedRef.current || mutation.isPending) return
    mutation.mutate(draft)
  }, [draft, mutation.isPending])

  return { draft, setDraft, isDirty, save, status, error, lastSavedAt, conflict }
}
