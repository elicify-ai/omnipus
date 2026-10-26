import { realpathSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import type { TestInfo } from '@playwright/test';
import type { ViteDevServer } from 'vite';

/**
 * Local diagnosis only (OMNIPUS_HISTORY_DEV=1): serve THIS checkout's
 * non-minified SPA through Vite, proxying every /api call and socket to the
 * same isolated real gateway. CI never sets it, so CI tests the embedded SPA.
 * OMNIPUS_HISTORY_DEV_CACHE names a warm, isolated optimizer cache; a cold one
 * can make Vite reload the page on its first visit.
 */
export async function historyDevOrigin(gatewayURL: string, info: TestInfo) {
  if (process.env.OMNIPUS_HISTORY_DEV !== '1') return { origin: gatewayURL, close: async () => {} };
  const { createServer } = await import('vite');
  const root = fileURLToPath(new URL('../../../../', import.meta.url));
  const dev: ViteDevServer = await createServer({ root, configFile: `${root}/vite.config.ts`,
    cacheDir: process.env.OMNIPUS_HISTORY_DEV_CACHE ?? info.outputPath('vite-cache'),
    server: { host: 'localhost', port: 0, strictPort: true,
      fs: { allow: [root, realpathSync(`${root}/node_modules`), info.outputDir] },
      proxy: { '/api': { target: gatewayURL, changeOrigin: true, ws: true, rewriteWsOrigin: true } } } });
  await dev.listen();
  const address = dev.httpServer?.address();
  if (!address || typeof address === 'string') throw new Error('Vite did not bind its isolated port');
  const transformed = await dev.transformRequest('/src/components/chat/useHistoryViewport.ts');
  await info.attach('viewport-development-source', { contentType: 'text/javascript', body: transformed?.code ?? '' });
  return { origin: `http://localhost:${address.port}`, close: () => dev.close() };
}
