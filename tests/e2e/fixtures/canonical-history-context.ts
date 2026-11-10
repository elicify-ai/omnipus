import type { Page } from '@playwright/test';
import type { GatewayProcess } from './gateway-process';

// not-wire-format: a Playwright worker's actual test-process owner, never an API payload.
export type CanonicalHistoryContext = { gateway: GatewayProcess; authFile: string; page?: Page };
let active: CanonicalHistoryContext | undefined;

export function setCanonicalHistoryContext(value: CanonicalHistoryContext | undefined) {
  if (value && active) throw new Error('Canonical history fixture already has a live owner in this worker');
  active = value;
}
export function canonicalHistoryContext(): CanonicalHistoryContext | undefined { return active; }
export function requireCanonicalHistoryContext(): CanonicalHistoryContext {
  if (!active) throw new Error('BLOCKED: canonical transcript seeding requires the real owned canonical-history Playwright fixture');
  return active;
}
