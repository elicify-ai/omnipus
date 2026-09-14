import { defineConfig } from '@playwright/test';
import base from './input-connection.config';
export default defineConfig({ ...base, testMatch: 'popout-handoff.spec.ts', timeout: 150000 });
