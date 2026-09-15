import base from './input-connection.config';
import { defineConfig } from '@playwright/test';
export default defineConfig({ ...base, testMatch: 'input-connection-authority.spec.ts', timeout: 150000 });
