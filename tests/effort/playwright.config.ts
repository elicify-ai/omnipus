// Deterministic, self-hosted effort browser lane. The spec starts and closes
// its own GatewayProcess with a local provider peer and its own authenticated
// cookie. Shared-suite live-key onboarding is inapplicable to this lane, not
// bypassed product authentication. No assertions, retries or validation change.
import { defineConfig } from '@playwright/test'
export default defineConfig({
 testDir:'../e2e',testMatch:'effort.spec.ts',workers:1,retries:0,
 timeout:90000,expect:{timeout:10000},
 outputDir:'../../test-results/effort-fix1',
 reporter:[['list'],['json',{outputFile:'../../test-results/effort-fix1.json'}]],
 use:{browserName:'chromium',headless:true,storageState:{cookies:[],origins:[]},trace:'retain-on-failure'},
})
