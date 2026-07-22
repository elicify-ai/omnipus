// SubagentQuestionMessage — ADR-053 FE-3 / US-7 / design §1 (D2).
//
// Renders a child (subagent) question as a NORMAL chat message — the human
// answers it by replying in-band in the same chat surface. There is NO
// per-question reply card, NO approval/correlation UX: the parent agent routes
// the correlation server-side (D2, channel-portable). Per the spec (US-7 AC3):
// "the human answers in normal chat, when they reply, correlation routing is
// the parent's job — no separate approval/correlation UX renders."
//
// Untrusted-origin chrome IS visible (FE-7 / MAJ-12): a child-authored question
// is never trusted at face value. The sender identity is shown so the user
// knows WHO asked; a muted "untrusted" tag is shown when `untrusted_origin` is
// true. The question text renders as plain text (sanitized) — links are
// non-clickable, no raw HTML — matching the untrusted-child-text safe render.
//
// Purely presentational — driven by the landed `SessionMessageQuestion` wire
// type (§6 Contract Surface, kind: "question").

import { ChatCircleDots, ArrowBendUpRight } from '@phosphor-icons/react'
import type { SessionMessageQuestion } from '@/lib/api/generated/openapi-types'

export interface SubagentQuestionMessageProps {
  /** The landed SessionMessage `kind: question` record (ADR-053 §6). */
  message: SessionMessageQuestion
}

export function SubagentQuestionMessage({ message }: SubagentQuestionMessageProps) {
  return (
    <div
      data-testid="subagent-question-message"
      className="my-1 flex flex-col gap-1"
    >
      {/* Sender row — who asked, + untrusted-origin tag */}
      <div className="flex items-center gap-1.5 text-[10px] text-[var(--color-muted)] uppercase tracking-wide">
        <ChatCircleDots size={11} aria-hidden="true" />
        <span className="font-medium">{message.sender_identity}</span>
        <span className="text-[var(--color-muted)]">asked</span>
        {message.untrusted_origin && (
          <span
            className="rounded border border-[var(--color-border)] px-1 py-px text-[9px] normal-case tracking-normal text-[var(--color-muted)]"
            data-testid="subagent-question-untrusted"
          >
            untrusted
          </span>
        )}
      </div>

      {/* Question body — plain text (sanitized), no raw HTML, links inactive.
          The user replies in normal chat to answer — no reply card, no
          approval/correlation UX (D2). */}
      <div
        className="rounded-lg rounded-tl-sm bg-[var(--color-surface-1)] border border-[var(--color-border)] px-3.5 py-2.5 text-sm text-[var(--color-secondary)]"
        data-testid="subagent-question-text"
      >
        {message.text}
      </div>

      {/* In-band reply hint — conversational, no buttons/forms */}
      <div className="flex items-center gap-1 text-[10px] text-[var(--color-muted)] italic">
        <ArrowBendUpRight size={10} aria-hidden="true" />
        <span>Reply in chat to answer</span>
      </div>
    </div>
  )
}
