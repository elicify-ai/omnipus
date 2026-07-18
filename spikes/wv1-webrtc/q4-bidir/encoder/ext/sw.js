// Q2 spike service worker. doCapture() finds the TONETAB + CONTROLLER tabs,
// asks tabCapture for a stream id consumable by the controller tab, and injects
// it into the controller page. Callable three ways: action click, keyboard
// command (_execute_action), or directly via CDP Runtime.evaluate on this
// worker's target ("globalThis.doCapture()").
'use strict';

async function doCapture() {
  const out = { at: Date.now() };
  try {
    const tabs = await chrome.tabs.query({});
    out.tabs = tabs.map(t => ({ id: t.id, title: t.title, url: t.url, active: t.active }));
    const tone = tabs.find(t => (t.title || '').includes('TONETAB') || (t.url || '').includes('tone.html'));
    const controller = tabs.find(t => (t.title || '').includes('CONTROLLER') || (t.url || '').includes('controller.html'));
    if (!tone || !controller) throw new Error('tabs not found: tone=' + !!tone + ' controller=' + !!controller);
    const streamId = await chrome.tabCapture.getMediaStreamId({ targetTabId: tone.id, consumerTabId: controller.id });
    out.streamId = streamId;
    await chrome.scripting.executeScript({
      target: { tabId: controller.id },
      func: (id) => { window.__streamId = id; if (window.__onStreamId) window.__onStreamId(id); },
      args: [streamId],
    });
    out.ok = true;
  } catch (e) {
    out.ok = false;
    out.error = String(e);
  }
  globalThis.__last = out;
  return out;
}

chrome.action.onClicked.addListener(() => doCapture());
globalThis.doCapture = doCapture;
