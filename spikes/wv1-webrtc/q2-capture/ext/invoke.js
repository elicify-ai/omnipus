// Extension PAGE context (not the SW): full chrome.* access, trivially CDP-evaluable.
'use strict';

window.doCapture = async function doCapture() {
  const out = { at: Date.now(), surface: 'ext-page' };
  try {
    const tabs = await chrome.tabs.query({});
    out.tabs = tabs.map(t => ({ id: t.id, title: t.title, active: t.active }));
    const tone = tabs.find(t => (t.title || '').includes('TONETAB') || (t.url || '').includes('tone.html'));
    const controller = tabs.find(t => (t.title || '').includes('CONTROLLER') || (t.url || '').includes('controller.html'));
    if (!tone || !controller) throw new Error('tabs not found: tone=' + !!tone + ' controller=' + !!controller);
    const streamId = await chrome.tabCapture.getMediaStreamId({ targetTabId: tone.id, consumerTabId: controller.id });
    out.streamId = streamId;
    await chrome.scripting.executeScript({
      target: { tabId: controller.id },
      world: 'MAIN', // default ISOLATED world cannot see the page's window.__onStreamId
      func: (id) => { window.__streamId = id; if (window.__onStreamId) window.__onStreamId(id); },
      args: [streamId],
    });
    out.ok = true;
  } catch (e) {
    out.ok = false;
    out.error = String(e);
  }
  window.__last = out;
  return out;
};

// Variant: consume the stream directly IN this extension page (no consumerTabId) —
// tests whether the extension itself can hold the capture (offscreen-doc pattern proxy).
window.doCaptureSelfConsume = async function () {
  const out = { at: Date.now(), surface: 'ext-page-self' };
  try {
    const tabs = await chrome.tabs.query({});
    const tone = tabs.find(t => (t.title || '').includes('TONETAB'));
    if (!tone) throw new Error('tone tab not found');
    const streamId = await chrome.tabCapture.getMediaStreamId({ targetTabId: tone.id });
    out.streamId = streamId;
    const stream = await navigator.mediaDevices.getUserMedia({
      audio: { mandatory: { chromeMediaSource: 'tab', chromeMediaSourceId: streamId } },
      video: { mandatory: { chromeMediaSource: 'tab', chromeMediaSourceId: streamId } },
    });
    out.gotStream = true;
    out.videoTracks = stream.getVideoTracks().map(t => ({ label: t.label, readyState: t.readyState, settings: t.getSettings() }));
    out.audioTracks = stream.getAudioTracks().map(t => ({ label: t.label, readyState: t.readyState, settings: t.getSettings() }));
    // quick RMS probe (1.2s) right here
    const ac = new AudioContext();
    await ac.resume();
    const src = ac.createMediaStreamSource(new MediaStream(stream.getAudioTracks()));
    const an = ac.createAnalyser(); an.fftSize = 2048; src.connect(an);
    const buf = new Float32Array(an.fftSize);
    const rms = [];
    for (let i = 0; i < 12; i++) {
      await new Promise(r => setTimeout(r, 100));
      an.getFloatTimeDomainData(buf);
      let s = 0; for (let j = 0; j < buf.length; j++) s += buf[j] * buf[j];
      rms.push(Number(Math.sqrt(s / buf.length).toFixed(5)));
    }
    out.rms = rms;
    stream.getTracks().forEach(t => t.stop());
    out.ok = true;
  } catch (e) {
    out.ok = false;
    out.error = String(e);
  }
  window.__lastSelf = out;
  return out;
};
