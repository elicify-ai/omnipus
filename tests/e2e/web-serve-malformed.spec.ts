/**
 * web-serve-malformed.spec.ts — T1.19
 *
 * T1.19: malformed_web_serve_result_renders_inline_error_block
 *   Seed a session transcript where a `web_serve` (or `serve_workspace`)
 *   tool call has a result missing required fields (no `url`, no `expires_at`).
 *   Open the session and assert:
 *   (a) The inline MalformedResultBlock is rendered:
 *       "web_serve tool returned a malformed result — cannot render preview."
 *   (b) The SPA did NOT crash or render a blank/empty UI.
 *   (c) The malformed block renders a "Show raw result" collapsible section.
 *
 * This test verifies the B1.3e guard in WebServeBlock.tsx: when isWebServeResult()
 * rejects the result and the tool is no longer running, MalformedResultBlock is
 * rendered instead of crashing or silently showing nothing.
 *
 * Test drives against the real embedded SPA (Go binary + Playwright).
 */

import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { seedAndOpenSession } from './fixtures/session-setup'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'
const PREVIEW_PORT = parseInt(process.env.OMNIPUS_PREVIEW_PORT || '6061', 10)
const SYNTHETIC_AGENT_ID = 'main'

// ── T1.19 ─────────────────────────────────────────────────────────────────────

test(
  'malformed_web_serve_result_renders_inline_error_block',
  async ({ page }) => {
    // Intercept /api/v1/about — needed so IframePreview doesn't stall on
    // aboutIsLoading forever (if the about query were to run before the guard).
    // In practice, the isWebServeResult() guard fires before IframePreview mounts,
    // so this is belt-and-suspenders.
    await page.route(`${BASE_URL}/api/v1/about`, async (route) => {
      let base: Record<string, unknown> = {
        version: 'test',
        go_version: 'go1.21',
        os: 'linux',
        arch: 'amd64',
        uptime_seconds: 1,
      }
      try {
        const real = await route.fetch()
        if (real.ok()) base = (await real.json()) as Record<string, unknown>
      } catch {
        // stub
      }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          ...base,
          preview_listener_enabled: true,
          preview_port: PREVIEW_PORT,
        }),
      })
    })

    // Seed a transcript with a MALFORMED web_serve result.
    // The result object is missing `url` and `expires_at` — the minimum fields
    // required by isWebServeResult() in WebServeUI.tsx.
    // We seed THREE variants to cover all the code paths:
    //   (a) Completely empty result object {}
    //   (b) Result with wrong types (url is a number, not a string)
    //   (c) Result that is null (isWebServeResult(null) returns false)
    //
    // Using variant (a) here — simplest and most clearly "malformed".
    await seedAndOpenSession(page, 'web-serve-malformed-t119', [
      {
        id: 'user-t119-1',
        role: 'user',
        content: 'serve my workspace',
        timestamp: new Date(Date.now() - 5000).toISOString(),
        agent_id: '',
      },
      {
        id: 'asst-t119-1',
        role: 'assistant',
        content: 'Here is the served workspace.',
        timestamp: new Date(Date.now() - 4000).toISOString(),
        agent_id: SYNTHETIC_AGENT_ID,
        tool_calls: [
          {
            id: 'tc-t119-malformed',
            // Use `web_serve` (the SPA legacy alias kept for session replay).
            // The canonical backend name is now `serve_web` (§7 rename), but
            // WebServeUI is registered under 'web_serve' for backward-compat
            // transcript replay until the SPA adds a ServeWebUI registration.
            tool: 'web_serve',
            status: 'success',
            duration_ms: 50,
            parameters: { path: '.' },
            // Malformed result: missing required fields `url` and `expires_at`
            result: {
              // deliberately invalid — no url, no expires_at, no path
              status: 'ok',
              message: 'workspace served',
              // no url field
              // no expires_at field
            },
          },
        ],
      },
    ])

    // (a) Assert: MalformedResultBlock is rendered.
    // The exact text from WebServeUI.tsx MalformedResultBlock line 107:
    // "web_serve tool returned a malformed result — cannot render preview."
    await expect(
      page.locator('text=web_serve tool returned a malformed result'),
    ).toBeVisible({ timeout: 15_000 })

    // (b) Assert: the SPA did not crash or show a blank screen.
    // The banner must still be visible (not replaced by an error boundary).
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 5_000 })

    // No React error boundary message
    await expect(
      page.locator('text=Something went wrong').or(page.locator('text=Unexpected error')),
    ).not.toBeVisible()

    // (c) Assert: "Show raw result" collapsible section is present.
    // MalformedResultBlock renders a <details> element with a <summary> containing
    // "Show raw result" — this is the power-user debug disclosure.
    await expect(
      page.locator('text=Show raw result'),
    ).toBeVisible({ timeout: 5_000 })

    // (d) Expand the raw result disclosure and verify it contains the malformed data.
    // The <details> element starts collapsed; click the <summary> to expand.
    await page.locator('summary:has-text("Show raw result")').first().click()

    // After expanding, the raw JSON should be visible (pre element).
    // The malformed result contained: { "status": "ok", "message": "workspace served" }
    const rawContent = await page.locator('pre').first().textContent({ timeout: 5_000 }).catch(() => '')
    // Should contain either the status key or the message key from the malformed result
    expect(
      rawContent,
      'Raw result disclosure must show the malformed JSON content',
    ).toMatch(/status|message|ok|workspace served/)
  },
)

// ── Additional variant: serve_workspace legacy malformed ──────────────────────

test(
  'malformed_serve_workspace_legacy_result_renders_inline_error_block',
  async ({ page }) => {
    // Same as T1.19 but using the legacy `serve_workspace` tool name and a
    // result with a wrong type for `url` (number instead of string).
    // Tests the hasPreviewShape() fallback path in isWebServeResult().

    await page.route(`${BASE_URL}/api/v1/about`, async (route) => {
      let base: Record<string, unknown> = {
        version: 'test',
        go_version: 'go1.21',
        os: 'linux',
        arch: 'amd64',
        uptime_seconds: 1,
      }
      try {
        const real = await route.fetch()
        if (real.ok()) base = (await real.json()) as Record<string, unknown>
      } catch {
        // stub
      }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          ...base,
          preview_listener_enabled: true,
          preview_port: PREVIEW_PORT,
        }),
      })
    })

    await seedAndOpenSession(page, 'serve-workspace-malformed-t119b', [
      {
        id: 'user-t119b-1',
        role: 'user',
        content: 'serve workspace',
        timestamp: new Date(Date.now() - 5000).toISOString(),
        agent_id: '',
      },
      {
        id: 'asst-t119b-1',
        role: 'assistant',
        content: 'Serving workspace.',
        timestamp: new Date(Date.now() - 4000).toISOString(),
        agent_id: SYNTHETIC_AGENT_ID,
        tool_calls: [
          {
            id: 'tc-t119b-malformed',
            tool: 'serve_workspace',
            status: 'success',
            duration_ms: 50,
            parameters: { path: '.' },
            // Malformed: url is a number (wrong type), expires_at missing
            result: {
              url: 12345,  // wrong type
              path: '/tmp',
              // expires_at missing
            },
          },
        ],
      },
    ])

    // The isWebServeResult() guard: typeof v.url === 'string' → false (url is number)
    // AND hasPreviewShape() also requires url to be a string → false.
    // So MalformedResultBlock must render.
    await expect(
      page.locator('text=tool returned a malformed result'),
    ).toBeVisible({ timeout: 15_000 })

    // No crash
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 5_000 })
    await expect(
      page.locator('text=Something went wrong').or(page.locator('text=Unexpected error')),
    ).not.toBeVisible()
  },
)
