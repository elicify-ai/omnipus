// AskUserQuestionCard — askuserquestion-tool-spec v3 (ADR-074 D4b), the SPA
// half of the AskUserQuestion structured clarification tool. Approved visual
// reference: docs/internal/design/askuserquestion-ui-mock.html v2 (flat
// hairline-delimited zone, tabbed one-question-at-a-time, flat option rows,
// underline free text, left-rule context) — with Phosphor icons in place of
// the mock's emoji glyphs (repo rule: no emoji in UI chrome).
//
// Behaviors (spec US-1..US-4, test 9):
//   - Tabs: short header labels + index, answered tabs marked, n/M counter,
//     auto-advance to the next unanswered question on selection.
//   - Options: flat rows, hover/selected tint, recommended badge LISTED
//     FIRST and never pre-selected; multi_select turns rows into toggles.
//   - Free text: underline input under every question; typing deselects,
//     re-selecting drops the text (EC-3 last-interaction-wins).
//   - Context: raw markdown rendered through the sanitized chat pipeline
//     (react-markdown, no raw HTML) behind a left rule — hostile markdown
//     stays inert (US-4, tested).
//   - Countdown on default-safe questions (fed by card.default_safe_at);
//     server-auto-resolved questions (card.auto_resolved) show as selected
//     with an auto marker.
//   - Answer disabled until every question is answered/resolved; Cancel
//     always present (discards selections, EC-1).
//   - Client-fired 5-minute grace auto-submit (US-3 S3): once every question
//     is selected/resolved, 5 minutes with no card interaction submits,
//     marking auto-resolved answers auto_default.
//   - Terminal card (status answered/cancelled) renders the collapsed
//     question → answer record from the registry record itself (§0.6),
//     auto-default answers marked so they are never mistaken for a human
//     choice.

import { useEffect, useMemo, useRef, useState } from 'react'
import { Check, X } from '@phosphor-icons/react'
import type { AskUserQuestionCard as AskUserCard } from '@/lib/api/generated/asyncapi-types'
import { useChatStore } from '@/store/chat'
import { HistoricalMessageMarkdown } from './historical-markdown'

/** Grace window before the client auto-submits a fully-answered card. */
export const ASK_GRACE_MS = 5 * 60 * 1000

type Question = AskUserCard['questions'][number]

interface AnswerDraft {
  selected: string[]
  freeText: string
}

function fmtRemaining(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000))
  const m = Math.floor(total / 60)
  const s = total % 60
  return `${m}:${s.toString().padStart(2, '0')}`
}

/** Options with the recommended one listed first (never pre-selected). */
function orderedOptions(q: Question): Question['options'] {
  if (!q.recommended) return q.options
  const rec = q.options.filter((o) => o.label === q.recommended)
  const rest = q.options.filter((o) => o.label !== q.recommended)
  return [...rec, ...rest]
}

export function AskUserQuestionCard({ card }: { card: AskUserCard }) {
  const sendAskUserAnswer = useChatStore((s) => s.sendAskUserAnswer)
  const autoResolved = useMemo(() => new Set(card.auto_resolved ?? []), [card.auto_resolved])

  const [active, setActive] = useState(0)
  const [drafts, setDrafts] = useState<Record<string, AnswerDraft>>({})
  const [now, setNow] = useState(() => Date.now())
  const lastInteraction = useRef(Date.now())
  const submitted = useRef(false)

  const questions = card.questions

  function draftFor(header: string): AnswerDraft {
    return drafts[header] ?? { selected: [], freeText: '' }
  }

  function isAnswered(q: Question): boolean {
    const d = draftFor(q.header)
    return d.selected.length > 0 || d.freeText.trim() !== '' || autoResolved.has(q.header)
  }

  const answeredCount = questions.filter(isAnswered).length
  const allAnswered = answeredCount === questions.length

  function touch() {
    lastInteraction.current = Date.now()
  }

  function advanceFrom(_header: string, nextDrafts: Record<string, AnswerDraft>) {
    // Auto-advance to the first question still unanswered under nextDrafts.
    const idx = questions.findIndex((q) => {
      const d = nextDrafts[q.header] ?? { selected: [], freeText: '' }
      return d.selected.length === 0 && d.freeText.trim() === '' && !autoResolved.has(q.header)
    })
    if (idx >= 0) setActive(idx)
  }

  function selectOption(q: Question, label: string) {
    touch()
    setDrafts((prev) => {
      const cur = prev[q.header] ?? { selected: [], freeText: '' }
      let selected: string[]
      if (q.multi_select) {
        selected = cur.selected.includes(label)
          ? cur.selected.filter((l) => l !== label)
          : [...cur.selected, label]
      } else {
        selected = [label]
      }
      // EC-3: re-selecting drops the free text (last interaction wins).
      const next = { ...prev, [q.header]: { selected, freeText: '' } }
      if (selected.length > 0 && !q.multi_select) {
        // Auto-advance shortly after a single-select choice.
        setTimeout(() => advanceFrom(q.header, next), 200)
      }
      return next
    })
  }

  function typeFreeText(q: Question, value: string) {
    touch()
    setDrafts((prev) => ({
      // Typing deselects (presence of free text IS the flag, US-2 S1).
      ...prev,
      [q.header]: { selected: [], freeText: value },
    }))
  }

  function buildAnswers() {
    return questions.map((q) => {
      const d = draftFor(q.header)
      if (d.freeText.trim() !== '') {
        return { header: q.header, free_text: d.freeText }
      }
      if (d.selected.length > 0) {
        return { header: q.header, selected: d.selected }
      }
      // Untouched but server-auto-resolved: submit the recommendation,
      // marked by origin (US-3 S3).
      return { header: q.header, selected: q.recommended ? [q.recommended] : [], auto_default: true }
    })
  }

  function submit() {
    if (submitted.current) return
    submitted.current = true
    sendAskUserAnswer({
      card_id: card.card_id,
      session_id: card.session_id,
      answers: buildAnswers(),
    })
  }

  function cancel() {
    if (submitted.current) return
    submitted.current = true
    sendAskUserAnswer({ card_id: card.card_id, session_id: card.session_id, cancel: true })
  }

  // 1s tick drives the countdown line; only armed while one is visible.
  const hasCountdown = card.status === 'pending' && Boolean(card.default_safe_at)
  useEffect(() => {
    if (!hasCountdown) return
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [hasCountdown])

  // Client-fired 5-minute grace auto-submit (US-3 S3): checked on a
  // low-frequency tick against the last interaction timestamp.
  //
  // The tick callback goes through a ref that is refreshed every render
  // (latest-ref pattern) instead of being captured by the interval closure.
  // The earlier version keyed the interval on `allAnswered` and closed over
  // that render's `submit`/`drafts`: once every question was first answered
  // the closure froze, so a LATER edit to an answer changed the drafts but
  // not the closure, and the grace timer submitted the stale pre-edit
  // answers. Regression test: "grace auto-submit sends the EDITED answer".
  const graceTick = useRef<() => void>(() => {})
  useEffect(() => {
    graceTick.current = () => {
      if (!submitted.current && allAnswered && Date.now() - lastInteraction.current >= ASK_GRACE_MS) {
        submit()
      }
    }
  })
  useEffect(() => {
    if (card.status !== 'pending') return
    const t = setInterval(() => graceTick.current(), 5000)
    return () => clearInterval(t)
  }, [card.status])

  // ── Terminal: collapsed question → answer record (§0.6) ──────────────────
  if (card.status !== 'pending') {
    return (
      <div
        data-testid="ask-user-collapsed"
        className="my-2 border-y border-[var(--color-border)] py-2.5 px-1 text-xs"
      >
        <div className="flex items-center gap-2 mb-1.5">
          {card.status === 'answered' ? (
            <Check size={12} weight="bold" className="text-[color:var(--color-success)]" aria-hidden="true" />
          ) : (
            <X size={12} weight="bold" className="text-[var(--color-muted)]" aria-hidden="true" />
          )}
          <span className="font-mono text-[10px] uppercase tracking-widest text-[var(--color-muted)]">
            {card.status === 'answered' ? 'Answered' : 'Cancelled'} · {card.agent_id}&apos;s questions
          </span>
        </div>
        {card.status === 'answered' &&
          (card.answers ?? []).map((a) => (
            <div key={a.header} className="flex items-baseline gap-3 my-1" data-testid="ask-user-record-row">
              <span className="flex-1 text-[var(--color-muted)] break-words">{a.question}</span>
              <span className="text-[color:var(--color-success)] font-medium whitespace-nowrap">
                {a.free_text ?? (a.selected ?? []).join(', ')}
                {a.auto_default && (
                  <span className="text-[var(--color-accent)] font-normal" data-testid="ask-user-auto-marker">
                    {' '}
                    · auto
                  </span>
                )}
              </span>
            </div>
          ))}
        {card.status === 'cancelled' && (
          <p className="text-[var(--color-muted)]">Questions dismissed — no answers were sent.</p>
        )}
      </div>
    )
  }

  // ── Pending: the flat, tabbed question zone ──────────────────────────────
  const q = questions[Math.min(active, questions.length - 1)]
  const remainingMs = card.default_safe_at ? new Date(card.default_safe_at).getTime() - now : 0

  return (
    <div
      data-testid="ask-user-question-card"
      role="group"
      aria-label={`Questions from ${card.agent_id}`}
      className="my-2 border-y border-[var(--color-border)] py-2.5 text-xs"
    >
      {/* Header: label + tabs */}
      <div className="flex items-center gap-2.5 mb-3">
        <span className="font-mono text-[10px] uppercase tracking-widest text-[var(--color-muted)]">
          <b className="text-[var(--color-accent)] font-medium">{card.agent_id}</b> needs your input
        </span>
        <div className="flex gap-1.5 ml-auto" role="tablist">
          {questions.map((tq, i) => {
            const done = isAnswered(tq)
            return (
              <button tabIndex={0}
                key={tq.header}
                type="button"
                role="tab"
                aria-selected={i === active}
                data-testid={`ask-user-tab-${i}`}
                onClick={() => {
                  touch()
                  setActive(i)
                }}
                className={
                  'font-mono text-[11px] bg-transparent border-0 border-b-2 px-1.5 pb-1 cursor-pointer ' +
                  (i === active
                    ? 'text-[var(--color-primary-fg,var(--color-secondary))] border-[var(--color-accent)]'
                    : done
                      ? 'text-[color:var(--color-success)] border-transparent'
                      : 'text-[var(--color-muted)] border-transparent hover:text-[var(--color-secondary)]')
                }
              >
                {i + 1} {tq.header}
                {done && <Check size={9} className="inline ml-0.5" aria-hidden="true" />}
              </button>
            )
          })}
        </div>
      </div>

      {/* Active question panel */}
      <div role="tabpanel" data-testid="ask-user-panel">
        <p className="text-sm font-medium text-[var(--color-secondary)] mb-3">{q.question}</p>

        {q.context && (
          <div
            className="border-l-2 border-[var(--color-border)] pl-3 mb-3 text-[var(--color-secondary)]"
            data-testid="ask-user-context"
          >
            <HistoricalMessageMarkdown content={q.context} />
          </div>
        )}

        <div className="flex flex-col" data-testid="ask-user-options">
          {orderedOptions(q).map((o) => {
            const d = draftFor(q.header)
            const selected = d.selected.includes(o.label)
            return (
              <button tabIndex={0}
                key={o.label}
                type="button"
                aria-pressed={selected}
                onClick={() => selectOption(q, o.label)}
                data-testid="ask-user-option"
                className={
                  'flex gap-3 items-start text-left w-full bg-transparent border-0 rounded-md px-2 py-2 cursor-pointer ' +
                  (selected ? 'bg-[var(--color-accent)]/10' : 'hover:bg-[var(--color-accent)]/10')
                }
              >
                <span
                  aria-hidden="true"
                  className={
                    'mt-0.5 shrink-0 w-3 h-3 border ' +
                    (q.multi_select ? 'rounded-sm ' : 'rounded-full ') +
                    (selected
                      ? 'border-[var(--color-accent)] bg-[var(--color-accent)]'
                      : 'border-[var(--color-muted)]')
                  }
                />
                <span className="min-w-0">
                  <span className="font-medium text-[13px] text-[var(--color-secondary)] flex items-center gap-2 flex-wrap">
                    {o.label}
                    {q.recommended === o.label && (
                      <span
                        className="font-mono text-[9px] uppercase tracking-wider text-[var(--color-accent)]"
                        data-testid="ask-user-recommended-badge"
                      >
                        Recommended
                      </span>
                    )}
                    {autoResolved.has(q.header) && q.recommended === o.label && (
                      <span
                        className="font-mono text-[9px] uppercase tracking-wider text-[var(--color-muted)]"
                        data-testid="ask-user-auto-resolved"
                      >
                        auto-selected
                      </span>
                    )}
                  </span>
                  {o.description && (
                    <span className="block text-[var(--color-muted)] mt-0.5">{o.description}</span>
                  )}
                </span>
              </button>
            )
          })}
          <div className="px-2 pt-1.5">
            <input
              type="text"
              tabIndex={0}
              value={draftFor(q.header).freeText}
              onChange={(e) => typeFreeText(q, e.target.value)}
              placeholder="Something else — type your own answer…"
              data-testid="ask-user-free-text"
              className="w-full bg-transparent border-0 border-b border-dashed border-[var(--color-border)] focus:border-solid focus:border-[var(--color-accent)] outline-none py-1.5 px-0.5 text-[13px] text-[var(--color-secondary)] placeholder:text-[var(--color-muted)]"
            />
          </div>
        </div>

        {q.default_safe && q.recommended && card.default_safe_at && !autoResolved.has(q.header) && (
          <div
            className="flex items-center gap-1.5 mt-2.5 font-mono text-[11px] text-[var(--color-muted)]"
            data-testid="ask-user-countdown"
          >
            <span className="w-1 h-1 rounded-full bg-[var(--color-accent)]" aria-hidden="true" />
            No answer in {fmtRemaining(remainingMs)} → &quot;{q.recommended}&quot; is chosen automatically
          </div>
        )}
      </div>

      {/* Footer: Answer + Cancel + counter */}
      <div className="flex items-center gap-3.5 mt-3.5">
        <button tabIndex={0}
          type="button"
          disabled={!allAnswered}
          onClick={submit}
          data-testid="ask-user-submit"
          className="text-[13px] font-medium rounded-lg px-4 py-1.5 bg-[var(--color-accent)] text-[#1a1503] disabled:opacity-35 disabled:cursor-not-allowed cursor-pointer border-0"
        >
          Answer
        </button>
        <button tabIndex={0}
          type="button"
          onClick={cancel}
          data-testid="ask-user-cancel"
          className="text-[13px] bg-transparent border-0 text-[var(--color-muted)] hover:text-[var(--color-secondary)] cursor-pointer px-1"
        >
          Cancel
        </button>
        <span
          className="ml-auto font-mono text-[11px] text-[var(--color-muted)]"
          data-testid="ask-user-progress"
        >
          {answeredCount} / {questions.length} answered
        </span>
      </div>
    </div>
  )
}

/**
 * Thread-tail mount: renders the active session's card (pending question
 * zone, or the collapsed record right after resolution). Renders nothing
 * when the session has no card.
 */
export function AskUserQuestionThreadTail() {
  const pendingAsk = useChatStore((s) => s.pendingAsk)
  // Dock ONLY while questions are actually pending. Once answered/cancelled the
  // card leaves no lingering summary above the composer (operator directive
  // 2026-09-06) — the answer is already in the transcript via the agent's reply.
  // Constrain to the chat column width (matching the composer's
  // `max-w-3xl mx-auto`) so the card never spans the full viewport.
  if (!pendingAsk || pendingAsk.status !== 'pending') return null
  return (
    <div className="w-full max-w-3xl mx-auto px-4" data-testid="ask-user-thread-tail">
      <AskUserQuestionCard card={pendingAsk} />
      <p
        className="text-center font-mono text-[11px] text-[var(--color-muted)] mt-1.5"
        data-testid="ask-user-composer-note"
      >
        chat input is locked while questions are pending — Cancel to unlock
      </p>
    </div>
  )
}
