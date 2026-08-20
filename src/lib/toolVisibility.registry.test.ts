// toolVisibility.registry.test.ts — enumeration test for issue #494.
//
// PROBLEM: toolVisibility.test.ts (the sibling file) only unit-tests
// shouldRenderToolCall's return values for a fixed set of known tool names.
// It never looks at the *registration surface* — the set of
// `makeAssistantToolUI({...})` call sites under src/components/chat/tools/,
// each of which registers a per-tool-name UI component with assistant-ui.
// As of this writing only 1 of those ~9 registration files (BashOutput.tsx)
// consults `shouldRenderToolCall` before rendering; the other 8 render
// unconditionally, so "verbose chat off" never hides their tool calls the
// way the design intends. (GenericToolCall.tsx is the other file that
// consults the gate, but it is a plain fallback component wired directly
// into ChatScreen.tsx rather than a makeAssistantToolUI registration, so it
// sits outside this particular scan — see the sanity-check test below.)
// Because no test scans the registration list, a brand-new tool UI
// component can skip the gate entirely and every existing test suite still
// passes.
//
// FIX: this test derives the registration list from disk (not from a
// hardcoded array) by scanning every *.tsx file directly under
// src/components/chat/tools/ (excluding *.test.tsx) for
// `makeAssistantToolUI(` call sites. Each file that registers a tool UI must
// either:
//   (a) import/call `shouldRenderToolCall` somewhere in the same file, or
//   (b) be named in the ALLOWLIST below, annotated with `// TODO(#494)`.
//
// The ALLOWLIST is seeded with the 8 currently-non-compliant components. That
// makes this test PASS today. The instant an 11th (or 12th, ...) tool UI file
// is added that calls makeAssistantToolUI without wiring shouldRenderToolCall
// AND without adding itself to the allowlist, this test FAILS — that failure
// is the entire point of the test. Shrinking the allowlist as #494 gets fixed
// is expected and welcome; growing it silently is not (each addition should
// carry its own justification, same as any other TODO).
//
// A second assertion guards the allowlist itself from rotting: every
// allowlisted filename must still exist under the tools directory and must
// still actually call makeAssistantToolUI. If a component is deleted or
// renamed, or someone fixes it and forgets to shrink the allowlist, that
// assertion fails loudly instead of the allowlist silently going stale.

import { describe, it, expect } from 'vitest'
import { readdirSync, readFileSync, existsSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = dirname(fileURLToPath(import.meta.url))
const TOOLS_DIR = join(__dirname, '..', 'components', 'chat', 'tools')

// Known non-compliant tool-UI registrations (issue #494). Each entry is a
// deliberate, tracked gap — not an oversight. Remove an entry only once the
// corresponding component has been wired to consult `shouldRenderToolCall`.
const ALLOWLIST = [
  // INTENTIONALLY EMPTY.
  // Operator decision 2026-08-20: no allowlist, no baseline, no ratchet.
  // This test asserts the TRUE requirement — every makeAssistantToolUI()
  // registration must consult shouldRenderToolCall. It fails until #494 is
  // actually fixed. A passing run must mean the gate is universally applied.
]

// Matches both `makeAssistantToolUI(` and the generic-parameterized call form
// `makeAssistantToolUI<Args, Result>(` (the pattern every real registration in
// this codebase uses) — but NOT the bare `import { makeAssistantToolUI }`
// line, which has no following `(` or `<`.
const CALLS_MAKE_ASSISTANT_TOOL_UI = /makeAssistantToolUI\s*(<[^()]*>)?\s*\(/

/** List every source (non-test) .tsx file directly under the tools dir that registers a tool UI. */
function findToolUiRegistrations(): string[] {
  return readdirSync(TOOLS_DIR)
    .filter((name) => name.endsWith('.tsx') && !name.includes('.test.') && !name.includes('.edge.'))
    .filter((name) => {
      const contents = readFileSync(join(TOOLS_DIR, name), 'utf-8')
      return CALLS_MAKE_ASSISTANT_TOOL_UI.test(contents)
    })
}

/** Does this file's source reference shouldRenderToolCall (import or call)? */
function referencesVisibilityGate(fileName: string): boolean {
  const contents = readFileSync(join(TOOLS_DIR, fileName), 'utf-8')
  return contents.includes('shouldRenderToolCall')
}

describe('tool-UI registration surface consults shouldRenderToolCall (#494)', () => {
  const registrations = findToolUiRegistrations()

  it('found at least the currently-known registrations (sanity check the scan itself works)', () => {
    // Guards against the scan silently finding zero files (e.g. wrong path,
    // renamed directory) and the test suite below vacuously passing.
    //
    // Note: GenericToolCall.tsx is deliberately NOT expected here — it does
    // not call makeAssistantToolUI itself. It's a plain React component
    // rendered directly by ChatScreen.tsx as the fallback view for any tool
    // without a dedicated registration, and it consults shouldRenderToolCall
    // from that call site. This test's scan targets the makeAssistantToolUI
    // registration surface specifically (issue #494's actual defect class),
    // which is a different — if related — set of call sites.
    expect(registrations.length).toBeGreaterThanOrEqual(9)
    expect(registrations).toContain('BashOutput.tsx')
    expect(registrations).toContain('BrowserNavigate.tsx')
    expect(registrations).toContain('WebServeUI.tsx')
  })

  it.each(registrations)('%s either gates on shouldRenderToolCall or is an explicit, tracked allowlist entry', (fileName) => {
    const gated = referencesVisibilityGate(fileName)
    const allowlisted = ALLOWLIST.includes(fileName)

    if (!gated && !allowlisted) {
      throw new Error(
        `${fileName} registers a tool UI via makeAssistantToolUI() but never references ` +
          `shouldRenderToolCall, and is not in the ALLOWLIST in toolVisibility.registry.test.ts. ` +
          `Either wire it to shouldRenderToolCall (see BashOutput.tsx / GenericToolCall.tsx for the ` +
          `pattern), or add it to ALLOWLIST with a // TODO(#494) comment if this is a deliberate, ` +
          `tracked gap.`,
      )
    }

    // A file that IS gated has no reason to also sit in the allowlist — that
    // would be a stale entry masking as an exception. Fail so it gets
    // trimmed promptly rather than drifting.
    if (gated && allowlisted) {
      throw new Error(
        `${fileName} references shouldRenderToolCall but is still listed in ALLOWLIST — remove it ` +
          `from ALLOWLIST in toolVisibility.registry.test.ts, it's no longer a gap.`,
      )
    }

    expect(gated || allowlisted).toBe(true)
  })
})

describe('ALLOWLIST hygiene (no stale entries)', () => {
  it.each(ALLOWLIST)('%s still exists in the tools dir and still calls makeAssistantToolUI', (fileName) => {
    const path = join(TOOLS_DIR, fileName)
    expect(existsSync(path)).toBe(true)
    const contents = readFileSync(path, 'utf-8')
    expect(CALLS_MAKE_ASSISTANT_TOOL_UI.test(contents)).toBe(true)
  })

  it('has no duplicate entries', () => {
    expect(new Set(ALLOWLIST).size).toBe(ALLOWLIST.length)
  })
})
