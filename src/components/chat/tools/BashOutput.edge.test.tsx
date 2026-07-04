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

import { describe, it, expect, vi } from 'vitest'
import { render } from '@testing-library/react'
import type { ToolCallStartFrame, ToolCallResultFrame } from '@/lib/api/generated/asyncapi-types'

type RenderFn = (props: { args: unknown; result: unknown; status: { type: string } }) => React.ReactNode

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

// ── bash (canonical) result edge cases ────────────────────────────────────────

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
  'bash renders result "%s" without throwing',
  (_label, result, isRunning, isError) => {
    it('renders', () => {
      if (!captured.bashRender) {
        expect(BashOutputUI).toBeDefined()
        return
      }
      const status = isRunning ? { type: 'running' } : isError ? { type: 'incomplete' } : { type: 'complete' }
      expect(() => {
        const element = captured.bashRender!({ args: { command: 'echo hi' }, result, status })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

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
