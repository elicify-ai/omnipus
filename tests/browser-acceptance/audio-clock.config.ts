import { defineConfig } from '@playwright/test';
import base from './input-connection.config';
export default defineConfig({ ...base, testMatch: 'audio-clock.spec.ts', timeout: 240000 });
