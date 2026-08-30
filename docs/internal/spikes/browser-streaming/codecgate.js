// Codec gate. Injected AHEAD of shim.js in the source Chrome so the page's very
// first capability query already sees the lie. Makes a chosen codec family look
// unsupported so an adaptive player (YouTube) negotiates a different one.
//
// record.js substitutes the two placeholders below before injection.
(function () {
  'use strict';
  var G = (typeof globalThis !== 'undefined') ? globalThis : self;
  if (G.__codecGateInstalled) return;
  G.__codecGateInstalled = true;

  var RE = __BLOCK_RE__;              // e.g. /av01|av1\b/i
  var HARD = __HARD_BLOCK__;          // also throw from addSourceBuffer
  var SEND = '__mseSend';
  var pending = [];
  var hits = 0;
  function send(o) {
    try {
      if (G[SEND]) { while (pending.length) G[SEND](JSON.stringify(pending.shift())); G[SEND](JSON.stringify(o)); }
      else pending.push(o);
    } catch (e) {}
  }
  function blocked(s) { try { return RE.test(String(s)); } catch (e) { return false; } }
  function note(api, arg, verdict) {
    hits++;
    if (hits <= 60) send({ ev: 'gate', api: api, arg: String(arg).slice(0, 120), verdict: verdict });
  }

  // 1. MediaSource.isTypeSupported / ManagedMediaSource.isTypeSupported
  ['MediaSource', 'ManagedMediaSource'].forEach(function (n) {
    var C = G[n];
    if (!C || !C.isTypeSupported) return;
    var orig = C.isTypeSupported.bind(C);
    C.isTypeSupported = function (mime) {
      if (blocked(mime)) { note(n + '.isTypeSupported', mime, false); return false; }
      return orig(mime);
    };
  });

  // 2. HTMLMediaElement.prototype.canPlayType
  if (typeof HTMLMediaElement !== 'undefined' && HTMLMediaElement.prototype.canPlayType) {
    var origCPT = HTMLMediaElement.prototype.canPlayType;
    HTMLMediaElement.prototype.canPlayType = function (mime) {
      if (blocked(mime)) { note('canPlayType', mime, ''); return ''; }
      return origCPT.apply(this, arguments);
    };
  }

  // 3. navigator.mediaCapabilities.decodingInfo — the modern query YouTube uses.
  try {
    var mc = G.navigator && G.navigator.mediaCapabilities;
    if (mc && mc.decodingInfo) {
      var origDI = mc.decodingInfo.bind(mc);
      mc.decodingInfo = function (cfg) {
        var ct = cfg && ((cfg.video && cfg.video.contentType) || (cfg.audio && cfg.audio.contentType));
        if (blocked(ct)) {
          note('decodingInfo', ct, false);
          return Promise.resolve({ supported: false, smooth: false, powerEfficient: false });
        }
        return origDI(cfg);
      };
    }
  } catch (e) {}

  // 4. Optional hard block: refuse the SourceBuffer outright. Off by default so
  //    we can first observe what the player negotiates when merely told "no".
  if (HARD && G.MediaSource && G.MediaSource.prototype.addSourceBuffer) {
    var origAdd = G.MediaSource.prototype.addSourceBuffer;
    G.MediaSource.prototype.addSourceBuffer = function (mime) {
      if (blocked(mime)) {
        note('addSourceBuffer(HARD)', mime, 'throw');
        throw new DOMException('gate: codec blocked', 'NotSupportedError');
      }
      return origAdd.apply(this, arguments);
    };
  }

  send({ ev: 'gate-installed', re: String(RE), hard: !!HARD });
})();
