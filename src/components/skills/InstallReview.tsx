// InstallReview — the review step before a skill is installed (Skills &
// Tools C-13, S-27, S-55, S-56, D15): the person sees the staged skill,
// picks its category and installs exactly the reviewed revision. The outcome
// line reports what the server confirms, never what was asked for.

import { useId, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import type { SkillCategory, SkillInstallReview, ToolPolicy } from '@/lib/api/generated/openapi-types'
import { SKILL_CATEGORY_WORDS } from './skillsToolsModel'

/** Trust words: today's three, plus the retired names an older caller may hold. */
type TrustWord = ToolPolicy | 'allow_all' | 'warn_unverified' | 'block_unverified'

const TRUST_FROM_RETIRED: Record<TrustWord, ToolPolicy> = {
  allow: 'allow',
  ask: 'ask',
  deny: 'deny',
  allow_all: 'allow',
  warn_unverified: 'ask',
  block_unverified: 'deny',
}

/** What the review shows. // not-wire-format: built from SkillInstallReview and the stored trust */
export interface InstallReviewView {
  reviewId: string
  skillName: string
  version: string
  category: string | null
  verified: boolean
  /** How the catalog lookup went (C-13); `unavailable` means it got no answer. */
  verification?: SkillInstallReview['verification']
  trust: TrustWord
}

/** The confirmed outcome of an install (C-13). // not-wire-format */
export type InstallOutcome =
  | { kind: 'confirming' }
  | { kind: 'checking' }
  | { kind: 'installed' }
  | { kind: 'not_installed'; reason?: string }
  | { kind: 'unknown' }

const CATEGORIES = Object.keys(SKILL_CATEGORY_WORDS) as SkillCategory[]

function isCategory(value: string | null): value is SkillCategory {
  return value !== null && (CATEGORIES as string[]).includes(value)
}

function outcomeText(outcome: InstallOutcome, skillName: string): string {
  switch (outcome.kind) {
    case 'confirming': return 'Installing…'
    case 'checking': return 'Could not confirm the install. Checking…'
    case 'installed': return `Installed ${skillName}.`
    case 'not_installed': return outcome.reason ? `Not installed: ${outcome.reason.replace(/\.+$/, '')}.` : 'Not installed.'
    case 'unknown': return "Couldn't confirm the install. Open Skills to see what is installed."
  }
}

interface InstallReviewProps {
  review: InstallReviewView
  outcome?: InstallOutcome
  /** True while the install request is on its way. */
  installing?: boolean
  onInstall?: (category: SkillCategory) => void
  /** Reads the install status again; never installs again. */
  onRetry?: () => void
  /** Before Install: discards the staged copy (DELETE) and closes. */
  onCancel?: () => void
  /** True while the discard request is on its way. */
  cancelling?: boolean
  /** After a known outcome: closes the review. */
  onClose?: () => void
}

export function InstallReview({
  review, outcome, installing = false, onInstall, onRetry, onCancel, cancelling = false, onClose,
}: InstallReviewProps) {
  const [category, setCategory] = useState<SkillCategory | null>(isCategory(review.category) ? review.category : null)
  const [missingCategory, setMissingCategory] = useState(false)
  const blockedId = useId()
  const errorId = useId()
  const trust = TRUST_FROM_RETIRED[review.trust]
  const blocked = !review.verified && trust === 'deny'
  const started = outcome !== undefined

  const install = () => {
    if (!category) {
      setMissingCategory(true)
      return
    }
    setMissingCategory(false)
    onInstall?.(category)
  }

  return (
    <section
      aria-label={`Review ${review.skillName}`}
      className="flex flex-col gap-[var(--space-2)] rounded-md border border-[var(--color-border)] p-[var(--space-3)]"
    >
      <div className="flex flex-wrap items-baseline gap-[var(--space-2)]">
        <h3 className="font-mono text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">{review.skillName}</h3>
        <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{review.version}</span>
        <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
          {review.verified ? 'Verified' : 'Unverified'}
        </span>
      </div>
      <div className="flex flex-wrap items-center gap-[var(--space-2)]">
        <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">Category</span>
        <Select value={category ?? undefined} onValueChange={(value) => { setCategory(value as SkillCategory); setMissingCategory(false) }} disabled={started}>
          <SelectTrigger aria-label="Category" aria-describedby={missingCategory ? errorId : undefined} className="w-56">
            <SelectValue placeholder="Choose a category" />
          </SelectTrigger>
          <SelectContent>
            {CATEGORIES.map((value) => (
              <SelectItem key={value} value={value}>{SKILL_CATEGORY_WORDS[value]}</SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      {review.verification === 'unavailable' && (
        <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">Not verified: the catalog did not answer.</p>
      )}
      {missingCategory && (
        <p id={errorId} className="text-[length:var(--type-caption-size)] text-[var(--color-error)]">Choose a category.</p>
      )}
      {blocked && (
        <p id={blockedId} className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
          Unverified skills are set to Deny above. Choose Ask or Allow to install this skill.
        </p>
      )}
      <div className="flex flex-wrap items-center gap-[var(--space-2)]">
        {!started && (
          <Button
            size="sm"
            disabled={blocked || installing || cancelling}
            aria-describedby={blocked ? blockedId : undefined}
            onClick={install}
          >
            Install skill
          </Button>
        )}
        {/* Before Install, Cancel discards the staged copy. After Install
            is pressed it stays disabled until the outcome is known (C-13),
            then the review closes with Close. */}
        {!started && onCancel && (
          <Button size="sm" variant="ghost" disabled={installing || cancelling} onClick={onCancel}>Cancel</Button>
        )}
        {started && (outcome.kind === 'checking' || outcome.kind === 'confirming') && (
          <Button size="sm" variant="ghost" disabled>Cancel</Button>
        )}
        {started && outcome.kind !== 'checking' && outcome.kind !== 'confirming' && onClose && (
          <Button size="sm" variant="ghost" onClick={onClose}>Close</Button>
        )}
      </div>
      {!outcome && installing && (
        <p className="text-[length:var(--type-caption-size)]">Installing…</p>
      )}
      {outcome && (
        <div className="flex flex-wrap items-center gap-[var(--space-2)] text-[length:var(--type-caption-size)]">
          <span>{outcomeText(outcome, review.skillName)}</span>
          {outcome.kind === 'unknown' && onRetry && (
            <Button size="sm" variant="outline" onClick={onRetry}>Retry</Button>
          )}
        </div>
      )}
    </section>
  )
}
