// Mint a Playwright storageState auth.json for one gateway.
// Replicates tests/e2e/global-setup.ts: onboarding/complete (idempotent) -> login
// -> write storageState with omnipus_auth_token in localStorage + CSRF cookie.
//
// Usage: node mkauth.mjs <baseURL> <outPath> <openrouterKey>
import { request } from '@playwright/test';
import fs from 'fs';

const [baseURL, outPath, apiKey, modelArg] = process.argv.slice(2);
const MODEL = modelArg || 'openrouter/z-ai/glm-5.2';

const ctx = await request.newContext({ baseURL });
try {
  // 1. Onboard (200 fresh, 409 already-complete both OK).
  const ob = await ctx.post('/api/v1/onboarding/complete', {
    data: {
      provider: { id: 'openrouter', api_key: apiKey, model: MODEL },
      admin: { username: 'admin', password: 'admin123' },
    },
  });
  if (![200, 409].includes(ob.status())) {
    throw new Error(`onboarding/complete -> ${ob.status()}: ${await ob.text()}`);
  }

  // 2. Login -> token.
  const lr = await ctx.post('/api/v1/auth/login', {
    data: { username: 'admin', password: 'admin123' },
  });
  if (!lr.ok()) throw new Error(`auth/login -> ${lr.status()}: ${await lr.text()}`);
  const { token } = await lr.json();
  if (!token) throw new Error('login response missing token');

  // 3. Carry over EVERY cookie login set — not just CSRF.
  //
  // WHY THIS MATTERS (2026-07-26): /auth/login sets TWO cookies —
  //   omnipus-session=... ; HttpOnly   <-- WebSocket auth depends on this
  //   csrf=...
  // This function used to keep only `csrf`. The SPA's WS client
  // (src/lib/ws.ts) authenticates via the omnipus-session cookie the browser
  // auto-attaches on the upgrade request — post-Wave-1 it deliberately sends
  // NO {type:'auth'} frame. Server-side, authenticateWS
  // (pkg/gateway/websocket.go) tries ResolveUserFromCookie FIRST and only
  // falls through to the legacy frame path when that fails. With the session
  // cookie dropped, that fallback demanded a frame the SPA will never send, so
  // the socket was closed and reconnected forever and CHAT WAS DEAD — in the
  // harness only. A UAT tester reported it as a product S1; it was this.
  // REST was unaffected (it uses the localStorage bearer token), which is why
  // only chat looked broken.
  const st = await ctx.storageState();
  const url = new URL(baseURL);
  const secure = url.protocol === 'https:';
  const cookies = st.cookies.map((c) => ({
    name: c.name,
    value: c.value,
    domain: c.domain || url.hostname,
    path: c.path || '/',
    expires: -1,
    httpOnly: c.httpOnly ?? false,
    secure: c.secure ?? secure,
    sameSite: c.sameSite || 'Strict',
  }));
  const csrf = cookies.find((c) => c.name === '__Host-csrf' || c.name === 'csrf')?.value ?? null;
  if (!cookies.some((c) => c.name === 'omnipus-session')) {
    console.warn('WARNING: no omnipus-session cookie captured — WebSocket auth (chat) will fail.');
  }

  const storageState = {
    cookies,
    origins: [{
      origin: baseURL,
      localStorage: [
        { name: 'omnipus_auth_token', value: token },
        { name: 'omnipus_auth_role', value: 'admin' },
        { name: 'omnipus_auth_username', value: 'admin' },
      ],
    }],
  };
  fs.writeFileSync(outPath, JSON.stringify(storageState, null, 2));
  console.log(`auth written: ${outPath} (token len ${token.length}, csrf ${csrf ? 'yes' : 'no'})`);
} finally {
  await ctx.dispose();
}
