# Wave 2 — Mail joins the shared side-panel shell (2026-09-28)

Branch: `feature/email-mail-fe` @ `1eb8f3303` (wave-2 wiring `33c8c4da2` + fix `5e83d0a2c`
+ design-system registration `1eb8f3303`).

## Status: browser-verify BLOCKED by a machine-level browser-launch outage

Every browser engine on this Mac fails at launch right now — Playwright chromium/firefox/
webkit, the Playwright MCP's own Chrome, and hand-launched installed Google Chrome
(including `--disable-crashpad` and `--headless=old` variants): all die at the macOS mach
bootstrap check-in (`bootstrap_check_in ... unknown error code (141)`, webkit `Abort trap: 6`,
firefox SIGABRT). The failure is in browser STARTUP, before any page or assertion runs, so no
screenshot or click evidence could be captured. This is an environment outage, not a finding
against the change: the same wiring passes its jsdom oracles (below), and CI (Linux) runs the
design-system/browser gates on every push.

## What the jsdom oracles prove (all green, exit 0, one vitest at a time)

| Oracle | Proof |
|---|---|
| Registry has exactly library, browser, mail | `npx vitest run src/store/panelShell.registry.test.ts` |
| Mail PanelDefinition contract | `npx vitest run src/components/workspaces/mail/mailPanelDefinition.test.tsx` |
| `?panel=mail` hard-load → choose-a-mailbox | `npx vitest run src/routes/_app/-workspaces.$workspaceId.chat.mailPanel.test.tsx` |
| panel search schema (valid = registered ids) | `npx vitest run src/routes/_app/-workspaces.$workspaceId.chat.panelSearch.test.ts` |
| Mail tab toggles like Library | `npx vitest run src/components/workspaces/WorkspaceTabBar.toggle.test.tsx` |
| Shell + deep-link adoption | `npx vitest run src/components/panel-shell/SidePanelShell.test.tsx src/components/panel-shell/usePanelDeepLink.test.tsx` |
| Draft-link flow through the /mail stub | `npx vitest run src/routes/_app/-workspaces.$workspaceId.chat.draftLink.test.tsx` |
| Mail oracle suites | `npx vitest run src/components/workspaces/mail/` |
| Mail signature suites | `npx vitest run src/components/workspaces/mail/signature/` |

## Ready-to-run browser recipe (next session with a working browser)

The committed type state has 3 PRE-EXISTING typecheck errors (base-proven at 84abaaa07:
`SidePanelShell.browserSettle.test.tsx`, `PanelTabPresenceBridge.tsx`,
`panelShell.width.test.ts`) that make `npm run build` fail its `tsc -b` step. Until
qa-lead/shell squad resolve them, build without the typecheck step:

```bash
npx vite build
rm -rf pkg/gateway/spa && cp -r dist/spa pkg/gateway/spa
CGO_ENABLED=0 go build -tags "goolm,stdjson" -o build/omnipus-wave2 ./cmd/omnipus
rm -rf /tmp/omnipus-wave2-home
OMNIPUS_HOME=/tmp/omnipus-wave2-home ./build/omnipus-wave2 start   # http://localhost:5000
```

Log in as admin, then verify in this order:

1. **Mail tab toggle** — click the workspace tab-strip Mail entry (Tray icon): Mail opens
   INSIDE the shared shell (shell header, resize handle, expand button present), tab shows
   `aria-pressed="true"`, SPA URL becomes `/#/workspaces/{id}/chat?panel=mail`.
2. **One panel at a time (SP-7)** — with Mail open, click Library: Library opens, Mail closes.
3. **`?panel=mail` hard-load** — paste `/#/workspaces/{id}/chat?panel=mail` in a fresh tab:
   Mail opens on the choose-a-mailbox state (no mailbox named by the URL).
4. **Expand** — the shell header's expand button opens a new tab on
   `/#/workspaces/{id}/mail` (the full page).

Save screenshots next to this README (dispatch target:
`uat/evidence/2026-09-28-email-mail-fe-wave2/`).
