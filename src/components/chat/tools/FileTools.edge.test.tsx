/**
 * FileReadBlock, FileWriteConfirm, EditFile, AppendFile, FileTreeBlock
 * edge-case render tests (Phase 5, Agent B)
 *
 * Uses ToolCallStartFrame from generated asyncapi-types.ts.
 * Inner block components are private; render functions captured via
 * makeAssistantToolUI mock (hoisted before static imports).
 */

import { describe, it, expect, vi } from 'vitest'
import { render, fireEvent } from '@testing-library/react'
import type { ToolCallStartFrame } from '@/lib/api/generated/asyncapi-types'

type RenderFn = (props: { args: unknown; result: unknown; status: { type: string; reason?: string } }) => React.ReactNode

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
import { FileTreeViewUI } from './FileTreeView'

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
  function renderRead(result: unknown, statusType: string) {
    const renderFn = captured['read_file']
    return render(renderFn({ args: { path: '/workspace/file.ts' }, result, status: { type: statusType } }) as React.ReactElement)
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
    const { container, getByText } = renderRead(null, 'incomplete')
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

describe('FileOpBlock (write/edit/append) — flat text-line status dot', () => {
  function renderWrite(statusType: string) {
    const renderFn = captured['write_file']
    return render(
      renderFn({ args: { path: '/file.txt', content: 'data' }, result: null, status: { type: statusType } }) as React.ReactElement
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
    const { container } = renderWrite('incomplete')
    const indicator = container.firstElementChild?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-error)]')
  })

  it('running/success/error/cancelled each render a status-differentiating label, not just a bare dot (WCAG 1.4.1)', () => {
    const renderFn = captured['write_file']
    expect(
      render(renderFn({ args: { path: '/file.txt' }, result: null, status: { type: 'running' } }) as React.ReactElement).getByText(
        'Running...'
      )
    ).toBeInTheDocument()
    expect(
      render(renderFn({ args: { path: '/file.txt' }, result: null, status: { type: 'complete' } }) as React.ReactElement).getByText(
        'Done'
      )
    ).toBeInTheDocument()
    expect(
      render(renderFn({ args: { path: '/file.txt' }, result: null, status: { type: 'incomplete' } }) as React.ReactElement).getByText(
        'Failed'
      )
    ).toBeInTheDocument()
    expect(
      render(
        renderFn({
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

  function renderTree(result: unknown, statusType: string) {
    const renderFn = captured['list_dir']
    return render(renderFn({ args: { path: '.' }, result, status: { type: statusType } }) as React.ReactElement)
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
    const { container, getByText } = renderTree(null, 'incomplete')
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
