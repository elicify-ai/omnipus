/**
 * BashOutputBlock edge-case render tests (consolidation of the retired
 * TerminalOutput.edge.test.tsx (`exec`) and WorkspaceShellUI.edge.test.tsx
 * (`workspace_shell` / `workspace_shell_bg`) per ADR-036 §3.1, §6 — one
 * unified `bash` tool replaces all three.
 *
 * Uses ToolCallStartFrame / ToolCallResultFrame from generated asyncapi-types.ts.
 *
 * Note: BashOutputBlock is not exported from BashOutput.tsx — render functions
 * are captured via a makeAssistantToolUI mock, one per registered tool name
 * (bash canonical + the 5 legacy aliases), keyed by config.toolName.
 *
 * vi.hoisted() is used to initialise the capture container before vi.mock runs,
 * avoiding the temporal dead zone issue with const declarations.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render } from '@testing-library/react'
import { act } from 'react'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import type { ToolCallStartFrame, ToolCallResultFrame } from '@/lib/api/generated/asyncapi-types'

// Issue #617: `isError` is now a real field on the render-prop object (set
// by omnipus-runtime.ts from the store's resolved ToolCall.status, then
// threaded through by AssistantUI's message-part state — see
// BashOutput.tsx's makeBashUI). It is NOT derivable from `status.type ===
// 'incomplete'` — that can never be true for a finished call carrying a
// result. Tests that need an error render must now pass `isError: true`
// explicitly, the same way the real pipeline would.
type RenderFn = (props: { args: unknown; result: unknown; status: { type: string; reason?: string }; isError?: boolean }) => React.ReactNode

// vi.hoisted runs before vi.mock factory and before all imports.
const captured = vi.hoisted(() => ({
  bashRender: null as RenderFn | null,
  execRender: null as RenderFn | null,
  shellRender: null as RenderFn | null,
  shellBgRender: null as RenderFn | null,
}))

vi.mock('@assistant-ui/react', async (importOriginal) => {
  const original = await importOriginal<typeof import('@assistant-ui/react')>()
  return {
    ...original,
    makeAssistantToolUI: (config: Record<string, unknown>) => {
      if (config.toolName === 'bash') captured.bashRender = config.render as RenderFn
      if (config.toolName === 'exec') captured.execRender = config.render as RenderFn
      if (config.toolName === 'workspace_shell') captured.shellRender = config.render as RenderFn
      if (config.toolName === 'workspace_shell_bg') captured.shellBgRender = config.render as RenderFn
      return config
    },
  }
})

// Static imports: vi.mock intercepts makeAssistantToolUI before these run.
import {
  BashOutputUI,
  ExecLegacyUI,
  WorkspaceShellLegacyUI,
  WorkspaceShellDotLegacyUI,
  WorkspaceShellBgLegacyUI,
  WorkspaceShellBgDotLegacyUI,
} from './BashOutput'

it('registers the canonical bash tool and all 5 legacy aliases', () => {
  expect(BashOutputUI).toBeDefined()
  expect(ExecLegacyUI).toBeDefined()
  expect(WorkspaceShellLegacyUI).toBeDefined()
  expect(WorkspaceShellDotLegacyUI).toBeDefined()
  expect(WorkspaceShellBgLegacyUI).toBeDefined()
  expect(WorkspaceShellBgDotLegacyUI).toBeDefined()
  expect(captured.bashRender).not.toBeNull()
})

// ── flat text-line status dot (ticket "Tool components in chat", P2) ────────
// The old bordered/backgrounded card (rounded-md border overflow-hidden +
// status-tinted border + bg-surface-1 header) is gone — the row is now a
// flat text line whose only status color comes from an 8px dot (running
// keeps the spinning icon in the same slot). The dark terminal output panel
// keeps its own identity (bg-[#0d1117]) but is no longer wrapped in a
// bordered outer frame — it now sits behind a border-l-2 left accent line.

describe('bash — flat text-line status dot', () => {
  /** The toggle button is the first (and only) <button> in the row; its
   * first child is always the status indicator (dot or spinner). */
  function getIndicatorEl(container: HTMLElement) {
    return container.querySelector('button')?.children[0] as HTMLElement | undefined
  }

  it('running: indicator is the spinning icon, not a dot', () => {
    if (!captured.bashRender) {
      expect(BashOutputUI).toBeDefined()
      return
    }
    const { container } = render(
      captured.bashRender({
        args: { command: 'sleep 1' },
        result: null,
        status: { type: 'running' },
      }) as React.ReactElement
    )
    const indicator = getIndicatorEl(container)
    expect(indicator?.tagName.toLowerCase()).toBe('svg')
    expect(indicator?.getAttribute('class')).toContain('animate-spin')
  })

  it('complete (success): indicator is an 8px success-colored dot', () => {
    if (!captured.bashRender) {
      expect(BashOutputUI).toBeDefined()
      return
    }
    const { container } = render(
      captured.bashRender({
        args: { command: 'echo hi' },
        result: 'hi\n',
        status: { type: 'complete' },
      }) as React.ReactElement
    )
    const indicator = getIndicatorEl(container)
    expect(indicator?.tagName.toLowerCase()).toBe('span')
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-success)]')
    expect(indicator?.getAttribute('class')).toContain('rounded-full')
  })

  it('errored (isError=true): indicator is an 8px error-colored dot', () => {
    if (!captured.bashRender) {
      expect(BashOutputUI).toBeDefined()
      return
    }
    // Issue #617: a finished bash call always carries a truthy `result`, so
    // AssistantUI's part status is 'complete' in production — never
    // 'incomplete' (that status can only arise for a RESULTLESS part; see
    // 'cancelled' below for the one legitimate resultless case this file
    // still covers). The real signal for a genuine failure is `isError`,
    // sourced from the store's resolved ToolCall.status by
    // omnipus-runtime.ts. {result: truthy, status: incomplete} — the old
    // shape this test used — is a pair BashOutputBlock never actually
    // receives; {result: truthy, status: complete, isError: true} is.
    const { container } = render(
      captured.bashRender({
        args: { command: 'false' },
        result: 'exit 1',
        status: { type: 'complete' },
        isError: true,
      }) as React.ReactElement
    )
    const indicator = getIndicatorEl(container)
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-error)]')
  })

  it('the outer container has no card-frame classes — flat/transparent on the thread', () => {
    if (!captured.bashRender) {
      expect(BashOutputUI).toBeDefined()
      return
    }
    const { container } = render(
      captured.bashRender({
        args: { command: 'echo hi' },
        result: 'hi\n',
        status: { type: 'complete' },
      }) as React.ReactElement
    )
    const root = container.firstElementChild as HTMLElement
    expect(root.className).not.toContain('rounded-md')
    expect(root.className).not.toContain('border')
    expect(root.className).not.toContain('overflow-hidden')
    expect(root.className).not.toContain('bg-[var(--color-surface-1)]')
  })

  it('the output panel (expanded by default) uses a left accent line, not a full bordered frame', () => {
    if (!captured.bashRender) {
      expect(BashOutputUI).toBeDefined()
      return
    }
    const { container } = render(
      captured.bashRender({
        args: { command: 'echo hi' },
        result: 'hi\n',
        status: { type: 'complete' },
      }) as React.ReactElement
    )
    // BashOutputBlock defaults to expanded — no click needed.
    const panel = container.querySelector('.border-l-2') as HTMLElement | null
    expect(panel).toBeTruthy()
    expect(panel?.className).not.toContain('border-t')
  })

  it('the dark terminal output panel keeps its own identity (bg-[#0d1117]) without an outer border', () => {
    if (!captured.bashRender) {
      expect(BashOutputUI).toBeDefined()
      return
    }
    const { container } = render(
      captured.bashRender({
        args: { command: 'echo hi' },
        result: 'hi\n',
        status: { type: 'complete' },
      }) as React.ReactElement
    )
    const terminal = container.querySelector('[class*="0d1117"]') as HTMLElement | null
    expect(terminal).toBeTruthy()
    expect(terminal?.className).not.toContain('border')
  })

  it('cancelled: indicator is an 8px muted-colored dot with a "Cancelled" label, not the red error dot', () => {
    if (!captured.bashRender) {
      expect(BashOutputUI).toBeDefined()
      return
    }
    const { container, getByText } = render(
      captured.bashRender({
        args: { command: 'sleep 30' },
        result: null,
        status: { type: 'incomplete', reason: 'cancelled' },
      }) as React.ReactElement
    )
    const indicator = getIndicatorEl(container)
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-muted)]')
    expect(indicator?.getAttribute('class')).not.toContain('bg-[var(--color-error)]')
    expect(getByText('Cancelled')).toBeInTheDocument()
  })

  it('the caret lives INSIDE the toggle button (no unjustified split-out sibling caret)', () => {
    if (!captured.bashRender) {
      expect(BashOutputUI).toBeDefined()
      return
    }
    const { container } = render(
      captured.bashRender({
        args: { command: 'echo hi' },
        result: 'hi\n',
        status: { type: 'complete' },
      }) as React.ReactElement
    )
    const button = container.querySelector('button')!
    expect(button.querySelector('svg[class*="Caret" i], svg')).toBeTruthy()
    // The row has exactly one button and no caret rendered as its sibling.
    expect(container.querySelectorAll('button').length).toBe(1)
  })

  it('no descendant carries a card-frame class (rounded-md/overflow-hidden/bg-surface-1) — border-l-2 accent survives', () => {
    if (!captured.bashRender) {
      expect(BashOutputUI).toBeDefined()
      return
    }
    const { container } = render(
      captured.bashRender({
        args: { command: 'echo hi' },
        result: 'hi\n',
        status: { type: 'complete' },
      }) as React.ReactElement
    )
    const root = container.firstElementChild as HTMLElement
    expect(
      root.querySelector('[class*="rounded-md"], [class*="overflow-hidden"], [class*="bg-[var(--color-surface-1)]"]')
    ).toBeNull()
  })

  it('running with no output: the inline "Executing..." spinner keeps animate-spin', () => {
    if (!captured.bashRender) {
      expect(BashOutputUI).toBeDefined()
      return
    }
    const { container, getByText } = render(
      captured.bashRender({
        args: { command: 'sleep 30' },
        result: null,
        status: { type: 'running' },
      }) as React.ReactElement
    )
    expect(getByText('Executing...')).toBeInTheDocument()
    const spinners = container.querySelectorAll('.animate-spin')
    // One in the header status indicator, one in the "Executing..." row.
    expect(spinners.length).toBeGreaterThanOrEqual(2)
  })
})

// ── bash (canonical) result edge cases ────────────────────────────────────────

describe.each([
  ['null result (running)', null, true],
  ['null result (done)', null, false],
  ['empty string result', '', false],
  ['single line output', 'hello world', false],
  ['multiline output', 'line1\nline2\nline3', false],
  ['very long output', 'x'.repeat(100_000), false],
  ['unicode output', '\u{1F680}\u{1F480}⚡\u{1F389}', false],
  ['ANSI escape codes in output', '\x1b[31mred text\x1b[0m', false],
  ['null bytes in output', 'before\x00after', false],
  ['output with XSS content', '<script>alert(1)</script>', false],
  ['number result (coerced to string)', 42, false],
  ['object result (coerced to string)', { exit_code: 0 }, false],
  ['array result (coerced to string)', [1, 2, 3], false],
  ['boolean result (coerced to string)', false, false],
] as Array<[string, unknown, boolean]>)(
  'bash renders result "%s" without throwing',
  (_label, result, isRunning) => {
    it('renders', () => {
      if (!captured.bashRender) {
        expect(BashOutputUI).toBeDefined()
        return
      }
      const status = isRunning ? { type: 'running' } : { type: 'complete' }
      expect(() => {
        const element = captured.bashRender!({ args: { command: 'echo hi' }, result, status })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// Issue #617: this used to be the last row of the table above
// (`['error state output', ..., isError:true]`, forcing `status:'incomplete'`
// — a pairing BashOutputBlock never actually receives for a finished call —
// and asserting only `.not.toThrow()`, which would have passed even if the
// error dot silently rendered as success). Pulled out into its own test with
// the producible shape (truthy result + status:complete + isError:true) and
// a real assertion on the rendered outcome, not just "didn't crash".
it('a genuine error outcome (isError=true) renders the error-colored dot and "Failed" label, not silently as success', () => {
  if (!captured.bashRender) {
    expect(BashOutputUI).toBeDefined()
    return
  }
  const { container, getByText } = render(
    captured.bashRender!({
      args: { command: 'echo hi' },
      result: 'error: command not found',
      status: { type: 'complete' },
      isError: true,
    }) as React.ReactElement
  )
  const indicator = container.querySelector('button')?.children[0] as HTMLElement | undefined
  expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-error)]')
  expect(getByText('Failed')).toBeInTheDocument()
})

// ── bash (canonical) args edge cases — ADR-036 §3.1 schema ───────────────────

describe.each([
  ['empty args', {}],
  ['args with command', { command: 'echo hello' }],
  ['args with very long command', { command: 'a'.repeat(10_000) }],
  ['args with cwd', { command: 'npm test', cwd: 'sub/dir' }],
  ['args with timeout_seconds', { command: 'sleep 10', timeout_seconds: 5 }],
  ['args with run_in_background', { command: 'tail -f log', run_in_background: true }],
  ['args with persistent', { command: 'tail -f log', run_in_background: true, persistent: true }],
  ['args with description', { description: 'background watcher', command: 'tail -f log' }],
  ['args with action=run', { action: 'run', command: 'ls' }],
  ['args with action=poll', { action: 'poll', session_id: 'sess-1' }],
  ['args with action=read', { action: 'read', session_id: 'sess-1' }],
  ['args with action=kill', { action: 'kill', session_id: 'sess-1' }],
  ['args with all bash fields', { command: 'npm run dev', cwd: 'sub', timeout_seconds: 120, run_in_background: true, persistent: false, action: 'run' }],
  ['args with unicode command', { command: 'ls /tmp/\u{1F680}' }],
  ['args with XSS in command', { command: '<script>alert(1)</script>' }],
  ['args with null command', { command: null }],
  ['args with undefined command', { command: undefined }],
] as Array<[string, Record<string, unknown>]>)(
  'bash renders args "%s" without throwing',
  (_label, args) => {
    it('renders', () => {
      if (!captured.bashRender) {
        expect(BashOutputUI).toBeDefined()
        return
      }
      expect(() => {
        const element = captured.bashRender!({ args, result: null, status: { type: 'running' } })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── ToolCallStartFrame params as args (using generated type) ──────────────────

describe.each([
  [
    'bash frame — run action',
    {
      type: 'tool_call_start' as const,
      session_id: 'sess-1',
      tool: 'bash',
      call_id: 'call-1',
      params: { command: 'ls /tmp', action: 'run' },
    } satisfies ToolCallStartFrame,
    null as unknown,
    'running' as const,
  ],
  [
    'bash frame — poll action',
    {
      type: 'tool_call_start' as const,
      session_id: 'sess-1',
      tool: 'bash',
      call_id: 'call-2',
      params: { action: 'poll', session_id: 'pty-sess-1' },
    } satisfies ToolCallStartFrame,
    'output text' as unknown,
    'complete' as const,
  ],
  [
    'bash frame — empty params',
    {
      type: 'tool_call_start' as const,
      session_id: 'sess-1',
      tool: 'bash',
      call_id: 'call-3',
      params: {},
    } satisfies ToolCallStartFrame,
    null as unknown,
    'running' as const,
  ],
] as Array<[string, ToolCallStartFrame, unknown, 'complete' | 'running' | 'incomplete']>)(
  'bash renders ToolCallStartFrame params "%s" without throwing',
  (_label, frame, result, statusType) => {
    it('renders', () => {
      if (!captured.bashRender) {
        expect(BashOutputUI).toBeDefined()
        return
      }
      expect(() => {
        const element = captured.bashRender!({
          args: frame.params,
          result,
          status: { type: statusType },
        })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── ToolCallResultFrame as result source (using generated type) ───────────────

describe.each([
  [
    'bash success result frame',
    {
      type: 'tool_call_result' as const,
      session_id: 'sess-1',
      tool: 'bash',
      call_id: 'call-1',
      result: 'command output\n',
      status: 'success' as const,
      duration_ms: 150,
    } satisfies ToolCallResultFrame,
  ],
  [
    'bash error result frame',
    {
      type: 'tool_call_result' as const,
      session_id: 'sess-1',
      tool: 'bash',
      call_id: 'call-2',
      result: null,
      status: 'error' as const,
      error: 'exit code 127',
    } satisfies ToolCallResultFrame,
  ],
] as Array<[string, ToolCallResultFrame]>)(
  'bash renders ToolCallResultFrame "%s" without throwing',
  (_label, frame) => {
    it('renders', () => {
      if (!captured.bashRender) {
        expect(BashOutputUI).toBeDefined()
        return
      }
      const statusType = frame.status === 'error' ? 'incomplete' : 'complete'
      expect(() => {
        const element = captured.bashRender!({
          args: { command: 'test' },
          result: frame.result,
          status: { type: statusType },
        })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── legacy `exec` transcript-compat edge cases (ported from TerminalOutput.edge.test.tsx) ──

describe.each([
  ['empty args', {}],
  ['args with command', { command: 'echo hello' }],
  ['args with session_id', { session_id: 'sess-001' }],
  ['args with all legacy ExecArgs fields', { command: 'ls', action: 'run', timeout: 30, background: false, pty: true, session_id: 'sess-1' }],
  ['args with action=read', { action: 'read', session_id: 'sess-1' }],
  ['args with action=kill', { action: 'kill', session_id: 'sess-1' }],
  ['args with action=write', { action: 'write', session_id: 'sess-1', command: 'data' }],
  ['args with action=send-keys', { action: 'send-keys', session_id: 'sess-1', command: '\r' }],
] as Array<[string, Record<string, unknown>]>)(
  'legacy exec renders args "%s" without throwing',
  (_label, args) => {
    it('renders', () => {
      if (!captured.execRender) {
        expect(ExecLegacyUI).toBeDefined()
        return
      }
      expect(() => {
        const element = captured.execRender!({ args, result: null, status: { type: 'running' } })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── legacy `workspace_shell` / `workspace_shell_bg` transcript-compat edge cases
// (ported from WorkspaceShellUI.edge.test.tsx) ────────────────────────────────

describe.each([
  ['empty args', {}],
  ['args with command', { command: 'ls -la' }],
  ['args with cwd', { command: 'npm test', cwd: '/workspace' }],
  ['args with timeout', { command: 'sleep 10', timeout: 5 }],
  ['args with description (shell_bg)', { description: 'background watcher', command: 'tail -f log' }],
] as Array<[string, Record<string, unknown>]>)(
  'legacy workspace_shell renders args "%s" without throwing',
  (_label, args) => {
    it('renders', () => {
      if (!captured.shellRender) {
        expect(WorkspaceShellLegacyUI).toBeDefined()
        return
      }
      expect(() => {
        const element = captured.shellRender!({ args, result: null, status: { type: 'running' } })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

describe.each([
  ['shell_bg null result (running)', null, true],
  ['shell_bg empty output', '', false],
  ['shell_bg long output', 'x'.repeat(50_000), false],
] as Array<[string, unknown, boolean]>)(
  'legacy workspace_shell_bg renders result "%s" without throwing',
  (_label, result, isRunning) => {
    it('renders', () => {
      if (!captured.shellBgRender) {
        expect(WorkspaceShellBgLegacyUI).toBeDefined()
        return
      }
      const status = isRunning ? { type: 'running' } : { type: 'complete' }
      expect(() => {
        const element = captured.shellBgRender!({
          args: { command: 'npm run dev', description: 'dev server' },
          result,
          status,
        })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── activity-bar tool visibility: verbose-chat gate ─────────────────────────
// Traces to: src/lib/toolVisibility.ts shouldRenderToolCall — `bash` has its
// own dedicated live UI (BashOutputBlock) that never goes through
// GenericToolCall, so it needs its own gate. Scoped to the literal `bash`
// tool name only; the legacy aliases (exec, workspace_shell,
// workspace_shell_bg, dotted forms) must NEVER be hidden — they render OLD,
// already-persisted transcripts as they were stored.

describe('bash — verbose chat gate', () => {
  beforeEach(() => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: false })
    })
  })

  it('hides a background bash run (run_in_background: true) by default', () => {
    if (!captured.bashRender) {
      expect(BashOutputUI).toBeDefined()
      return
    }
    const element = captured.bashRender({
      args: { command: 'tail -f log', run_in_background: true },
      result: null,
      status: { type: 'running' },
    })
    const { container } = render(element as React.ReactElement)
    expect(container).toBeEmptyDOMElement()
  })

  it('shows a background bash run when verboseChatEnabled is true', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    if (!captured.bashRender) {
      expect(BashOutputUI).toBeDefined()
      return
    }
    const element = captured.bashRender({
      args: { command: 'tail -f log', run_in_background: true },
      result: null,
      status: { type: 'running' },
    })
    const { container } = render(element as React.ReactElement)
    expect(container).not.toBeEmptyDOMElement()
  })

  it('always shows a foreground bash run (no run_in_background) regardless of verbose setting', () => {
    if (!captured.bashRender) {
      expect(BashOutputUI).toBeDefined()
      return
    }
    const element = captured.bashRender({
      args: { command: 'echo hi' },
      result: 'hi\n',
      status: { type: 'complete' },
    })
    const { container } = render(element as React.ReactElement)
    expect(container).not.toBeEmptyDOMElement()
  })

  it('regression guard: a LEGACY-named workspace_shell_bg call always renders regardless of verbose setting (exemption)', () => {
    if (!captured.shellBgRender) {
      expect(WorkspaceShellBgLegacyUI).toBeDefined()
      return
    }
    const element = captured.shellBgRender({
      args: { command: 'tail -f log', background: true },
      result: null,
      status: { type: 'running' },
    })
    const { container } = render(element as React.ReactElement)
    expect(container).not.toBeEmptyDOMElement()
  })

  it('regression guard: a LEGACY-named exec call always renders regardless of verbose setting (exemption)', () => {
    if (!captured.execRender) {
      expect(ExecLegacyUI).toBeDefined()
      return
    }
    const element = captured.execRender({
      args: { command: 'echo hi', background: true },
      result: null,
      status: { type: 'running' },
    })
    const { container } = render(element as React.ReactElement)
    expect(container).not.toBeEmptyDOMElement()
  })

  // REVISED 2026-07-16 (was: "REGRESSION ... is NOT hidden", asserting the
  // isError-status ('incomplete') override forced this background dispatch
  // visible). shouldRenderToolCall's isError override no longer applies to
  // background bash (poll/read/dispatch) — same LLM-mediated rationale as
  // delegate (toolVisibility.ts doc comment): the failure is explained by
  // the calling agent's own response text, and the raw output stays
  // inspectable in the ActivityPanel. Only verboseChatEnabled brings this
  // row back into the thread now (see the next test).
  it('a background bash run with a genuine error outcome (isError=true) stays HIDDEN — no isError exception for background bash', () => {
    // (item 8f, 2026-07-16 fix wave): hard-fail if the render fn was never
    // captured, instead of soft-guarding into a vacuous pass — this is a
    // NEW regression test added by the visibility-policy fix, so silently
    // no-op'ing here would mean the test could report green while asserting
    // nothing at all about the fix it exists to pin.
    //
    // Issue #617: a finished call always carries a truthy `result`, so
    // `status.type === 'incomplete'` (the old shape here) is never the real
    // signal for a failed background bash run — `isError` is (see the type
    // comment at the top of this file). The producible pairing is
    // result:truthy + status:complete + isError:true.
    expect(captured.bashRender).toBeDefined()
    const element = captured.bashRender!({
      args: { command: 'tail -f log', run_in_background: true },
      result: 'command not found',
      status: { type: 'complete' },
      isError: true,
    })
    const { container } = render(element as React.ReactElement)
    expect(container).toBeEmptyDOMElement()
  })

  it('...but becomes visible once verbose chat is enabled', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    // (item 8f) same hard guard as the test above.
    expect(captured.bashRender).toBeDefined()
    const element = captured.bashRender!({
      args: { command: 'tail -f log', run_in_background: true },
      result: 'command not found',
      status: { type: 'complete' },
      isError: true,
    })
    const { container } = render(element as React.ReactElement)
    expect(container).not.toBeEmptyDOMElement()
  })
})
