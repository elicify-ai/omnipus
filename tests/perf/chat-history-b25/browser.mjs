import fs from 'node:fs';
import path from 'node:path';
import { timingVerdict, normalVerdict, capVerdict, missing, result, combine } from './metrics.mjs';

const SCROLLER = '[data-testid="virtualized-message-list"]';
const INPUT = 'textarea[aria-label="Message input"]';
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const settle = page => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));

// Runs in the actual renderer. No replacement transport, state, geometry or UI.
function installCoverageObserver() {
  const data = { frames: 0, gaps: 0, missing: 0, running: false };
  window.__b25Coverage = data;
  const sample = () => {
    if (data.running) {
      const scroller = document.querySelector('[data-testid="virtualized-message-list"]');
      const content = scroller?.querySelector('[data-history-content]');
      if (!scroller || !content) data.missing++;
      else {
        const view = scroller.getBoundingClientRect(), box = content.getBoundingClientRect();
        const top = Math.max(view.top, box.top), bottom = Math.min(view.bottom, box.bottom);
        const rows = [...content.querySelectorAll('[data-history-anchor]')].map(row => row.getBoundingClientRect())
          .filter(row => row.height > 0 && row.bottom > top && row.top < bottom).sort((a,b) => a.top - b.top);
        const declaredGap = parseFloat(getComputedStyle(content).rowGap) || 0;
        let end = top, gap = false;
        for (const row of rows) { if (row.top > end + declaredGap + 1) gap = true; end = Math.max(end, row.bottom); }
        if (bottom > top + 1 && (rows.length === 0 || end < bottom - 1)) gap = true;
        data.frames++; if (gap) data.gaps++;
      }
    }
    requestAnimationFrame(sample);
  };
  requestAnimationFrame(sample);
}
function visibleAnchor() {
  const scroller = document.querySelector('[data-testid="virtualized-message-list"]');
  if (!scroller) return null;
  const top = scroller.getBoundingClientRect().top;
  const row = [...scroller.querySelectorAll('[data-history-anchor]')].find(row => {
    const box = row.getBoundingClientRect(); return box.height > 0 && box.bottom > top && box.top < top + scroller.clientHeight;
  });
  return row ? { key: row.getAttribute('data-history-anchor'), top: row.getBoundingClientRect().top - top } : null;
}
function anchorOffset(key) {
  const scroller = document.querySelector('[data-testid="virtualized-message-list"]');
  const row = [...(scroller?.querySelectorAll('[data-history-anchor]') ?? [])].find(item => item.getAttribute('data-history-anchor') === key);
  return row ? row.getBoundingClientRect().top - scroller.getBoundingClientRect().top : null;
}
export async function retainedState(cdp, sid) {
  // Query the REAL reachable Zustand API, then its current state. Never infer
  // retained payload bounds from a virtualized DOM. Read-only, content-free.
  // This implementation-specific instrument fails UNMEASURED if unrecognizable.
  const group = 'b25-store-probe';
  try {
    const prototype = await cdp.send('Runtime.evaluate', { expression: 'Object.prototype', objectGroup: group });
    const objects = await cdp.send('Runtime.queryObjects', { prototypeObjectId: prototype.result.objectId, objectGroup: group });
    const answer = await cdp.send('Runtime.callFunctionOn', { objectId: objects.objects.objectId,
      functionDeclaration: `function(sid) {
        const results = [];
        for (const api of this) {
          const get = api && Object.getOwnPropertyDescriptor(api, 'getState')?.value;
          const sub = api && Object.getOwnPropertyDescriptor(api, 'subscribe')?.value;
          const init = api && Object.getOwnPropertyDescriptor(api, 'getInitialState')?.value;
          if (typeof get !== 'function' || typeof sub !== 'function' || typeof init !== 'function') continue;
          const state = get();
          if (!state?.sessionsById || typeof state.loadOlderHistory !== 'function') continue;
          const bucket = state.sessionsById[sid];
          if (!bucket?.history || !Array.isArray(bucket.history.readerPositions) || !Array.isArray(bucket.history.tailEntries)) continue;
          const h = bucket.history, map = bucket.messagesById ?? {};
          results.push({reader:h.readerPositions.length,tail:h.tailEntries.length,payloads:Object.keys(map).length,
            aliases:Object.keys(bucket.historySourceAliases ?? {}).length,tools:Object.keys(bucket.toolCalls ?? {}).length,
            tool_order:bucket.toolCallOrder?.length ?? 0,spans:Object.keys(bucket.spanBySpanId ?? {}).length,
            replay_aliases:Object.keys(bucket.mergedReplayMessageIds ?? {}).length,
            orphan_tool_owners:Object.values(bucket.toolCallOwnerMessageId ?? {}).filter(id=>!map[id]).length,
            history_loaded:h.historyLoaded,live_controls_ready:h.liveControlsReady,view_mode:h.viewMode,
            has_earlier:h.hasEarlier,has_later:h.hasLater,
            pending:Object.values(h.requests).some(r=>r.status==='loading'),errors:Object.values(h.requests).filter(r=>r.status==='error').length});
        }
        return results;
      }`, arguments: [{ value: sid }], returnByValue: true });
    if (answer.exceptionDetails) throw new Error('read-only retained-store probe exception');
    const states = answer.result.value;
    if (!Array.isArray(states) || !states.length) return { available: false, reason: 'CDP could not identify the actual chat store API' };
    if (!states.every(value => JSON.stringify(value) === JSON.stringify(states[0]))) return { available: false, reason: 'ambiguous retained chat store APIs' };
    return { available: true, ...states[0] };
  } catch (error) { return { available: false, reason: String(error.message) }; }
  finally { await cdp.send('Runtime.releaseObjectGroup', { objectGroup: group }); }
}
async function heap(cdp, stage) {
  await cdp.send('HeapProfiler.collectGarbage');
  const { metrics } = await cdp.send('Performance.getMetrics');
  const values = Object.fromEntries(metrics.map(row => [row.name, row.value]));
  return { stage, retained_js_heap_bytes: values.JSHeapUsedSize ?? null, js_heap_total_bytes: values.JSHeapTotalSize ?? null,
    documents: values.Documents ?? null, nodes: values.Nodes ?? null, layout_count: values.LayoutCount ?? null,
    instrument: 'CDP HeapProfiler.collectGarbage then Performance.getMetrics; renderer-wide retained heap, not Go allocated bytes' };
}

function trafficObserver(page, cdp, wire) {
  const totals = { http_encoded_bytes: 0, ws_received_bytes: 0, ws_sent_bytes: 0, ws_received_frames: 0, ws_sent_frames: 0,
    by_type: {}, snapshots: [], catchups: 0, invalid_frames: 0, http_failures: 0 };
  let current = null;
  const requests = new Map();
  totals.http_failure_kinds = [];
  totals.canceled_non_history_requests = 0;
  const bodies = new Set();
  const attachStarted = new Map();
  const attachRoundTrips = [];
  page.on('websocket', socket => {
    socket.on('framesent', ({ payload }) => {
      totals.ws_sent_frames++; totals.ws_sent_bytes += Buffer.byteLength(payload);
      const parsed = wire.WsFrame.safeParse(JSON.parse(payload.toString()));
      if (!parsed.success) { totals.invalid_frames++; return; }
      if (parsed.data.type === 'attach_session') attachStarted.set(parsed.data.session_id, performance.now());
    });
    socket.on('framereceived', ({ payload }) => {
      totals.ws_received_frames++; totals.ws_received_bytes += Buffer.byteLength(payload);
      let parsed;
      try { parsed = wire.WsFrame.safeParse(JSON.parse(payload.toString())); }
      catch { totals.invalid_frames++; return; }
      if (!parsed.success) { totals.invalid_frames++; return; }
      const frame = parsed.data;
      totals.by_type[frame.type] = (totals.by_type[frame.type] ?? 0) + 1;
      if (frame.type === 'session_snapshot') {
        current = { source_group_count: frame.page_info.source_group_count, row_count: frame.page_info.row_count,
          projected_bytes: frame.page_info.projected_bytes, soft_byte_budget: frame.page_info.soft_byte_budget,
          budget_override: frame.page_info.budget_override ?? null, replay_source_groups: 0, replay_frames: 0 };
        bodies.clear(); totals.snapshots.push(current);
      }
      if (frame.history_origin && current) {
        current.replay_frames++; bodies.add(frame.history_origin.position); current.replay_source_groups = bodies.size;
      }
      if (frame.type === 'catch_up_complete') {
        totals.catchups++;
        const started = attachStarted.get(frame.session_id);
        if (started !== undefined) attachRoundTrips.push(performance.now() - started);
      }
    });
  });
  cdp.on('Network.requestWillBeSent', event => { requests.set(event.requestId, /\/api\/v1\/sessions\/[^/]+\/messages(?:\?|$)/.test(event.request.url)); });
  cdp.on('Network.loadingFinished', event => { totals.http_encoded_bytes += event.encodedDataLength; requests.delete(event.requestId); });
  cdp.on('Network.loadingFailed', event => {
    if (event.canceled && !requests.get(event.requestId)) totals.canceled_non_history_requests++;
    else totals.http_failures++;
    totals.http_failure_kinds.push({ canceled: event.canceled === true, history: requests.get(event.requestId) === true,
      resource_type: event.type, error_class: event.errorText?.startsWith('net::') ? event.errorText.slice(0,80) : 'other' });
    requests.delete(event.requestId);
  });
  return { totals, attachRoundTrips };
}
async function addBadge(page, text) {
  await page.evaluate(text => {
    document.getElementById('b25-evidence-badge')?.remove();
    const badge = document.createElement('div'); badge.id = 'b25-evidence-badge'; badge.textContent = text;
    Object.assign(badge.style, { position: 'fixed', bottom: '0', right: '0', zIndex: '2147483647',
      font: '12px monospace', color: 'white', background: '#111', padding: '6px', pointerEvents: 'none' });
    document.body.append(badge);
  }, text);
}
async function screenshot(page, directory, name, badge) {
  await addBadge(page, badge);
  await page.screenshot({ path: path.join(directory, `${name}.png`), fullPage: false });
  // No page text/body snapshot for a genuine copy: keep only structural counts.
  const snapshot = await page.evaluate(() => ({ title: document.title,
    conversation_regions: document.querySelectorAll('[aria-label="Conversation history"]').length,
    mounted_source_groups: document.querySelectorAll('[data-history-position]').length,
    focused_role: document.activeElement?.getAttribute('role'), focused_tag: document.activeElement?.tagName }));
  fs.writeFileSync(path.join(directory, `${name}.redacted.json`), JSON.stringify(snapshot, null, 2), { mode: 0o600 });
}
async function selectSession(page, gateway, sid, observer, wire, expect) {
  await page.goto(`${gateway.baseURL}/#/sessions/${sid}`);
  await expect(page.locator(`[data-active-session-id="${sid}"]`)).toBeVisible();
  await expect(page.locator(INPUT)).toBeEnabled();
  await expect(page.locator(SCROLLER)).toBeVisible();
  await expect(page.locator(`${SCROLLER} [data-history-position]`).first()).toBeVisible();
  await expect.poll(() => observer.totals.catchups).toBeGreaterThan(0);
  await expect(page.locator(SCROLLER)).not.toHaveAttribute('aria-busy', 'true');
  await page.evaluate(() => document.fonts.ready);
  // Verify bottom geometry remains unchanged across layout turns. Include every
  // contiguous opening request: a momentary idle between top-ups is insufficient.
  let prior = null, stable = 0;
  for (let attempt = 0; attempt < 180; attempt++) {
    await settle(page);
    const view = await page.locator(SCROLLER).evaluate(el => ({
      bottom: el.scrollHeight - el.scrollTop - el.clientHeight,
      busy: el.getAttribute('aria-busy') === 'true', height: el.scrollHeight,
      rows: el.querySelectorAll('[data-history-position]').length,
    }));
    if (!view.busy && Math.abs(view.bottom) <= 1 && prior && view.height === prior.height && view.rows === prior.rows) stable++;
    else stable = 0;
    prior = view;
    if (stable >= 3) return view;
  }
  throw new Error('opening never settled at the recent bottom with current controls ready');
}

export async function browserCampaign(gateway, wire, sid, otherSid, options) {
  const directory = options.output;
  const browser = await wire.chromium.launch({ headless: true, env: { ...process.env, ...options.childEnvironment } });
  const context = await browser.newContext({ storageState: await gateway.browserStorageState(), viewport: { width: 1280, height: 1000 } });
  await context.tracing.start({ screenshots: true, snapshots: true, sources: false });
  await context.addInitScript(installCoverageObserver);
  const page = await context.newPage();
  const cdp = await context.newCDPSession(page);
  await cdp.send('Performance.enable'); await cdp.send('Network.enable'); await cdp.send('HeapProfiler.enable');
  const observer = trafficObserver(page, cdp, wire);
  const measurements = { browser: await browser.version(), viewport: { width: 1280, height: 1000 },
    user_agent: null, opens: [], normal: [], traversal: [], heaps: [], traffic: observer.totals,
    badge: `${gateway.adminUsername} / operator evaluator / ${options.kind} / ${options.candidate.slice(0,12)}` };
  measurements.console_errors = [];
  const campaignStarted = performance.now();
  page.on('console', async message => {
    if (message.type() !== 'error') return;
    const entry = { elapsed_ms: performance.now() - campaignStarted, kind: 'console-error' };
    if (options.kind === 'synthetic') {
      entry.text = message.text();
      entry.stacks = [];
      for (const arg of message.args()) {
        try { entry.stacks.push(await arg.evaluate(value => value && typeof value === 'object' ? { name: value.name, stack: value.stack, componentStack: value.componentStack } : String(value))); }
        catch { entry.stacks.push('console handle unavailable'); }
      }
    } else entry.reason = 'raw console arguments withheld for permissioned data; inspect private trace';
    measurements.console_errors.push(entry);
  });
  page.on('pageerror', error => measurements.console_errors.push({ elapsed_ms: performance.now() - campaignStarted,
    kind: 'pageerror', ...(options.kind === 'synthetic' ? { text: error.message, stack: error.stack } : { reason: 'raw error withheld for permissioned data' }) }));
  const rows = {};
  options.onProgress?.(measurements);
  try {
    // First load/bootstrap is outside selection timing; every measured selection
    // starts from an actually selected other session in the same embedded SPA.
    await page.goto(`${gateway.baseURL}/#/sessions/${otherSid}`);
    await wire.expect(page.locator(INPUT)).toBeEnabled();
    measurements.user_agent = await page.evaluate(() => navigator.userAgent);
    measurements.heaps.push(await heap(cdp, 'before-opens'));
    for (let n = 0; n < (options.smoke ? 3 : 30); n++) {
      await page.goto(`${gateway.baseURL}/#/sessions/${otherSid}`);
      await wire.expect(page.locator(`[data-active-session-id="${otherSid}"]`)).toBeVisible();
      await wire.expect(page.locator(INPUT)).toBeEnabled();
      const pingStart = performance.now();
      const health = await page.request.get(`${gateway.baseURL}/health`);
      const gateway_round_trip_ms = performance.now() - pingStart;
      if (!health.ok()) throw new Error(`gateway reference RTT health HTTP ${health.status()}`);
      const catchups = observer.totals.catchups;
      const begin = performance.now();
      await selectSession(page, gateway, sid, observer, wire, wire.expect);
      await wire.expect.poll(() => observer.totals.catchups).toBeGreaterThan(catchups);
      // A final layout turn AFTER completion, not replay done, is in the timer.
      await settle(page);
      measurements.opens.push({ ordinal: n + 1, ready_ms: performance.now() - begin, gateway_round_trip_ms });
      if ([0, 29].includes(n)) await screenshot(page, directory, `open-${String(n+1).padStart(2,'0')}`, measurements.badge);
    }
    const uiTiming = timingVerdict(measurements.opens.map(row => row.ready_ms), 1500);
    const rtt = result(measurements.opens.every(row => row.gateway_round_trip_ms <= 100), { samples_ms: measurements.opens.map(row => row.gateway_round_trip_ms), target_ms: 100 }, 'actual /health gateway round trip before each selection; not history preparation time');
    rows['UI ready'] = rtt.status === 'FAIL' ? missing('gateway round-trip reference envelope >100 ms', { ui: uiTiming, gateway_round_trip: rtt, opens: measurements.opens }) : combine({ ui: uiTiming, reference_round_trip: rtt }, { opens: measurements.opens });
    measurements.heaps.push(await heap(cdp, 'after-opens'));
    let traversal;
    try { traversal = await traverse(page, cdp, wire, sid, directory, measurements, options.smoke ? 6 : 100); }
    catch (error) {
      measurements.traversal_error = String(error.message);
      traversal = result(false, { collected_pages: measurements.traversal.length, error: measurements.traversal_error }, 'actual scrolling or its instrument failed; inspect failure screenshot/trace');
      await screenshot(page, directory, 'scroll-failure', measurements.badge);
      // A failure remains FAIL even when zero boundaries completed. Previously
      // completed UI-open measurements are not overwritten by a later crash.
      rows['Normal reading'] = result(false, { samples: measurements.normal, completed_boundaries: measurements.normal.length, error: measurements.traversal_error }, 'scrolling failed before the 30-boundary campaign completed');
    }
    measurements.inactive_before = await retainedState(cdp, sid);
    await page.goto(`${gateway.baseURL}/#/sessions/${otherSid}`);
    await wire.expect(page.locator(`[data-active-session-id="${otherSid}"]`)).toBeVisible();
    await wire.expect(page.locator(INPUT)).toBeEnabled(); await settle(page);
    measurements.inactive_after = await retainedState(cdp, sid);
    measurements.heaps.push(await heap(cdp, 'after-inactive-release'));
    await screenshot(page, directory, 'inactive-release', measurements.badge);
    const inactive = measurements.inactive_after;
    const release = inactive.available ? result(['reader','tail','payloads','aliases','tools','tool_order','spans','replay_aliases'].every(key => inactive[key] === 0), { before: measurements.inactive_before, after: inactive }, 'inactive idle fixture releases retained payload/maps, not merely mounted DOM') : missing(inactive.reason);
    const cleanup = measurements.traversal.some(row => row.state?.available && row.state.orphan_tool_owners > 0) ? result(false, {}, 'orphan tool owners retained')
      : measurements.traversal.length === 100 && measurements.traversal.every(row => row.state?.available) ? result(true, {}, 'no retained orphan tool owner in 100 actual page states') : missing('100 retained-state cleanup probes not available');
    rows['Normal reading'] ??= normalVerdict(measurements.normal);
    rows['Row memory/replay'] = combine({ caps: traversal, cleanup, inactive_release: release,
      server_allocated_bytes: options.allocation ?? missing('no candidate-bound Section-H allocation receipt supplied'),
      server_peak_allocated_bytes: missing('no isolated Go allocator peak profile supplied; sampled process RSS is not Go allocated/peak bytes'),
      browser_retained_heap: measurements.heaps.every(row => Number.isFinite(row.retained_js_heap_bytes)) ? result(true, { samples: measurements.heaps }, 'actual renderer retained heap after CDP collection; no invented byte budget') : missing('CDP retained heap unavailable'),
      transport: result(observer.totals.invalid_frames === 0 && observer.totals.http_failures === 0, { ...observer.totals, attach_completion_ms: observer.attachRoundTrips }, 'real encoded HTTP bytes plus WS payload bytes/frame counts; excludes TCP/TLS framing') }, measurements);
    return { rows, measurements };
  } finally {
    // Independent traffic/partial observations survive a browser-stage failure.
    options.onProgress?.(measurements);
    await context.tracing.stop({ path: path.join(directory, 'browser-trace.zip') });
    await context.close(); await browser.close();
  }
}

async function traverse(page, cdp, wire, sid, directory, measurements, pageTarget) {
  let busy = false, lockWheel = false, error = null;
  const seenCuts = new Set();
  const traversalStarted = performance.now();
  await page.route('**/api/v1/sessions/*/messages?*', async route => {
    const url = new URL(route.request().url());
    if (!url.searchParams.has('before')) { await route.continue(); return; }
    busy = true;
    const begin = performance.now();
    try {
      const response = await route.fetch();
      const raw = wire.SessionHistoryResponse.parse(await response.json());
      if (!response.ok()) throw new Error(`older boundary HTTP ${response.status()}`);
      const cut = raw.page_info.first_position;
      if (!cut && raw.entries.length === 0 && !raw.has_earlier) { await route.fulfill({ response }); return; }
      if (!cut || seenCuts.has(cut)) throw new Error('older traversal did not advance a distinct page');
      seenCuts.add(cut);
      lockWheel = true;
      const anchor = await page.evaluate(visibleAnchor);
      const coverageBefore = await page.evaluate(() => ({ ...window.__b25Coverage }));
      await route.fulfill({ response }); // Deliver the actual gateway response, unchanged.
      const response_ms = performance.now() - begin;
      await wire.expect(page.locator(SCROLLER)).not.toHaveAttribute('aria-busy', 'true');
      await settle(page);
      const top = anchor ? await page.evaluate(anchorOffset, anchor.key) : null;
      const coverage = await page.evaluate(() => ({ ...window.__b25Coverage }));
      const drift_px = top === null || !anchor ? null : Math.abs(top - anchor.top);
      const state = await retainedState(cdp, sid);
      const row = { ordinal: measurements.traversal.length + 1, response_ms, drift_px,
        gap: coverage.missing || coverage.frames === 0 ? null : coverage.gaps > 0,
        coverage_frames: coverage.frames, coverage_before_delivery: coverageBefore.frames,
        maximum_viewports_per_second: 0.75, source_group_count: raw.page_info.source_group_count,
        row_count: raw.page_info.row_count, projected_bytes: raw.page_info.projected_bytes,
        budget_override: raw.page_info.budget_override ?? null, state };
      measurements.traversal.push(row);
      if (measurements.normal.length < 30) measurements.normal.push(row);
      await page.evaluate(() => { Object.assign(window.__b25Coverage, { frames: 0, gaps: 0, missing: 0 }); });
    } catch (cause) { error = String(cause.message); await route.abort(); }
    finally { lockWheel = false; busy = false; }
  });
  await page.evaluate(() => { window.__b25Coverage.running = true; });
  const scroller = page.locator(SCROLLER);
  const box = await scroller.boundingBox();
  if (!box) throw new Error('actual conversation viewport not visible');
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  let idleTicks = 0;
  let priorScrollTop = await scroller.evaluate(el => el.scrollTop);
  measurements.scroll_start = await scroller.evaluate(el => ({ top: el.scrollTop, height: el.scrollHeight, viewport: el.clientHeight }));
  // Bounded by measured progress, not a larger tolerated latency. Exhaustion or
  // paused product paging reports UNMEASURED with every collected sample intact.
  for (let tick = 0; tick < 16000 && measurements.traversal.length < pageTarget && !error; tick++) {
    const prior = measurements.traversal.length;
    if (!lockWheel) await page.mouse.wheel(0, -box.height * .075);
    await delay(100); // <=0.75 viewport/s; never fast-scroll to force a pass.
    if (await scroller.count() === 0) {
      measurements.scroll_crash = { elapsed_ms: performance.now() - traversalStarted,
        last_observed: measurements.scroll_end ?? measurements.scroll_start };
      throw new Error('Conversation history disappeared during real wheel scrolling; inspect console/component stack and failure screenshot');
    }
    const currentScrollTop = await scroller.evaluate(el => el.scrollTop);
    if (!busy && prior === measurements.traversal.length && Math.abs(currentScrollTop - priorScrollTop) < 1 && currentScrollTop <= 1) idleTicks++; else idleTicks = 0;
    priorScrollTop = currentScrollTop;
    measurements.scroll_end = { top: currentScrollTop, ticks: tick + 1, completed_pages: measurements.traversal.length,
      elapsed_ms: performance.now() - traversalStarted };
    if (idleTicks > 120) break; // stopped at actual older edge, not merely inside a tall loaded page
    if ([30, 60, 100].includes(measurements.traversal.length) && measurements.traversal.length !== prior) {
      await screenshot(page, directory, `traversal-${measurements.traversal.length}`, measurements.badge);
      measurements.heaps.push(await heap(cdp, `traversal-${measurements.traversal.length}`));
    }
  }
  for (let n = 0; busy && n < 600; n++) await delay(100);
  await page.evaluate(() => { window.__b25Coverage.running = false; });
  await page.unrouteAll({ behavior: 'wait' });
  if (error) return result(false, { error, samples: measurements.traversal }, 'real traversal/instrument failure; inspect trace');
  return capVerdict(measurements.traversal.map(row => row.state.available ? row.state : {}));
}
