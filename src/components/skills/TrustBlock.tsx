// TrustBlock — "Unverified skills": the skill trust choice, first on the
// Skills tab (spec C-3, FR-027, S-26, S-32). A radio group with one Tab stop
// (spec Accessibility "Trust block"): an arrow key selects and saves like a
// click, and the arrows stop at Allow and Deny instead of wrapping round, so
// the keyboard never passes through Allow (ruling 2026-11-26).

import { useId } from 'react'
import { Callout, CalloutDescription } from '@/components/ui/callout'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import type { ToolPolicy } from '@/lib/api/generated/openapi-types'
import type { PermissionSaveQueue } from '@/lib/permissionSaveQueue'
import { SaveStatusLine } from './PermissionRow'
import { POLICIES, POLICY_WORDS } from './skillsToolsModel'

const MEANINGS: Record<ToolPolicy, string> = {
  allow: 'Unverified skills install and load without a check.',
  ask: 'Installing or loading an unverified skill asks you first.',
  deny: 'Unverified skills cannot be installed or loaded.',
}

export function TrustBlock({ queue, disabled }: { queue: PermissionSaveQueue; disabled: boolean }) {
  const trust = queue.valueOf('skill_trust') as ToolPolicy | undefined
  const idBase = useId()
  const meaningId = (choice: ToolPolicy) => `${idBase}-meaning-${choice}`
  // One path for click, arrow keys, Home and End; choosing the stored value saves nothing.
  const choose = (value: string) => {
    if (value !== trust) queue.change([{ section: 'skill_trust', value: value as ToolPolicy }])
  }
  return (
    <section
      data-testid="trust-block"
      aria-labelledby="trust-block-title"
      className="flex flex-col gap-[var(--space-2)] rounded-md border border-[var(--color-border)] p-[var(--space-3)]"
    >
      <div className="flex flex-wrap items-center justify-between gap-[var(--space-2)]">
        <h3 id="trust-block-title" className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">
          Unverified skills
        </h3>
        <RadioGroup
          aria-label="Unverified skills"
          loop={false}
          value={trust ?? ''}
          onValueChange={choose}
          disabled={disabled}
          className="gap-[var(--space-1)]"
        >
          {POLICIES.map((choice) => (
            <RadioGroupItem
              key={choice}
              value={choice}
              aria-describedby={meaningId(choice)}
              className="w-auto px-[var(--space-2)] py-[var(--space-1)] text-[length:var(--type-caption-size)] pointer-coarse:min-h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]"
            >
              {POLICY_WORDS[choice]}
            </RadioGroupItem>
          ))}
        </RadioGroup>
      </div>
      <dl className="grid grid-cols-[auto_1fr] gap-x-[var(--space-2)] gap-y-[var(--space-1)] text-[length:var(--type-caption-size)]">
        {POLICIES.map((choice) => (
          <div key={choice} className="contents">
            <dt className="font-medium text-[var(--color-secondary)]">{POLICY_WORDS[choice]}</dt>
            <dd id={meaningId(choice)} className="text-[var(--color-muted)]">{MEANINGS[choice]}</dd>
          </div>
        ))}
      </dl>
      {trust === 'allow' && (
        <Callout variant="warning">
          <CalloutDescription>Allow turns off the check for unverified skills. Choose Ask to turn it back on.</CalloutDescription>
        </Callout>
      )}
      <SaveStatusLine queue={queue} section="skill_trust" />
    </section>
  )
}
