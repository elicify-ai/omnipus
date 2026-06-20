// Mint a Playwright storageState auth.json for one gateway.
// Replicates tests/e2e/global-setup.ts: onboarding/complete (idempotent) -> login
// -> write storageState with omnipus_auth_token in localStorage + CSRF cookie.
//
// Usage: node mkauth.mjs <baseURL> <outPath> <openrouterKey>
import { request } from '@playwright/test';
import fs from 'fs';

const [baseURL, outPath, apiKey, modelArg] = process.argv.slice(2);
const MODEL = modelArg || 'openrouter/google/gemini-2.5-flash';

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

  // 3. Extract CSRF cookie from context state.
  let csrf = null;
  const st = await ctx.storageState();
  for (const c of st.cookies) {
    if (c.name === '__Host-csrf' || c.name === 'csrf') { csrf = c.value; break; }
  }

  const url = new URL(baseURL);
  const secure = url.protocol === 'https:';
  const cookies = csrf
    ? [{
        name: secure ? '__Host-csrf' : 'csrf', value: csrf,
        domain: url.hostname, path: '/', expires: -1,
        httpOnly: false, secure, sameSite: 'Strict',
      }]
    : [];

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
