/**
 * FileReadBlock, FileWriteConfirm, EditFile, AppendFile, FileTreeBlock
 * edge-case render tests (Phase 5, Agent B)
 *
 * Uses ToolCallStartFrame from generated asyncapi-types.ts.
 * Inner block components are private; render functions captured via
 * makeAssistantToolUI mock (hoisted before static imports).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, fireEvent } from '@testing-library/react'
import type { ToolCallStartFrame } from '@/lib/api/generated/asyncapi-types'
import { useChatStore } from '@/store/chat'
import type { ToolCall } from '@/lib/api'

// Issue #617: `isError` (for read_file/file.read/list_dir) is now a real
// field on the render-prop object, set by omnipus-runtime.ts from the
// store's resolved ToolCall.status — it is NOT derivable from
// `status.type === 'incomplete'` any more. `toolCallId` (for
// write_file/edit_file/append_file, routed through FileWriteConfirm.tsx's
// FileOpRow) is the key FileOpRow uses to read the SAME resolved status
// from the store directly via a hook — see the `seedCall` helper below,
// which mirrors FileWriteConfirm.test.tsx's established pattern.
type RenderFn = (props: {
  toolCallId?: string
  args: unknown
  result: unknown
  status: { type: string; reason?: string }
  isError?: boolean
}) => React.ReactNode

/** seedCall puts a tool call in the store the way the WS frame handler does
 *  (mirrors FileWriteConfirm.test.tsx's seedCall — see that file's header
 *  comment for why FileOpRow needs a real store record rather than a bare
 *  `status.type === 'incomplete'` prop to render an error). */
function seedCall(toolCallId: string, partial: Partial<ToolCall>) {
  useChatStore.setState({
    toolCalls: {
      [toolCallId]: {
        id: toolCallId,
        call_id: toolCallId,
        tool: 'write_file',
        params: { path: '/file.txt' },
        status: 'success',
        ...partial,
      } as ToolCall & { call_id: string },
    },
  })
}

beforeEach(() => {
  useChatStore.setState({ toolCalls: {} })
})

// vi.hoisted runs before vi.mock factory and before all imports.
const captured = vi.hoisted((): Record<string, RenderFn> => ({}))

vi.mock('@assistant-ui/react', async (importOriginal) => {
  const original = await importOriginal<typeof import('@assistant-ui/react')>()
  return {
    ...original,
    makeAssistantToolUI: (config: Record<string, unknown>) => {
      if (typeof config.toolName === 'string') {
        captured[config.toolName] = config.render as RenderFn
      }
      return config
    },
  }
})

// Static imports: vi.mock intercepts makeAssistantToolUI before these run.
import { FileReadPreviewUI, FileReadAliasDotUI } from './FileReadPreview'
import { FileWriteConfirmUI, EditFileConfirmUI, AppendFileConfirmUI } from './FileWriteConfirm'
import { FileTreeViewUI, FileListAliasDotUI } from './FileTreeView'

// ── FileReadBlock result edge cases ───────────────────────────────────────────

describe.each([
  ['null result (running)', null, true],
  ['null result (done)', null, false],
  ['empty string result', '', false],
  ['single line content', 'hello world', false],
  ['20 line content (at threshold)', Array.from({ length: 20 }, (_, i) => `line ${i + 1}`).join('\n'), false],
  ['21 line content (truncated)', Array.from({ length: 21 }, (_, i) => `line ${i + 1}`).join('\n'), false],
  ['very long content', 'x'.repeat(100_000), false],
  ['unicode content', '\u{1F680}\n\u{1F480}\n⚡\n', false],
  ['content with XSS', '<script>alert(1)</script>\n', false],
  ['content with null bytes', 'before\x00after', false],
  ['number result (coerced)', 42, false],
] as Array<[string, unknown, boolean]>)(
  'FileReadBlock renders result "%s" without throwing',
  (_label, result, isRunning) => {
    it('renders', () => {
      const renderFn = captured['read_file']
      if (!renderFn) {
        expect(FileReadPreviewUI).toBeDefined()
        return
      }
      const status = isRunning ? { type: 'running' } : { type: 'complete' }
      expect(() => {
        const element = renderFn({ args: { path: '/workspace/file.txt' }, result, status })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── FileReadBlock args edge cases ─────────────────────────────────────────────

describe.each([
  ['empty args', {}],
  ['args with path', { path: '/workspace/src/main.ts' }],
  ['args with very long path', { path: '/' + 'a/'.repeat(500) + 'file.txt' }],
  ['args with unicode path', { path: '/workspace/\u{1F680}.ts' }],
  ['args with XSS in path', { path: '<script>alert(1)</script>.ts' }],
  ['args with offset and length', { path: '/file.txt', offset: 100, length: 50 }],
  ['args with null path', { path: null }],
] as Array<[string, Record<string, unknown>]>)(
  'FileReadBlock renders args "%s" without throwing',
  (_label, args) => {
    it('renders', () => {
      const renderFn = captured['read_file']
      if (!renderFn) {
        expect(FileReadPreviewUI).toBeDefined()
        return
      }
      expect(() => {
        const element = renderFn({ args, result: null, status: { type: 'running' } })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── FileReadBlock dot-alias (file.read) ───────────────────────────────────────

describe.each([
  ['dot alias — basic result', 'hello world', false],
  ['dot alias — null result running', null, true],
] as Array<[string, unknown, boolean]>)(
  'FileReadBlock (file.read alias) renders "%s" without throwing',
  (_label, result, isRunning) => {
    it('renders', () => {
      const renderFn = captured['file.read']
      if (!renderFn) {
        expect(FileReadAliasDotUI).toBeDefined()
        return
      }
      const status = isRunning ? { type: 'running' } : { type: 'complete' }
      expect(() => {
        const element = renderFn({ args: { path: '/workspace/file.ts' }, result, status })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── FileWriteConfirm args edge cases ──────────────────────────────────────────

describe.each([
  ['empty args', {}],
  ['args with path only', { path: '/workspace/file.txt' }],
  ['args with path and empty content', { path: '/workspace/file.txt', content: '' }],
  ['args with path and very long content', { path: '/workspace/file.txt', content: 'x'.repeat(100_000) }],
  ['args with unicode content', { path: '/workspace/file.txt', content: '\u{1F680}\n' }],
  ['args with XSS content', { path: '/workspace/file.txt', content: '<script>alert(1)</script>' }],
  ['args with null path', { path: null, content: 'data' }],
  ['args with null content', { path: '/workspace/file.txt', content: null }],
] as Array<[string, Record<string, unknown>]>)(
  'FileWriteBlock renders args "%s" without throwing',
  (_label, args) => {
    it('renders', () => {
      const renderFn = captured['write_file']
      if (!renderFn) {
        expect(FileWriteConfirmUI).toBeDefined()
        return
      }
      expect(() => {
        const element = renderFn({ args, result: null, status: { type: 'running' } })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── FileWriteConfirm status edge cases ────────────────────────────────────────

describe.each([
  ['running', 'running'],
  ['complete', 'complete'],
  ['incomplete/error', 'incomplete'],
] as Array<[string, string]>)(
  'FileWriteBlock renders status "%s" without throwing',
  (_label, statusType) => {
    it('renders', () => {
      const renderFn = captured['write_file']
      if (!renderFn) {
        expect(FileWriteConfirmUI).toBeDefined()
        return
      }
      expect(() => {
        const element = renderFn({
          args: { path: '/file.txt', content: 'data' },
          result: null,
          status: { type: statusType },
        })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── EditFileConfirmUI args edge cases ─────────────────────────────────────────

describe.each([
  ['empty args', {}],
  ['args with path', { path: '/workspace/file.ts' }],
  ['args with old and new string', { path: '/workspace/file.ts', old_string: 'foo', new_string: 'bar' }],
  ['args with very long old_string', { path: '/file.ts', old_string: 'x'.repeat(50_000), new_string: 'y' }],
  ['args with replace_all=true', { path: '/file.ts', old_string: 'a', new_string: 'b', replace_all: true }],
  ['args with null strings', { path: '/file.ts', old_string: null, new_string: null }],
  ['args with XSS content', { path: '/file.ts', old_string: '<script>', new_string: '' }],
] as Array<[string, Record<string, unknown>]>)(
  'EditFileBlock renders args "%s" without throwing',
  (_label, args) => {
    it('renders', () => {
      const renderFn = captured['edit_file']
      if (!renderFn) {
        expect(EditFileConfirmUI).toBeDefined()
        return
      }
      expect(() => {
        const element = renderFn({ args, result: null, status: { type: 'running' } })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── AppendFile args edge cases ────────────────────────────────────────────────

describe.each([
  ['empty args', {}],
  ['args with path and content', { path: '/log.txt', content: 'appended line\n' }],
  ['args with very long content', { path: '/log.txt', content: 'x'.repeat(100_000) }],
  ['args with null content', { path: '/log.txt', content: null }],
] as Array<[string, Record<string, unknown>]>)(
  'AppendFileBlock renders args "%s" without throwing',
  (_label, args) => {
    it('renders', () => {
      const renderFn = captured['append_file']
      if (!renderFn) {
        expect(AppendFileConfirmUI).toBeDefined()
        return
      }
      expect(() => {
        const element = renderFn({ args, result: null, status: { type: 'running' } })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── FileTreeBlock result edge cases ───────────────────────────────────────────

describe.each([
  ['null result (running)', null, true],
  ['null result (done)', null, false],
  ['empty string result', '', false],
  ['simple ls output', 'file1.txt\nfile2.txt\ndir1/\n', false],
  ['tree-style output', '├── src/\n│   ├── main.ts\n│   └── utils.ts\n└── README.md\n', false],
  ['200 entries (at cap)', Array.from({ length: 200 }, (_, i) => `file${i}.txt`).join('\n'), false],
  ['201 entries (over cap)', Array.from({ length: 201 }, (_, i) => `file${i}.txt`).join('\n'), false],
  ['very long single line', 'a'.repeat(50_000), false],
  ['unicode filenames', '\u{1F680}.ts\n\u{1F480}.js\n', false],
  ['XSS in filenames', '<script>alert(1)</script>\n', false],
  ['Windows-style paths (backslash dirs)', 'src\\\nutils\\\n', false],
] as Array<[string, unknown, boolean]>)(
  'FileTreeBlock renders result "%s" without throwing',
  (_label, result, isRunning) => {
    it('renders', () => {
      const renderFn = captured['list_dir']
      if (!renderFn) {
        expect(FileTreeViewUI).toBeDefined()
        return
      }
      const status = isRunning ? { type: 'running' } : { type: 'complete' }
      expect(() => {
        const element = renderFn({ args: { path: '.' }, result, status })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── ToolCallStartFrame params as args (using generated type) ──────────────────

// ── flat text-line status dot (ticket "Tool components in chat", P2) ────────
// The old bordered/backgrounded cards were replaced by a flat text line whose
// only status color comes from an 8px dot (running keeps the spinning icon in
// the same slot) — see GenericToolCall.tsx/toolStatusConfig.tsx for the
// reference language this mirrors. None of these blocks have a data-testid on
// their root, so assertions use `container` queries.

describe('FileReadBlock — flat text-line status dot', () => {
  function renderRead(result: unknown, statusType: string, isError?: boolean) {
    const renderFn = captured['read_file']
    return render(
      renderFn({ args: { path: '/workspace/file.ts' }, result, status: { type: statusType }, isError }) as React.ReactElement
    )
  }

  it('running: indicator is the spinning icon, not a dot', () => {
    const { container } = renderRead(null, 'running')
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.tagName.toLowerCase()).toBe('svg')
    expect(indicator?.getAttribute('class')).toContain('animate-spin')
  })

  it('success: indicator is an 8px success-colored dot', () => {
    const { container } = renderRead('const x = 1\n', 'complete')
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.tagName.toLowerCase()).toBe('span')
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-success)]')
    expect(indicator?.getAttribute('class')).toContain('rounded-full')
  })

  it('error: indicator is an 8px error-colored dot with a "Failed" label — not a green dot, not silent', () => {
    // Issue #617: `isError` is now a real, separate field on the render-prop
    // object (set by omnipus-runtime.ts from the store's resolved
    // ToolCall.status) — `status.type === 'incomplete'` alone no longer
    // drives the error dot. The pairing here (result:null, status:complete,
    // isError:true) matches a read that genuinely failed with no output.
    const { container, getByText } = renderRead(null, 'complete', true)
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.tagName.toLowerCase()).toBe('span')
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-error)]')
    expect(indicator?.getAttribute('class')).not.toContain('bg-[var(--color-success)]')
    expect(getByText('Failed')).toBeInTheDocument()
  })

  it('cancelled: indicator is an 8px muted-colored dot with a "Cancelled" label', () => {
    const renderFn = captured['read_file']
    const { container, getByText } = render(
      renderFn({
        args: { path: '/workspace/file.ts' },
        result: null,
        status: { type: 'incomplete', reason: 'cancelled' },
      }) as React.ReactElement
    )
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-muted)]')
    expect(indicator?.getAttribute('class')).not.toContain('bg-[var(--color-error)]')
    expect(getByText('Cancelled')).toBeInTheDocument()
  })

  it('a completed read of a genuinely empty file shows a success dot + "0 lines" — no silent terminal row', () => {
    const { container, getByText } = renderRead('', 'complete')
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-success)]')
    expect(getByText('0 lines')).toBeInTheDocument()
  })

  it('button toggle: disabled and aria-expanded omitted while running', () => {
    const { container } = renderRead(null, 'running')
    const toggle = container.querySelector('button')!
    expect(toggle).toBeDisabled()
    expect(toggle).not.toHaveAttribute('aria-expanded')
  })

  it('the root has no card-frame classes — flat/transparent on the thread', () => {
    const { container } = renderRead('const x = 1\n', 'complete')
    const root = container.firstElementChild as HTMLElement
    expect(root.className).not.toContain('rounded-md')
    expect(root.className).not.toContain('overflow-hidden')
    expect(root.className).not.toMatch(/\bborder\b/)
    expect(root.className).not.toContain('bg-[var(--color-surface-1)]')
  })

  it('no descendant carries a card-frame class (rounded-md/overflow-hidden/bg-surface-1) — border-l-2 accent survives', () => {
    const { container } = renderRead('const x = 1\n', 'complete')
    fireEvent.click(container.querySelector('button')!)
    const root = container.firstElementChild as HTMLElement
    expect(
      root.querySelector('[class*="rounded-md"], [class*="overflow-hidden"], [class*="bg-[var(--color-surface-1)]"]')
    ).toBeNull()
  })

  it('expanded content panel uses a left accent line (dark code styling preserved)', () => {
    const { container } = renderRead('const x = 1\n', 'complete')
    fireEvent.click(container.querySelector('button')!)
    const root = container.firstElementChild as HTMLElement
    const panel = root.children[1] as HTMLElement
    expect(panel.className).toContain('border-l-2')
    expect(panel.className).not.toMatch(/\bborder-b\b/)
    // The <pre> keeps its dark code-block background.
    const pre = panel.querySelector('pre')
    expect(pre?.className).toContain('bg-[#0d1117]')
  })
})

// M7 (second review wave): `captured['file.read']` (the BRD C.6.1.4
// dot-notation alias, FileReadAliasDotUI) previously only got the
// `.not.toThrow()` smoke coverage above — never an isError-threading
// assertion. It is a SEPARATE `makeAssistantToolUI({...})` render closure
// from the canonical `read_file` registration (FileReadPreview.tsx), so the
// canonical block's coverage above says nothing about whether this one
// wires `isError` correctly.
describe('FileReadBlock (file.read alias) live wiring — issue #617', () => {
  it('isError=true renders the error dot and "Failed" label', () => {
    const renderFn = captured['file.read']!
    const { container, getByText } = render(
      renderFn({
        args: { path: '/workspace/file.ts' },
        result: null,
        status: { type: 'complete' },
        isError: true,
      }) as React.ReactElement
    )
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-error)]')
    expect(getByText('Failed')).toBeInTheDocument()
  })

  it('isError=false with a result renders the success dot, not error', () => {
    const renderFn = captured['file.read']!
    const { container } = render(
      renderFn({
        args: { path: '/workspace/file.ts' },
        result: 'const x = 1\n',
        status: { type: 'complete' },
        isError: false,
      }) as React.ReactElement
    )
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-success)]')
    expect(indicator?.getAttribute('class')).not.toContain('bg-[var(--color-error)]')
  })
})

describe('FileOpBlock (write/edit/append) — flat text-line status dot', () => {
  const WRITE_CALL_ID = 'call-write-1'

  function renderWrite(statusType: string, toolCallId = WRITE_CALL_ID) {
    const renderFn = captured['write_file']
    return render(
      renderFn({
        toolCallId,
        args: { path: '/file.txt', content: 'data' },
        result: null,
        status: { type: statusType },
      }) as React.ReactElement
    )
  }

  it('running: indicator is the spinning icon, not a dot', () => {
    const { container } = renderWrite('running')
    const indicator = container.firstElementChild?.children[0] as HTMLElement
    expect(indicator?.tagName.toLowerCase()).toBe('svg')
    expect(indicator?.getAttribute('class')).toContain('animate-spin')
  })

  it('success: indicator is an 8px success-colored dot', () => {
    const { container } = renderWrite('complete')
    const indicator = container.firstElementChild?.children[0] as HTMLElement
    expect(indicator?.tagName.toLowerCase()).toBe('span')
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-success)]')
    expect(indicator?.getAttribute('class')).toContain('rounded-full')
  })

  it('error: indicator is an 8px error-colored dot', () => {
    // Issue #617: FileOpRow reads the real outcome from the chat store
    // (ToolCall.status via a hook keyed by toolCallId), not from
    // `status.type === 'incomplete'` — see FileWriteConfirm.test.tsx's file
    // header for the full rationale (a finished call always carries a
    // truthy result, so the part status is always 'complete' in
    // production). Seed the store with the SAME toolCallId this render
    // passes, matching how the WS frame handler populates it.
    seedCall(WRITE_CALL_ID, { status: 'error', result: 'disk full while writing output', error: 'disk full while writing output' })
    const { container } = renderWrite('complete')
    const indicator = container.firstElementChild?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-error)]')
  })

  it('running/success/error/cancelled each render a status-differentiating label, not just a bare dot (WCAG 1.4.1)', () => {
    const renderFn = captured['write_file']
    expect(
      render(
        renderFn({ toolCallId: 'call-running', args: { path: '/file.txt' }, result: null, status: { type: 'running' } }) as React.ReactElement
      ).getByText('Running...')
    ).toBeInTheDocument()
    expect(
      render(
        renderFn({ toolCallId: 'call-success', args: { path: '/file.txt' }, result: null, status: { type: 'complete' } }) as React.ReactElement
      ).getByText('Done')
    ).toBeInTheDocument()
    // Issue #617: producible pairing — seed the store for this toolCallId
    // with status:'error' and render with the real part status
    // (status:'complete', since the call carries a result).
    seedCall('call-error', { status: 'error', result: 'boom', error: 'boom' })
    expect(
      render(
        renderFn({ toolCallId: 'call-error', args: { path: '/file.txt' }, result: 'boom', status: { type: 'complete' } }) as React.ReactElement
      ).getByText('Failed')
    ).toBeInTheDocument()
    expect(
      render(
        renderFn({
          toolCallId: 'call-cancelled',
          args: { path: '/file.txt' },
          result: null,
          status: { type: 'incomplete', reason: 'cancelled' },
        }) as React.ReactElement
      ).getByText('Cancelled')
    ).toBeInTheDocument()
  })

  it('cancelled: indicator is an 8px muted-colored dot, not the red error dot', () => {
    const renderFn = captured['write_file']
    const { container } = render(
      renderFn({
        args: { path: '/file.txt' },
        result: null,
        status: { type: 'incomplete', reason: 'cancelled' },
      }) as React.ReactElement
    )
    const indicator = container.firstElementChild?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-muted)]')
    expect(indicator?.getAttribute('class')).not.toContain('bg-[var(--color-error)]')
  })

  it('the root has no card-frame classes and is not a toggle (write ops have no detail panel)', () => {
    const { container } = renderWrite('complete')
    const root = container.firstElementChild as HTMLElement
    expect(root.className).not.toContain('rounded-md')
    expect(root.className).not.toMatch(/\bborder\b/)
    expect(root.className).not.toContain('bg-[var(--color-surface-1)]')
    expect(container.querySelector('button')).toBeNull()
  })

  it('no descendant carries a card-frame class (rounded-md/overflow-hidden/bg-surface-1)', () => {
    const { container } = renderWrite('complete')
    const root = container.firstElementChild as HTMLElement
    expect(
      root.querySelector('[class*="rounded-md"], [class*="overflow-hidden"], [class*="bg-[var(--color-surface-1)]"]')
    ).toBeNull()
  })
})

describe('FileTreeBlock — flat text-line status dot', () => {
  const treeResult = 'file1.txt\nfile2.txt\ndir1/\n'

  function renderTree(result: unknown, statusType: string, isError?: boolean) {
    const renderFn = captured['list_dir']
    return render(
      renderFn({ args: { path: '.' }, result, status: { type: statusType }, isError }) as React.ReactElement
    )
  }

  it('running: indicator is the spinning icon, not a dot', () => {
    const { container } = renderTree(null, 'running')
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.tagName.toLowerCase()).toBe('svg')
    expect(indicator?.getAttribute('class')).toContain('animate-spin')
  })

  it('success: indicator is an 8px success-colored dot', () => {
    const { container } = renderTree(treeResult, 'complete')
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.tagName.toLowerCase()).toBe('span')
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-success)]')
    expect(indicator?.getAttribute('class')).toContain('rounded-full')
  })

  it('error: indicator is an 8px error-colored dot with a "Failed" label — not a green dot, not silent', () => {
    // Issue #617: `isError` is now a real, separate render-prop field — see
    // the equivalent FileReadBlock test above for the full rationale.
    const { container, getByText } = renderTree(null, 'complete', true)
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-error)]')
    expect(indicator?.getAttribute('class')).not.toContain('bg-[var(--color-success)]')
    expect(getByText('Failed')).toBeInTheDocument()
  })

  it('cancelled: indicator is an 8px muted-colored dot with a "Cancelled" label', () => {
    const renderFn = captured['list_dir']
    const { container, getByText } = render(
      renderFn({ args: { path: '.' }, result: null, status: { type: 'incomplete', reason: 'cancelled' } }) as React.ReactElement
    )
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-muted)]')
    expect(indicator?.getAttribute('class')).not.toContain('bg-[var(--color-error)]')
    expect(getByText('Cancelled')).toBeInTheDocument()
  })

  it('a completed listing of an empty directory shows a success dot + "0 entries" — no silent terminal row', () => {
    const { container, getByText } = renderTree('', 'complete')
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-success)]')
    expect(getByText('0 entries')).toBeInTheDocument()
  })

  it('button toggle: disabled and aria-expanded omitted while running', () => {
    const { container } = renderTree(null, 'running')
    const toggle = container.querySelector('button')!
    expect(toggle).toBeDisabled()
    expect(toggle).not.toHaveAttribute('aria-expanded')
  })

  it('the root has no card-frame classes — flat/transparent on the thread', () => {
    const { container } = renderTree(treeResult, 'complete')
    const root = container.firstElementChild as HTMLElement
    expect(root.className).not.toContain('rounded-md')
    expect(root.className).not.toContain('overflow-hidden')
    expect(root.className).not.toMatch(/\bborder\b/)
    expect(root.className).not.toContain('bg-[var(--color-surface-1)]')
  })

  it('no descendant carries a card-frame class (rounded-md/overflow-hidden/bg-surface-1) — border-l-2 accent survives', () => {
    const { container } = renderTree(treeResult, 'complete')
    fireEvent.click(container.querySelector('button')!)
    const root = container.firstElementChild as HTMLElement
    expect(
      root.querySelector('[class*="rounded-md"], [class*="overflow-hidden"], [class*="bg-[var(--color-surface-1)]"]')
    ).toBeNull()
  })

  it('expanded tree panel uses a left accent line, and entries keep their Folder/File icons + indentation', () => {
    const { container } = renderTree(treeResult, 'complete')
    fireEvent.click(container.querySelector('button')!)
    const root = container.firstElementChild as HTMLElement
    const panel = root.children[1] as HTMLElement
    expect(panel.className).toContain('border-l-2')
    // 3 entries parsed from treeResult — each keeps its own icon (svg) + name.
    const rows = panel.querySelectorAll(':scope > div')
    expect(rows.length).toBe(3)
    rows.forEach((row) => {
      expect(row.querySelector('svg')).toBeTruthy()
      expect((row as HTMLElement).style.paddingLeft).toBeDefined()
    })
  })
})

// M7 (second review wave): `captured['file.list']` (the BRD C.6.1.4
// dot-notation alias, FileListAliasDotUI) had ZERO coverage — not even a
// `.not.toThrow()` smoke test, unlike its `file.read` sibling. It is a
// SEPARATE `makeAssistantToolUI({...})` render closure from the canonical
// `list_dir` registration (FileTreeView.tsx).
describe('FileTreeBlock (file.list alias) live wiring — issue #617', () => {
  it('is registered as a distinct render function from the canonical list_dir registration', () => {
    expect(FileListAliasDotUI).toBeDefined()
    expect(captured['file.list']).toBeDefined()
    expect(captured['file.list']).not.toBe(captured['list_dir'])
  })

  it('isError=true renders the error dot and "Failed" label', () => {
    const renderFn = captured['file.list']!
    const { container, getByText } = render(
      renderFn({
        args: { path: '.' },
        result: null,
        status: { type: 'complete' },
        isError: true,
      }) as React.ReactElement
    )
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-error)]')
    expect(getByText('Failed')).toBeInTheDocument()
  })

  it('isError=false with a result renders the success dot, not error', () => {
    const renderFn = captured['file.list']!
    const { container } = render(
      renderFn({
        args: { path: '.' },
        result: 'file1.txt\nfile2.txt\n',
        status: { type: 'complete' },
        isError: false,
      }) as React.ReactElement
    )
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-success)]')
    expect(indicator?.getAttribute('class')).not.toContain('bg-[var(--color-error)]')
  })
})

describe.each([
  [
    'read_file frame with path',
    'read_file',
    {
      type: 'tool_call_start' as const,
      session_id: 'sess-1',
      tool: 'read_file',
      call_id: 'call-1',
      params: { path: '/workspace/src/main.ts', offset: 0, length: 100 },
    } satisfies ToolCallStartFrame,
  ],
  [
    'write_file frame',
    'write_file',
    {
      type: 'tool_call_start' as const,
      session_id: 'sess-1',
      tool: 'write_file',
      call_id: 'call-2',
      params: { path: '/workspace/out.txt', content: 'hello world' },
    } satisfies ToolCallStartFrame,
  ],
  [
    'list_dir frame',
    'list_dir',
    {
      type: 'tool_call_start' as const,
      session_id: 'sess-1',
      tool: 'list_dir',
      call_id: 'call-3',
      params: { path: '/workspace' },
    } satisfies ToolCallStartFrame,
  ],
  [
    'edit_file frame',
    'edit_file',
    {
      type: 'tool_call_start' as const,
      session_id: 'sess-1',
      tool: 'edit_file',
      call_id: 'call-4',
      params: { path: '/workspace/file.ts', old_string: 'foo', new_string: 'bar' },
    } satisfies ToolCallStartFrame,
  ],
] as Array<[string, string, ToolCallStartFrame]>)(
  'File tool renders ToolCallStartFrame params "%s" without throwing',
  (_label, toolName, frame) => {
    it('renders', () => {
      const renderFn = captured[toolName]
      if (!renderFn) {
        expect(FileReadPreviewUI).toBeDefined()
        return
      }
      expect(() => {
        const element = renderFn({ args: frame.params, result: null, status: { type: 'running' } })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)
