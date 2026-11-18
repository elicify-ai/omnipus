// RecapLimitsArea.tsx — the collapsed "Recap limits" area of the Memory screen
// (effort-levels-spec-r10.md S-82, S-89, S-93; FR-054 "Two independent
// comparisons").
//
// Ten draft fields in two groups. The summary on the trigger compares the
// displayed values with the SHIPPED DEFAULTS ("all at defaults" / "N
// changed"); it never reflects the save state, which the section's
// SaveStatus shows. Typing only edits drafts: nothing is sent until the
// section's Save. An out-of-range draft shows the screen's range sentence.

import { useId } from 'react'
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from '@/components/ui/accordion'
import { Button } from '@/components/ui/button'
import { FormError } from '@/components/ui/FormError'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  CHATGPT_OUTPUT_LIMIT_TEXT,
  RECAP_LIMIT_FIELDS,
  RECAP_LIMIT_GROUP_TITLES,
  recapLimitError,
  recapLimitHelp,
  recapLimitsSummary,
  type RecapLimitDrafts,
  type RecapLimitField,
  type RecapLimitGroup,
  type RecapLimitKey,
} from './recapLimits'

interface RecapLimitsAreaProps {
  drafts: RecapLimitDrafts
  onChange: (key: RecapLimitKey, draft: string) => void
  onReset: () => void
  /** The recap model or a recap fallback runs on the ChatGPT sign-in route (S-89). */
  chatGptRoute: boolean
}

interface LimitFieldProps {
  field: RecapLimitField
  draft: string
  help: string
  extraHelp?: string
  onChange: (draft: string) => void
}

function LimitField({ field, draft, help, extraHelp, onChange }: LimitFieldProps) {
  const id = useId()
  const helpId = `${id}-help`
  const errorId = `${id}-error`
  const error = recapLimitError(field, draft)
  return (
    <div className="space-y-[var(--space-1)]">
      <Label htmlFor={id}>{field.unit ? `${field.label} (${field.unit})` : field.label}</Label>
      <p id={helpId} className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] leading-relaxed">
        {help}
        {extraHelp && (
          <>
            {' '}
            {extraHelp}
          </>
        )}
      </p>
      <Input
        id={id}
        type="number"
        inputMode="numeric"
        min={field.min}
        max={field.max}
        step={1}
        value={draft}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${helpId} ${errorId}` : helpId}
        onChange={(event) => onChange(event.target.value)}
        className="w-32"
      />
      <FormError id={errorId} error={error} />
    </div>
  )
}

const GROUP_ORDER: RecapLimitGroup[] = ['reads-writes', 'timing']

export function RecapLimitsArea({ drafts, onChange, onReset, chatGptRoute }: RecapLimitsAreaProps) {
  const summary = recapLimitsSummary(drafts)
  return (
    <Accordion type="single" collapsible>
      <AccordionItem value="recap-limits" className="border-0">
        <AccordionTrigger>
          <span className="flex items-baseline gap-[var(--space-2)]">
            <span>Recap limits</span>
            <span className="text-[length:var(--type-utility-xs-size)] font-[var(--font-weight-regular)] text-[var(--color-muted)]">
              {summary}
            </span>
          </span>
        </AccordionTrigger>
        <AccordionContent>
          <div className="space-y-[var(--space-3)]">
            {GROUP_ORDER.map((group) => (
              <div key={group} className="space-y-[var(--space-2-5)]">
                <p className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-secondary)]">
                  {RECAP_LIMIT_GROUP_TITLES[group]}
                </p>
                {RECAP_LIMIT_FIELDS.filter((field) => field.group === group).map((field) => (
                  <LimitField
                    key={field.key}
                    field={field}
                    draft={drafts[field.key]}
                    help={recapLimitHelp(field.key, drafts.recap_chars_per_token)}
                    extraHelp={field.key === 'recap_max_output_tokens' && chatGptRoute ? CHATGPT_OUTPUT_LIMIT_TEXT : undefined}
                    onChange={(draft) => onChange(field.key, draft)}
                  />
                ))}
              </div>
            ))}
            <div className="flex justify-start">
              <Button type="button" variant="outline" size="sm" onClick={onReset}>
                Reset to defaults
              </Button>
            </div>
          </div>
        </AccordionContent>
      </AccordionItem>
    </Accordion>
  )
}
