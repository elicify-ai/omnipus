/**
 * AutoApproveAskList — the collapsed "Still asks every time" disclosure on
 * the Security → Auto-approve card (SecuritySection.tsx::AutoApproveControl).
 *
 * Founder feedback (2026-09-24): the old card put the full grouped list of
 * 28 always-ask tools, the workspace path rule, an example, and the Windows
 * caveat into one long paragraph — "a huge blob of text, not well written".
 * This component is the redesign's collapsed-by-default list: one short
 * plain-language line per group (`AUTO_APPROVE_ASK_GROUPS`,
 * `src/lib/autoApproveAskGroups.ts`), collapsed until the operator asks to
 * see it.
 *
 * Built on `DisclosureRow`, the catalogued disclosure primitive
 * (`src/components/ui/disclosure-row.tsx`) also used by
 * `AdvancedDisclosure` — never a hand-rolled expand/collapse control
 * (design-system rule 14: prefer a catalogued component over a new one).
 */
import { useState } from 'react'
import { DisclosureRow } from '@/components/ui/disclosure-row'
import { AUTO_APPROVE_ASK_GROUPS } from '@/lib/autoApproveAskGroups'

export function AutoApproveAskList({ defaultOpen = false }: { defaultOpen?: boolean }) {
  const [open, setOpen] = useState(defaultOpen)

  return (
    <div
      className="rounded-md border border-[var(--color-border)] overflow-hidden"
      data-testid="auto-approve-ask-list"
    >
      <DisclosureRow
        expanded={open}
        onExpandedChange={setOpen}
        expandable
        caretSize={12}
        data-testid="auto-approve-ask-list-trigger"
        className="w-full rounded-none px-[var(--space-2-5)] py-[var(--space-2)] text-[length:var(--type-utility-xs-size)] text-[var(--color-secondary)]"
      >
        <span>Still asks every time</span>
        <span className="flex-1" aria-hidden="true" />
        <span className="text-[var(--color-muted)]">{open ? 'Hide list' : 'Show list'}</span>
      </DisclosureRow>

      {open && (
        <ul
          className="list-disc list-inside space-y-[var(--space-1)] px-[var(--space-2-5)] pb-[var(--space-2-5)] pt-[var(--space-1)] border-t border-[var(--color-border)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]"
          data-testid="auto-approve-ask-list-content"
        >
          {AUTO_APPROVE_ASK_GROUPS.map((group) => (
            <li key={group.label}>{group.label}</li>
          ))}
        </ul>
      )}
    </div>
  )
}
