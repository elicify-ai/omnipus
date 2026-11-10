// ComposerMenu — the composer's partitioned dropdown: "/" opens Commands +
// Skills (FR-005), a leading "@" opens Agents (the agent-mention menu,
// restored by founder decision 2026-10-11 Q1 B — see useSlashMenu.ts's file
// header; selection addresses the MESSAGE, it never switches the chat,
// FR-029). The two triggers are mutually exclusive by construction, so
// `slashItems` is always either [commands, skills] or [agents], never a mix.
//
// Moved verbatim out of ChatScreen.tsx (2026-10-11), which sits over the file
// size budget and may only shrink; the only render change is the restored
// agent rows (AgentMark in front of the label, same listbox/option ARIA
// contract as the command and skill rows). F6: one unified slashItems.map()
// emits a section header on each section transition. FR-014/R3: skill items
// show their argument_hint as muted help text.
//
// ARIA (W3C APG combobox pattern): this container is the combobox's
// `listbox` — `role="listbox"`, a stable `id` the textarea's `aria-controls`
// points to, and an `aria-label` since the listbox has no visible heading of
// its own (its rows have section headers, but the LISTBOX itself doesn't).
// Rows are `role="option"` with `tabIndex={-1}` and `aria-selected`; the
// textarea (ChatScreen.tsx's ComposerPrimitive.Input) carries
// `aria-activedescendant={composer-option-${slashHighlight}}` so screen
// readers announce the highlighted row without moving DOM focus off the
// input. The rows are raw `<button>` elements by reviewed retention (the
// Button primitive's ActionAnnouncement sibling would pollute the listbox's
// expected child structure — see the ledger entries for this file and for
// ChatScreen.tsx, which previously hosted these rows).

import React from 'react'
import type { UseSlashMenuResult } from '@/hooks/useSlashMenu'
import { AgentMark } from '@/components/agents/AgentMark'
import { cn } from '@/lib/utils'

export interface ComposerMenuProps {
  slashMenu: UseSlashMenuResult
  /** Single source of truth for "the menu is actually mounted right now" — ChatScreen folds shouldShowSlash, slashOpen and hasMenuContent into it. */
  menuIsRendered: boolean
  /** Render-time clamped highlight index (undefined with zero items) — keeps aria-activedescendant pointing at a real row between renders. */
  clampedSlashHighlight: number | undefined
  /** The visible "+N more" footer copy for the active capped section (null when nothing is hidden). */
  footerCopy: string | null
  /** Click-selecting a menu row must land focus back in the input (keyboard selection never loses it; mouse would). */
  focusChatInput: () => void
}

const SECTION_LABELS: Record<UseSlashMenuResult['slashItems'][number]['section'], string> = {
  commands: 'Commands',
  skills: 'Skills',
  agents: 'Agents',
}

export function ComposerMenu({ slashMenu, menuIsRendered, clampedSlashHighlight, footerCopy, focusChatInput }: ComposerMenuProps) {
  if (!menuIsRendered) return null
  return (
    <div
      data-testid="slash-menu"
      id="composer-slash-menu"
      role="listbox"
      aria-label="Commands, skills and agents"
      // max-h + scroll: the menu opens UPWARD from the composer, so a tall
      // list (many commands + skills) pushed its top rows above the
      // viewport. 40dvh caps it to the visible area; overflow-y scrolls.
      className="mb-[var(--space-2)] rounded-xl border border-[var(--color-border)] bg-[var(--color-surface-0)] overflow-hidden shadow-2xl max-h-[40dvh] overflow-y-auto overscroll-contain"
    >
      {/* LOW S8: fetchCommands('web') errored — surface the gap instead of
          letting every backend command silently vanish from the palette.
          Muted styling matches SearchModal's own query-error row
          (src/components/search/SearchModal.tsx). Hidden during the
          "/skills" filter (D9 already hides the whole Commands section
          there, so an error row would be a non-sequitur) and in "@"
          mention mode — a commands-fetch error has nothing to do with the
          agent list, and this render gate matches useSlashMenu's own
          shouldShowSlash fallback, which already excludes mention mode from
          the "keep the menu open on error" carve-out.
          `role="presentation"`: informational text, not a selectable
          listbox option — without it the row would silently pollute the
          listbox's accessible children with a non-option row. */}
      {slashMenu.commandsError && !slashMenu.isSkillsFilter && !slashMenu.isMentionMode && (
        <div
          data-testid="slash-commands-error"
          role="presentation"
          className="px-[var(--space-2-5)] py-[var(--space-2)] text-[length:var(--type-utility-xs-size)] text-[var(--color-error)] italic"
        >
          Commands unavailable
        </div>
      )}
      {slashMenu.slashItems.map((item, globalIndex) => {
        const prevSection = globalIndex > 0 ? slashMenu.slashItems[globalIndex - 1].section : null
        const isFirstInSection = item.section !== prevSection
        const nextSection = globalIndex < slashMenu.slashItems.length - 1 ? slashMenu.slashItems[globalIndex + 1].section : null
        const isLastInSection = item.section !== nextSection
        // The section's overflow count, rendered as a muted footer row right
        // after that section's last visible row. Only skills/agents are
        // capped (commands has no cap — see useSlashMenu.ts) — 0 for
        // commands, so the footer simply never renders there.
        const sectionHiddenCount = item.section === 'skills'
          ? slashMenu.skillsHiddenCount
          : item.section === 'agents'
            ? slashMenu.agentsHiddenCount
            : 0
        return (
          <React.Fragment key={item.key}>
            {isFirstInSection && (
              <div
                role="presentation"
                className={cn(
                  'px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-caption-size)] font-semibold uppercase tracking-wider text-[var(--color-muted)]',
                  globalIndex === 0
                    ? 'border-b border-[var(--color-border)]'
                    : 'border-t border-[var(--color-border)]',
                )}>
                {SECTION_LABELS[item.section]}
              </div>
            )}
            <button
              type="button"
              id={`composer-option-${globalIndex}`}
              role="option"
              aria-selected={globalIndex === clampedSlashHighlight}
              // APG combobox pattern: options inside a combobox's listbox
              // popup are NOT separately tabbable — focus stays on the
              // textarea the whole time the menu is open (that's the entire
              // point of aria-activedescendant), and ArrowUp/ArrowDown move
              // the active-descendant highlight without ever moving real DOM
              // focus. tabIndex=0 here was actively harmful: Tab-focusing a
              // row started the textarea's 150ms blur-close timer
              // (onInputBlur, useSlashMenu.ts), which unmounted the menu out
              // from under the just-focused row and dropped focus to `body` —
              // a keyboard dead end. Selection still works via onMouseDown
              // (mouse) and Enter (keyboard, through the textarea's own
              // onKeyDown → slashMenu.handleKeyDown) — neither depends on
              // this button ever receiving focus.
              tabIndex={-1}
              // Fix 11: semantic highlight marker — lets a test assert the
              // highlighted row without depending on the visual class string
              // below (which stays purely presentational; `undefined` when
              // not highlighted, so the attribute doesn't render at all
              // rather than serializing as `data-highlighted="false"`).
              data-highlighted={globalIndex === clampedSlashHighlight || undefined}
              // "@" mention menu rows only — mirrors the slash-command/skill
              // rows' shared markup exactly, so this testid is additive
              // rather than a fork of the row.
              data-testid={item.section === 'agents' ? 'agent-mention-item' : undefined}
              className={cn(
                'w-full flex items-baseline gap-[var(--space-2-5)] px-[var(--space-2-5)] py-[var(--space-2)] text-left transition-colors',
                globalIndex === clampedSlashHighlight
                  ? 'bg-[var(--color-accent)]/10 text-[var(--color-secondary)]'
                  : 'text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]',
              )}
              onMouseDown={(e) => {
                e.preventDefault()
                item.onSelect()
                // Click-selecting a slash item must land focus back in the
                // input (keyboard selection never loses it; mouse would).
                focusChatInput()
              }}
              onMouseEnter={() => slashMenu.onHoverItem(globalIndex)}
            >
              {/* Agent rows only — the catalogued AgentMark (the ONE avatar
                  system; src/components/agents/AgentMark.tsx) in front of the
                  label, so the mention menu reads as the same "agent row" as
                  the sidebar and the chat bubbles. aria-hidden: the mark is
                  decorative; the row's accessible name comes from the
                  label/description text. The old menu's hand-rolled
                  initial-dot markup (agentColor inline style + initialOf)
                  was replaced by AgentMark in the 2026-10-11 restore — no
                  second avatar system. */}
              {item.section === 'agents' && item.agent && (
                <span aria-hidden="true" className="shrink-0 self-center">
                  <AgentMark agent={item.agent} size={18} />
                </span>
              )}
              {/* Fixed-width label column so descriptions align across rows
                  (a two-column table, not per-row flow). 9.5rem fits the
                  longest current label; truncate guards outliers. */}
              <span className="w-[9.5rem] shrink-0 truncate font-mono text-[length:var(--type-utility-xs-size)] text-[var(--color-accent)]">{item.label}</span>
              <span className="text-[length:var(--type-caption-size)] flex-1 min-w-0 truncate">{item.description}</span>
              {/* FR-014/R3: show argument_hint as muted help text for skills;
                  SD-C7 extends this to `delivery: agent` commands (e.g. /goal, /loop). */}
              {(item.section === 'skills' || item.section === 'commands') && item.argumentHint && (
                <span className="ml-auto text-[length:var(--type-caption-size)] text-[var(--color-muted)] opacity-70 font-mono shrink-0">
                  {item.argumentHint}
                </span>
              )}
            </button>
            {/* "+N more" footer — rendered right after the LAST visible row
                of a capped section that still has overflow. Deliberately
                `role="presentation"` and OUTSIDE `slashMenu.slashItems`
                entirely (not just visually de-emphasized) — it is not part
                of `globalIndex`'s sequence, so it can never receive an
                `id="composer-option-N"`, can never be `aria-selected`, and
                ArrowUp/ArrowDown (which cycle modulo `slashItems.length`)
                can never land the highlight on it. */}
            {isLastInSection && sectionHiddenCount > 0 && (
              <div
                data-testid="slash-menu-footer"
                role="presentation"
                className="px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)] italic"
              >
                {/* `footerCopy` (ChatScreen, alongside menuIsRendered)
                    already accounts for the "/skills" special-filter state;
                    `sectionHiddenCount > 0` here is guaranteed to equal the
                    activeSectionHiddenCount footerCopy was computed from
                    (only one capped section can be rendering a footer at a
                    time — "/" caps skills, "@" caps agents, and the two
                    triggers are mutually exclusive), so reusing the single
                    shared value is exactly correct, not a coincidence. */}
                {footerCopy}
              </div>
            )}
          </React.Fragment>
        )
      })}
    </div>
  )
}
