/**
 * Shared AssistantUI test stand-in (ds-safety-net, Route D ruling).
 *
 * The 36 ChatScreen test files hand-rolled their vi.mock('@assistant-ui/react')
 * factories — 19 drifting variants of the same idea, each rendering raw
 * createElement('button') / createElement('textarea') leaves.
 *
 * This module replaces ALL of them:
 *   - MockButton / MockTextarea — the ONE catalog-backed leaf pair
 *     (src/components/ui/button.tsx, src/components/ui/textarea.tsx). No raw
 *     controls here: the design-system locks scan this module like any other
 *     catalog use.
 *   - createAssistantUiMock(overrides?) — the ONE mocked-module factory the 36
 *     files use in their vi.mock. Defaults = the most common copy (the
 *     11-file variant group); per-file differences are shallow overrides
 *     merged per namespace ({ ComposerPrimitive: { Send: ... } }) or whole
 *     hooks (useMessage, useComposerRuntime, ...).
 *
 * Behavior contract: every component the factory returns receives EXACTLY the
 * props the hand-rolled copies forwarded (data-testid, type, disabled,
 * className, aria-*, tabIndex, handlers, children) — the catalog components
 * forward every prop they receive (Button places tabIndex={0} before
 * {...props}, so a caller-supplied tabIndex wins; type defaults to 'button'
 * only when the caller passes none).
 *
 * Test-only: import from vitest test files exclusively.
 */
import { forwardRef } from 'react'
import type * as React from 'react'
import type { ComponentProps, ReactNode } from 'react'
import { vi } from 'vitest'
import { cn } from '@/lib/utils'
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

/** Per-file differences over the default mock: namespace members or whole
 * hooks. Anything absent falls through to the default. */
export type AssistantUiMockOverrides = Record<
  string,
  Record<string, unknown> | ((...args: never[]) => unknown)
>

/**
 * Defaults = the most common hand-rolled copy (the 11-file variant group),
 * with the leaves rendered as elements. ChatScreen renders every member
 * below; per-file overrides only ever replace members, never the shape.
 */
/**
 * The default mock components, bound to names: the design-system scanners
 * classify an identifier-bound forwarding component as a registrable
 * extension boundary (`X#className`), while the same arrow inline in an
 * object literal is an unregistrable "unsupported" hard error.
 */
const MockThreadRoot = ({ children, className }: { children?: React.ReactNode; className?: string }) => (
  <div className={cn(className)}>{children}</div>
)

const MockThreadViewport = forwardRef(
  (
    {
      children,
      className,
      style,
      'data-testid': testId,
    }: {
      children?: React.ReactNode
      className?: string
      style?: React.CSSProperties
      'data-testid'?: string
    },
    ref: React.Ref<HTMLDivElement>,
  ) => (
    <div ref={ref} className={cn(className)} style={style} data-testid={testId}>
      {children}
    </div>
  ),
)

const MockMessageRoot = ({ children, className }: { children?: React.ReactNode; className?: string }) => (
  <div className={cn(className)}>{children}</div>
)

const MockComposerRoot = ({ children, className }: { children?: React.ReactNode; className?: string }) => (
  <div className={cn(className)}>{children}</div>
)

const MockComposerInput = ({
  disabled,
  placeholder,
  className,
  onChange,
  onKeyDown,
  onBlur,
}: {
  disabled?: boolean
  placeholder?: string
  className?: string
  onChange?: (e: React.ChangeEvent<HTMLTextAreaElement>) => void
  onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void
  onBlur?: () => void
}) => (
  <MockTextarea
    disabled={disabled}
    placeholder={placeholder}
    className={cn(className)}
    onChange={onChange}
    onKeyDown={onKeyDown}
    onBlur={onBlur}
    data-testid="composer-input"
  />
)

const MockComposerSend = ({
  disabled,
  children,
  className,
  'data-testid': testId,
}: {
  disabled?: boolean
  children?: React.ReactNode
  className?: string
  'data-testid'?: string
}) => (
  <MockButton
    type="button"
    disabled={disabled}
    className={cn(className)}
    data-testid={testId ?? 'chat-send'}
  >
    {children}
  </MockButton>
)

const MockComposerAddAttachment = ({
  disabled,
  children,
  className,
}: {
  disabled?: boolean
  children?: React.ReactNode
  className?: string
}) => (
  <MockButton type="button" disabled={disabled} className={cn(className)} data-testid="add-attachment">
    {children}
  </MockButton>
)

const MockAttachmentRoot = ({ children, className }: { children?: React.ReactNode; className?: string }) => (
  <div className={cn(className)}>{children}</div>
)

const MockAttachmentRemove = ({ children, className }: { children?: React.ReactNode; className?: string }) => (
  <MockButton type="button" className={cn(className)}>{children}</MockButton>
)

const defaults = {
  useThreadViewportStore: () => ({ getState: () => ({ isAtBottom: true }) }),

  ThreadPrimitive: { Root: MockThreadRoot, Viewport: MockThreadViewport, Messages: () => null },


  MessagePrimitive: { Root: MockMessageRoot, Parts: () => null },


  ComposerPrimitive: {
    Root: MockComposerRoot,
    Input: MockComposerInput,
    Send: MockComposerSend,
    AddAttachment: MockComposerAddAttachment,
    Attachments: () => null,
  },


  AttachmentPrimitive: {
    Root: MockAttachmentRoot,
    Name: () => null,
    Remove: MockAttachmentRemove,
    Thumb: () => null,
  },


  MessagePartPrimitive: {
    InProgress: () => null,
  },

  ActionBarPrimitive: {
    Root: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    Copy: ({ children }: { children: React.ReactNode }) => <span>{children}</span>,
  },

  AuiIf: () => null,

  useComposerRuntime: () => ({
    getState: () => ({ text: '' }),
    setText: vi.fn(),
    addAttachment: vi.fn(),
    subscribe: vi.fn(() => vi.fn()),
  }),

  useMessage: () => ({
    id: 'msg_streaming',
    role: 'assistant',
    status: { type: 'running' },
    content: [],
  }),

  useAttachment: vi.fn(() => ({
    id: 'att-default',
    name: 'file.txt',
    contentType: 'text/plain',
    file: undefined,
    status: { type: 'complete' },
    content: [],
  })),

  makeAssistantToolUI: () => () => null,
}

/**
 * Build the mocked '@assistant-ui/react' module object: the defaults with any
 * per-file overrides merged in. Namespace members are replaced individually
 * ({ ComposerPrimitive: { Send } } keeps the default Root/Input/...); hooks
 * and values are replaced wholesale. Anything the overrides leave out falls
 * through to the default.
 */
export function createAssistantUiMock(overrides?: AssistantUiMockOverrides) {
  const out: Record<string, unknown> = {}
  const keys = new Set([...Object.keys(defaults), ...Object.keys(overrides ?? {})])
  for (const key of keys) {
    const d = (defaults as Record<string, unknown>)[key]
    const o = (overrides ?? {}) as Record<string, unknown>
    const oVal = o[key]
    if (oVal === undefined) {
      out[key] = d
    } else if (isNamespace(d) && isNamespace(oVal)) {
      out[key] = { ...d, ...oVal }
    } else {
      out[key] = oVal
    }
  }
  return out
}

function isNamespace(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}
