import fs from 'node:fs';
import path from 'node:path';
import type { Page } from '@playwright/test';
import { test as base } from './console-errors';
import { GatewayProcess } from './gateway-process';
import { requireCanonicalHistoryContext, setCanonicalHistoryContext } from './canonical-history-context';

/** Real private gateway ownership is required by the offline canonical driver.
 * This fixture never repoints the shared shard home or fabricates a stopped owner. */
export const test = base.extend<{ canonicalGateway: GatewayProcess; canonicalPage: Page }>({
  canonicalGateway: [async ({ browserName }, use, info) => {
    if (!process.env.OPENROUTER_API_KEY_CI && !process.env.OPENROUTER_API_KEY) {
      throw new Error('BLOCKED: canonical-history real-gateway fixture requires the supplied OpenRouter key; CI-only without it');
    }
    const gateway = await GatewayProcess.start({ adminUsername: 'admin', adminPassword: 'admin123' });
    const authFile = info.outputPath('canonical-auth.json');
    fs.mkdirSync(path.dirname(authFile), { recursive: true });
    fs.writeFileSync(authFile, JSON.stringify(await gateway.browserStorageState()), { mode: 0o600 });
    setCanonicalHistoryContext({ gateway, authFile });
    try {
      await info.attach('canonical-gateway-owner', { contentType: 'application/json', body: JSON.stringify({ browser: browserName, home: gateway.homeDir, origin: gateway.baseURL }) });
      await use(gateway);
    } finally {
      setCanonicalHistoryContext(undefined);
      await gateway.stop();
    }
  }, { auto: true }],
  baseURL: async ({ canonicalGateway }, use) => { await use(canonicalGateway.baseURL); },
  storageState: async ({ canonicalGateway }, use) => { await use(await canonicalGateway.browserStorageState()); },
  canonicalPage: [async ({ page, canonicalGateway }, use) => {
    const context = requireCanonicalHistoryContext();
    if (context.gateway !== canonicalGateway) throw new Error('Canonical page/gateway ownership mismatch');
    context.page = page;
    try { await use(page); }
    finally { context.page = undefined; }
  }, { auto: true }],
});

export function canonicalHistoryAuthFile(): string { return requireCanonicalHistoryContext().authFile; }
