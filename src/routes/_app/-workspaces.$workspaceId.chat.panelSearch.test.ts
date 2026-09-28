// workspaces.$workspaceId.chat.panelSearch.test.ts — RED pack for
// side-panel-shell-spec.md §8.2's URL/deep-link contract (wave 1), tested at
// the route's `validateSearch` schema level — no rendering, no router
// mount required, so this file is fast and isolates the schema from the
// runtime replace/adoption behaviour (covered separately in
// workspaces.$workspaceId.chat.deepLink.test.tsx).
//
// Oracle — §8.2, verbatim:
//   "Param `panel` on the workspace Chat route's search ... The workspace
//   Chat route is the only route whose search schema declares `panel` (plus
//   `agent`, meaningful only with `panel=mail`, SP-23) (today it declares
//   none — verified)."
//   "Valid `panel` values are the REGISTERED panel ids (MAJ-012): an
//   unregistered-but-future id (`tasks` in wave 1) is treated exactly like
//   an unknown id — dropped with a URL replace."
//   "Browser — EXCLUDED from URL restore (SP-21 + SP-28, decided): a
//   `panel=browser` param is ALWAYS dropped ... bare or carrying context ...
//   No session id is ever placed in a shareable chat-link."
// Plus §12's dataset "panel param validation" rows 1, 3, 4, 5, 6, 7 and
// MAJ-012's own wording that wave 1 registers only `library` and `browser`
// (§10) — so every OTHER panel id (`calendar`, `team`, `tasks`, `mail`) is,
// in wave 1, in the exact same "not yet registered" bucket as `bogus`
// (dataset rows for calendar/mail describe the FEATURE's end state across
// all three waves, not wave-1 behaviour specifically — deferred, noted in
// the RED report rather than asserted here as a wave-1 requirement).
//
// WAVE 2 AMENDMENT (2026-09-28, RED): side-panel-shell-spec.md §10 "Wave 2 —
// Mail adopts the shell" + FR-014 register mail, so §12's dataset row 8
// ("`mail`, no agent selected | Mail panel opens on 'choose a mailbox'
// (SP-23, wave 2) | §8.2") and §8.2's Mail bullet ("`panel=mail` with no
// agent/mailbox selected opens the Mail panel on its 'choose a mailbox'
// state") now apply AT THE SCHEMA LEVEL: `panel=mail` is a REGISTERED panel
// id and must be KEPT by validateSearch — dropped-with-a-replace is the
// treatment for unknown AND unregistered ids only (US-7 AS-4). The `agent`
// key stays "meaningful only with panel=mail" (SP-23). New tests below; the
// wave-1 assertions above are unchanged (they were correct then and remain
// true now: `tasks`/`team`/`calendar` are still unregistered at wave 2).
//
// RED evidence (2026-09-27, read
// src/routes/_app/workspaces.$workspaceId.chat.tsx in full): the route
// declares NO `validateSearch` at all — `createFileRoute(...)({ component:
// WorkspaceChatRoute })`, nothing else. `Route.options.validateSearch` is
// `undefined`; every call below throws
// "Route.options.validateSearch is not a function".

import { describe, it, expect } from 'vitest'
import { Route } from './workspaces.$workspaceId.chat'

function validate(search: Record<string, unknown>): unknown {
  const fn = (Route.options as unknown as { validateSearch?: (s: unknown) => unknown }).validateSearch
  if (typeof fn !== 'function') {
    throw new TypeError('Route.options.validateSearch is not a function')
  }
  return fn(search)
}

describe('workspaces/$workspaceId/chat — panel search schema (§8.2, wave 1)', () => {
  it('keeps a registered panel id — library (dataset row 1)', () => {
    expect(validate({ panel: 'library' })).toMatchObject({ panel: 'library' })
  })

  it('keeps a registered panel id — browser is a registered wave-1 id at the SCHEMA level (its URL-restore exclusion is a separate rule, SP-28)', () => {
    // The schema itself must accept `browser` as a syntactically valid
    // registered id — SP-28's "always dropped" is a RESTORE-time rule
    // (§8.2's "browser — EXCLUDED FROM URL RESTORE"), not a schema-level
    // rejection; a value the schema itself refused could never reach the
    // restore-time drop logic to be dropped FOR THE RIGHT REASON.
    expect(validate({ panel: 'browser' })).toMatchObject({ panel: 'browser' })
  })

  it('drops an unknown panel id — no `panel` key survives validation (US-7 AS-4, dataset row 3)', () => {
    const result = validate({ panel: 'bogus' }) as Record<string, unknown>
    expect(result.panel).toBeUndefined()
  })

  it('drops an unregistered-but-future wave-1 id exactly like an unknown one (MAJ-012, dataset row 7 — "tasks" is unregistered in wave 1)', () => {
    const result = validate({ panel: 'tasks' }) as Record<string, unknown>
    expect(result.panel).toBeUndefined()
  })

  it('no panel key present at all → no panel (US-7 AS-3, dataset row 4)', () => {
    const result = validate({}) as Record<string, unknown>
    expect(result.panel).toBeUndefined()
  })

  it('declares the `agent` param, meaningful only with panel=mail (SP-23) — accepted alongside panel=mail', () => {
    // Wave 1 dropped `panel=mail` itself (Mail was unregistered then); at
    // wave 2 the schema KEEPS it — asserted by the wave-2 test below. This
    // test's own oracle is unchanged: `agent` is a DECLARED search key on
    // this route (SP-23's contract), kept even on its own.
    const result = validate({ agent: 'agent-1' }) as Record<string, unknown>
    expect(result.agent).toBe('agent-1')
  })

  // ── Wave 2 (spec §10 "Wave 2 — Mail adopts the shell", §8.2 Mail bullet,
  //    §12 dataset row 8, FR-014): mail is a REGISTERED panel id. ────────
  describe('wave 2 — mail is registered (SP-23, dataset row 8)', () => {
    it('keeps a registered panel id — mail (§12 dataset row 8; §8.2 Mail bullet)', () => {
      expect(validate({ panel: 'mail' })).toMatchObject({ panel: 'mail' })
    })

    it('keeps `agent` alongside panel=mail — the SP-23 landing param is schema-valid (§8.2: "`agent`, meaningful only with panel=mail")', () => {
      expect(validate({ panel: 'mail', agent: 'agent-1' })).toMatchObject({
        panel: 'mail',
        agent: 'agent-1',
      })
    })
  })
})
