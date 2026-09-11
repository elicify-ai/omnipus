// embedMountStates.tsx — the loading/error placeholders every inline embed
// mount shows while it resolves the real `LibraryEntry` its shared renderer
// needs (ADR-083 embedded-content spec). `knowledgeMarkdown.tsx` already has
// its own private `EmbedMountPlaceholder`/`EmbedMountError` for the kinds it
// mounts directly (image, base, transclusion) — this is the SAME visual
// contract (same test ids, same copy shape), kept as a small standalone pair
// here because this file's own mount components (`KbAudioEmbedMount.tsx`,
// `KbVideoEmbedMount.tsx`, `KbPdfPageEmbedMount.tsx`) live outside that
// module and cannot import its private functions. Two independent
// definitions of the SAME markup is not the EMB-027 "one renderer per kind"
// violation Step 6's register warns about — that rule governs the KIND
// renderers (LibraryAudioPreview, LibraryVideoPreview, LibraryPdfPreview),
// not this shared, kind-agnostic loading/error chrome.

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
