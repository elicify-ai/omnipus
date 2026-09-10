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
//
// ── B1 fix (lost update re-entering through the read door) ─────────────────
// The version read above and `initialContent` come from two DIFFERENT reads
// at two DIFFERENT times — this hook's own fetchLibraryContentVersioned is
// always strictly LATER than whatever read produced `initialContent`
// (LibraryPreviewPane's own contentQuery, which the editor mounts only after
// it resolves). If a writer changes the file in between, this hook used to
// keep the FRESH token but pair it with the STALE `initialContent` as the
// save baseline — a save built on the old bytes then passes the server's
// compare-and-swap (the token really is current) and silently destroys the
// intervening write. The read effect below now compares the version read's
// OWN content (`res.data.content`) against the current save baseline and:
//   - if nothing has been typed yet, safely REBASES the baseline (and the
//     visible draft) onto those bytes, so the token and the content it is
//     paired with always come from the same read;
//   - if the user already started typing against the older baseline before
//     this read resolved, rebasing would silently discard their keystrokes —
//     instead the draft is left untouched and save() is refused (via
//     `contentMismatchRef`, checked in mutationFn) until the file is
//     reopened. Either way, a save can never leave this hook pairing a
//     token with bytes it was not read with.
//
// ── stale-promise resurrection fix ──────────────────────────────────────
// `ensureVersion` falls back to `versionPromiseRef.current` so a save
// triggered before the mount-time read settles still gets a real token. Once
// that read HAS settled, though, the settled promise must stop being
// consulted: `versionRef.current` can later be set back to null by a 409
// whose body has no `actual_version` (LibraryConflictError.yaml: "absent
// when the file has been deleted since") or by a successful save whose
// response had no ETag (a proxy stripped it). Leaving the long-settled
// mount-time promise in `versionPromiseRef` meant `ensureVersion` silently
// resurrected the ORIGINAL, now-meaningless token in either case, so Save
// resent the exact same stale token and got the exact same 409 forever, with
// no way out short of a page reload. The read effect now clears
// `versionPromiseRef.current` once it settles (see the `.finally` below), so
// a later null in `versionRef.current` is correctly treated as "no current
// token is known" — mutationFn's existing `expect_version_unavailable`
// refusal fires honestly instead of an identical retry loop.

import { useCallback, useEffect, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  ApiError,
  putLibraryContent,
  fetchLibraryContentVersioned,
  isLibraryVersionConflict,
  LibraryVersionConflictError,
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

// B1 — shared between the read effect's proactive conflict state (fires the
// moment the mismatch is discovered, even before a save is attempted) and
// mutationFn's refusal (fires if a save is attempted anyway, or was already
// racing the read — see mutationFn's comment) so the two never drift apart.
const CONTENT_DIVERGED_MESSAGE =
  'This file changed elsewhere while you were editing. Your changes are kept here, but cannot be saved automatically — reopen the file to see the latest version, then reapply your changes.'

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
  // The ONE exception is the B1 safe-rebase below, which moves this and
  // `draft` together — never one without the other.
  const savedRef = useRef(initialContent)
  const fadeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  // Mirrors `draft` for the version-read effect's async callback below,
  // which must see the LATEST draft at the moment the read settles, not the
  // value closed over when the effect ran (a plain `draft` reference in that
  // closure would be stale the instant the user types before the read
  // resolves). Updated SYNCHRONOUSLY by `updateDraft` below, at the exact
  // call site that changes `draft` — not via a `useEffect([draft])`
  // mirror, which would only catch up on the next passive-effects flush and
  // could still be one tick stale if the version read settles in the same
  // microtask window as a keystroke.
  const draftRef = useRef(initialContent)

  // ADR-083 EMB-007 — the version token to send as expect_version on the
  // NEXT save. Populated by the read effect below on mount/file-change, and
  // REPLACED on every subsequent successful save (from that save's own
  // response) or refused save (from the 409 body's actual_version) — never
  // left pointing at the token that was just proven stale or the one that
  // was proven correct-but-now-superseded.
  const versionRef = useRef<string | null>(null)
  // In-flight promise for the current read, so save() can AWAIT it rather
  // than race it — a save triggered before the read settles still gets a
  // real token instead of failing on a timing accident. Deliberately
  // cleared back to null once the read settles (see the effect below) —
  // it must never be consulted again after that point, or a LATER null in
  // `versionRef.current` (a 409 with no actual_version, or a success with a
  // stripped ETag) would silently resurrect this long-stale, already-used
  // token instead of being treated as "no current token is known."
  const versionPromiseRef = useRef<Promise<string | null> | null>(null)
  // Set (non-null) by the read effect below when its OWN read returns
  // content that differs from the CURRENT save baseline while the user has
  // already typed against that baseline (B1) — save() must refuse until the
  // file is reopened, never silently pair the fresh token in
  // `actualVersion` with a diff built on the stale bytes the user is still
  // looking at.
  const contentMismatchRef = useRef<{ actualVersion: string | null } | null>(null)

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
    contentMismatchRef.current = null
    const promise = fetchLibraryContentVersioned(workspaceId, path)
      .then((res) => {
        if (cancelled) return res.version
        versionRef.current = res.version
        // B1 — pair the token with the bytes THIS read returned, not
        // whatever `initialContent` this hook was constructed with (an
        // earlier, strictly-prior read — see the module doc above).
        // `res.data.content` is `undefined` for a binary or too-large file
        // (LibraryContentResponse.content is optional) — this hook is only
        // ever used for the editable text/mermaid/code kinds, so that
        // shouldn't occur in practice, but there is nothing to pair a token
        // against without it, so fall through with no mismatch bookkeeping
        // rather than compare against `undefined`.
        const freshContent = res.data.content
        if (freshContent === undefined || freshContent === savedRef.current) {
          contentMismatchRef.current = null
        } else if (draftRef.current === savedRef.current) {
          // Nothing has been typed yet — safe to rebase both the
          // dirty-tracking baseline and the visible draft onto the bytes
          // this token was actually read with. Nothing is lost: the user
          // had not diverged from the old baseline at all.
          savedRef.current = freshContent
          draftRef.current = freshContent
          setDraft(freshContent)
          contentMismatchRef.current = null
          addToast({
            message: 'This file changed since it was opened — showing the latest version.',
            variant: 'warning',
          })
        } else {
          // The user already started typing against the OLDER baseline
          // before this fresher read resolved. Rebasing now would silently
          // discard their keystrokes; letting a save through would silently
          // pair a genuinely-current token with a diff built on stale
          // bytes — the exact same lost update either way. Leave the draft
          // untouched and refuse save() (mutationFn below) until the file
          // is reopened.
          contentMismatchRef.current = { actualVersion: res.version }
          setConflict({
            message: CONTENT_DIVERGED_MESSAGE,
            path,
            expectedVersion: undefined,
            actualVersion: res.version ?? undefined,
          })
          setStatus('conflict')
          setError(CONTENT_DIVERGED_MESSAGE)
          addToast({ message: CONTENT_DIVERGED_MESSAGE, variant: 'error' })
        }
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
    // Once this settles, it must never be treated as a live fallback again
    // — see versionPromiseRef's own doc comment above for why leaving a
    // long-settled promise in place lets a later null resurrect a stale,
    // already-used token instead of being reported honestly.
    promise
      .finally(() => {
        if (versionPromiseRef.current === promise) versionPromiseRef.current = null
      })
      .catch(() => {
        // finally's own rejection path can't occur here (the chain above
        // already converts every rejection into a resolved null) — this
        // exists only so an unhandled-rejection warning can never surface
        // from this bookkeeping branch.
      })
    versionPromiseRef.current = promise
    return () => {
      cancelled = true
    }
  }, [workspaceId, path])

  /** Resolves the token to send on THIS save attempt: the last-known value
   * if already read, otherwise waits for the in-flight read. Never invents
   * one and never returns a stale value silently. Once the initial read has
   * settled, `versionPromiseRef.current` is cleared (see the read effect),
   * so a null `versionRef.current` after that point is never masked by
   * falling back to the long-stale settled promise — see its doc comment. */
  const ensureVersion = useCallback(async (): Promise<string | null> => {
    if (versionRef.current) return versionRef.current
    if (versionPromiseRef.current) return versionPromiseRef.current
    return null
  }, [])

  const mutation = useMutation({
    mutationFn: async (content: string) => {
      // Awaited BEFORE the contentMismatchRef check below (not the other
      // way round): a save triggered while the version read is still
      // in-flight must observe whatever that SAME read discovers — checking
      // the flag first would race the read's own mismatch detection and
      // could let a save through before it had a chance to set the flag.
      const expectVersion = await ensureVersion()
      if (contentMismatchRef.current) {
        // B1 — the version read that produced `expectVersion` returned
        // content that no longer matches what this draft was built on, and
        // the user had already started typing before that was discovered
        // (see the read effect). Refuse rather than silently pair a
        // genuinely-current token with a diff built on stale bytes.
        throw new LibraryVersionConflictError(
          {
            error: CONTENT_DIVERGED_MESSAGE,
            code: 'library_version_conflict',
            path,
            expected_version: undefined,
            actual_version: contentMismatchRef.current.actualVersion ?? undefined,
          },
          '',
        )
      }
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

  // Wraps the raw useState setter so `draftRef` is updated in the SAME
  // synchronous call, not on a later effect flush — see draftRef's own doc
  // comment for why that matters to the B1 read-effect's mismatch check.
  const updateDraft = useCallback((value: string) => {
    draftRef.current = value
    setDraft(value)
  }, [])

  return { draft, setDraft: updateDraft, isDirty, save, status, error, lastSavedAt, conflict }
}
