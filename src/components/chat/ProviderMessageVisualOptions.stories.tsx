import type { Meta, StoryObj } from '@storybook/react-vite'
import { ArrowsLeftRight, CaretDown, Hourglass, Wallet } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import {
  formatProviderCountdown,
  PROVIDER_DEMO_CONTENT,
  PROVIDER_DEMO_MAX_DETAIL_CHARS,
  useProviderDemoCountdown,
  type ProviderDemoKind,
} from './ProviderMessageDemoContent'

/**
 * PM-6 design exploration — three genuinely different visual directions for
 * provider-event messages: the rate-limit countdown, the Fallback model
 * note, out-of-credit, and the verbose raw-provider-payload variant.
 *
 * Static mocks only: no chat store, no frames, no API calls. The four cases
 * render identical content in every option (shared module
 * ProviderMessageDemoContent) so the founder compares treatments, not copy.
 * The countdown stories tick live. A normal assistant bubble (MessageItem)
 * is deliberately none of these: a provider event must never read as
 * assistant prose.
 *
 * WHY THE COMPONENTS LIVE IN THIS FILE: the Storybook Tailwind scan covers
 * src/components/ui/* (library.css @source lines) plus *.stories.* files
 * (.storybook/preview.css). A component file elsewhere under src/components/
 * compiles and renders but its unique classes are silently never generated
 * (the design-system skill rule 9.3 trap). Keeping the exploration markup in
 * this stories file puts every class inside the scan; if one of these
 * options graduates to production, it ships as a real component through the
 * four-part publication contract instead.
 *
 * Options:
 * - Option A "Provider Kind Card" — kind-colored left edge strip + icon +
 *   uppercase kind header; the event is a message KIND (amber retry, blue
 *   fallback, red error).
 * - Option B "Event Pill" — compact centered stadium chip with a
 *   RateLimitIndicator-style tint; a system event, not a message; the pill
 *   itself expands in the verbose story.
 * - Option C "Console Strip" — desaturated operator-console strip: mono
 *   metadata line + one small kind-colored status dot; all text monochrome.
 */

interface DemoMessageProps {
  kind: ProviderDemoKind
  /** Render the verbose "Technical details" disclosure (case 4). */
  verbose?: boolean
}

/** Countdown sentence split around the live mono value. */
function DemoMessageSentence({ kind, accent }: { kind: ProviderDemoKind; accent?: string }) {
  const content = PROVIDER_DEMO_CONTENT[kind]
  const remaining = useProviderDemoCountdown(content.countdownSeconds ?? 0)
  if (content.countdownSeconds === undefined) return <>{content.message}</>
  return (
    <>
      {content.countdownLead}{' '}
      <span className={cn('font-mono font-semibold', accent)}>
        {formatProviderCountdown(remaining)}
      </span>{' '}
      {content.countdownTail}
    </>
  )
}

/** Verbose "Technical details" disclosure — MessageItem's native <details>
 * pattern (PM-6's reused accordion) with MessageItem's 512-char cap. */
function DemoTechDetails({ kind, monoSummary }: { kind: ProviderDemoKind; monoSummary?: boolean }) {
  const content = PROVIDER_DEMO_CONTENT[kind]
  if (!verboseContentHasPayload(content.rawPayload)) return null
  return (
    <details className="mt-[var(--space-2)]">
      <summary
        className={cn(
          'inline-flex cursor-pointer select-none items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)] transition-colors hover:text-[var(--color-secondary)]',
          monoSummary && 'font-mono',
        )}
      >
        Technical details
      </summary>
      <pre className="mt-[var(--space-1)] max-h-40 overflow-y-auto whitespace-pre-wrap break-all rounded-md bg-[var(--color-code-surface)] px-[var(--space-2)] py-[var(--space-1)] font-mono text-[length:var(--type-code-size)] leading-relaxed text-[var(--color-secondary)]">
        {content.rawPayload.slice(0, PROVIDER_DEMO_MAX_DETAIL_CHARS)}
      </pre>
    </details>
  )
}

function verboseContentHasPayload(raw: string): boolean {
  return raw.trim().length > 0
}

// ─── Option A - Provider Kind Card ──────────────────────────────────────────

const KIND_CARD_META: Record<ProviderDemoKind, { label: string; Icon: typeof Hourglass; strip: string; text: string }> = {
  'rate-limit': {
    label: 'Provider retry',
    Icon: Hourglass,
    strip: 'bg-[var(--color-warning)]',
    text: 'text-[var(--color-warning)]',
  },
  fallback: {
    label: 'Fallback model',
    Icon: ArrowsLeftRight,
    strip: 'bg-[var(--color-info)]',
    text: 'text-[var(--color-info)]',
  },
  'out-of-credit': {
    label: 'Provider error',
    Icon: Wallet,
    strip: 'bg-[var(--color-error)]',
    text: 'text-[var(--color-error)]',
  },
}

function ProviderKindCardDemo({ kind, verbose = false }: DemoMessageProps) {
  const meta = KIND_CARD_META[kind]
  const { Icon } = meta
  return (
    <div className="w-full max-w-[85%]" role="status" aria-live="polite">
      <div className="flex overflow-hidden rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)]">
        <div aria-hidden="true" className={cn('w-[var(--space-0-5)] shrink-0', meta.strip)} />
        <div className="min-w-0 flex-1 px-[var(--space-3)] py-[var(--space-2-5)]">
          <div className="flex items-center gap-[var(--space-2)]">
            <Icon aria-hidden="true" size={13} className={meta.text} />
            <span
              className={cn(
                'text-[length:var(--type-label-size)] font-medium uppercase tracking-[var(--type-label-letter-spacing)]',
                meta.text,
              )}
            >
              {meta.label}
            </span>
          </div>
          <p className="mt-[var(--space-2)] text-[length:var(--type-body-compact-size)] leading-relaxed text-[var(--color-secondary)]">
            <DemoMessageSentence kind={kind} accent={meta.text} />
          </p>
          {verbose && <DemoTechDetails kind={kind} />}
        </div>
      </div>
    </div>
  )
}

// ─── Option B - Event Pill ──────────────────────────────────────────────────

const KIND_PILL_META: Record<ProviderDemoKind, { Icon: typeof Hourglass; border: string; fill: string; text: string }> = {
  'rate-limit': {
    Icon: Hourglass,
    border: 'border-[color-mix(in_srgb,var(--color-warning)_30%,transparent)]',
    fill: 'bg-[color-mix(in_srgb,var(--color-warning)_5%,transparent)]',
    text: 'text-[var(--color-warning)]',
  },
  fallback: {
    Icon: ArrowsLeftRight,
    border: 'border-[color-mix(in_srgb,var(--color-info)_30%,transparent)]',
    fill: 'bg-[color-mix(in_srgb,var(--color-info)_5%,transparent)]',
    text: 'text-[var(--color-info)]',
  },
  'out-of-credit': {
    Icon: Wallet,
    border: 'border-[color-mix(in_srgb,var(--color-error)_30%,transparent)]',
    fill: 'bg-[color-mix(in_srgb,var(--color-error)_5%,transparent)]',
    text: 'text-[var(--color-error)]',
  },
}

function ProviderEventPillDemo({ kind, verbose = false }: DemoMessageProps) {
  const meta = KIND_PILL_META[kind]
  const { Icon } = meta
  const body = (
    <>
      <Icon aria-hidden="true" size={13} className={cn('shrink-0', meta.text)} />
      <span className="text-[length:var(--type-caption-size)] text-[var(--color-secondary)]">
        <DemoMessageSentence kind={kind} accent={meta.text} />
      </span>
      {verbose && (
        <CaretDown aria-hidden="true" size={12} className="ml-[var(--space-1)] shrink-0 text-[var(--color-muted)]" />
      )}
    </>
  )
  const pillClasses = cn(
    'flex w-fit max-w-full items-center gap-[var(--space-2)] rounded-full border px-[var(--space-3)] py-[var(--space-2)] text-left',
    meta.border,
    meta.fill,
    verbose ? 'cursor-pointer select-none' : 'cursor-default',
  )
  if (verbose) {
    // The pill itself is the disclosure summary: clicking anywhere on the
    // chip expands the raw provider payload below it. <details> keeps the
    // UA default display (flex on <details> itself breaks toggling on some
    // engines); the pill centers with mx-auto instead.
    return (
      <div className="px-[var(--space-3)] py-[var(--space-2)]" role="status" aria-live="polite">
        <details className="mx-auto w-full max-w-[85%]">
          <summary className={cn(pillClasses, 'mx-auto list-none [&::-webkit-details-marker]:hidden')}>
            {body}
          </summary>
          <pre className="mt-[var(--space-2)] w-full max-h-40 overflow-y-auto whitespace-pre-wrap break-all rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-3)] py-[var(--space-2)] font-mono text-[length:var(--type-code-size)] leading-relaxed text-[var(--color-secondary)]">
            {PROVIDER_DEMO_CONTENT[kind].rawPayload.slice(0, PROVIDER_DEMO_MAX_DETAIL_CHARS)}
          </pre>
        </details>
      </div>
    )
  }
  return (
    <div className="flex justify-center px-[var(--space-3)] py-[var(--space-2)]" role="status" aria-live="polite">
      <div className={pillClasses}>{body}</div>
    </div>
  )
}

// ─── Option C - Console Strip ───────────────────────────────────────────────

const KIND_CONSOLE_META: Record<ProviderDemoKind, { dot: string; meta: string }> = {
  'rate-limit': { dot: 'bg-[var(--color-warning)]', meta: '429 · retry 2/3' },
  fallback: { dot: 'bg-[var(--color-info)]', meta: 'model · fallback' },
  'out-of-credit': { dot: 'bg-[var(--color-error)]', meta: '402 · out of credit' },
}

function ProviderConsoleStripDemo({ kind, verbose = false }: DemoMessageProps) {
  const meta = KIND_CONSOLE_META[kind]
  return (
    <div className="w-full max-w-[85%]" role="status" aria-live="polite">
      <div className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-3)] py-[var(--space-2)]">
        <div className="flex items-center gap-[var(--space-2)] font-mono text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
          <span aria-hidden="true" className={cn('inline-block h-[var(--space-1)] w-[var(--space-1)] shrink-0 rounded-full', meta.dot)} />
          <span>provider: {PROVIDER_DEMO_CONTENT[kind].provider.toLowerCase()}</span>
          <span aria-hidden="true" className="text-[var(--color-border)]">·</span>
          <span>{meta.meta}</span>
        </div>
        <p className="mt-[var(--space-2)] text-[length:var(--type-body-compact-size)] leading-relaxed text-[var(--color-secondary)]">
          <DemoMessageSentence kind={kind} />
        </p>
        {verbose && <DemoTechDetails kind={kind} monoSummary />}
      </div>
    </div>
  )
}

// ─── Stories ────────────────────────────────────────────────────────────────

const meta = {
  title: 'Chat/Provider message visual options',
  parameters: {
    layout: 'padded',
  },
} satisfies Meta

export default meta
type Story = StoryObj<typeof meta>

export const OptionARateLimitCountdown: Story = {
  name: 'Option A - Rate limit countdown',
  render: () => <ProviderKindCardDemo kind="rate-limit" />,
}

export const OptionAFallbackModel: Story = {
  name: 'Option A - Fallback model',
  render: () => <ProviderKindCardDemo kind="fallback" />,
}

export const OptionAOutOfCredit: Story = {
  name: 'Option A - Out of credit',
  render: () => <ProviderKindCardDemo kind="out-of-credit" />,
}

export const OptionAVerboseRawPayload: Story = {
  name: 'Option A - Verbose with raw payload',
  render: () => <ProviderKindCardDemo kind="fallback" verbose />,
}

export const OptionBRateLimitCountdown: Story = {
  name: 'Option B - Rate limit countdown',
  render: () => <ProviderEventPillDemo kind="rate-limit" />,
}

export const OptionBFallbackModel: Story = {
  name: 'Option B - Fallback model',
  render: () => <ProviderEventPillDemo kind="fallback" />,
}

export const OptionBOutOfCredit: Story = {
  name: 'Option B - Out of credit',
  render: () => <ProviderEventPillDemo kind="out-of-credit" />,
}

export const OptionBVerboseRawPayload: Story = {
  name: 'Option B - Verbose with raw payload',
  render: () => <ProviderEventPillDemo kind="fallback" verbose />,
}

export const OptionCRateLimitCountdown: Story = {
  name: 'Option C - Rate limit countdown',
  render: () => <ProviderConsoleStripDemo kind="rate-limit" />,
}

export const OptionCFallbackModel: Story = {
  name: 'Option C - Fallback model',
  render: () => <ProviderConsoleStripDemo kind="fallback" />,
}

export const OptionCOutOfCredit: Story = {
  name: 'Option C - Out of credit',
  render: () => <ProviderConsoleStripDemo kind="out-of-credit" />,
}

export const OptionCVerboseRawPayload: Story = {
  name: 'Option C - Verbose with raw payload',
  render: () => <ProviderConsoleStripDemo kind="fallback" verbose />,
}
