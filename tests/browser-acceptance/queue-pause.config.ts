import { defineConfig } from '@playwright/test';
import base from './input-connection.config';
export default defineConfig({ ...base, testMatch: 'queue-pause.spec.ts', timeout: 90000 });
