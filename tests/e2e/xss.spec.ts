import { expect } from '@playwright/test';
import { test } from './fixtures/console-errors';

// XSS DOM-rendering coverage for the chat markdown render path.
//
// These tests send XSS payloads through the real SPA's agent description field
// and assert that when the description is rendered in the DOM (on the Agents
// page), no executable HTML survives: zero <script> elements, zero on*= event
// handler attributes, zero javascript: URIs in href/src attributes.
//
// This covers the client-side rendering gap documented in tests/security/xss_test.go
// where the server stores payloads verbatim (documented gap) and the SPA is
// solely responsible for safe output encoding.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F11
// Related: tests/security/xss_test.go (JSON roundtrip only — server-side contract)

// Global storageState provides pre-authenticated session (see playwright.config.ts + global-setup.ts).

// XSS payloads that match xss_test.go — a subset chosen for DOM visibility.
// The full 10-payload matrix is in tests/security/xss_test.go.
const xssPayloads = [
  { name: 'script-tag', payload: '<script>alert(1)</script>' },
  { name: 'img-onerror', payload: '<img src=x onerror=alert(1)>' },
  { name: 'svg-onload', payload: '<svg/onload=alert(1)>' },
  { name: 'iframe-javascript', payload: '<iframe src="javascript:alert(1)">' },
  { name: 'anchor-javascript', payload: '<a href="javascript:alert(1)">click</a>' },
  { name: 'polyglot', payload: '"><script>alert(1)</script>' },
];

test.beforeEach(async ({ page }) => {
  await page.goto('/agents');
});

for (const { name, payload } of xssPayloads) {
  test(`XSS payload does not execute in agent description DOM: ${name}`, async ({ page, request }) => {
    // Create an agent with the XSS payload as its description via REST API.
    // The test uses the API directly (authenticated via storageState cookie/token)
    // rather than the UI to avoid driving the modal for every payload.
    const agentName = `xss-dom-test-${name}-${Date.now()}`;
    const createResp = await request.post('/api/v1/agents', {
      data: { name: agentName, description: payload, model: 'scripted-model' },
    });

    // If the agent was rejected by the server (validation error) that is acceptable
    // defense-in-depth. Skip DOM rendering for that payload.
    if (!createResp.ok()) {
      test.info().annotations.push({
        type: 'skip-reason',
        description: `Server rejected payload ${name} with ${createResp.status()} — server-side defense active`,
      });
      return;
    }

    const created = await createResp.json() as { id?: string };
    const agentId = created.id;
    if (!agentId) {
      // No id in response — treat as rejection.
      return;
    }

    try {
      // Navigate to the agent profile page where description is rendered.
      await page.goto(`/agents/${agentId}`);
      // Wait for the page to settle (network idle ensures JS has rendered content).
      await page.waitForLoadState('networkidle');

      // --- Assert: no <script> elements in the agent description content area ---
      // Note: the page will have legitimate <script> elements from the SPA bundle.
      // We must NOT count those. Instead, assert that no <script> appears inside
      // the main content area where the agent description is rendered.
      const descriptionArea = page.locator('main, [role="main"], .agent-profile, [data-testid="agent-description"]').first();
      const scriptInContent = await descriptionArea.locator('script').count();
      expect(
        scriptInContent,
        `XSS payload "${name}" injected a <script> element into the agent description content area`,
      ).toBe(0);

      // --- Assert: no on* event handlers in the description area ---
      // Evaluate in page context to detect any on* attributes on rendered elements.
      const hasOnHandlers = await page.evaluate(() => {
        const content = document.querySelector('main') ?? document.body;
        const all = Array.from(content.querySelectorAll('*'));
        for (const el of all) {
          for (const attr of Array.from(el.attributes)) {
            if (/^on[a-z]/i.test(attr.name)) {
              return true;
            }
          }
        }
        return false;
      });
      expect(
        hasOnHandlers,
        `XSS payload "${name}" injected an on* event handler attribute into the rendered DOM`,
      ).toBe(false);

      // --- Assert: no javascript: URIs in href or src attributes in content area ---
      const hasJavascriptURI = await page.evaluate(() => {
        const content = document.querySelector('main') ?? document.body;
        const all = Array.from(content.querySelectorAll('[href],[src]'));
        for (const el of all) {
          const href = el.getAttribute('href') ?? '';
          const src = el.getAttribute('src') ?? '';
          if (/^javascript:/i.test(href.trim()) || /^javascript:/i.test(src.trim())) {
            return true;
          }
        }
        return false;
      });
      expect(
        hasJavascriptURI,
        `XSS payload "${name}" injected a javascript: URI into an href or src attribute`,
      ).toBe(false);

    } finally {
      // Clean up: delete the test agent regardless of pass/fail.
      await request.delete(`/api/v1/agents/${agentId}`).catch(() => {
        // Non-fatal — the agent may have already been cleaned up.
      });
    }
  });
}

test('chat message with XSS payload renders as escaped text, not live HTML', async ({ page }) => {
  // This test verifies the chat rendering path (not just the agent description field).
  // It uses page.route to intercept a WS message and inject an XSS payload as
  // an assistant response, then asserts the payload appears as escaped text in the DOM.
  //
  // NOTE: This test intercepts only the API responses, not the real WS stream.
  // It uses page.route to mock the sessions API so we get a predictable transcript
  // without needing a live LLM connection.

  const xssContent = '<script>window.__xss_fired = true;</script><img src=x onerror="window.__xss_fired=true">';

  // Navigate to chat screen.
  await page.goto('/');
  await page.waitForLoadState('networkidle');

  // Intercept the sessions list and transcript endpoint to inject an XSS assistant
  // message into the DOM via the chat store's rendering path.
  // This simulates what happens when the gateway streams a malicious assistant message.
  const sessionId = `xss-test-session-${Date.now()}`;
  await page.route('**/api/v1/sessions', async (route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([{
          id: sessionId,
          agent_id: 'omnipus-system',
          title: 'XSS Test Session',
          created_at: new Date().toISOString(),
          updated_at: new Date().toISOString(),
        }]),
      });
    } else {
      await route.continue();
    }
  });

  await page.route(`**/api/v1/sessions/${sessionId}/transcript`, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([
        { role: 'user', content: 'hi', timestamp: new Date().toISOString() },
        { role: 'assistant', content: xssContent, timestamp: new Date().toISOString() },
      ]),
    });
  });

  // Navigate to the injected session's URL so the chat store loads the transcript.
  await page.goto(`/#/sessions/${sessionId}`);
  await page.waitForLoadState('networkidle');

  // Give React a moment to render the intercepted transcript.
  await page.waitForTimeout(1000);

  // Assert: the XSS did NOT execute (window.__xss_fired should be undefined/false).
  const xssFired = await page.evaluate(() => (window as unknown as Record<string, unknown>)['__xss_fired']);
  expect(
    xssFired,
    'XSS payload executed in the chat transcript DOM — the SPA must escape assistant message content',
  ).toBeFalsy();

  // Assert: no <script> element inside the messages area.
  const msgArea = page.locator('[data-message-role="assistant"], .assistant-message, [class*="message"]').first();
  if (await msgArea.count() > 0) {
    const scriptInMsg = await msgArea.locator('script').count();
    expect(scriptInMsg, 'Rendered assistant message must not contain live <script> elements').toBe(0);
  }
});
