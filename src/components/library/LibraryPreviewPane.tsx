// LibraryPreviewPane — the Library preview + edit pane (library-spec.md D-5 /
// section 4; ADR-067 D15 / spec STAGE 1). Mounted by LibraryExplorer.tsx into
// its "PREVIEW/EDIT PANE PLACEHOLDER" slot.
//
// RENDER-FIRST (ADR-067 D15): the pane shows the ARTIFACT, not its source.
// Source appears only after pressing Edit. Owns the entry header (name +
// Close, plus the slot the editable bodies portal their view/edit/save
// controls into) and the content surface, chosen by `classifyLibraryEntry`:
//
//   image/video/audio -> plain <img>/<video controls>/<audio controls>, no
//                   content fetch needed (the raw download URL IS the source).
//   pdf          -> LibraryPdfPreview (PDF.js, drawn by our own SPA into a
//                   canvas — a PDF never becomes a browser document, D15.1).
//   html         -> a SANDBOXED <iframe src> on the preview-token path, with
//                   Edit revealing the markup (US-1 AS-1/AS-2). See
//                   LibraryHtmlFrame below for the isolation contract.
//   markdown     -> LibraryMarkdownPreview (HistoricalMessageMarkdown + Mermaid)
//   mermaid      -> LibraryMermaidPreview (MermaidDiagram)
//   text         -> LibraryCodePreview (ShikiCodeBlock + CodeMirror)
//   other        -> LibraryDownloadCard
//
// THE DISPATCH IS AN EXHAUSTIVE SWITCH, NOT AN `&&` CHAIN. It used to be a
// chain of `{kind === 'x' && <X/>}` lines, which meant a widened
// `LibraryPreviewKind` compiled clean and rendered an EMPTY PANE — spec
// SC-017, the exact failure this stage would otherwise have shipped while
// adding three kinds at once. `renderBody`'s `default` branch assigns `kind`
// to `never`, so an unhandled member is now a compile error, and it still
// returns the download card at runtime so a hypothetical unhandled kind
// degrades to something honest rather than to blank space. Test 89
// (`TestLibraryPreviewPane_NoUnhandledKind`) guards the runtime half, because
// the `never` check disappears the moment someone reintroduces a chain.
//
// For the text kinds, GET .../content's `is_text`/`too_large` flags are the
// AUTHORITATIVE check (LibraryEntry.is_text_editable, used to reach "text" in
// the first place, is only a best-effort listing-time hint per its own schema
// doc) — either flag failing falls back to LibraryDownloadCard rather than
// rendering garbage. HTML deliberately does NOT take that fallback: the frame
// renders from the token path and needs no content at all, so a too-large or
// unreadable HTML file still RENDERS and merely loses its Edit affordance.

import { useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { X, SpinnerGap, ShieldWarning, ArrowClockwise, WarningCircle } from '@phosphor-icons/react'
import { QueryErrorState } from '@/components/shared/QueryErrorState'
import { fetchLibraryContent, libraryDownloadUrl, libraryQueryKeys } from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'
import type {
  LibraryPreviewTokenRequest,
  LibraryPreviewTokenResponse,
} from '@/lib/api/generated/openapi-types'
import { classifyLibraryEntry } from './preview/libraryPreviewKind'
import { BasePreview } from './preview/BasePreview'
import { LibraryImagePreview } from './preview/LibraryImagePreview'
import { LibraryVideoPreview } from './preview/LibraryVideoPreview'
import { LibraryPdfPreview } from './preview/LibraryPdfPreview'
import { LibraryMarkdownPreview } from './preview/LibraryMarkdownPreview'
import { LibraryMermaidPreview } from './preview/LibraryMermaidPreview'
import { LibraryCodePreview } from './preview/LibraryCodePreview'
import { LibraryTextPreview } from './preview/LibraryTextPreview'
import { LibraryDownloadCard } from './preview/LibraryDownloadCard'
import { PreviewHeaderSlotProvider } from './preview/previewHeaderSlot'

// Every control in the single header row is a bare icon, matching the browser
// panel's toolbar treatment: no border, no fill, hover as the only chrome.
export const LIBRARY_ICON_BTN =
  'flex h-7 w-7 shrink-0 items-center justify-center rounded transition-colors ' +
  'text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] ' +
  'disabled:cursor-not-allowed disabled:opacity-40 ' +
  'pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]'

/**
 * Mints a preview token (`POST /api/v1/library/preview-token`,
 * LibraryPreviewTokenRequest/Response). Injected rather than imported so the
 * pane does not have to reach past its own file for the one call it cannot
 * make yet — see PREVIEW_TOKEN_MINTER below.
 */
export type MintLibraryPreviewToken = (
  request: LibraryPreviewTokenRequest,
) => Promise<LibraryPreviewTokenResponse>

/**
 * THE MINT CLIENT DOES NOT EXIST YET. `POST /api/v1/library/preview-token` is
 * wave-3 backend work (spec FR-003f); its request/response schemas are already
 * in `contracts/` and generated above, but no `src/lib/api.ts` wrapper calls
 * them, and this file does not own `api.ts`.
 *
 * Until it lands this is `null`, and every HTML preview renders the explicit
 * "preview unavailable" state below — NEVER a blank or broken frame, which is
 * the failure mode FR-003c/FR-003n exist to prevent. When the wrapper ships,
 * this constant becomes that function and nothing else in the SPA changes:
 * no consumer passes it, so no consumer has to be edited. Tests inject their
 * own via the `mintPreviewToken` prop.
 */
const PREVIEW_TOKEN_MINTER: MintLibraryPreviewToken | null = null

export interface LibraryPreviewPaneProps {
  workspaceId: string
  entry: LibraryEntry
  onClose: () => void
  /** Still required: the download CARD (too-large / binary / unsupported
   *  fallbacks) is the only remaining download affordance inside the pane. */
  onDownload: (entry: LibraryEntry) => void
  /** Override for the preview-token minter. Production passes nothing and
   *  gets PREVIEW_TOKEN_MINTER; tests inject a stub. */
  mintPreviewToken?: MintLibraryPreviewToken | null
  /**
   * Open another file in place, workspace-relative (FR-012, US-7 AS-7).
   * Followed when a reader clicks a wikilink, a relative link or a linked
   * mention inside an open note. Omit and those links still render — they
   * simply do not swap the pane.
   */
  onOpenNote?: (workspacePath: string) => void
}

export function LibraryPreviewPane({
  workspaceId,
  entry,
  onClose,
  onDownload,
  mintPreviewToken = PREVIEW_TOKEN_MINTER,
  onOpenNote,
}: LibraryPreviewPaneProps) {
  const kind = classifyLibraryEntry(entry)
  // HTML is in this set for its EDIT side only — the rendered view comes from
  // the token path and never touches this response.
  const needsContent =
    kind === 'markdown' || kind === 'mermaid' || kind === 'text' || kind === 'html'

  // Tracks the entry's OWN display metadata (size/modified) so a successful
  // save updates the header immediately without waiting for the parent's
  // entries-list refetch to flow a fresh `entry` prop back down.
  const [liveEntry, setLiveEntry] = useState(entry)
  useEffect(() => {
    setLiveEntry(entry)
  }, [entry])

  const contentQuery = useQuery({
    queryKey: libraryQueryKeys.content(workspaceId, entry.path),
    queryFn: () => fetchLibraryContent(workspaceId, entry.path),
    enabled: needsContent,
    staleTime: 10_000,
  })

  const [headerSlot, setHeaderSlot] = useState<HTMLDivElement | null>(null)

  const loadingBody = (
    <div className="flex flex-1 items-center justify-center gap-2 text-xs text-[var(--color-muted)]">
      <SpinnerGap size={16} className="animate-spin" /> Loading file…
    </div>
  )

  function renderBody(): ReactNode {
    switch (kind) {
      case 'image':
        return <LibraryImagePreview workspaceId={workspaceId} entry={liveEntry} />
      case 'video':
        return <LibraryVideoPreview workspaceId={workspaceId} entry={liveEntry} />
      case 'audio':
        return <LibraryAudioPreview workspaceId={workspaceId} entry={liveEntry} />
      case 'base':
        // A .base file renders as its views (tabs over evaluated view
        // results, view-kinds-design-2026-09-03 §7). BasePreview fetches the
        // file's own content itself (same query key, so react-query dedupes)
        // because it also needs the collection walk and per-view results.
        return <BasePreview workspaceId={workspaceId} entry={liveEntry} />
      case 'pdf':
        return <LibraryPdfPreview workspaceId={workspaceId} entry={liveEntry} />
      case 'html': {
        // Wait for the content read to SETTLE before mounting the body, even
        // though the render itself needs none of those bytes. Without this the
        // tree flips from frame-only to the LibraryTextPreview shell the moment
        // the read lands, which unmounts and remounts the frame — mints a
        // second one-shot token, and reloads a page the reader may already be
        // looking at. The mint is in flight over the same window anyway, so
        // nothing is actually slower.
        if (contentQuery.isLoading) return loadingBody
        return (
          <LibraryHtmlBody
            workspaceId={workspaceId}
            entry={liveEntry}
            mintPreviewToken={mintPreviewToken ?? null}
            // Source for the Edit side only; `undefined` while the read is in
            // flight or after it failed, which costs Edit and never the render.
            content={
              contentQuery.isSuccess &&
              contentQuery.data.is_text &&
              !contentQuery.data.too_large
                ? contentQuery.data.content
                : undefined
            }
            onSaved={setLiveEntry}
          />
        )
      }
      case 'markdown':
      case 'mermaid':
      case 'text': {
        if (contentQuery.isLoading) return loadingBody
        if (contentQuery.isError) {
          return (
            <QueryErrorState
              layout="fill"
              message="Could not load this file."
              onRetry={() => void contentQuery.refetch()}
              testId="library-content-error"
            />
          )
        }
        if (!contentQuery.isSuccess) return loadingBody
        return (
          <LibraryTextBody
            workspaceId={workspaceId}
            entry={liveEntry}
            kind={kind}
            content={contentQuery.data}
            onDownload={onDownload}
            onSaved={setLiveEntry}
            {...(onOpenNote ? { onOpenNote } : {})}
          />
        )
      }
      case 'other':
        return <LibraryDownloadCard entry={liveEntry} reason="unsupported" onDownload={onDownload} />
      default: {
        // Exhaustiveness: a new LibraryPreviewKind reaching here is a COMPILE
        // error (`kind` is no longer `never`). The runtime fallback is the
        // download card rather than nothing, so even a chain-shaped regression
        // shows the reader something real.
        const unhandled: never = kind
        void unhandled
        return <LibraryDownloadCard entry={liveEntry} reason="unsupported" onDownload={onDownload} />
      }
    }
  }

  return (
    <div className="flex h-full min-h-0 flex-col" data-testid="library-preview-pane">
      {/* ONE header row (operator direction, 2026-08-04). Was three: a tall
          identity strip (40px type icon, name, size, modified), a row of
          outline buttons (Download / Rename / Move / Delete), and the
          view/edit/save toolbar inside the body — roughly 130px of chrome
          above a pane that already only gets half the panel's height.

          The four actions were not moved, they were REMOVED: every entry row
          already carries the identical set in its own DotsThree menu
          (LibraryEntryRow), so the strip was a second copy of a menu the user
          had just used to open the file. Size/modified went with it — the list
          row shows both, one click away.

          Close stays. It is the ONLY way to dismiss the pane (LibraryExplorer
          clears selectedEntry from here and from destructive mutations, not
          from a second click on the row), so dropping it would strand an open
          preview. It is an icon like the rest.

          The middle slot is filled by whichever body mounts an editor — see
          previewHeaderSlot for why that is a portal and not lifted state. */}
      <div className="flex h-9 shrink-0 items-center gap-2 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-2">
        <p
          className="min-w-0 flex-1 truncate text-xs font-medium text-[var(--color-secondary)]"
          title={liveEntry.name}
          data-testid="library-preview-title"
        >
          {liveEntry.name}
        </p>
        <div ref={setHeaderSlot} className="flex shrink-0 items-center gap-0.5" />
        <button
          type="button"
          tabIndex={0}
          onClick={onClose}
          aria-label="Close preview"
          title="Close preview"
          data-testid="library-preview-close"
          className={LIBRARY_ICON_BTN}
        >
          <X size={15} />
        </button>
      </div>

      {/* FR-007 — the untrusted-content boundary. It lives HERE, in the pane's
          own chrome between the header and the body, for two reasons a banner
          placed next to the frame would not satisfy:
            * outside the frame, so the page cannot draw it, move it or cover
              it — an opaque origin stops a page reading the session, it does
              NOT stop it painting a convincing login prompt inside itself;
            * outside the body's scroll container, so it stays put while the
              rendered page scrolls ("persistent", US-2 AS-4).
          Shown for `html` only: that is the sole kind the BROWSER executes.
          Everything else on this pane is drawn by our own components from
          bytes we parse (D15.1 / FR-017), and marking those "untrusted" would
          teach the reader to ignore the marker where it matters. */}
      {kind === 'html' && <UntrustedContentBoundary />}

      {/* Body — the actual preview/edit surface. */}
      <PreviewHeaderSlotProvider slot={headerSlot}>
        <div className="flex flex-1 min-h-0 flex-col overflow-hidden">{renderBody()}</div>
      </PreviewHeaderSlotProvider>
    </div>
  )
}

function UntrustedContentBoundary() {
  return (
    <div
      role="note"
      aria-label="Untrusted content"
      data-testid="library-preview-untrusted-boundary"
      className="flex shrink-0 items-center gap-2 border-b border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 px-3 py-1.5"
    >
      <ShieldWarning size={14} weight="fill" className="shrink-0 text-[var(--color-warning)]" />
      <p className="text-[11px] leading-snug text-[var(--color-warning)]">
        Untrusted content — Omnipus did not write this page. It runs isolated: it cannot read your
        session or reach the network.
      </p>
    </div>
  )
}

// LibraryAudioPreview — plain <audio controls>, the exact shape
// LibraryVideoPreview already is for video: the raw authenticated download URL
// is the source, so there is nothing to fetch, decode or sandbox. It lives in
// this file rather than in preview/ because this pane is the only mount point
// and the component is four lines; if a second host ever needs it, move it
// beside LibraryVideoPreview then.
function LibraryAudioPreview({ workspaceId, entry }: { workspaceId: string; entry: LibraryEntry }) {
  const src = libraryDownloadUrl(workspaceId, entry.path)
  return (
    <div
      className="flex flex-1 min-h-0 items-center justify-center overflow-auto bg-[var(--color-surface-0)] p-4"
      data-testid="library-audio-preview"
    >
      {/* eslint-disable-next-line jsx-a11y/media-has-caption -- workspace files carry no caption tracks to attach */}
      <audio controls src={src} className="w-full max-w-lg">
        Your browser does not support playing this audio file. Use Download instead.
      </audio>
    </div>
  )
}

// The HTML body: rendered page by default, source behind Edit (US-1 AS-1/AS-2).
//
// When the file's text is available it reuses LibraryTextPreview — the same
// view/edit/save shell markdown, mermaid and code already use, so Edit gives a
// real editor with the same portalled controls and dirty guard, and "view" is
// the live frame. When it is NOT available (still loading, read failed, too
// large, or not text) the frame renders on its own: the render path never
// depended on those bytes, so losing them must cost Edit and nothing else.
function LibraryHtmlBody({
  workspaceId,
  entry,
  content,
  mintPreviewToken,
  onSaved,
}: {
  workspaceId: string
  entry: LibraryEntry
  content: string | undefined
  mintPreviewToken: MintLibraryPreviewToken | null
  onSaved: (entry: LibraryEntry) => void
}) {
  const frame = <LibraryHtmlFrame workspaceId={workspaceId} entry={entry} mint={mintPreviewToken} />
  if (content === undefined) {
    return <div className="flex flex-1 min-h-0 flex-col overflow-auto">{frame}</div>
  }
  return (
    <LibraryTextPreview
      workspaceId={workspaceId}
      entry={entry}
      content={content}
      editorFilename={entry.name}
      onSaved={onSaved}
      renderView={() => frame}
    />
  )
}

/**
 * Builds the mint request for one file (spec §10.5).
 *
 * A page in a subdirectory is minted as a **bundle** rooted at that directory,
 * because that is the only scope under which its own relative stylesheet,
 * script, font and media resolve (FR-003 / US-1 AS-4) — the token is in the
 * URL, so subresources inherit it by being relative. A page sitting at the
 * work-tree root is minted as a **file**: "bundle" there would mean the whole
 * workspace, which §10.5 forbids outright.
 */
function previewTokenRequestFor(workspaceId: string, path: string): LibraryPreviewTokenRequest {
  const slash = path.lastIndexOf('/')
  if (slash <= 0) {
    return { workspace_id: workspaceId, path, scope: 'file' }
  }
  return {
    workspace_id: workspaceId,
    path: path.slice(0, slash),
    scope: 'bundle',
    entry_path: path.slice(slash + 1),
  }
}

/**
 * The sandboxed frame. Isolation contract, spec §10.3 / §10.6 / FR-005b:
 *
 * `<iframe src>`, NEVER `srcdoc`. `srcdoc` resolves relative URLs against the
 * EMBEDDER, so not one stylesheet, script, font or media file of a bundle
 * would load — and it has no response of its own, so there is nothing to carry
 * the isolation policy the whole control depends on.
 *
 * The three attributes below are defence in depth for the header, not a
 * replacement for it. THE EFFECTIVE SANDBOX IS THE INTERSECTION OF THE
 * ATTRIBUTE AND THE `Content-Security-Policy: sandbox …` DIRECTIVE ON THE
 * RESPONSE — a capability exists only if BOTH grant it, which is the opposite
 * of what "add the token I need" intuition suggests. Adding `allow-same-origin`
 * to one side alone grants nothing; adding it to BOTH hands the page the
 * session cookie and voids the entire measurement this design rests on
 * (§10.3, "the load-bearing omission").
 *
 * `referrerpolicy="no-referrer"` keeps the URL-borne token out of `Referer`
 * (FR-003c). `allow=""` delegates no Permissions-Policy feature — camera,
 * microphone, or whatever is added to that list next.
 */
function LibraryHtmlFrame({
  workspaceId,
  entry,
  mint,
}: {
  workspaceId: string
  entry: LibraryEntry
  mint: MintLibraryPreviewToken | null
}) {
  const tokenQuery = useQuery({
    queryKey: ['library', workspaceId, 'preview-token', entry.path],
    queryFn: () => {
      // Non-null by `enabled` below; react-query never calls a disabled query.
      const doMint = mint as MintLibraryPreviewToken
      return doMint(previewTokenRequestFor(workspaceId, entry.path))
    },
    enabled: mint !== null,
    // A token is a one-shot 15-minute credential. `staleTime: Infinity` keeps
    // an ordinary re-render from silently minting a second one (re-minting
    // INVALIDATES the previous token, so a stray refetch would kill the frame
    // that is currently using it); `gcTime: 0` drops it the moment the pane
    // closes. Reload calls refetch(), which bypasses staleTime by design.
    staleTime: Infinity,
    gcTime: 0,
    retry: false,
  })

  const token = tokenQuery.data
  // FR-003m: expiry is announced, not detected. The frame is cross-origin,
  // opaque and sandboxed, so `onload` fires for the gateway's 404 page exactly
  // as it does for the document — the embedder CANNOT tell that the request
  // failed. The only honest signal is the expiry the mint already told us
  // about, and the only honest response is a visible notice plus an explicit
  // Reload. No timer-driven silent reload: that would throw away the reader's
  // scroll position and any in-document state, a decision nobody has made.
  const [expired, setExpired] = useState(false)
  useEffect(() => {
    setExpired(false)
    if (!token) return
    const ms = Math.max(0, token.expires_in_seconds) * 1000
    const timer = setTimeout(() => setExpired(true), ms)
    return () => clearTimeout(timer)
  }, [token])

  if (mint === null) {
    return (
      <PreviewUnavailable
        detail="Omnipus could not get a preview link for this page. Rendering it needs the isolated preview endpoint, which this build does not serve yet."
        testId="library-html-preview-unavailable"
      />
    )
  }
  if (tokenQuery.isLoading) {
    return (
      <div
        className="flex flex-1 items-center justify-center gap-2 p-4 text-xs text-[var(--color-muted)]"
        data-testid="library-html-preview-loading"
      >
        <SpinnerGap size={16} className="animate-spin" /> Preparing preview…
      </div>
    )
  }
  if (tokenQuery.isError || !token) {
    return (
      <PreviewUnavailable
        detail="Omnipus could not get a preview link for this page, so it cannot be rendered safely."
        testId="library-html-preview-unavailable"
        onRetry={() => void tokenQuery.refetch()}
      />
    )
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2">
      {expired && (
        <div
          role="status"
          data-testid="library-html-preview-expired"
          className="flex shrink-0 items-center gap-2 rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 px-3 py-2"
        >
          <WarningCircle size={14} weight="fill" className="shrink-0 text-[var(--color-warning)]" />
          <p className="flex-1 text-xs leading-snug text-[var(--color-warning)]">
            This preview link has expired. Anything the page loads from now on will fail.
          </p>
          <button
            type="button"
            tabIndex={0}
            onClick={() => void tokenQuery.refetch()}
            data-testid="library-html-preview-reload"
            className="shrink-0 rounded px-2 py-1 text-xs font-medium text-[var(--color-warning)] transition-colors hover:bg-[var(--color-warning)]/20"
          >
            <ArrowClockwise size={12} className="mr-1 inline" />
            Reload
          </button>
        </div>
      )}
      {/* The height is explicit because this frame is often rendered inside
          LibraryTextPreview's view body — a padded, auto-height block in a
          file this component does not own — where a percentage height would
          collapse an iframe to its 150px intrinsic default. */}
      <iframe
        key={token.token}
        src={token.url}
        title={`Preview of ${entry.name}`}
        data-testid="library-html-preview-frame"
        sandbox="allow-scripts"
        referrerPolicy="no-referrer"
        allow=""
        className="h-[70vh] min-h-[20rem] w-full shrink-0 rounded-md border border-[var(--color-border)] bg-white"
      />
    </div>
  )
}

function PreviewUnavailable({
  detail,
  testId,
  onRetry,
}: {
  detail: string
  testId: string
  onRetry?: () => void
}) {
  return (
    <div
      role="alert"
      data-testid={testId}
      className="flex flex-1 min-h-0 flex-col items-center justify-center gap-3 overflow-auto p-8 text-center"
    >
      <WarningCircle size={28} weight="fill" className="text-[var(--color-warning)]" />
      <p className="text-sm font-medium text-[var(--color-secondary)]">Preview unavailable</p>
      <p className="max-w-sm text-xs leading-relaxed text-[var(--color-muted)]">{detail}</p>
      {onRetry && (
        <button
          type="button"
          tabIndex={0}
          onClick={onRetry}
          data-testid="library-html-preview-retry"
          className="rounded border border-[var(--color-border)] px-3 py-1 text-xs text-[var(--color-secondary)] transition-colors hover:bg-[var(--color-surface-2)]"
        >
          Try again
        </button>
      )}
    </div>
  )
}

// Split out so the "honour is_text/too_large" branch reads as one clear
// decision rather than nesting inside the parent's already-busy render body.
function LibraryTextBody({
  workspaceId,
  entry,
  kind,
  content,
  onDownload,
  onSaved,
  onOpenNote,
}: {
  workspaceId: string
  entry: LibraryEntry
  kind: 'markdown' | 'mermaid' | 'text'
  content: { content?: string; is_text: boolean; too_large: boolean }
  onDownload: (entry: LibraryEntry) => void
  onSaved: (entry: LibraryEntry) => void
  onOpenNote?: (workspacePath: string) => void
}) {
  if (content.too_large) {
    return <LibraryDownloadCard entry={entry} reason="too_large" onDownload={onDownload} />
  }
  if (!content.is_text || content.content === undefined) {
    return <LibraryDownloadCard entry={entry} reason="binary" onDownload={onDownload} />
  }
  const text = content.content
  if (kind === 'markdown') {
    return (
      <LibraryMarkdownPreview
        workspaceId={workspaceId}
        entry={entry}
        content={text}
        onSaved={onSaved}
        {...(onOpenNote ? { onOpenNote } : {})}
      />
    )
  }
  if (kind === 'mermaid') {
    return <LibraryMermaidPreview workspaceId={workspaceId} entry={entry} content={text} onSaved={onSaved} />
  }
  return <LibraryCodePreview workspaceId={workspaceId} entry={entry} content={text} onSaved={onSaved} />
}
