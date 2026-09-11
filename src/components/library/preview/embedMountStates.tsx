// embedMountStates.tsx — the loading/error placeholders every inline embed
// mount shows while it resolves the real `LibraryEntry` its shared renderer
// needs (ADR-083 embedded-content spec). This module is the SINGLE
// definition of that chrome: `knowledgeMarkdown.tsx` imports these for the
// kinds it mounts directly (image, base, transclusion), and so do the Step 6
// mounts that live outside that module (`KbAudioEmbedMount.tsx`,
// `KbVideoEmbedMount.tsx`, `KbPdfPageEmbedMount.tsx`).
//
// It was briefly two definitions — a byte-identical private pair inside
// knowledgeMarkdown.tsx plus this exported one — on the reasoning that the
// Step 6 mounts "cannot import its private functions". That constraint was
// self-imposed: the fix was to export one copy, not to keep two. Two places
// holding one visual contract (same test ids, same classes, same Retry
// button) is a drift hazard with nothing to catch it but a human diffing
// both files, which is the same shape as the EMB-027 duplicate-renderer rule
// even though that rule itself governs the KIND renderers
// (LibraryAudioPreview, LibraryVideoPreview, LibraryPdfPreview) rather than
// this kind-agnostic chrome.

import { SpinnerGap, Warning } from '@phosphor-icons/react'

export function EmbedMountPlaceholder() {
  return (
    <div
      data-testid="kb-embed-mount-loading"
      aria-hidden="true"
      className="flex items-center justify-center gap-2 rounded-md border border-[var(--color-border)] px-3 py-6 text-xs text-[var(--color-muted)]"
    >
      <SpinnerGap size={14} className="animate-spin" /> Loading…
    </div>
  )
}

/** The listing arrived and this file was not in it — a renamed, moved or
 *  deleted target, NOT a failed request. Deliberately carries no Retry: the
 *  listing already succeeded, so retrying it returns the same answer, and
 *  offering the control anyway teaches the reader to blame the server for an
 *  out-of-date link. Naming the directory is the actionable part. */
export function EmbedMountMissing({ workspacePath, parentDir }: { workspacePath: string; parentDir: string }) {
  const name = workspacePath.slice(workspacePath.lastIndexOf('/') + 1)
  return (
    <div
      data-testid="kb-embed-mount-missing"
      className="flex flex-col items-center gap-1 rounded-md border border-dashed border-[var(--color-warning)]/50 px-3 py-6 text-center text-xs text-[var(--color-warning)]"
    >
      <Warning size={16} />
      <span>
        “{name}” is no longer in {parentDir === '' ? 'this workspace’s root folder' : parentDir}.
      </span>
      <span className="text-[11px] text-[var(--color-muted)]">The embed’s link needs updating.</span>
    </div>
  )
}

export function EmbedMountError({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div
      data-testid="kb-embed-mount-error"
      className="flex flex-col items-center gap-2 rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/5 px-3 py-6 text-center text-xs text-[var(--color-warning)]"
    >
      <Warning size={16} />
      <span>{message}</span>
      {onRetry && (
        <button
          type="button"
          tabIndex={0}
          onClick={onRetry}
          className="text-[11px] underline underline-offset-2"
        >
          Retry
        </button>
      )}
    </div>
  )
}
