/**
 * Shared AssistantUI test stand-in leaves (ds-safety-net, Route D ruling).
 *
 * The 36 ChatScreen test files used to hand-roll their
 * `vi.mock('@assistant-ui/react')` primitive stand-ins, each rendering raw
 * `createElement('button')` / `createElement('textarea')` leaves — 36 copies
 * of raw controls, each needing its own reviewed-boundary ledger entry.
 *
 * This module is the ONE catalog-backed leaf pair those mocks now render.
 * It contains no raw controls: every leaf is the catalogued `Button`
 * (src/components/ui/button.tsx) or `Textarea` (src/components/ui/textarea.tsx),
 * scanned by the design-system locks like any other catalog use.
 *
 * Behavior contract with the codemod'd mocks: the stand-in receives EXACTLY
 * the props each mock's destructuring forwards (data-testid, type, disabled,
 * className, aria-*, tabIndex, handlers, children) and passes them straight
 * through — the catalog components already forward every prop they receive
 * (Button places tabIndex={0} before {...props}, so a caller-supplied tabIndex
 * wins; type defaults to 'button' only when the caller passes none).
 *
 * Test-only: import from vitest test files' vi.mock factories exclusively.
 */
import type { ComponentProps, ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'

type ButtonProps = ComponentProps<typeof Button>
type TextareaProps = ComponentProps<typeof Textarea>

/**
 * Mock props stay loose on PURPOSE: the mock components' destructurings hand
 * values like `aria-disabled: string | boolean` straight through, which the
 * catalog components' precise Booleanish types would reject. The casts live
 * inside the two stand-ins; the catalog components keep their strict props.
 */
export interface MockButtonProps {
  [key: string]: unknown
}

export interface MockTextareaProps {
  [key: string]: unknown
}

/** Catalog-backed stand-in for the raw buttons the AssistantUI mocks rendered. */
export function MockButton(props: MockButtonProps) {
  const { children, ...rest } = props
  return <Button {...(rest as ButtonProps)}>{children as ReactNode}</Button>
}

/** Catalog-backed stand-in for the raw textareas the AssistantUI mocks rendered. */
export function MockTextarea(props: MockTextareaProps) {
  const { children, ...rest } = props
  return <Textarea {...(rest as TextareaProps)}>{children as ReactNode}</Textarea>
}
