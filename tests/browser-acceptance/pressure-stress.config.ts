import { defineConfig } from '@playwright/test';
import base from './input-connection.config';
export default defineConfig({ ...base, testMatch: 'pressure-stress.spec.ts', timeout: 300000 });
