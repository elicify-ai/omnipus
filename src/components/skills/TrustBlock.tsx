// TrustBlock — "Unverified skills": the skill trust choice, first on the
// Skills tab (spec C-3, FR-027, S-26, S-32).

import { Callout, CalloutDescription } from '@/components/ui/callout'
import { TextToggle } from '@/components/ui/text-toggle'
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
        <div className="flex items-center gap-[var(--space-1)]">
          {POLICIES.map((choice) => (
            <TextToggle
              key={choice}
              size="sm"
              pressed={trust === choice}
              disabled={disabled}
              className="pointer-coarse:min-h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]"
              onPressedChange={() => { if (trust !== choice) queue.change([{ section: 'skill_trust', value: choice }]) }}
            >
              {POLICY_WORDS[choice]}
            </TextToggle>
          ))}
        </div>
      </div>
      <dl className="grid grid-cols-[auto_1fr] gap-x-[var(--space-2)] gap-y-[var(--space-1)] text-[length:var(--type-caption-size)]">
        {POLICIES.map((choice) => (
          <div key={choice} className="contents">
            <dt className="font-medium text-[var(--color-secondary)]">{POLICY_WORDS[choice]}</dt>
            <dd className="text-[var(--color-muted)]">{MEANINGS[choice]}</dd>
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
