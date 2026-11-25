// PermissionRow — one tool or skill on Skills & Tools: Allow / Ask / Deny,
// "What the model sees" and the row's own save status (C-16).

import type { ReactNode } from 'react'
import { CaretDown, SealCheck } from '@phosphor-icons/react'
import { formatRelative } from '@/lib/formatRelative'
import type { ModelVisibility, ToolPolicy } from '@/lib/api/generated/openapi-types'
import { Button } from '@/components/ui/button'
import { TextToggle } from '@/components/ui/text-toggle'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { SAVE_TEXT, type PermissionSaveQueue, type PermissionSection, type SaveStatus } from '@/lib/permissionSaveQueue'
import {
  LEVELS,
  LEVEL_MEANINGS,
  LEVEL_WORDS,
  LOCKED_LEVEL_SENTENCE,
  POLICIES,
  POLICY_WORDS,
  fullTokensText,
  levelOf,
  policyOf,
  type CatalogItem,
} from './skillsToolsModel'

function statusText(status: SaveStatus): string {
  switch (status.kind) {
    case 'saving': return SAVE_TEXT.saving
    case 'saved': return SAVE_TEXT.saved
    case 'refused': return status.text
    case 'not_applied': return SAVE_TEXT.notApplied
    case 'checking': return SAVE_TEXT.checking
    case 'shown_stored': return SAVE_TEXT.shownStored
    case 'load_failed': return SAVE_TEXT.loadFailed
  }
}

const RETRYABLE: ReadonlySet<SaveStatus['kind']> = new Set(['refused', 'not_applied', 'load_failed'])

/**
 * A row's save status with its Retry, shown inside the row it belongs to.
 * Spec Accessibility, "Save signal": progress and "Saved" go to a polite
 * status region that stays mounted (so it announces its changes); a problem
 * uses role="alert".
 */
export function SaveStatusLine({ queue, section, name }: { queue: PermissionSaveQueue; section: PermissionSection; name?: string }) {
  const status = queue.statusOf(section, name)
  const problem = status !== undefined && status.kind !== 'saving' && status.kind !== 'saved'
  return (
    <div className="flex flex-wrap items-center gap-[var(--space-2)] text-[length:var(--type-caption-size)]">
      <span role="status" className="text-[var(--color-muted)]">{status && !problem ? statusText(status) : ''}</span>
      {status && problem && <span role="alert" className="text-[var(--color-warning)]">{statusText(status)}</span>}
      {status && RETRYABLE.has(status.kind) && (
        <Button variant="outline" size="sm" className="h-7 pointer-coarse:min-h-[var(--target-touch-minimum)]" onClick={() => queue.retry(section, name)}>
          Retry
        </Button>
      )}
    </div>
  )
}

const FACT_CLASS = 'text-[length:var(--type-caption-size)] text-[var(--color-muted)]'

/**
 * A line label shown only on a phone, where each row becomes a card with a
 * "Permission" and a "What the model sees" line (US-10 AS3, S-68, FR-051).
 */
function PhoneLabel({ children }: { children: string }) {
  return <span className={`sm:hidden ${FACT_CLASS}`}>{children}</span>
}

/**
 * A skill row's facts: the Verified mark (S-34, C-5), when it was last used
 * (ADR-072 D3.1) and, at Full, what it costs (C-14, D18 rows 1-5).
 */
function SkillFacts({ facts, sizeShown }: { facts: NonNullable<CatalogItem['skill']>; sizeShown: boolean }) {
  return (
    <>
      {/* The spaces keep each fact a separate word in the row's text. */}
      {' '}
      {facts.verified && (
        <span className={`inline-flex items-center gap-[var(--space-1)] ${FACT_CLASS}`}>
          <SealCheck size={12} weight="bold" aria-hidden="true" />
          Verified
        </span>
      )}
      {facts.verified && ' '}
      <span className={FACT_CLASS}>
        {facts.lastInvoked ? `Last used ${formatRelative(facts.lastInvoked)}` : 'Never used'}
      </span>
      {' '}
      {sizeShown && facts.fullTokens !== undefined && (
        <span className={FACT_CLASS}>{fullTokensText(facts.fullTokens)}</span>
      )}
    </>
  )
}

interface PermissionRowProps {
  item: CatalogItem
  queue: PermissionSaveQueue
  disabled: boolean
  testIdPrefix: 'tool' | 'skill'
  onPolicy: (item: CatalogItem, policy: ToolPolicy) => void
  onLevel: (item: CatalogItem, level: ModelVisibility) => void
  onRowFocus: (key: string, choice: string | null) => void
  /** The row's own menu (More actions on a skill row). */
  actions?: ReactNode
}

export function PermissionRow({ item, queue, disabled, testIdPrefix, onPolicy, onLevel, onRowFocus, actions }: PermissionRowProps) {
  const policy = policyOf(queue, item)
  const level = levelOf(queue, item)
  const statusSection = queue.statusOf(item.levelSection, item.key) && !queue.statusOf(item.policySection, item.key)
    ? item.levelSection
    : item.policySection
  return (
    <div
      data-testid={`${testIdPrefix}-row-${item.key}`}
      data-row-key={item.key}
      onFocus={(event) => onRowFocus(item.key, (event.target as HTMLElement).getAttribute('data-choice'))}
      className="flex flex-col gap-[var(--space-1)] border-b border-[var(--color-border)] py-[var(--space-2)] last:border-b-0 max-sm:my-[var(--space-1)] max-sm:rounded-md max-sm:border max-sm:px-[var(--space-2)] max-sm:last:border-b"
    >
      <div className="flex flex-wrap items-start justify-between gap-[var(--space-2)]">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-x-[var(--space-2)]">
            <span className="font-mono text-[length:var(--type-utility-xs-size)] text-[var(--color-secondary)] wrap-anywhere">{item.label}</span>
            {item.skill && <SkillFacts facts={item.skill} sizeShown={level === 'full' && !item.loadError} />}
          </div>
          {item.description && (
            <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] wrap-anywhere">{item.description}</p>
          )}
          {item.loadError && (
            <p className="text-[length:var(--type-caption-size)] wrap-anywhere">
              <span className="font-medium text-[var(--color-error)]">Cannot load</span>
              <span className="text-[var(--color-muted)]">{`: ${item.loadError}`}</span>
            </p>
          )}
        </div>
        {item.fixedSentence ? (
          <p className="max-w-[28rem] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{item.fixedSentence}</p>
        ) : (
          <div className="flex shrink-0 flex-wrap items-center gap-[var(--space-1)] max-sm:w-full">
            <PhoneLabel>Permission</PhoneLabel>
            <div role="group" aria-label={`Permission for ${item.label}`} className="flex items-center gap-[var(--space-1)]">
              {POLICIES.map((choice) => (
                <TextToggle
                  key={choice}
                  size="sm"
                  data-choice={choice}
                  pressed={policy === choice}
                  disabled={disabled || item.refused.includes(choice)}
                  onPressedChange={() => { if (policy !== choice) onPolicy(item, choice) }}
                  className="pointer-coarse:min-h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]"
                >
                  {POLICY_WORDS[choice]}
                </TextToggle>
              ))}
            </div>
            {actions}
          </div>
        )}
        {item.fixedSentence && actions}
      </div>
      <div className="flex flex-wrap items-center gap-[var(--space-2)]">
        {item.levelLocked ? (
          item.fixedSentence ? null : (
            <>
              <PhoneLabel>What the model sees</PhoneLabel>
              <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{LOCKED_LEVEL_SENTENCE}</p>
            </>
          )
        ) : (
          <>
            <PhoneLabel>What the model sees</PhoneLabel>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={disabled}
                  data-choice="level"
                  aria-label={`What the model sees for ${item.label}: ${LEVEL_WORDS[level]}`}
                  className="h-7 gap-[var(--space-1)] px-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)] pointer-coarse:min-h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]"
                >
                  {LEVEL_WORDS[level]}
                  <CaretDown size={11} aria-hidden="true" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start">
                {LEVELS.map((choice) => (
                  <DropdownMenuItem key={choice} onClick={() => { if (choice !== level) onLevel(item, choice) }}>
                    <span>
                      <span className="font-medium">{LEVEL_WORDS[choice]}</span>
                      {`: ${LEVEL_MEANINGS[choice]}`}
                    </span>
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          </>
        )}
        <SaveStatusLine queue={queue} section={statusSection} name={item.key} />
      </div>
    </div>
  )
}
