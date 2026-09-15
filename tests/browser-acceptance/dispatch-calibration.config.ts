import { defineConfig } from '@playwright/test';
import base from './input-connection.config';
export default defineConfig({ ...base, testDir: '.', testMatch: 'dispatch-calibration.spec.ts', timeout: 120000 });
