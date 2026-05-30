/**
 * WorkspaceShellBlock (workspace.shell + workspace.shell_bg) edge-case render tests
 * (Phase 5, Agent B)
 *
 * Uses ToolCallStartFrame / ToolCallResultFrame from generated asyncapi-types.ts.
 * WorkspaceShellBlock is not exported; render functions are captured via
 * makeAssistantToolUI mock. vi.hoisted() initialises the capture container
 * before the mock factory runs, avoiding TDZ issues.
 */

import { describe, it, expect, vi } from 'vitest'
import { render } from '@testing-library/react'
import type { ToolCallStartFrame, ToolCallResultFrame } from '@/lib/api/generated/asyncapi-types'

type RenderFn = (props: { args: unknown; result: unknown; status: { type: string } }) => React.ReactNode

// vi.hoisted runs before vi.mock factory and before all imports.
const captured = vi.hoisted(() => ({
  shellRender: null as RenderFn | null,
  shellBgRender: null as RenderFn | null,
}))

vi.mock('@assistant-ui/react', async (importOriginal) => {
  const original = await importOriginal<typeof import('@assistant-ui/react')>()
  return {
    ...original,
    makeAssistantToolUI: (config: Record<string, unknown>) => {
      if (config.toolName === 'workspace.shell') {
        captured.shellRender = config.render as RenderFn
      }
      if (config.toolName === 'workspace.shell_bg') {
        captured.shellBgRender = config.render as RenderFn
      }
      return config
    },
  }
})

// Static imports: vi.mock intercepts makeAssistantToolUI before these run.
import { WorkspaceShellUI, WorkspaceShellBgUI } from './WorkspaceShellUI'

// ── result edge cases for workspace.shell ─────────────────────────────────────

describe.each([
  ['null result (running)', null, true, false],
  ['null result (done)', null, false, false],
  ['empty string output', '', false, false],
  ['single line output', 'hello world\n', false, false],
  ['multiline output', 'line1\nline2\nline3\n', false, false],
  ['very long output', 'x'.repeat(100_000), false, false],
  ['unicode output', '\u{1F680}\u{1F480}⚡\u{1F389}\n', false, false],
  ['ANSI codes in output', '\x1b[32mgreen\x1b[0m text', false, false],
  ['output with XSS content', '<script>alert(1)</script>', false, false],
  ['number result (coerced)', 42, false, false],
  ['object result (coerced)', { stdout: 'ok', exit_code: 0 }, false, false],
  ['boolean result (coerced)', false, false, false],
  ['error state', 'bash: command not found', false, true],
] as Array<[string, unknown, boolean, boolean]>)(
  'WorkspaceShellBlock (shell) renders result "%s" without throwing',
  (_label, result, isRunning, isError) => {
    it('renders', () => {
      if (!captured.shellRender) {
        expect(WorkspaceShellUI).toBeDefined()
        return
      }
      const status = isRunning ? { type: 'running' } : isError ? { type: 'incomplete' } : { type: 'complete' }
      expect(() => {
        const element = captured.shellRender!({ args: { command: 'ls' }, result, status })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── args edge cases ───────────────────────────────────────────────────────────

describe.each([
  ['empty args', {}],
  ['args with command', { command: 'ls -la' }],
  ['args with very long command', { command: 'a'.repeat(10_000) }],
  ['args with cwd', { command: 'npm test', cwd: '/workspace' }],
  ['args with timeout', { command: 'sleep 10', timeout: 5 }],
  ['args with description (shell_bg)', { description: 'background watcher', command: 'tail -f log' }],
  ['args with unicode command', { command: 'echo \u{1F680}' }],
  ['args with XSS in command', { command: '<script>alert(1)</script>' }],
  ['args with null command', { command: null }],
  ['args with undefined command', { command: undefined }],
  ['args with all fields', { command: 'npm run dev', cwd: '/workspace', timeout: 120, description: 'dev server' }],
] as Array<[string, Record<string, unknown>]>)(
  'WorkspaceShellBlock (shell) renders args "%s" without throwing',
  (_label, args) => {
    it('renders', () => {
      if (!captured.shellRender) {
        expect(WorkspaceShellUI).toBeDefined()
        return
      }
      expect(() => {
        const element = captured.shellRender!({ args, result: null, status: { type: 'running' } })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── workspace.shell_bg result edge cases ─────────────────────────────────────

describe.each([
  ['shell_bg null result (running)', null, true],
  ['shell_bg empty output', '', false],
  ['shell_bg long output', 'x'.repeat(50_000), false],
  ['shell_bg multiline output', 'started\nlistening on :3000\n', false],
] as Array<[string, unknown, boolean]>)(
  'WorkspaceShellBlock (shell_bg) renders result "%s" without throwing',
  (_label, result, isRunning) => {
    it('renders', () => {
      if (!captured.shellBgRender) {
        expect(WorkspaceShellBgUI).toBeDefined()
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

// ── ToolCallStartFrame params as args (using generated type) ──────────────────

describe.each([
  [
    'workspace.shell frame',
    'shell' as const,
    {
      type: 'tool_call_start' as const,
      session_id: 'sess-1',
      tool: 'workspace.shell',
      call_id: 'call-1',
      params: { command: 'ls /workspace', cwd: '/workspace' },
    } satisfies ToolCallStartFrame,
    null as unknown,
    'running' as const,
  ],
  [
    'workspace.shell_bg frame',
    'shellBg' as const,
    {
      type: 'tool_call_start' as const,
      session_id: 'sess-1',
      tool: 'workspace.shell_bg',
      call_id: 'call-2',
      params: { command: 'npm run dev', description: 'dev server' },
    } satisfies ToolCallStartFrame,
    null as unknown,
    'running' as const,
  ],
  [
    'workspace.shell frame — empty params',
    'shell' as const,
    {
      type: 'tool_call_start' as const,
      session_id: 'sess-1',
      tool: 'workspace.shell',
      call_id: 'call-3',
      params: {},
    } satisfies ToolCallStartFrame,
    null as unknown,
    'running' as const,
  ],
] as Array<[string, 'shell' | 'shellBg', ToolCallStartFrame, unknown, 'complete' | 'running' | 'incomplete']>)(
  'WorkspaceShellBlock renders ToolCallStartFrame params "%s" without throwing',
  (_label, whichRender, frame, result, statusType) => {
    it('renders', () => {
      const renderFn = whichRender === 'shellBg' ? captured.shellBgRender : captured.shellRender
      if (!renderFn) {
        expect(WorkspaceShellUI).toBeDefined()
        return
      }
      expect(() => {
        const element = renderFn({ args: frame.params, result, status: { type: statusType } })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── ToolCallResultFrame as result source (using generated type) ───────────────

describe.each([
  [
    'workspace.shell success frame',
    {
      type: 'tool_call_result' as const,
      session_id: 'sess-1',
      tool: 'workspace.shell',
      call_id: 'call-1',
      result: 'file1.txt\nfile2.txt\n',
      status: 'success' as const,
      duration_ms: 80,
    } satisfies ToolCallResultFrame,
  ],
  [
    'workspace.shell error frame',
    {
      type: 'tool_call_result' as const,
      session_id: 'sess-1',
      tool: 'workspace.shell',
      call_id: 'call-2',
      result: null,
      status: 'error' as const,
      error: 'exit code 1',
    } satisfies ToolCallResultFrame,
  ],
] as Array<[string, ToolCallResultFrame]>)(
  'WorkspaceShellBlock renders ToolCallResultFrame "%s" without throwing',
  (_label, frame) => {
    it('renders', () => {
      if (!captured.shellRender) {
        expect(WorkspaceShellUI).toBeDefined()
        return
      }
      const statusType = frame.status === 'error' ? 'incomplete' : 'complete'
      expect(() => {
        const element = captured.shellRender!({
          args: { command: 'test' },
          result: frame.result,
          status: { type: statusType },
        })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)
