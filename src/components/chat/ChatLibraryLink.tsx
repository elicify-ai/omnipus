// ChatLibraryLink — the anchor a chat link renders as (chat-links spec FR-001,
// FR-004, BS-1.14, BS-1.15).
//
// A real anchor, so Tab reaches it, Enter activates it and the design-system
// focus ring applies. Its `href` is the standalone full-page Library address,
// rebuilt from the parsed target (never the agent's raw text, SEC-10):
//   - plain click / Enter: opens the docked Library beside the chat, through
//     the leave gate (openLibraryAt);
//   - Ctrl/Cmd/Shift/Alt or middle click: left to the browser, which opens the
//     full-page Library in a new tab. No target="_blank" on this anchor.
//
// Spoofing defence is not hover-only: when the visible text names neither the
// target's path nor its base name, a muted suffix "W / path" follows the text.
// The suffix has an id the anchor points at with aria-describedby, and both
// carry the same words as `title`.

import { isValidElement, useId, type MouseEvent, type ReactNode } from 'react'
import {
  chatLinkTargetLabel,
  libraryPageHref,
  linkTextNamesTarget,
  type ChatLinkTarget,
} from '@/lib/chatLinks'
import { openLibraryAt } from '@/components/library/openLibraryAt'

/** Visible text of rendered link children (strings, numbers, nested elements). */
function textOf(node: ReactNode): string {
  if (typeof node === 'string' || typeof node === 'number') return String(node)
  if (Array.isArray(node)) return node.map(textOf).join('')
  if (isValidElement<{ children?: ReactNode }>(node)) return textOf(node.props.children)
  return ''
}

/** A click the browser should handle itself (new tab / window / download). */
function isModifiedClick(event: MouseEvent<HTMLAnchorElement>): boolean {
  return event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey
}

export function ChatLibraryLink({ target, children }: { target: ChatLinkTarget; children?: ReactNode }) {
  const suffixId = useId()
  const label = chatLinkTargetLabel(target)
  const showSuffix = !linkTextNamesTarget(textOf(children), target)

  const onClick = (event: MouseEvent<HTMLAnchorElement>) => {
    if (event.defaultPrevented || isModifiedClick(event)) return
    event.preventDefault()
    openLibraryAt(target)
  }

  return (
    <a
      tabIndex={0}
      href={libraryPageHref(target)}
      data-testid="markdown-link"
      onClick={onClick}
      title={showSuffix ? label : undefined}
      aria-describedby={showSuffix ? suffixId : undefined}
      className="text-[var(--color-accent)] underline underline-offset-2 hover:opacity-80 transition-opacity"
    >
      {children}
      {showSuffix && (
        // inline-block stops the anchor's underline from running through the hint.
        <span
          id={suffixId}
          title={label}
          className="inline-block ml-[var(--space-1)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]"
        >
          {label}
        </span>
      )}
    </a>
  )
}
