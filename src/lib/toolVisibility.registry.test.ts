// toolVisibility.registry.test.ts — enumeration + BEHAVIOURAL gate test for issue #494.
//
// ── WHY THE PREVIOUS ORACLE WAS REPLACED ────────────────────────────────────
//
// The prior version of this file enforced "every tool-UI registration
// consults shouldRenderToolCall" with a TEXT SCAN:
//   contents.includes('shouldRenderToolCall')
// That only proves the 21-character string appears somewhere in the file —
// including inside an import line, a comment, or dead code. An independent
// audit proved this has ZERO detection power: it copied the repo to a
// scratchpad, deleted the import AND the entire
// `if (!shouldRenderToolCall(...)) return null` block from
// WebSearchResult.tsx (leaving the symbol only in a comment), and the full
// suite still reported 673/673 passed, exit 0. The guard suite passed with
// the feature deleted.
//
// SECOND FINDING from the same audit: `shouldRenderToolCall` (./toolVisibility.ts)
// has no `case` for ANY of the 9 tool names these registration files gate on
// (browser.navigate/browser_navigate, read_file/file.read, list_dir/file.list,
// fetch_url/web_fetch, web_search, web_serve, write_file/file.write,
// edit_file, append_file, plus the 6 browser.<verb> pairs in BrowserTool.tsx)
// — they all fall through to `default: return true`. So even where the gate
// IS wired correctly, it is a runtime no-op for every one of these
// registrations today: no combination of args ever makes shouldRenderToolCall
// return false for these tool names. That is a real constraint on what a
// behavioural test can assert using only the tool names the classifier
// currently hides — see "DECISION ON THE HIDE-LIST" below.
//
// ── FIX: BEHAVIOURAL, NOT TEXTUAL ───────────────────────────────────────────
//
// This file still discovers the registration surface from disk (the
// enumeration property is real and worth keeping — a brand-new tool-UI file
// is picked up automatically, same scan as before: every *.tsx file directly
// under src/components/chat/tools/, excluding *.test.tsx/*.edge.tsx, that
// contains a `makeAssistantToolUI(` call site).
//
// But instead of grepping the file text, it MOUNTS the actual registered
// component (via @testing-library/react) and asserts on the real DOM output:
//   - when the gate reports HIDDEN, the registration must render nothing
//     (`toBeEmptyDOMElement()`), for at least one of its tool-name
//     registrations (a file can register several names off one shared block
//     — e.g. BashOutput.tsx's `bash` vs. its 5 legacy aliases, which are
//     deliberately EXEMPT from the gate; see "why per-file, not per-name"
//     below).
//   - when the gate reports VISIBLE, every registration must render real
//     content (`not.toBeEmptyDOMElement()`) — this catches the opposite
//     mutation, a component that always returns null regardless of the
//     gate's answer.
//
// `makeAssistantToolUI` is intercepted (same pattern as
// BashOutput.edge.test.tsx) to capture each `{ toolName, render }` config
// pair as each file is dynamically imported, so the render function under
// test is the SAME closure the app registers with assistant-ui — not a
// hand-rolled stand-in.
//
// ── WHY PER-FILE, NOT PER-REGISTERED-NAME ───────────────────────────────────
//
// BashOutput.tsx registers `bash` (gated) plus 5 legacy aliases — `exec`,
// `workspace_shell`, `workspace.shell`, `workspace_shell_bg`,
// `workspace.shell_bg` — that are DELIBERATELY exempt from the gate forever
// (they render old, already-persisted transcripts as originally stored; see
// BashOutput.tsx's own comment and BashOutput.edge.test.tsx's "regression
// guard" tests). A per-registered-name assertion of "must render null when
// hidden" would be actively WRONG for those 5 aliases — it would encode a
// requirement the spec explicitly rejects. So the HIDDEN-case assertion is
// scoped per FILE: "at least one of this file's registrations honoured the
// gate" — true today (via `bash`), and still catches the audited mutation
// (delete the gate block from a file with only one registration, e.g.
// WebSearchResult.tsx — then NONE of its registrations ever call the gate,
// and the per-file assertion fails). The VISIBLE-case assertion has no such
// exemption and applies to every registration.
//
// ── DECISION ON THE HIDE-LIST (task item 3) ─────────────────────────────────
//
// No new tool name was added to shouldRenderToolCall's hide-list. Every one
// of these 9 files' registered names is exactly the case toolVisibility.ts's
// own docstring calls "a deliberate, meaningful action" a chat reader would
// want to see (reading a file, searching the web, navigating a page,
// writing a file) — the opposite of the "noisy background infra" the
// existing hide-list cases (`ToolSearch`, `delegate` action=run/status,
// `bash` action=poll/read) target. Inventing a hide-list entry for one of
// these just to give the test something to exercise would be fabricating
// product intent to reach green, which is exactly the failure mode this
// rewrite exists to eliminate.
//
// Because none of the 9 files' own registered names are hideable today, a
// behavioural test needs a way to observe "does this component call the
// gate and honour its answer" independent of which literal names the
// classifier currently hides (a fact owned by toolVisibility.test.ts, not
// this file). This file mocks `shouldRenderToolCall` itself (a boundary this
// suite owns — see mocking-and-isolation guidance: mock at a real dependency
// edge, never the unit under test) so it can force HIDDEN/VISIBLE
// deterministically across all 9 files. A second, narrower test below skips
// that mock entirely and drives BashOutput's real `bash` + `action: 'poll'`
// case through the REAL, unmocked classifier — the literal fallback the task
// suggested — as an end-to-end sanity check that the mocked-boundary tests
// above are not testing a fiction.

import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest'
import { render } from '@testing-library/react'
import { act } from 'react'
import type { ReactNode } from 'react'
import { readdirSync, readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { useChatPreferencesStore } from '@/store/chatPreferences'

const __dirname = dirname(fileURLToPath(import.meta.url))
const TOOLS_DIR = join(__dirname, '..', 'components', 'chat', 'tools')

// Matches both `makeAssistantToolUI(` and the generic-parameterized call form
// `makeAssistantToolUI<Args, Result>(`.
const CALLS_MAKE_ASSISTANT_TOOL_UI = /makeAssistantToolUI\s*(<[^()]*>)?\s*\(/

/** Every source (non-test) .tsx file directly under the tools dir that registers a tool UI. */
function findToolUiRegistrationFiles(): string[] {
  return readdirSync(TOOLS_DIR)
    .filter((name) => name.endsWith('.tsx') && !name.includes('.test.') && !name.includes('.edge.'))
    .filter((name) => CALLS_MAKE_ASSISTANT_TOOL_UI.test(readFileSync(join(TOOLS_DIR, name), 'utf-8')))
    .sort()
}

type RenderProps = {
  toolCallId?: string
  args?: Record<string, unknown>
  result?: unknown
  status: { type: string; reason?: string }
  isError?: boolean
}
type RenderFn = (props: RenderProps) => ReactNode

interface Registration {
  file: string
  toolName: string
  render: RenderFn
}

// vi.hoisted: initialised before the vi.mock factories below run (temporal
// dead zone workaround for const — same pattern as BashOutput.edge.test.tsx).
const state = vi.hoisted(() => ({
  registrations: [] as Registration[],
  currentFile: '',
  // 'forced' — shouldRenderToolCall ignores its real args and returns
  // `gateResult`, for every tool name. 'real' — calls straight through to
  // the actual classifier (used only by the unmocked end-to-end test).
  mode: 'forced' as 'forced' | 'real',
  gateResult: true,
  gateCallCount: 0,
}))

vi.mock('@assistant-ui/react', async (importOriginal) => {
  const original = await importOriginal<typeof import('@assistant-ui/react')>()
  return {
    ...original,
    makeAssistantToolUI: (config: Record<string, unknown>) => {
      state.registrations.push({
        file: state.currentFile,
        toolName: config.toolName as string,
        render: config.render as RenderFn,
      })
      return config
    },
  }
})

vi.mock('@/lib/toolVisibility', async (importOriginal) => {
  const original = await importOriginal<typeof import('@/lib/toolVisibility')>()
  return {
    ...original,
    shouldRenderToolCall: (
      tool: string,
      params: Record<string, unknown> | undefined,
      verboseChatEnabled: boolean,
      isError = false,
    ) => {
      state.gateCallCount += 1
      if (state.mode === 'real') {
        return original.shouldRenderToolCall(tool, params, verboseChatEnabled, isError)
      }
      return state.gateResult
    },
  }
})

/** Dynamically import every discovered registration file, tagging each captured config with its source file. */
async function loadAllRegistrations(files: string[]): Promise<void> {
  for (const file of files) {
    state.currentFile = file
    const bare = file.replace(/\.tsx$/, '')
    await import(`@/components/chat/tools/${bare}.tsx`)
  }
  state.currentFile = ''
}

const GENERIC_PROPS: RenderProps = {
  toolCallId: 'test-tool-call-1',
  args: {},
  result: null,
  status: { type: 'complete' },
  isError: false,
}

describe('tool-UI registration surface honours shouldRenderToolCall (#494)', () => {
  const files = findToolUiRegistrationFiles()

  beforeAll(async () => {
    await loadAllRegistrations(files)
  })

  beforeEach(() => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: false })
    })
    state.mode = 'forced'
  })

  it('found at least the currently-known registration files (sanity check the scan itself works)', () => {
    // Guards against the scan silently finding zero files (wrong path,
    // renamed directory) and every assertion below passing vacuously.
    expect(files.length).toBeGreaterThanOrEqual(9)
    expect(files).toContain('BashOutput.tsx')
    expect(files).toContain('BrowserNavigate.tsx')
    expect(files).toContain('WebServeUI.tsx')
  })

  it('captured at least one makeAssistantToolUI registration for every discovered file', () => {
    // Guards the capture mechanism itself (the mocked makeAssistantToolUI):
    // if importing a file produced zero captures, every per-file assertion
    // below would iterate zero registrations and pass vacuously — exactly
    // the shape of failure this rewrite exists to eliminate.
    for (const file of files) {
      const count = state.registrations.filter((r) => r.file === file).length
      expect(count, `${file} produced no captured makeAssistantToolUI registrations`).toBeGreaterThan(0)
    }
  })

  describe.each(files)('%s', (file) => {
    function registrationsFor(f: string): Registration[] {
      return state.registrations.filter((r) => r.file === f)
    }

    it('at least one of its registrations renders NOTHING when the gate reports HIDDEN', () => {
      state.gateResult = false
      const regs = registrationsFor(file)
      let gateHonoured = false

      for (const reg of regs) {
        state.gateCallCount = 0
        const { container, unmount } = render(
          reg.render(GENERIC_PROPS) as Parameters<typeof render>[0],
        )
        if (state.gateCallCount > 0) {
          gateHonoured = true
          expect(
            container,
            `${file} (toolName "${reg.toolName}") called shouldRenderToolCall, got HIDDEN, but still rendered content`,
          ).toBeEmptyDOMElement()
        }
        unmount()
      }

      // This is the assertion that catches the audited mutation directly:
      // if the `if (!shouldRenderToolCall(...)) return null` block is
      // deleted from every registration in this file, shouldRenderToolCall
      // is never called, gateHonoured stays false, and this fails — even
      // though every individual render() call above executed without error.
      expect(
        gateHonoured,
        `${file} never invoked shouldRenderToolCall from any of its ${regs.length} registration(s) — the visibility gate appears to have been removed`,
      ).toBe(true)
    })

    it('every registration renders real content when the gate reports VISIBLE', () => {
      state.gateResult = true
      const regs = registrationsFor(file)
      expect(regs.length).toBeGreaterThan(0)

      for (const reg of regs) {
        const { container, unmount } = render(
          reg.render(GENERIC_PROPS) as Parameters<typeof render>[0],
        )
        expect(
          container,
          `${file} (toolName "${reg.toolName}") rendered nothing even though the gate reports VISIBLE`,
        ).not.toBeEmptyDOMElement()
        unmount()
      }
    })
  })
})

// ── End-to-end sanity check: the REAL, unmocked classifier ─────────────────
//
// The suite above proves "component honours whatever shouldRenderToolCall
// says" using a forced mock. This test proves that wiring means something in
// production by driving one real, already-hidden case
// (`bash` + `action: 'poll'` — see toolVisibility.ts's `bash` switch case)
// through the ACTUAL classifier, no mock. It is the literal fallback named
// in the task: "construct the test using a name the classifier already
// hides." BashOutput.tsx is the only one of the 9 files where such a name
// exists today (see "DECISION ON THE HIDE-LIST" above).
describe('bash — real classifier, no mock (end-to-end sanity check)', () => {
  beforeEach(() => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: false })
    })
    state.mode = 'real'
  })

  function bashRegistration(): Registration {
    const reg = state.registrations.find((r) => r.file === 'BashOutput.tsx' && r.toolName === 'bash')
    if (!reg) throw new Error('BashOutput.tsx did not register a "bash" tool UI — cannot run the real-classifier sanity check')
    return reg
  }

  it('renders nothing for a background poll (action: "poll") under the real classifier', () => {
    const { container } = render(
      bashRegistration().render({
        toolCallId: 'poll-1',
        args: { action: 'poll', session_id: 'abc' },
        result: null,
        status: { type: 'running' },
        isError: false,
      }) as Parameters<typeof render>[0],
    )
    expect(container).toBeEmptyDOMElement()
  })

  it('renders real content for a foreground run under the real classifier', () => {
    const { container } = render(
      bashRegistration().render({
        toolCallId: 'run-1',
        args: { command: 'echo hi' },
        result: 'hi\n',
        status: { type: 'complete' },
        isError: false,
      }) as Parameters<typeof render>[0],
    )
    expect(container).not.toBeEmptyDOMElement()
  })
})
