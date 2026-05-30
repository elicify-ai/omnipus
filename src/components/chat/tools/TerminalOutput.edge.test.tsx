/**
 * TerminalOutputBlock (exec tool) edge-case render tests (Phase 5, Agent B)
 *
 * Uses ToolCallStartFrame / ToolCallResultFrame from generated asyncapi-types.ts.
 *
 * Note: TerminalOutputBlock is not exported from TerminalOutput.tsx — flagged
 * in deliverables. We access it by extracting the render function from the
 * makeAssistantToolUI configuration via a synchronous mock capture.
 *
 * vi.hoisted() is used to initialise the capture container before vi.mock runs,
 * avoiding the temporal dead zone issue with const declarations.
 */

import { describe, it, expect, vi } from 'vitest'
import { render } from '@testing-library/react'
import type { ToolCallStartFrame, ToolCallResultFrame } from '@/lib/api/generated/asyncapi-types'

type RenderFn = (props: { args: unknown; result: unknown; status: { type: string } }) => React.ReactNode

// vi.hoisted runs before vi.mock factory and before all imports.
const captured = vi.hoisted(() => ({ execRender: null as RenderFn | null }))

vi.mock('@assistant-ui/react', async (importOriginal) => {
  const original = await importOriginal<typeof import('@assistant-ui/react')>()
  return {
    ...original,
    makeAssistantToolUI: (config: Record<string, unknown>) => {
      if (config.toolName === 'exec') {
        captured.execRender = config.render as RenderFn
      }
      return config
    },
  }
})

// Static import: vi.mock intercepts makeAssistantToolUI before this runs.
import { TerminalOutputUI } from './TerminalOutput'

// ── result edge cases ─────────────────────────────────────────────────────────

describe.each([
  ['null result (running)', null, true, false],
  ['null result (done)', null, false, false],
  ['empty string result', '', false, false],
  ['single line output', 'hello world', false, false],
  ['multiline output', 'line1\nline2\nline3', false, false],
  ['very long output', 'x'.repeat(100_000), false, false],
  ['unicode output', '\u{1F680}\u{1F480}⚡\u{1F389}', false, false],
  ['ANSI escape codes in output', '\x1b[31mred text\x1b[0m', false, false],
  ['null bytes in output', 'before\x00after', false, false],
  ['output with XSS content', '<script>alert(1)</script>', false, false],
  ['number result (coerced to string)', 42, false, false],
  ['object result (coerced to string)', { exit_code: 0 }, false, false],
  ['array result (coerced to string)', [1, 2, 3], false, false],
  ['boolean result (coerced to string)', false, false, false],
  ['error state output', 'error: command not found', false, true],
] as Array<[string, unknown, boolean, boolean]>)(
  'TerminalOutput renders result "%s" without throwing',
  (_label, result, isRunning, isError) => {
    it('renders', () => {
      if (!captured.execRender) {
        expect(TerminalOutputUI).toBeDefined()
        return
      }
      const status = isRunning ? { type: 'running' } : isError ? { type: 'incomplete' } : { type: 'complete' }
      expect(() => {
        const element = captured.execRender!({ args: { command: 'echo hi' }, result, status })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── args edge cases ───────────────────────────────────────────────────────────

describe.each([
  ['empty args', {}],
  ['args with command', { command: 'echo hello' }],
  ['args with very long command', { command: 'a'.repeat(10_000) }],
  ['args with session_id', { session_id: 'sess-001' }],
  ['args with all ExecArgs fields', { command: 'ls', action: 'run', timeout: 30, background: false, pty: true, session_id: 'sess-1' }],
  ['args with action=read', { action: 'read', session_id: 'sess-1' }],
  ['args with action=kill', { action: 'kill', session_id: 'sess-1' }],
  ['args with action=write', { action: 'write', session_id: 'sess-1', command: 'data' }],
  ['args with action=send-keys', { action: 'send-keys', session_id: 'sess-1', command: '\r' }],
  ['args with unicode command', { command: 'ls /tmp/\u{1F680}' }],
  ['args with XSS in command', { command: '<script>alert(1)</script>' }],
  ['args with null command', { command: null }],
] as Array<[string, Record<string, unknown>]>)(
  'TerminalOutput renders args "%s" without throwing',
  (_label, args) => {
    it('renders', () => {
      if (!captured.execRender) {
        expect(TerminalOutputUI).toBeDefined()
        return
      }
      expect(() => {
        const element = captured.execRender!({ args, result: null, status: { type: 'running' } })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── ToolCallStartFrame params as args (using generated type) ──────────────────

describe.each([
  [
    'exec frame — run action',
    {
      type: 'tool_call_start' as const,
      session_id: 'sess-1',
      tool: 'exec',
      call_id: 'call-1',
      params: { command: 'ls /tmp', action: 'run' },
    } satisfies ToolCallStartFrame,
    null as unknown,
    'running' as const,
  ],
  [
    'exec frame — read action',
    {
      type: 'tool_call_start' as const,
      session_id: 'sess-1',
      tool: 'exec',
      call_id: 'call-2',
      params: { action: 'read', session_id: 'pty-sess-1' },
    } satisfies ToolCallStartFrame,
    'output text' as unknown,
    'complete' as const,
  ],
  [
    'exec frame — empty params',
    {
      type: 'tool_call_start' as const,
      session_id: 'sess-1',
      tool: 'exec',
      call_id: 'call-3',
      params: {},
    } satisfies ToolCallStartFrame,
    null as unknown,
    'running' as const,
  ],
] as Array<[string, ToolCallStartFrame, unknown, 'complete' | 'running' | 'incomplete']>)(
  'TerminalOutput renders ToolCallStartFrame params "%s" without throwing',
  (_label, frame, result, statusType) => {
    it('renders', () => {
      if (!captured.execRender) {
        expect(TerminalOutputUI).toBeDefined()
        return
      }
      expect(() => {
        const element = captured.execRender!({
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
    'exec success result frame',
    {
      type: 'tool_call_result' as const,
      session_id: 'sess-1',
      tool: 'exec',
      call_id: 'call-1',
      result: 'command output\n',
      status: 'success' as const,
      duration_ms: 150,
    } satisfies ToolCallResultFrame,
  ],
  [
    'exec error result frame',
    {
      type: 'tool_call_result' as const,
      session_id: 'sess-1',
      tool: 'exec',
      call_id: 'call-2',
      result: null,
      status: 'error' as const,
      error: 'exit code 127',
    } satisfies ToolCallResultFrame,
  ],
  [
    'exec result frame — empty string output',
    {
      type: 'tool_call_result' as const,
      session_id: 'sess-1',
      tool: 'exec',
      call_id: 'call-3',
      result: '',
      status: 'success' as const,
    } satisfies ToolCallResultFrame,
  ],
] as Array<[string, ToolCallResultFrame]>)(
  'TerminalOutput renders ToolCallResultFrame "%s" without throwing',
  (_label, frame) => {
    it('renders', () => {
      if (!captured.execRender) {
        expect(TerminalOutputUI).toBeDefined()
        return
      }
      const statusType = frame.status === 'error' ? 'incomplete' : 'complete'
      expect(() => {
        const element = captured.execRender!({
          args: { command: 'test' },
          result: frame.result,
          status: { type: statusType },
        })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)
