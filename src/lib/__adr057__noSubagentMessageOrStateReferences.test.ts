// __adr057__noSubagentMessageOrStateReferences.test.ts — ADR-057 FR-047,
// TDD-plan test #109 (`TestDrillDown_NoSubagentMessageOrStateReferences`).
//
// FR-047: "The drill-down surface MUST filter on `producing_session_id`, and
// a static gate MUST assert zero non-test references to `subagent_message`
// or `subagent_state` in `src/`, having first asserted it located >= 1
// reference to `producing_session_id` there (binding rule 4)."
//
// Binding rule 4 (spec, "Four rules bind every test in this spec"): every
// negative/zero-count/exclusion gate MUST first prove its search mechanism
// is LIVE by asserting a stated positive lower bound, so a broken search
// (typo'd pattern, wrong directory, wrong extension) cannot pass the
// zero-assertion vacuously. This test does that in two layers:
//   1. A sanity count on the file walk itself (mirrors the precedent at
//      src/lib/tabindex-convention.test.ts:155 and
//      src/lib/__adr052__wireContracts.test.ts) — proves the walk visited a
//      realistic slice of the tree, not an empty/wrong directory.
//   2. The FR-047-mandated positive lower bound: >= 1 real, hand-written,
//      non-test reference to `producing_session_id` in src/ — verified
//      present at src/store/chat.ts:87,3947,4005 (2026-08-03) — before the
//      zero-assertion for `subagent_message`/`subagent_state` is allowed to
//      run.
//
// ADR-057 context (why the zero-assertion is expected to hold): the
// `subagent_message`/`subagent_state` WS frames have ZERO Go emitters
// anywhere in the codebase — `WsFrameTypeSubagentMessage`/
// `WsFrameTypeSubagentState` don't even exist as constants in
// pkg/api/generated/asyncapi_types.gen.go (unlike `subagent_start`/`_end`,
// which have real emit call sites in pkg/gateway/websocket.go), and they are
// absent from the `WsFrameType` enum on both the Go and TS sides (ADR-057
// Explicit Non-Behaviors). The one-time cleanup that made this gate
// satisfiable removed the entire ADR-053 FE-5 "live session list" surface
// (src/store/sessionActivity.ts — deleted; src/hooks/useRunningActivity.ts
// and src/components/chat/ActivityPanel.tsx — mid-span join and the
// lifecycle-badge/peek/reply/steer/stop UI block removed; plus the two
// upstream call sites that fed it, src/store/chat.ts's WS frame-dispatch
// case and src/components/chat/ActivityBar.tsx's onSessionAction handler).
//
// Scope decision, stated explicitly (not left implicit): this gate walks
// hand-written `src/**/*.{ts,tsx}`, EXCLUDING:
//   - test files (`*.test.ts` / `*.test.tsx` — the vitest `include` glob at
//     vite.config.ts:53) — FR-047 says "non-test references", and this file
//     itself necessarily contains both search strings in prose above, so it
//     MUST exclude itself the same way every other test file is excluded.
//   - `src/lib/api/generated/**` (any directory literally named
//     `generated`) — contract-derived wire types (Constraint #8). ADR-057's
//     own "Explicit Non-Behaviors" section states these frames "are absent
//     from the WsFrameType enum in contracts, Go and TS, and their structs
//     are dead declarations" — i.e. the spec itself tolerates the dead
//     struct/schema declarations that necessarily exist in generated code
//     because contracts/asyncapi.yaml still defines SubagentMessageFrame /
//     SubagentStateFrame as message schemas (receiveSubagentMessage /
//     receiveSubagentState operations). Retracting those schemas is a
//     separate, much larger contract change (Constraint #8's 5-step
//     pipeline, touching contracts/asyncapi.yaml + regeneration) that this
//     unit was not authorized to make and that FR-047's own wording does
//     not require — it targets hand-written RELIANCE code. This mirrors an
//     existing sibling gate in this same spec (test #58's SC-003: "The
//     search MUST be restricted to Go source") scoping to hand-authored
//     code rather than every byte in the tree.

import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'

const SRC_ROOT = join(__dirname, '..') // this file lives in src/lib/ -> src/

const EXCLUDED_DIR_NAMES = new Set(['generated', 'node_modules'])

function isTestFile(path: string): boolean {
  return /\.test\.(ts|tsx)$/.test(path)
}

function isSourceFile(path: string): boolean {
  return /\.(ts|tsx)$/.test(path) && !isTestFile(path)
}

/** Recursively collects hand-written (non-test, non-generated) .ts/.tsx files under `dir`. */
function collectHandWrittenSourceFiles(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    const st = statSync(full)
    if (st.isDirectory()) {
      if (EXCLUDED_DIR_NAMES.has(entry)) continue
      collectHandWrittenSourceFiles(full, out)
    } else if (isSourceFile(full)) {
      out.push(full)
    }
  }
  return out
}

describe('ADR-057 FR-047 — drill-down does not depend on subagent_message/subagent_state (test #109)', () => {
  it('locates >= 1 producing_session_id reference (positive lower bound), then asserts zero non-test subagent_message/subagent_state references in src/', () => {
    const files = collectHandWrittenSourceFiles(SRC_ROOT)

    // Layer 1 — sanity: the walk itself visited a realistic slice of the
    // tree (mirrors src/lib/tabindex-convention.test.ts's own >100 check).
    // A near-empty result here would mean SRC_ROOT resolved to the wrong
    // directory, which would make every assertion below meaningless.
    expect(files.length).toBeGreaterThan(100)

    // Layer 2 (FR-085 / binding rule 4) — the FR-047-mandated positive lower
    // bound: prove the `producing_session_id` search is live BEFORE trusting
    // any zero-count result from the same mechanism.
    const producingSessionIdHits = files.filter((file) =>
      readFileSync(file, 'utf8').includes('producing_session_id'),
    )
    expect(
      producingSessionIdHits.length,
      'Rule 4 positive lower bound failed: found zero hand-written, non-test ' +
        'references to `producing_session_id` in src/. Either the drill-down ' +
        'filtering was removed (a real FR-047 regression) or this gate\'s own ' +
        'search mechanism is broken (wrong root, wrong extension filter, ' +
        'excluded the wrong directory) — either way the zero-assertion below ' +
        'cannot be trusted until this is >= 1.',
    ).toBeGreaterThanOrEqual(1)

    // FR-047 exclusion — zero non-test, non-generated references to the dead
    // mid-span frames anywhere in src/.
    const offenders = files.filter((file) => {
      const src = readFileSync(file, 'utf8')
      return src.includes('subagent_message') || src.includes('subagent_state')
    })
    expect(
      offenders.map((f) => f.replace(SRC_ROOT, 'src')),
      'FR-047 violation: subagent_message/subagent_state have zero Go ' +
        'emitters (ADR-057 Explicit Non-Behaviors) and MUST NOT be relied on ' +
        'by hand-written src/ code. Found in:',
    ).toEqual([])
  })
})
