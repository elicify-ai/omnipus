// LibraryExplorer — the file explorer over workspace work/ trees
// (library-spec.md D-2). This is the ONE component both entry points (D-3)
// render: the sidebar opens it with `initialWorkspaceId` undefined (virtual
// root — every workspace as a top-level node); a workspace-scoped opener
// (e.g. a future chat/header-bar affordance, wired through
// `useUiStore.getState().openLibraryPanel(workspaceId)`) opens it with a real
// id and lands straight inside that workspace's work/ tree. Only the INITIAL
// selection differs — from there the same navigation (drill into a
// directory, back out via breadcrumb, "Library" crumb to return to the
// virtual root) is available regardless of how the panel was opened.
//
// Preview/edit pane (library-spec.md D-5): LibraryPreviewPane, mounted below
// where the "PREVIEW/EDIT PANE PLACEHOLDER" comment used to mark the spot —
// see that mount site further down. It owns the actual content surface
// (img/video/CodeMirror/Shiki/Mermaid); this file owns only navigation
// (workspace/dir/file selection, breadcrumb) and the entry actions
// (download/rename/move/copy/delete) that the pane's header delegates back
// up to via props, unchanged from before the pane existed. The
// confirmDiscardLibraryEdits() guard sprinkled through the navigation
// handlers below is this file's one piece of pane-aware wiring: it's a
// no-op whenever no editor is open or nothing is unsaved (see
// preview/unsavedGuard.ts), so it does not change behavior for any caller
// that never touches the editor.
//
// Deep-linking (ADR-067 FR-012, US-3 AS-2/3/4/5): "which workspace, which
// file" is expressible as an ADDRESS — see `LibraryAddress` below. A caller
// that can put that address in a URL (the /library pop-out route) passes it
// in and receives every change back; a caller that cannot (the docked
// panel) passes neither and this component keeps the same state internally,
// exactly as before. The addressed mode is deliberately CONTROLLED rather
// than an initial-value-plus-sync-effect: there is then no second copy of
// the selection to drift out of step with the URL, and an inbound change —
// the back button, a pasted link, a wikilink or search hit in a later wave —
// is an ordinary re-render instead of a reconciliation pass. What this file
// still owns in both modes is the BROWSED FOLDER, which is derived from the
// address rather than carried in it (see `browsedDir`).

import { useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Buildings,
  Files,
  CaretRight,
  X,
  ArrowSquareOut,
  FolderOpen,
} from '@phosphor-icons/react'
import { Switch } from '@/components/ui/switch'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { QueryErrorState } from '@/components/shared/QueryErrorState'
import { useUiStore } from '@/store/ui'
import { cn } from '@/lib/utils'
import {
  fetchLibraryWorkspaces,
  fetchLibraryEntries,
  fetchKnowledgeBaseInfo,
  deleteLibraryEntry,
  renameLibraryEntry,
  moveLibraryEntry,
  copyLibraryEntry,
  uploadLibraryFiles,
  mkdirLibraryEntry,
  createLibraryTextFile,
  libraryDownloadUrl,
  libraryQueryKeys,
  createWorkspaceMount,
  deleteWorkspaceMount,
  mountSkillsDisclosure,
  isApiError,
} from '@/lib/api'
import type { LibraryEntry, LibraryTransferRequest, LibraryWorkspaceNode, MountSkillsDisclosure } from '@/lib/api'
import { LibraryEntryRow } from './LibraryEntryRow'
import { LibraryRenameDialog } from './LibraryRenameDialog'
import { LibraryTransferDialog } from './LibraryTransferDialog'
import { LibraryAddMountDialog } from './LibraryAddMountDialog'
import { LibraryCreateMenu } from './LibraryCreateMenu'
import { LibraryMountsDialog } from './LibraryMountsDialog'
import { mountNameFromPath } from './libraryMountName'
import { LibraryNewFolderDialog } from './LibraryNewFolderDialog'
import { LibraryNewNoteDialog } from './LibraryNewNoteDialog'
import { LibraryPreviewPane } from './LibraryPreviewPane'
import { LibraryErrorBanner } from './LibraryErrorBanner'
import { KnowledgePanel } from './knowledge/KnowledgePanel'
import { LibrarySearchBar } from './search/LibrarySearchBar'
import { useLibraryCrossTabRefresh } from './useLibraryCrossTabRefresh'
import { confirmDiscardLibraryEdits } from './preview/unsavedGuard'
import { getLibraryErrorMessage } from './libraryErrorMessage'

/**
 * The URL-addressable location of the Library (ADR-067 FR-012).
 *
 * Historically only two fields. The BROWSED FOLDER was deliberately not one
 * of them: a folder is always derivable from a selected file, and addressing
 * folders too would put two things in the URL that can disagree with each
 * other. Closing the preview therefore leaves you in the folder you were
 * reading from without that folder ever having been in the address.
 *
 * C4 (library-b-c-design-2026-09-07.md "fullscreen carries the selection")
 * adds `folder` as a narrow, one-directional exception to that rule — see
 * its own doc below. It does not reopen the "two things that can disagree"
 * risk: `goTo()` (this file's one place address changes are emitted) NEVER
 * includes `folder` in what it reports back, so nothing in normal navigation
 * ever WRITES it, and it is consumed only once, at mount, before either field
 * has had a chance to move. There is exactly one path where it applies (no
 * `path` is set) and it never fights a `path` that IS set.
 */
export interface LibraryAddress {
  /** undefined = the virtual root (every workspace as a top-level node). */
  workspaceId?: string
  /** Work-tree-relative path of the selected FILE; undefined = nothing selected. */
  path?: string
  /**
   * Work-tree-relative path of the BROWSED FOLDER, consulted ONLY as a
   * one-time initial seed when `path` is absent (a selected file's own
   * parent folder always wins over this — see `selectedDir` below). Exists
   * so a caller that can only address a folder, not a file inside it — the
   * fullscreen pop-out's initial URL, built from whatever folder the docked
   * panel had open with nothing selected (C4) — can still land there. Never
   * emitted by `goTo()`/`onAddressChange`, so it cannot drift into a second,
   * disagreeing source of truth once real navigation begins.
   */
  folder?: string
}

export interface LibraryExplorerProps {
  /** undefined = start at the virtual root (D-3 sidebar entry point).
   *  Ignored when `address` (+ `onAddressChange`) is supplied — the address
   *  says where to be, and says it again on every change, so an "initial"
   *  value would only be a second answer to the same question. */
  initialWorkspaceId?: string
  /**
   * URL-addressed location (ADR-067 FR-012). Supply this together with
   * `onAddressChange` to hand the caller control of workspace + selection;
   * omit both to keep them as this component's own state (the docked panel).
   *
   * One without the other is treated as "not addressed": an `address` with no
   * way to report a change back would freeze selection on whatever the URL
   * happened to say, which is worse than not deep-linking at all.
   */
  address?: LibraryAddress
  /** Fires whenever the explorer's own navigation changes the address —
   *  selecting a file, closing the preview, changing workspace, or a
   *  rename/delete/move that moves or removes the selected file. Always
   *  called AFTER the unsaved-edits guard has passed, never before. */
  onAddressChange?: (next: LibraryAddress) => void
  /** Omit to hide the Close button (e.g. the fullscreen pop-out route). */
  onClose?: () => void
  /** Omit to hide the pop-out button (the pop-out route itself has nowhere further to pop out to). */
  onPopOut?: () => void
  /** Extra classes for the root element — e.g. the pop-out route's `absolute inset-0` fill. */
  className?: string
  /** Fires whenever the workspace currently being VIEWED changes (including
   * the initial mount) — null for the virtual root. library-spec.md D-4's
   * pop-out route uses this to know what to announce via libraryHandoff.ts,
   * and it must keep using THIS rather than reading the workspace back out of
   * its own URL: this fires at the moment the workspace changes, whereas the
   * URL is written by a router navigation that settles a tick later — and at
   * `pagehide` there is no later tick. (Before deep-linking the reason was
   * different but the conclusion identical: the param went stale the moment
   * the user navigated inside the explorer.) */
  onWorkspaceChange?: (workspaceId: string | null) => void
  /**
   * Fires whenever the CURRENT selection changes (the selected file, or —
   * with no file selected — the browsed folder), including the initial
   * mount. C4's fullscreen pop-out (LibraryPanel.tsx's `handlePopOut`) uses
   * this to build the pop-out URL from wherever the docked panel actually
   * is, the same way `onWorkspaceChange` already does for the workspace
   * half of the address. Deliberately a plain reporting callback, not
   * `onAddressChange` — the docked panel that needs this is NOT addressed
   * (it owns its own navigation state; see `addressed` below), so there is
   * no address for it to "change".
   */
  onSelectionChange?: (selection: { path: string | null; folder: string }) => void
  /**
   * How the file list and the open preview divide the space.
   *
   * 'stacked' (default, the docked <aside>): preview BELOW the list. The aside
   * is a narrow column, so a side-by-side split there would leave neither half
   * usable.
   * 'split' (the fullscreen /#/library tab): preview to the RIGHT, taking 60%
   * — a full-width window has the room, and an editor is far more useful tall
   * than wide.
   */
  layout?: 'stacked' | 'split'
}

function sortEntries(entries: LibraryEntry[]): LibraryEntry[] {
  return [...entries].sort((a, b) => {
    if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1
    return a.name.localeCompare(b.name)
  })
}

/** Work-tree-relative parent folder of a file path; '' for a top-level file. */
function parentDirOf(filePath: string): string {
  const cut = filePath.lastIndexOf('/')
  return cut === -1 ? '' : filePath.slice(0, cut)
}

function baseNameOf(filePath: string): string {
  const cut = filePath.lastIndexOf('/')
  return cut === -1 ? filePath : filePath.slice(cut + 1)
}

/**
 * The upload toast (UAT D-124, 2026-09-13). A duplicate upload is correctly
 * auto-suffixed server-side (`photo (1).png`), but the toast said only
 * "Uploaded 1 file." — the reader who uploaded `photo.png` found out it was
 * now `photo (2).png` only by reading the list. Renames are matched by
 * position: the server returns one entry per uploaded file, in order.
 * Exported for the test; pure.
 */
export function uploadOutcomeMessage(fileNames: readonly string[], savedNames: readonly string[]): string {
  const count = savedNames.length
  let message = `Uploaded ${count} file${count === 1 ? '' : 's'}.`
  const renamed: string[] = []
  for (let i = 0; i < Math.min(fileNames.length, savedNames.length); i++) {
    if (fileNames[i] !== savedNames[i]) renamed.push(`${fileNames[i]} was saved as ${savedNames[i]}`)
  }
  if (renamed.length > 0) {
    message += ` ${renamed.join('; ')} because ${renamed.length === 1 ? 'that name was' : 'those names were'} already taken.`
  }
  return message
}

export function LibraryExplorer({
  initialWorkspaceId,
  address,
  onAddressChange,
  onClose,
  onPopOut,
  className,
  onWorkspaceChange,
  onSelectionChange,
  layout = 'stacked',
}: LibraryExplorerProps) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const fileInputRef = useRef<HTMLInputElement>(null)

  // Uncontrolled fallbacks — used only when the caller does NOT address the
  // Library by URL. In addressed mode these are never read or written, so
  // there is exactly one copy of "where am I" at any moment.
  //
  // They seed from `address` as well, which matters only in the degraded case
  // of an address supplied with no `onAddressChange`: that caller still gets
  // taken to the place it asked for, it simply owns nothing afterwards. A
  // frozen pane is the failure mode being avoided here, not a lost initial
  // position.
  const [internalWorkspaceId, setInternalWorkspaceId] = useState<string | null>(
    initialWorkspaceId ?? address?.workspaceId ?? null,
  )
  const [internalSelectedPath, setInternalSelectedPath] = useState<string | null>(address?.path ?? null)
  const addressed = address !== undefined && onAddressChange !== undefined
  const workspaceId = addressed ? address?.workspaceId ?? null : internalWorkspaceId
  const selectedPath = addressed ? address?.path ?? null : internalSelectedPath

  // D-107, pull half: this tab's listing refreshes when the user returns to
  // it (focus / visibilitychange→visible). The push half — the
  // library_changed WS frame emitted by every Library write — covers tabs
  // that never see a focus event (two windows side by side).
  useLibraryCrossTabRefresh(workspaceId)

  // C4: seeds the fullscreen pop-out's INITIAL browsed folder from
  // `address.folder` when there is no `address.path` to derive one from
  // (see LibraryAddress's own doc comment). Seeded once, like
  // `initialWorkspaceId`/`internalSelectedPath` above — the pop-out route
  // never remounts on a later address change, so this initializer running
  // exactly once at mount is precisely "the tab's starting folder", not an
  // ongoing sync.
  const [browsedDir, setBrowsedDir] = useState(address?.path ? parentDirOf(address.path) : address?.folder ?? '')
  const [includeHidden, setIncludeHidden] = useState(false)
  const isSplit = layout === 'split'
  const [renameTarget, setRenameTarget] = useState<LibraryEntry | null>(null)
  const [renameError, setRenameError] = useState<string>()
  const [deleteTarget, setDeleteTarget] = useState<LibraryEntry | null>(null)
  const [deleteError, setDeleteError] = useState<string>()
  const [unmountTarget, setUnmountTarget] = useState<LibraryEntry | null>(null)
  const [unmountError, setUnmountError] = useState<string>()
  const [addMountOpen, setAddMountOpen] = useState(false)
  const [mountsOpen, setMountsOpen] = useState(false)
  const [addMountError, setAddMountError] = useState<string>()
  // D-117 response half (2026-09-14 fix round): the mount create's OWN answer,
  // rendered inside the Add-mount dialog instead of a toast.
  //   - `addMountWarning`: the 201 body's `warning` (a broad grant the server
  //     let through). Holding `addMountOpen` true while it is set is what
  //     keeps the verdict on screen until acknowledged.
  //   - `addMountRefusal`: the 403 body's reason (a grant the server refused
  //     — e.g. the Omnipus data directory). Kept apart from `addMountError`
  //     so a policy refusal never reads like a transport failure.
  const [addMountWarning, setAddMountWarning] = useState<string>()
  const [addMountRefusal, setAddMountRefusal] = useState<string>()
  // The skills disclosure that a WARNED create also produced, held back until
  // the operator acknowledges the warning — two stacked dialogs would let the
  // disclosure cover the very warning it rides on. Carries the mount's name
  // too, so the deferred success toast can name what was mounted.
  const [pendingWarnedMount, setPendingWarnedMount] = useState<{
    name: string
    disclosure: (MountSkillsDisclosure & { mountName: string }) | null
  } | null>(null)
  // ADR-072 D1.2/FR-074/FR-074a: what the just-created mount's recognised
  // skills directory grants — shown as its own dialog (not a toast, which
  // can be missed and auto-dismisses) so the operator explicitly
  // acknowledges the auto-loadable-instructions grant before moving on, the
  // same way LibraryAddMountDialog itself surfaces the write-grant decision.
  // `null` = no skills directory found (nothing to disclose) or the backend
  // does not send the disclosure yet (see mountSkillsDisclosure's doc
  // comment in lib/api.ts).
  const [skillsDisclosure, setSkillsDisclosure] = useState<
    (MountSkillsDisclosure & { mountName: string }) | null
  >(null)
  const [transferTarget, setTransferTarget] = useState<{ entry: LibraryEntry; mode: 'move' | 'copy' } | null>(null)
  const [transferError, setTransferError] = useState<string>()
  const [uploadError, setUploadError] = useState<string>()
  const [newFolderOpen, setNewFolderOpen] = useState(false)
  const [newFolderError, setNewFolderError] = useState<string>()
  // UAT #699 / D-115: "New note" — a markdown file in the current folder.
  const [newNoteOpen, setNewNoteOpen] = useState(false)
  const [newNoteError, setNewNoteError] = useState<string>()

  useEffect(() => {
    onWorkspaceChange?.(workspaceId)
    // Only the explorer's OWN navigation state matters here — re-running
    // this because the caller passed a new function reference would be
    // harmless but pointless noise.
     
  }, [workspaceId])

  // C4: reports the current selection (selected file, or — with none — the
  // browsed folder) on every change, mirroring the onWorkspaceChange effect
  // just above. The docked LibraryPanel is the one caller today (it is not
  // `addressed`, so it has no other way to learn this), and uses it to build
  // the fullscreen pop-out's URL from wherever the panel actually is.
  useEffect(() => {
    onSelectionChange?.({ path: selectedPath, folder: browsedDir })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedPath, browsedDir])

  // A file address implies its folder, and that implication is the whole of
  // "a deep link opens the containing folder" (US-3 AS-5): the listing this
  // component fetches is `browsedDir`, so pointing it at the selection's
  // parent is what puts the file's own folder on screen — whether the file
  // turns out to exist or not. It also means closing the preview leaves you
  // IN that folder rather than bouncing to the workspace root.
  const selectedDir = selectedPath === null ? null : parentDirOf(selectedPath)
  useEffect(() => {
    if (selectedDir === null) return
    setBrowsedDir((cur) => (cur === selectedDir ? cur : selectedDir))
  }, [selectedDir])

  // UAT D-108 (2026-09-13): `address.folder` used to be read ONCE, as the
  // mount-time seed above, so a same-route navigation to a different
  // `folder=` (a pasted link, the back button) changed the URL and nothing
  // else — the breadcrumb and listing stayed where they were and the address
  // bar became a quiet lie. A CHANGE in the folder param, with no file
  // selected to imply a folder instead, is now acted on. It fires only when
  // the param actually changes: in-app folder navigation never rewrites the
  // param (goTo reports paths, not folders), so it cannot fight the click.
  const addressFolder = addressed ? address?.folder : undefined
  useEffect(() => {
    if (!addressed || selectedPath !== null || addressFolder === undefined) return
    setBrowsedDir((cur) => (cur === addressFolder ? cur : addressFolder))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [addressFolder])

  // UAT D-64 (2026-09-13): switching workspace BY URL kept the previous
  // workspace's browsed folder, so the breadcrumb read
  // "Library › My Workspace › UAT Vault › Projects" for a folder that never
  // existed there — and the listing query fired for it. In-app workspace
  // navigation already resets the folder (handleOpenWorkspaceNode); an
  // addressed change does the same here, to whatever the new address itself
  // implies — the selected file's folder, the folder param, or the root.
  // Adjusted DURING render (React's derived-state pattern: state remembers
  // the workspace it was computed for) rather than in an effect, so the
  // entries query below never runs even once against the stale folder.
  const [browsedForWorkspace, setBrowsedForWorkspace] = useState(workspaceId)
  if (browsedForWorkspace !== workspaceId) {
    setBrowsedForWorkspace(workspaceId)
    if (addressed) setBrowsedDir(selectedDir ?? addressFolder ?? '')
  }

  // Always fetched (cheap, small list) — backs the virtual-root listing AND
  // resolves the current workspace's display name for the breadcrumb + the
  // destination picker inside LibraryTransferDialog.
  const workspacesQuery = useQuery({
    queryKey: libraryQueryKeys.workspaces(),
    queryFn: fetchLibraryWorkspaces,
    staleTime: 30_000,
  })

  const entriesQuery = useQuery({
    queryKey: libraryQueryKeys.entries(workspaceId ?? '', browsedDir, includeHidden),
    queryFn: () => fetchLibraryEntries(workspaceId as string, browsedDir, includeHidden),
    enabled: workspaceId !== null,
    staleTime: 10_000,
  })

  const sortedWorkspaces = useMemo(
    () => [...(workspacesQuery.data ?? [])].sort((a, b) => a.name.localeCompare(b.name)),
    [workspacesQuery.data],
  )
  const sortedEntries = useMemo(() => sortEntries(entriesQuery.data ?? []), [entriesQuery.data])

  // The selected FILE is resolved from the folder listing rather than stored
  // as a second copy of it, so an address arriving from outside (a fresh
  // load, the back button, a link) needs no different code path from a click.
  const lastResolvedEntryRef = useRef<LibraryEntry | null>(null)
  const selectedEntry = useMemo(() => {
    if (selectedPath === null) return null
    const hit = sortedEntries.find((e) => e.path === selectedPath && !e.is_dir)
    if (hit) return hit
    // Listing for this folder is mid-flight (a "Show hidden" toggle, a
    // post-mutation invalidation): keep the entry already resolved for THIS
    // path rather than tearing the open preview down and rebuilding it. A
    // different path never matches, so a real navigation is never masked.
    return lastResolvedEntryRef.current?.path === selectedPath ? lastResolvedEntryRef.current : null
  }, [selectedPath, sortedEntries])
  useEffect(() => {
    if (selectedEntry) lastResolvedEntryRef.current = selectedEntry
  }, [selectedEntry])

  // The preview pane needs a real workspace to fetch from, so the virtual root
  // never opens one however the selection got set.
  const previewOpen = selectedEntry !== null && workspaceId !== null

  // US-3 AS-5 — an address naming a file that isn't there must land on the
  // containing folder with a message, never an error page and never a blank
  // pane. Claimed ONLY when the listing on screen is the one that would hold
  // the file (`selectedDir === browsedDir`) and it actually loaded: while it
  // is loading, or when the folder itself failed to load, we do not know that
  // the file is missing and must not say so. A failed folder listing keeps
  // its own retryable error state instead.
  const deepLinkUnresolved =
    selectedPath !== null &&
    selectedEntry === null &&
    workspaceId !== null &&
    selectedDir === browsedDir &&
    entriesQuery.isSuccess
  // UAT D-36 (2026-09-13): a `path` that names a FOLDER in the listing is not
  // "not found" — it is right there, one row down. Saying so was a plain
  // falsehood (the OP lane read it as an encoding bug). Such an address now
  // OPENS the folder: the browsed folder becomes the path, and the address
  // drops the path so the URL and the screen agree. No banner is shown for
  // it, not even for the one render before the effect runs.
  const deepLinkIsFolder =
    deepLinkUnresolved && selectedPath !== null && sortedEntries.some((e) => e.is_dir && e.path === selectedPath)
  useEffect(() => {
    if (!deepLinkIsFolder || selectedPath === null) return
    setBrowsedDir(selectedPath)
    goTo(workspaceId, null)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deepLinkIsFolder, selectedPath])
  // A dot-prefixed target IS in the folder, just filtered out of the listing.
  // Saying "not found" there would be a plain falsehood, so it gets its own
  // wording and the action that fixes it.
  const deepLinkHiddenFromView =
    deepLinkUnresolved && selectedPath !== null && baseNameOf(selectedPath).startsWith('.') && !includeHidden
  const deepLinkMessage = !deepLinkUnresolved || deepLinkIsFolder
    ? null
    : deepLinkHiddenFromView
      ? `"${selectedPath}" is a hidden file. Turn on Show hidden to open it.`
      : `"${selectedPath}" was not found. Showing the folder that would contain it.`

  // The mounts visible at the CURRENT level. Only a first-segment name can
  // identify a mount, and the transfer destination is expressed relative to the
  // work-tree root, so this is the set the dialog needs to recognise one.
  // Mounts live at the work-tree ROOT, so the count must come from there rather
  // than from whatever folder is currently open — otherwise it silently reads
  // zero the moment you navigate into a subfolder, which is exactly when you
  // are least able to notice it is wrong. Same query key as the root listing,
  // so it is free while browsing the root.
  const rootEntriesQuery = useQuery({
    queryKey: libraryQueryKeys.entries(workspaceId ?? '', '', false),
    queryFn: () => fetchLibraryEntries(workspaceId as string, '', false),
    enabled: !!workspaceId,
  })
  const workspaceMounts = useMemo(
    () => (rootEntriesQuery.data ?? []).filter((e) => e.mount),
    [rootEntriesQuery.data],
  )

  const currentWorkspaceName =
    sortedWorkspaces.find((w) => w.id === workspaceId)?.name ?? workspaceId ?? ''

  /**
   * The ONE place workspace + selection change. In addressed mode it reports
   * the new address and changes nothing locally (the caller writes the URL and
   * the new address comes back as props); otherwise it writes the local state.
   * Routing every navigation through here is what keeps the two modes from
   * growing separate behaviour — and every caller has already cleared the
   * unsaved-edits guard by the time it gets here.
   */
  function goTo(nextWorkspaceId: string | null, nextPath: string | null) {
    if (addressed) {
      // Folder navigation with nothing selected leaves the address exactly as
      // it was. Reporting it anyway would have the caller push a history entry
      // identical to the current one, so leaving a folder would take as many
      // back presses as folders you had opened.
      const unchanged =
        (address?.workspaceId ?? null) === nextWorkspaceId && (address?.path ?? null) === nextPath
      if (unchanged) return
      onAddressChange?.({ workspaceId: nextWorkspaceId ?? undefined, path: nextPath ?? undefined })
      return
    }
    setInternalWorkspaceId(nextWorkspaceId)
    setInternalSelectedPath(nextPath)
  }

  function invalidateEntries(wsId: string) {
    void queryClient.invalidateQueries({ queryKey: ['library', wsId, 'entries'] })
  }
  function invalidateWorkspaces() {
    void queryClient.invalidateQueries({ queryKey: libraryQueryKeys.workspaces() })
  }

  // Adding a mount grants write access to a real folder on this machine, so a
  // failure here is surfaced verbatim in the dialog rather than as a toast that
  // scrolls away — the operator needs to read WHY a grant was refused.
  const addMountMutation = useMutation({
    mutationFn: (hostPath: string) => {
      if (!workspaceId) throw new Error('No workspace selected.')
      // The mount's name inside work/ is derived from the folder's own name,
      // so the operator is not asked to invent a second one for something they
      // just pointed at. The server owns uniqueness and rejects a collision —
      // deriving it here does not make it the client's rule.
      const name = mountNameFromPath(hostPath)
      if (!name) throw new Error('That path has no folder name to use.')
      return createWorkspaceMount(workspaceId, { host_path: hostPath, name })
    },
    onMutate: () => {
      setAddMountError(undefined)
      setAddMountRefusal(undefined)
    },
    onSuccess: (res) => {
      if (workspaceId) invalidateEntries(workspaceId)
      invalidateWorkspaces()
      // ADR-072 D1.2/FR-074a: the mount's first recognised skills directory
      // discloses what it grants, independent of the count threshold — a
      // 3-skill mount gets the same disclosure as a 500-skill one, just
      // without the extra ThresholdWarning line. See mountSkillsDisclosure's
      // doc comment: this is `null` today until the backend sends it.
      const disclosure = mountSkillsDisclosure(res)
      // D-117 response half: a broad grant is allowed but must never be
      // silent — the 201 body's `warning` renders in the Add-mount dialog
      // (held open) rather than a toast, which auto-dismisses exactly when
      // nobody is looking. Any skills disclosure from the same create waits
      // for the warning's acknowledgement so it cannot cover it.
      if (res.warning) {
        setAddMountWarning(res.warning)
        setPendingWarnedMount({
          name: res.name,
          disclosure: disclosure ? { ...disclosure, mountName: res.name } : null,
        })
        return
      }
      setAddMountOpen(false)
      addToast({ message: `Mounted "${res.name}".`, variant: 'success' })
      if (disclosure) {
        setSkillsDisclosure({ ...disclosure, mountName: res.name })
      }
    },
    onError: (err) => {
      // D-117 response half: the server's policy refusal (403) carries the
      // reason it refused; branch on the status rather than making the
      // operator guess whether "rejected" means policy or transport.
      //
      // UAT re-test (2026-09-13), U-21: this used to read `err.userMessage`
      // directly, but 403 is a "known" status for `ApiError.fromResponse`
      // (src/lib/api-error.ts), which deliberately OVERRIDES `userMessage`
      // with the generic "You don't have permission to perform this
      // action." for known statuses — the server's actual, specific reason
      // (e.g. naming the refused path) survives only on `.body`. The same
      // field-name mismatch libraryErrorMessage.ts already exists to fix for
      // Rename/Move/etc — reuse it here instead of reading userMessage.
      if (isApiError(err) && err.status === 403) {
        setAddMountRefusal(getLibraryErrorMessage(err, err.userMessage))
        return
      }
      setAddMountError(getLibraryErrorMessage(err, 'Could not mount that folder.'))
    },
  })

  // The "Done" click on a warned create: retire the warning, close the
  // dialog, and only then surface anything the same create has queued (the
  // success toast, the skills disclosure).
  function acknowledgeMountWarning() {
    const outcome = pendingWarnedMount
    setAddMountWarning(undefined)
    setPendingWarnedMount(null)
    setAddMountOpen(false)
    if (outcome) {
      addToast({ message: `Mounted "${outcome.name}".`, variant: 'success' })
      if (outcome.disclosure) setSkillsDisclosure(outcome.disclosure)
    }
  }

  const unmountMutation = useMutation({
    mutationFn: (name: string) => {
      if (!workspaceId) throw new Error('No workspace selected.')
      return deleteWorkspaceMount(workspaceId, name)
    },
    onMutate: () => {
      setUnmountError(undefined)
    },
    onSuccess: (_data, name) => {
      if (workspaceId) invalidateEntries(workspaceId)
      invalidateWorkspaces()
      setUnmountTarget(null)
      setUnmountError(undefined)
      addToast({ message: `Unmounted "${name}". Your files were not touched.`, variant: 'success' })
    },
    onError: (err) => {
      // This used to close the dialog AND rely on a toast that auto-dismisses
      // — a failed unmount looked identical to a successful one the moment
      // the toast scrolled away. The dialog now stays open (unmountTarget is
      // untouched here) with a persistent, actionable banner naming the
      // server's own reason, the same single-channel pattern Delete/Rename/
      // Move/New folder already use.
      setUnmountError(getLibraryErrorMessage(err, 'Could not unmount that folder.'))
    },
  })

  // UAT #701 / D-123 (2026-09-13): inside a knowledge base the Library's
  // delete goes to `.omnipus-vault/trash/` and its rename rewrites inbound
  // links, so the dialogs must say so. Same key KnowledgePanel already uses
  // for the browsed folder, so this is a cache hit whenever the panel is up.
  const browsedKnowledgeQuery = useQuery({
    queryKey: ['knowledge-base-info', workspaceId, browsedDir],
    queryFn: () => fetchKnowledgeBaseInfo(workspaceId as string, browsedDir),
    enabled: workspaceId !== null,
    staleTime: 30_000,
  })
  const browsedInOmnipusVault =
    browsedKnowledgeQuery.data?.is_knowledge_base === true && browsedKnowledgeQuery.data.marker === 'omnipus_vault'
  const isVaultNote = (e: LibraryEntry | null): boolean =>
    e !== null && !e.is_dir && browsedInOmnipusVault && /\.(md|markdown)$/i.test(e.name)

  const deleteMutation = useMutation({
    mutationFn: ({ wsId, entryPath }: { wsId: string; entryPath: string }) => deleteLibraryEntry(wsId, entryPath),
    onMutate: () => {
      setDeleteError(undefined)
    },
    onSuccess: (_data, vars) => {
      invalidateEntries(vars.wsId)
      invalidateWorkspaces()
      addToast({ message: isVaultNote(deleteTarget) ? 'Moved to the knowledge base’s trash.' : 'Deleted.', variant: 'success' })
      setDeleteTarget(null)
      setDeleteError(undefined)
      if (selectedPath === vars.entryPath) goTo(workspaceId, null)
    },
    onError: (err) => {
      // UAT re-test (2026-09-13), U-58: a refused delete (e.g. the server's
      // reserved-location guard on a knowledge base's .omnipus-vault marker)
      // used to show NOTHING — no banner, no toast — so a failed, destructive
      // action looked exactly like a click that did nothing. The dialog stays
      // open (deleteTarget is untouched here) with a persistent, actionable
      // banner naming the server's own reason, the same single-channel
      // pattern Rename/Move/New folder already use — no duplicate toast.
      setDeleteError(getLibraryErrorMessage(err, 'Delete failed'))
    },
  })

  const renameMutation = useMutation({
    mutationFn: ({ wsId, from, to }: { wsId: string; from: string; to: string }) =>
      renameLibraryEntry(wsId, { from, to }),
    onMutate: () => {
      setRenameError(undefined)
    },
    onSuccess: (updated, vars) => {
      invalidateEntries(vars.wsId)
      addToast({ message: 'Renamed.', variant: 'success' })
      setRenameTarget(null)
      setRenameError(undefined)
      // Renaming the open file follows it to its new path — which in
      // addressed mode also keeps the URL pointing at the file the user is
      // still looking at, rather than at a name that no longer exists.
      if (selectedPath === vars.from) goTo(workspaceId, updated.path)
    },
    onError: (err) => {
      // Never silently swallowed: the dialog stays open (renameTarget is
      // untouched here) with a persistent, actionable banner naming the
      // server's own reason (permissions, transient network, a genuine
      // collision the client-side check couldn't anticipate).
      //
      // UAT fix (Dana, re-verified v8): this used to ALSO fire a toast for
      // the same error — "two simultaneous displays... is noise" (the
      // tester's words, about the identical pattern on Move). The dialog is
      // already open and the banner sits right next to the input the user
      // is looking at, so the toast added nothing but noise; picking the
      // inline surface nearest the input.
      setRenameError(getLibraryErrorMessage(err, 'Rename failed'))
    },
  })

  const transferMutation = useMutation({
    mutationFn: ({ mode, body }: { mode: 'move' | 'copy'; body: LibraryTransferRequest }) =>
      mode === 'move' ? moveLibraryEntry(body) : copyLibraryEntry(body),
    onMutate: () => {
      setTransferError(undefined)
    },
    onSuccess: (_data, vars) => {
      invalidateEntries(vars.body.from_workspace_id)
      invalidateEntries(vars.body.to_workspace_id)
      invalidateWorkspaces()
      addToast({ message: vars.mode === 'move' ? 'Moved.' : 'Copied.', variant: 'success' })
      if (vars.mode === 'move' && selectedPath === vars.body.from_path) {
        goTo(workspaceId, null)
      }
      setTransferTarget(null)
      setTransferError(undefined)
    },
    onError: (err, vars) => {
      // Dialog stays open (transferTarget untouched) with the destination
      // fields exactly as typed, plus a persistent, actionable banner —
      // preferring the server's own guidance (getLibraryErrorMessage) over
      // a generic "not found", e.g. naming the specific missing destination
      // directory for a 404 rather than swallowing it.
      //
      // UAT fix (Dana, re-verified v8, exact repro): moving onto a missing
      // destination parent used to show the SAME generic message in a toast
      // AND this banner at once — "two simultaneous displays... is noise".
      // The banner alone (nearest the input) is now the one channel.
      setTransferError(getLibraryErrorMessage(err, `${vars.mode === 'copy' ? 'Copy' : 'Move'} failed`))
    },
  })

  const uploadMutation = useMutation({
    mutationFn: ({ wsId, files, dir }: { wsId: string; files: File[]; dir: string }) =>
      uploadLibraryFiles(wsId, files, dir),
    onMutate: () => {
      setUploadError(undefined)
    },
    onSuccess: (data, vars) => {
      invalidateEntries(vars.wsId)
      invalidateWorkspaces()
      setUploadError(undefined)
      addToast({
        message: uploadOutcomeMessage(
          vars.files.map((f) => f.name),
          data.entries.map((e) => e.name),
        ),
        variant: 'success',
      })
    },
    onError: (err) => {
      // No dialog to keep open here (upload is a direct file-picker action,
      // not a form) — the toolbar-level banner (rendered below) is the
      // persistent surface, dismissible since there's no "retry" step that
      // would otherwise clear it.
      const message = getLibraryErrorMessage(err, 'Upload failed')
      setUploadError(message)
      addToast({ message, variant: 'error' })
    },
  })

  const mkdirMutation = useMutation({
    mutationFn: ({ wsId, dirPath }: { wsId: string; dirPath: string }) => mkdirLibraryEntry(wsId, { path: dirPath }),
    onMutate: () => {
      setNewFolderError(undefined)
    },
    onSuccess: (_entry, vars) => {
      invalidateEntries(vars.wsId)
      invalidateWorkspaces()
      addToast({ message: 'Folder created.', variant: 'success' })
      setNewFolderOpen(false)
      setNewFolderError(undefined)
    },
    onError: (err) => {
      // Same single-channel (banner-only, no toast) treatment as
      // Rename/Move now use — the dialog stays open with a persistent,
      // actionable banner naming the server's own reason (e.g. a 409
      // because a regular FILE already occupies that name).
      setNewFolderError(getLibraryErrorMessage(err, 'Could not create folder'))
    },
  })

  // UAT #699 / D-115 (2026-09-13). The note is created through the same
  // compare-and-swap PUT the editor saves with, sending the ABSENT version
  // token: a name that is already taken comes back as a 409 with the
  // server's own reason, never an overwrite. On success the new file is
  // SELECTED (goTo with its path), so the reader lands in it — the pane's
  // Edit button is one click away and the view shows the seeded title.
  const newNoteMutation = useMutation({
    mutationFn: ({ wsId, path, content }: { wsId: string; path: string; content: string }) =>
      createLibraryTextFile(wsId, path, content),
    onMutate: () => {
      setNewNoteError(undefined)
    },
    onSuccess: (result, vars) => {
      invalidateEntries(vars.wsId)
      invalidateWorkspaces()
      setNewNoteOpen(false)
      setNewNoteError(undefined)
      addToast({ message: `Created ${result.data.name}.`, variant: 'success' })
      goTo(vars.wsId, result.data.path)
    },
    onError: (err) => {
      setNewNoteError(getLibraryErrorMessage(err, 'Could not create the note'))
    },
  })

  function openNewNoteDialog() {
    setNewNoteError(undefined)
    setNewNoteOpen(true)
  }

  function handleCreateNote(fileName: string) {
    if (!workspaceId) return
    const path = browsedDir ? `${browsedDir}/${fileName}` : fileName
    const title = fileName.replace(/\.(md|markdown)$/i, '')
    newNoteMutation.mutate({ wsId: workspaceId, path, content: `# ${title}\n\n` })
  }

  function openRenameDialog(entry: LibraryEntry) {
    setRenameError(undefined)
    setRenameTarget(entry)
  }
  function openDeleteDialog(entry: LibraryEntry) {
    setDeleteError(undefined)
    setDeleteTarget(entry)
  }
  function openUnmountDialog(entry: LibraryEntry) {
    setUnmountError(undefined)
    setUnmountTarget(entry)
  }
  function openTransferDialog(entry: LibraryEntry, mode: 'move' | 'copy') {
    setTransferError(undefined)
    setTransferTarget({ entry, mode })
  }
  function openNewFolderDialog() {
    setNewFolderError(undefined)
    setNewFolderOpen(true)
  }

  function handleOpenWorkspaceNode(node: LibraryWorkspaceNode) {
    if (!confirmDiscardLibraryEdits()) return
    setBrowsedDir('')
    goTo(node.id, null)
  }
  function handleGoRoot() {
    if (!confirmDiscardLibraryEdits()) return
    setBrowsedDir('')
    goTo(null, null)
  }
  function handleGoWorkspaceRoot() {
    if (!confirmDiscardLibraryEdits()) return
    setBrowsedDir('')
    goTo(workspaceId, null)
  }
  function handleOpenDirectory(entry: LibraryEntry) {
    if (!confirmDiscardLibraryEdits()) return
    setBrowsedDir(entry.path)
    goTo(workspaceId, null)
  }
  function handleBreadcrumbSegment(index: number, segments: string[]) {
    if (!confirmDiscardLibraryEdits()) return
    setBrowsedDir(segments.slice(0, index + 1).join('/'))
    goTo(workspaceId, null)
  }
  // Selecting a file that's ALREADY selected is not navigation (no editor
  // would be discarded), so it skips the guard entirely rather than prompting
  // to confirm leaving the file the user is already looking at.
  function handleSelectFile(entry: LibraryEntry) {
    if (selectedPath === entry.path) return
    if (!confirmDiscardLibraryEdits()) return
    goTo(workspaceId, entry.path)
  }
  function handleDownload(entry: LibraryEntry) {
    if (!workspaceId) return
    const url = libraryDownloadUrl(workspaceId, entry.path)
    const a = document.createElement('a')
    a.href = url
    a.download = entry.name
    a.rel = 'noopener'
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
  }
  function handleFileInputChange(e: React.ChangeEvent<HTMLInputElement>) {
    const files = e.target.files
    if (!files || files.length === 0 || !workspaceId) return
    uploadMutation.mutate({ wsId: workspaceId, files: Array.from(files), dir: browsedDir })
    e.target.value = ''
  }
  function handleCreateFolder(name: string) {
    if (!workspaceId) return
    const dirPath = browsedDir ? `${browsedDir}/${name}` : name
    mkdirMutation.mutate({ wsId: workspaceId, dirPath })
  }

  const pathSegments = browsedDir ? browsedDir.split('/').filter(Boolean) : []
  // library-spec.md D-1: work/.library/ is the reserved, server-managed home
  // for chat-uploaded attachments (not user-organized files). Uploading or
  // creating new folders into it from the explorer itself would silently mix
  // user files into that internal namespace — flagged by live UAT as
  // something to deliberately decide on rather than leave as an accident.
  // Decision: block it client-side (disable Upload/New Folder while inside
  // .library or any of its subdirectories) rather than let it through
  // silently; existing entries already inside .library are still fully
  // browsable/renamable/downloadable/deletable — only adding NEW content via
  // these two actions is restricted.
  const isReservedLibraryDir = browsedDir === '.library' || browsedDir.startsWith('.library/')

  return (
    <div className={cn('flex h-full flex-col', className)} data-testid="library-explorer">
      {/* Toolbar / breadcrumb row */}
      <div className="flex items-center gap-2 px-3 h-chrome-header min-h-chrome-header shrink-0 border-b border-[var(--color-border)] bg-[var(--color-surface-1)]">
        <nav aria-label="Library breadcrumb" className="flex items-center gap-1 min-w-0 flex-1 text-[length:var(--type-body-compact-size)] overflow-hidden">
          <button
            type="button"
            tabIndex={0}
            onClick={handleGoRoot}
            data-testid="library-crumb-root"
            className={cn(
              'flex items-center gap-1.5 shrink-0 rounded px-1.5 py-1 transition-colors',
              workspaceId === null
                ? 'text-[var(--color-accent)] font-medium'
                : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]',
            )}
          >
            <Files size={14} />
            Library
          </button>
          {workspaceId !== null && (
            <>
              <CaretRight size={12} className="text-[var(--color-muted)] shrink-0" aria-hidden="true" />
              <button
                type="button"
                tabIndex={0}
                onClick={handleGoWorkspaceRoot}
                data-testid="library-crumb-workspace"
                className={cn(
                  'truncate rounded px-1.5 py-1 transition-colors min-w-0',
                  pathSegments.length === 0
                    ? 'text-[var(--color-accent)] font-medium'
                    : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]',
                )}
              >
                {currentWorkspaceName}
              </button>
            </>
          )}
          {pathSegments.map((seg, i) => (
            <span key={`${seg}-${i}`} className="flex items-center gap-1 min-w-0">
              <CaretRight size={12} className="text-[var(--color-muted)] shrink-0" aria-hidden="true" />
              <button
                type="button"
                tabIndex={0}
                onClick={() => handleBreadcrumbSegment(i, pathSegments)}
                className={cn(
                  'truncate rounded px-1.5 py-1 transition-colors min-w-0',
                  i === pathSegments.length - 1
                    ? 'text-[var(--color-accent)] font-medium'
                    : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]',
                )}
              >
                {seg}
              </button>
            </span>
          ))}
        </nav>

        <div className="flex items-center gap-1 shrink-0">
          {workspaceId !== null && (
            <label
              htmlFor="library-show-hidden"
              className="flex items-center gap-1.5 text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] mr-1 select-none cursor-pointer"
            >
              <Switch
                id="library-show-hidden"
                data-testid="library-show-hidden-toggle"
                checked={includeHidden}
                onCheckedChange={setIncludeHidden}
              />
              Show hidden
            </label>
          )}
          {/* Status indicator only — the ACTION that used to live on this
              pill ("Manage mounted folders") moved into the unified "+"
              create menu below, alongside New folder / Upload / Add mount /
              New vault / New workspace (feature C2). This stays a passive
              at-a-glance readout (count + broad-grant color) rather than a
              second path to the same dialog the menu already opens. */}
          {workspaceId !== null && workspaceMounts.length > 0 && (
            <span
              title="Folders on your Mac mounted into this workspace"
              data-testid="library-mounts-count"
              className="flex items-center gap-1.5 rounded px-1.5 py-1 text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]"
            >
              <span
                className={`h-1.5 w-1.5 rounded-full ${
                  workspaceMounts.some((e) => e.mount?.broad)
                    ? 'bg-[var(--color-warning)]'
                    : 'bg-[var(--color-info)]'
                }`}
              />
              {workspaceMounts.length} mounted
            </span>
          )}
          {workspaceId !== null && (
            <input
              tabIndex={0}
              ref={fileInputRef}
              type="file"
              multiple
              onChange={handleFileInputChange}
              data-testid="library-upload-input"
              className="hidden"
            />
          )}
          <LibraryCreateMenu
            workspaceId={workspaceId}
            browsedDir={browsedDir}
            isReservedLibraryDir={isReservedLibraryDir}
            mountedCount={workspaceMounts.length}
            uploadPending={uploadMutation.isPending}
            onNewFolder={openNewFolderDialog}
            onNewNote={openNewNoteDialog}
            onAddMount={() => setAddMountOpen(true)}
            onManageMounts={() => setMountsOpen(true)}
            onUpload={() => fileInputRef.current?.click()}
            onVaultCreated={(entry) => {
              if (!workspaceId) return
              setBrowsedDir(entry.path)
              goTo(workspaceId, null)
            }}
          />
          {onPopOut && (
            <button
              type="button"
              tabIndex={0}
              onClick={() => {
                if (confirmDiscardLibraryEdits()) onPopOut()
              }}
              aria-label="Open Library in a new tab"
              title="Open in new tab"
              data-testid="library-popout-button"
              className="rounded p-1.5 text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] transition-colors"
            >
              <ArrowSquareOut size={16} />
            </button>
          )}
          {onClose && (
            <button
              type="button"
              tabIndex={0}
              onClick={() => {
                if (confirmDiscardLibraryEdits()) onClose()
              }}
              aria-label="Close Library"
              title="Close"
              data-testid="library-close-button"
              className="rounded p-1.5 text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] transition-colors"
            >
              <X size={16} />
            </button>
          )}
        </div>
      </div>

      {/* Upload error — persistent (not a fire-and-forget toast alone) and
          dismissible, since upload has no dialog of its own to keep open the
          way Rename/Move do. A failed upload must never look identical to
          nothing happening. */}
      {uploadError && (
        <div className="shrink-0 p-2 pb-0">
          <LibraryErrorBanner
            message={uploadError}
            onDismiss={() => setUploadError(undefined)}
            testId="library-upload-error"
          />
        </div>
      )}

      {/* Deep link that resolved to nothing (US-3 AS-5). Not dismissible on
          purpose: it is a statement about the address currently on screen, so
          it clears itself the moment that address changes — a dismiss button
          would only let it disagree with the URL. */}
      {deepLinkMessage && (
        <div className="shrink-0 p-2 pb-0">
          <LibraryErrorBanner message={deepLinkMessage} testId="library-deeplink-unresolved" />
        </div>
      )}

      {/* ── Knowledge base (ADR-067 US-4) ────────────────────────────────────
          The first-run/indexing-status surface: it sits above the listing of
          the folder it is describing, so "is this a collection, and is its
          index current?" is answered where the folder is, rather than on a
          screen of its own (no new top-level screen). Search itself lives in
          LibrarySearchBar below, not here (unified-search-and-grep-spec.md
          US-5 — a vault folder used to carry two search boxes; MV-10 keeps
          it to exactly one).

          Mounted only when a workspace is open — the virtual root lists
          workspaces, not files, so there is no folder to ask about. The panel
          itself decides what to say; it renders nothing at all for a folder
          whose index is finished and current.

          `progress` is not passed here because it is no longer this file's to
          pass: the knowledge_index_progress WS frame is routed by
          src/store/chat.ts into src/store/knowledgeIndex.ts, and KnowledgePanel
          reads the frame for its own collection_id from there. Do not add a
          poll — the frame is the contract's answer to progress (FR-080). */}
      {workspaceId !== null && (
        <div className="shrink-0 p-2 pb-0">
          {/* UAT #699 / D-115: the empty-collection state's "Write the first
              note" button is live only when a handler is wired; with none the
              panel used to say nothing at all about how a note gets made. */}
          <KnowledgePanel
            workspaceId={workspaceId}
            path={browsedDir}
            {...(!isReservedLibraryDir ? { onCreateNote: openNewNoteDialog } : {})}
          />
          {/* UAT D-122 (2026-09-13): a folder with `.obsidian/` and no
              `.omnipus-vault/` is detected and its notes are indexed, but
              every search reported Records 0 / Views 0 with nothing saying
              why — the zeros looked like an empty vault rather than an
              un-imported one. The import is CLI-only today (Appendix B /
              FR-103), so that is stated rather than hidden. */}
          {browsedKnowledgeQuery.data?.is_knowledge_base === true &&
            browsedKnowledgeQuery.data.marker === 'obsidian' && (
              <div
                role="status"
                data-testid="library-obsidian-not-imported"
                className="mt-2 rounded-md border border-[var(--color-accent)]/30 bg-[var(--color-accent)]/5 px-3 py-2 text-[length:var(--type-utility-xs-size)] leading-relaxed text-[var(--color-muted)]"
              >
                This is an Obsidian vault that has not been imported into Omnipus yet. Its notes are
                indexed and searchable, but record types, records and views stay at zero until the
                vault is imported — today that is done from the command line with{' '}
                <span className="font-mono text-[var(--color-secondary)]">omnipus records import-obsidian</span>
                . Importing writes an <span className="font-mono">.omnipus-vault</span> folder here and never
                changes the <span className="font-mono">.obsidian</span> one.
              </div>
            )}
        </div>
      )}

      {/* ── List + preview split ────────────────────────────────────────────
          Stacked in the docked aside, side-by-side in the fullscreen tab. In
          BOTH the list stays visible and clickable while a file is open, which
          is the in-app navigation path confirmDiscardLibraryEdits() guards. */}
      <div className={cn('flex min-h-0 flex-1', isSplit ? 'flex-row' : 'flex-col')}>
      {/* Body */}
      <div
        className={cn(
          'min-h-0 min-w-0 overflow-y-auto p-2 relative',
          // Preview open: it takes the larger share (60% split / 55% stacked —
          // the stacked figure is the old even split plus the 10% the operator
          // asked for). Closed: the list has the whole box to itself.
          !previewOpen ? 'flex-1' : isSplit ? 'flex-[40]' : 'flex-[45]',
        )}
      >
        {workspaceId === null ? (
          // US-4 AS-2: the bar renders in EVERY Library location, disabled at
          // the virtual root (workspaceId null) with its explanatory
          // placeholder. It wraps the workspace list rather than replacing it
          // — LibrarySearchBar renders `children` untouched whenever it is
          // disabled — so "one bar, everywhere" is literally true instead of
          // true only once a workspace is open.
          <LibrarySearchBar workspaceId={null} folderPath="" onOpenNote={() => {}}>
            {workspacesQuery.isLoading && <ListSkeleton />}
            {workspacesQuery.isError && (
              <QueryErrorState
                layout="fill"
                message="Could not load workspaces."
                onRetry={() => void workspacesQuery.refetch()}
                testId="library-workspaces-error"
              />
            )}
            {!workspacesQuery.isLoading && !workspacesQuery.isError && sortedWorkspaces.length === 0 && (
              <EmptyState icon={<Buildings size={28} />} message="No workspaces yet." />
            )}
            {!workspacesQuery.isLoading &&
              !workspacesQuery.isError &&
              sortedWorkspaces.map((node) => (
                <button
                  key={node.id}
                  type="button"
                  tabIndex={0}
                  onClick={() => handleOpenWorkspaceNode(node)}
                  data-testid={`library-workspace-node-${node.id}`}
                  className="flex w-full items-center gap-3 rounded-lg px-3 py-2 hover:bg-[var(--color-surface-2)] text-left transition-colors"
                >
                  {/* Icon-consistency pass (2026-09-07): every surface that
                      names a workspace uses the same Phosphor Buildings glyph
                      — the sidebar workspace list and the workspace tab bar
                      both moved off Tray to this same icon, so a workspace no
                      longer wears three different glyphs depending on where
                      it is shown. */}
                  <Buildings size={18} className="text-[var(--color-accent)] shrink-0" />
                  <span className="flex-1 truncate text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">{node.name}</span>
                  <span className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] shrink-0">
                    {node.entry_count} item{node.entry_count === 1 ? '' : 's'}
                  </span>
                </button>
              ))}
          </LibrarySearchBar>
        ) : (
          // C1 — persistent Library search bar (library-b-c-design-2026-09-07
          // §C1). LibrarySearchBar owns the input, the segmented filter, and
          // the grouped results; `children` (this folder's existing listing,
          // unchanged) renders exactly as before whenever no query is active,
          // and is replaced entirely by results while one is.
          <LibrarySearchBar
            workspaceId={workspaceId}
            folderPath={browsedDir}
            onOpenNote={(workspacePath) => {
              if (!confirmDiscardLibraryEdits()) return
              goTo(workspaceId, workspacePath)
            }}
            onOpenFolder={(workspacePath) => {
              // Finding S1: report back whether navigation actually
              // happened. LibrarySearchBar only clears its query/results
              // once it KNOWS this returned true — a "Cancel" on the
              // discard-unsaved-edits prompt must leave the search exactly
              // as the user left it, not wipe it as a side effect of a
              // navigation that never occurred.
              if (!confirmDiscardLibraryEdits()) return false
              setBrowsedDir(workspacePath)
              goTo(workspaceId, null)
              return true
            }}
          >
            {entriesQuery.isLoading && <ListSkeleton />}
            {entriesQuery.isError && (
              <QueryErrorState
                layout="fill"
                message="Could not load this folder."
                onRetry={() => void entriesQuery.refetch()}
                testId="library-entries-error"
              />
            )}
            {!entriesQuery.isLoading && !entriesQuery.isError && sortedEntries.length === 0 && (
              <EmptyState
                icon={<FolderOpen size={28} />}
                message={browsedDir ? 'This folder is empty.' : 'No files in this workspace yet.'}
              />
            )}
            {!entriesQuery.isLoading &&
              !entriesQuery.isError &&
              sortedEntries.map((entry) => (
                <LibraryEntryRow
                  key={entry.path}
                  workspaceId={workspaceId}
                  entry={entry}
                  selected={selectedPath === entry.path}
                  onOpenDirectory={handleOpenDirectory}
                  onSelectFile={handleSelectFile}
                  onDownload={handleDownload}
                  onRename={openRenameDialog}
                  onTransfer={openTransferDialog}
                  onDelete={openDeleteDialog}
                  onUnmount={openUnmountDialog}
                />
              ))}
          </LibrarySearchBar>
        )}
      </div>

      {/* ── Preview / edit pane (library-spec.md D-5) ─────────────────────── */}
      {previewOpen && (
        <div
          className={cn(
            'min-h-0 min-w-0 border-[var(--color-border)]',
            isSplit ? 'flex-[60] border-l' : 'flex-[55] border-t',
          )}
          data-testid="library-preview-pane-wrapper"
        >
          <LibraryPreviewPane
            workspaceId={workspaceId}
            entry={selectedEntry}
            onClose={() => {
              if (!confirmDiscardLibraryEdits()) return
              goTo(workspaceId, null)
            }}
            onDownload={handleDownload}
            // FR-012 / US-7 AS-7: following a wikilink, a relative link or a
            // linked mention inside an open note swaps the pane to the target
            // AND updates the address, so the note the reader is looking at is
            // the note the URL names.
            onOpenNote={(workspacePath) => {
              if (!confirmDiscardLibraryEdits()) return
              goTo(workspaceId, workspacePath)
            }}
          />
        </div>
      )}
      </div>

      {/* ── Rename dialog ────────────────────────────────────────────────── */}
      <LibraryRenameDialog
        open={renameTarget !== null}
        onOpenChange={(open) => {
          if (!open) {
            setRenameTarget(null)
            setRenameError(undefined)
          }
        }}
        entry={renameTarget}
        rewritesLinks={isVaultNote(renameTarget)}
        siblingNames={new Set(sortedEntries.filter((e) => e.path !== renameTarget?.path).map((e) => e.name))}
        isPending={renameMutation.isPending}
        error={renameError}
        onSubmit={(to) => {
          if (!workspaceId || !renameTarget) return
          renameMutation.mutate({ wsId: workspaceId, from: renameTarget.path, to })
        }}
      />

      {/* ── New Folder dialog (creates inside the CURRENT directory) ───────── */}
      <LibraryNewFolderDialog
        open={newFolderOpen}
        onOpenChange={(open) => {
          setNewFolderOpen(open)
          if (!open) setNewFolderError(undefined)
        }}
        siblingNames={new Set(sortedEntries.map((e) => e.name))}
        isPending={mkdirMutation.isPending}
        error={newFolderError}
        onSubmit={handleCreateFolder}
      />

      {/* ── New note dialog (UAT #699 / D-115; creates inside the CURRENT directory) ── */}
      <LibraryNewNoteDialog
        open={newNoteOpen}
        onOpenChange={(open) => {
          setNewNoteOpen(open)
          if (!open) setNewNoteError(undefined)
        }}
        siblingNames={new Set(sortedEntries.map((e) => e.name))}
        isPending={newNoteMutation.isPending}
        error={newNoteError}
        onSubmit={handleCreateNote}
      />

      {/* ── Move / Copy dialog (D-9, cross-workspace) ──────────────────────── */}
      <LibraryTransferDialog
        open={transferTarget !== null}
        onOpenChange={(open) => {
          if (!open) {
            setTransferTarget(null)
            setTransferError(undefined)
          }
        }}
        mode={transferTarget?.mode ?? 'move'}
        entry={transferTarget?.entry ?? null}
        sourceWorkspaceId={workspaceId ?? ''}
        workspaces={sortedWorkspaces}
        isPending={transferMutation.isPending}
        error={transferError}
        onSubmit={(body) => {
          if (!transferTarget) return
          transferMutation.mutate({ mode: transferTarget.mode, body })
        }}
      />

      {/* ── Delete confirm (destructive action — hard confirm, no silent data loss) ── */}
      <LibraryMountsDialog
        open={mountsOpen}
        onOpenChange={setMountsOpen}
        mounts={workspaceMounts}
        workspaceId={workspaceId}
        workspaceName={
          sortedWorkspaces.find((w) => w.id === workspaceId)?.name ?? 'this workspace'
        }
        onUnmount={(entry) => {
          setMountsOpen(false)
          openUnmountDialog(entry)
        }}
        isPending={unmountMutation.isPending}
      />

      <LibraryAddMountDialog
        open={addMountOpen}
        onOpenChange={(open) => {
          setAddMountOpen(open)
          if (!open) {
            setAddMountError(undefined)
            setAddMountRefusal(undefined)
            setAddMountWarning(undefined)
            setPendingWarnedMount(null)
          }
        }}
        onConfirm={(hostPath) => addMountMutation.mutate(hostPath)}
        isPending={addMountMutation.isPending}
        error={addMountError}
        refusal={addMountRefusal}
        createdWarning={addMountWarning}
        onAcknowledgeWarning={acknowledgeMountWarning}
      />

      {/* ADR-072 D1.2/FR-074/FR-074a: the just-created mount's recognised
          skills directory disclosure — grants message always shown when the
          mount carries any skills (even a handful), the threshold warning
          only when it exceeds the configured count. A dedicated dialog
          rather than folded into the mount-success toast because a toast
          auto-dismisses and this is a standing grant of auto-loadable agent
          instructions the operator needs to actually register. */}
      <AlertDialog
        open={skillsDisclosure !== null}
        onOpenChange={(open) => !open && setSkillsDisclosure(null)}
      >
        <AlertDialogContent data-testid="library-mount-skills-disclosure-dialog">
          <AlertDialogHeader>
            <AlertDialogTitle>
              &ldquo;{skillsDisclosure?.mountName}&rdquo; grants {skillsDisclosure?.count}{' '}
              skill{skillsDisclosure?.count === 1 ? '' : 's'}
            </AlertDialogTitle>
            <AlertDialogDescription className="space-y-2">
              <span className="block">{skillsDisclosure?.grantsMessage}</span>
              {skillsDisclosure?.thresholdWarning && (
                <span
                  className="block text-[var(--color-warning)]"
                  data-testid="library-mount-skills-threshold-warning"
                >
                  {skillsDisclosure.thresholdWarning}
                </span>
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogAction size="sm" onClick={() => setSkillsDisclosure(null)}>
              Got it
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* Unmount is deliberately NOT the delete dialog with different words.
          The whole risk is that the two read alike, so this one states what
          survives, in the affirmative, before the confirm button. */}
      <AlertDialog
        open={unmountTarget !== null}
        onOpenChange={(open) => {
          if (!open) {
            setUnmountTarget(null)
            setUnmountError(undefined)
          }
        }}
      >
        <AlertDialogContent data-testid="library-unmount-dialog">
          <AlertDialogHeader>
            <AlertDialogTitle>Unmount &ldquo;{unmountTarget?.name}&rdquo;?</AlertDialogTitle>
            <AlertDialogDescription>
              This removes the workspace&rsquo;s access to the folder.{' '}
              <strong>Every file stays exactly where it is</strong>, at{' '}
              <span className="font-mono">{unmountTarget?.mount?.host_path}</span>. Nothing is
              deleted from your disk. You can mount it again at any time.
            </AlertDialogDescription>
          </AlertDialogHeader>
          {unmountError && (
            <LibraryErrorBanner message={unmountError} testId="library-unmount-dialog-error" />
          )}
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={unmountMutation.isPending}
              data-testid="library-unmount-confirm"
              onClick={() => {
                const name = unmountTarget?.mount?.name
                if (name) unmountMutation.mutate(name)
              }}
            >
              Unmount
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) {
            setDeleteTarget(null)
            setDeleteError(undefined)
          }
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {deleteTarget?.name === VAULT_MARKER_DIR
                ? 'Delete this knowledge base’s settings?'
                : isVaultNote(deleteTarget)
                  ? 'Move note to trash?'
                  : `Delete ${deleteTarget?.is_dir ? 'folder' : 'file'}?`}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {deleteTarget
                ? deleteTarget.name === VAULT_MARKER_DIR
                  ? // UAT D-121 (2026-09-13): the marker folder IS the
                    // knowledge base. The generic folder wording said
                    // nothing about that, and the UI cannot recreate a
                    // knowledge base over the same folder afterwards.
                    `"${VAULT_MARKER_DIR}" is what makes "${parentFolderName(deleteTarget.path)}" a knowledge base. Deleting it removes its record types, id sequences, saved views and trash — the notes stay as plain files, but the folder stops being a knowledge base and cannot be made one again from here. This cannot be undone.`
                  : isVaultNote(deleteTarget)
                    ? `"${deleteTarget.name}" will be moved to this knowledge base’s trash. Links to it from other notes will stop resolving until it is restored; an agent can restore it from the trash.`
                    : deleteTarget.is_dir
                      ? `"${deleteTarget.name}" and everything inside it will be permanently deleted. This cannot be undone.`
                      : `"${deleteTarget.name}" will be permanently deleted. This cannot be undone.`
                : ''}
            </AlertDialogDescription>
          </AlertDialogHeader>
          {deleteError && (
            <LibraryErrorBanner message={deleteError} testId="library-delete-dialog-error" />
          )}
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={deleteMutation.isPending}
              data-testid="library-delete-confirm"
              onClick={() => {
                if (!workspaceId || !deleteTarget) return
                deleteMutation.mutate({ wsId: workspaceId, entryPath: deleteTarget.path })
              }}
            >
              {deleteMutation.isPending ? 'Deleting…' : isVaultNote(deleteTarget) ? 'Move to trash' : 'Delete'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function ListSkeleton() {
  return (
    <div className="flex flex-col gap-1.5 p-1" data-testid="library-loading-skeleton">
      {[1, 2, 3, 4].map((i) => (
        <div
          key={i}
          className="h-9 rounded-lg bg-[var(--color-surface-2)] animate-pulse"
          style={{ width: `${70 + i * 6}%` }}
        />
      ))}
    </div>
  )
}

/** The knowledge-base marker directory's name (pkg/knowledge's VaultDir). */
const VAULT_MARKER_DIR = '.omnipus-vault'

/** The display name of the folder that contains `path`, or the workspace root. */
function parentFolderName(path: string): string {
  const parts = path.split('/').filter(Boolean)
  return parts.length >= 2 ? parts[parts.length - 2] : 'this workspace'
}

function EmptyState({ icon, message }: { icon: React.ReactNode; message: string }) {
  return (
    <div className="absolute inset-0 flex flex-col items-center justify-center gap-2 p-8 text-center text-[var(--color-muted)]">
      {icon}
      <p className="text-[length:var(--type-body-compact-size)]">{message}</p>
    </div>
  )
}

// LibraryDetailsStrip (the narrow metadata-only placeholder this task
// replaces) is gone — LibraryPreviewPane.tsx now owns the entry header AND
// the actual content surface. See the mount site above.
