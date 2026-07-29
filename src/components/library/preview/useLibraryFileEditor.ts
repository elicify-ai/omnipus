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

import { useCallback, useEffect, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { putLibraryContent, libraryQueryKeys, getErrorMessage } from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'
import type { AutoSaveStatus } from '@/hooks/useAutoSave'
import { useUiStore } from '@/store/ui'
import { setLibraryEditorDirty } from './unsavedGuard'

interface UseLibraryFileEditorOptions {
  workspaceId: string
  path: string
  initialContent: string
  /** Invoked after a successful save with the server's echoed LibraryEntry
   * (updated size/modified_at) — callers use this to refresh their own
   * cached entry metadata (e.g. the header strip's size/modified display). */
  onSaved?: (entry: LibraryEntry) => void
}

export interface UseLibraryFileEditorResult {
  draft: string
  setDraft: (value: string) => void
  isDirty: boolean
  save: () => void
  status: AutoSaveStatus
  error: string | undefined
  lastSavedAt: Date | undefined
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
  // The last successfully-persisted text — NOT re-derived from `initialContent`
  // after mount, so a background refetch of the content query can never
  // silently overwrite an in-progress edit's dirty baseline (same
  // draft-ownership principle useAutoSave.ts documents at its own top).
  const savedRef = useRef(initialContent)
  const fadeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

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

  const mutation = useMutation({
    mutationFn: (content: string) => putLibraryContent(workspaceId, { path, content }),
    onMutate: () => {
      setStatus('saving')
      setError(undefined)
    },
    onSuccess: (entry, content) => {
      savedRef.current = content
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
      // indicator (stays 'error' until the next save attempt) AND a toast.
      const message = getErrorMessage(err, 'Save failed')
      setStatus('error')
      setError(message)
      addToast({ message, variant: 'error' })
    },
  })

  const save = useCallback(() => {
    if (draft === savedRef.current || mutation.isPending) return
    mutation.mutate(draft)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draft, mutation.isPending])

  return { draft, setDraft, isDirty, save, status, error, lastSavedAt }
}
