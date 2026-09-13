import base from './input-connection.config';
import { defineConfig } from '@playwright/test';
export default defineConfig({ ...base, testMatch: 'input-connection-recovery.spec.ts', timeout: 180000 });
