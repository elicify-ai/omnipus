import { defineConfig } from '@playwright/test';
import base from './input-connection.config';
export default defineConfig({ ...base, testMatch: 'wheel-continuation.spec.ts', timeout: 120000 });
