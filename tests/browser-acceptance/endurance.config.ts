import { defineConfig } from '@playwright/test';
import base from './input-connection.config';
export default defineConfig({ ...base, testMatch: 'endurance.spec.ts', timeout: 1500000 });
