// MSE interception shim. Injected via Page.addScriptToEvaluateOnNewDocument,
// so it runs before ANY page script in every frame.
// Exfiltrates via Runtime.addBinding (CSP-proof, unlike fetch/WebSocket).
(function () {
  'use strict';
  // Scope-agnostic: this shim must run in a Window AND in a DedicatedWorkerGlobalScope
  // (MSE-in-Worker). There is no `window` in a worker, so bind the global once.
  var G = (typeof globalThis !== 'undefined') ? globalThis
        : (typeof self !== 'undefined') ? self : this;
  var IS_WORKER = (typeof WorkerGlobalScope !== 'undefined' && G instanceof WorkerGlobalScope)
               || (typeof Window === 'undefined');
  if (G.__mseShimInstalled) return;
  G.__mseShimInstalled = true;

  var SEND = '__mseSend';       // Runtime binding name
  var T0 = Date.now();
  var seq = 0;
  var sbId = 0;
  var msId = 0;
  var pending = [];
  var CHUNK = 512 * 1024;       // split big base64 payloads to avoid CDP message limits

  function now() { return Date.now() - T0; }

  function send(obj) {
    try {
      if (G[SEND]) {
        while (pending.length) { G[SEND](JSON.stringify(pending.shift())); }
        G[SEND](JSON.stringify(obj));
      } else {
        pending.push(obj);
      }
    } catch (e) { /* never break the page */ }
  }

  // Fast-ish base64 of an ArrayBuffer/TypedArray.
  var B64 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
  function b64(u8) {
    var out = '', i, l = u8.length, a, b, c;
    for (i = 0; i + 2 < l; i += 3) {
      a = u8[i]; b = u8[i + 1]; c = u8[i + 2];
      out += B64[a >> 2] + B64[((a & 3) << 4) | (b >> 4)] + B64[((b & 15) << 2) | (c >> 6)] + B64[c & 63];
    }
    if (i < l) {
      a = u8[i]; b = (i + 1 < l) ? u8[i + 1] : 0;
      out += B64[a >> 2] + B64[((a & 3) << 4) | (b >> 4)];
      out += (i + 1 < l) ? B64[(b & 15) << 2] : '=';
      out += '=';
    }
    return out;
  }

  function toU8(data) {
    if (data == null) return new Uint8Array(0);
    if (data instanceof ArrayBuffer) return new Uint8Array(data);
    if (ArrayBuffer.isView(data)) return new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
    return new Uint8Array(0);
  }

  function emitData(id, u8) {
    var s = b64(u8);
    var parts = Math.ceil(s.length / CHUNK) || 1;
    for (var p = 0; p < parts; p++) {
      send({ ev: 'data', id: id, part: p, parts: parts, b64: s.substr(p * CHUNK, CHUNK) });
    }
  }

  // ---- SourceBuffer -------------------------------------------------------
  if (G.SourceBuffer) {
    var SBp = SourceBuffer.prototype;
    var origAppend = SBp.appendBuffer;
    var origRemove = SBp.remove;
    var origChangeType = SBp.changeType;

    SBp.appendBuffer = function (data) {
      var u8 = toU8(data);
      var id = ++seq;
      var rec = {
        ev: 'append', id: id, t: now(), sb: this.__sbId, bytes: u8.length,
        mime: this.__mime, mode: undefined, tsOffset: undefined,
        awStart: undefined, awEnd: undefined, msDuration: undefined,
        buffered: undefined
      };
      try { rec.mode = this.mode; } catch (e) {}
      try { rec.tsOffset = this.timestampOffset; } catch (e) {}
      try { rec.awStart = this.appendWindowStart; } catch (e) {}
      try { rec.awEnd = this.appendWindowEnd; } catch (e) {}
      try {
        var br = [], i;
        for (i = 0; i < this.buffered.length; i++) br.push([this.buffered.start(i), this.buffered.end(i)]);
        rec.buffered = br;
      } catch (e) {}
      try { if (this.__ms) rec.msDuration = this.__ms.duration; } catch (e) {}
      send(rec);
      emitData(id, u8);
      return origAppend.apply(this, arguments);
    };

    SBp.remove = function (start, end) {
      send({ ev: 'remove', t: now(), sb: this.__sbId, start: start, end: end });
      return origRemove.apply(this, arguments);
    };

    if (origChangeType) {
      SBp.changeType = function (mime) {
        send({ ev: 'changeType', t: now(), sb: this.__sbId, mime: mime });
        this.__mime = mime;
        return origChangeType.apply(this, arguments);
      };
    }

    // property setters we care about, observed rather than blocked
    ['timestampOffset', 'appendWindowStart', 'appendWindowEnd', 'mode'].forEach(function (prop) {
      var d = Object.getOwnPropertyDescriptor(SBp, prop);
      if (!d || !d.set) return;
      Object.defineProperty(SBp, prop, {
        configurable: true, enumerable: d.enumerable,
        get: d.get,
        set: function (v) {
          send({ ev: 'prop', t: now(), sb: this.__sbId, prop: prop, value: v });
          return d.set.call(this, v);
        }
      });
    });
  }

  // ---- MediaSource --------------------------------------------------------
  function wrapMS(MSCtor, label) {
    if (!MSCtor || !MSCtor.prototype) return;
    var p = MSCtor.prototype;
    var origAdd = p.addSourceBuffer;
    var origEnd = p.endOfStream;
    var origRemoveSB = p.removeSourceBuffer;

    if (origAdd) {
      p.addSourceBuffer = function (mime) {
        var sb = origAdd.apply(this, arguments);
        try {
          sb.__sbId = ++sbId;
          sb.__mime = mime;
          sb.__ms = this;
          if (this.__msId == null) this.__msId = ++msId;
        } catch (e) {}
        send({ ev: 'addSourceBuffer', t: now(), sb: sb.__sbId, ms: this.__msId, mime: mime, impl: label });
        return sb;
      };
    }
    if (origRemoveSB) {
      p.removeSourceBuffer = function (sb) {
        send({ ev: 'removeSourceBuffer', t: now(), sb: sb && sb.__sbId });
        return origRemoveSB.apply(this, arguments);
      };
    }
    if (origEnd) {
      p.endOfStream = function (reason) {
        send({ ev: 'endOfStream', t: now(), ms: this.__msId, reason: reason });
        return origEnd.apply(this, arguments);
      };
    }
    var dd = Object.getOwnPropertyDescriptor(p, 'duration');
    if (dd && dd.set) {
      Object.defineProperty(p, 'duration', {
        configurable: true, enumerable: dd.enumerable, get: dd.get,
        set: function (v) { send({ ev: 'duration', t: now(), ms: this.__msId, value: v }); return dd.set.call(this, v); }
      });
    }
  }
  wrapMS(G.MediaSource, 'MediaSource');
  wrapMS(G.ManagedMediaSource, 'ManagedMediaSource');

  // ---- evasion detectors --------------------------------------------------
  // (a) MSE-in-Worker: MediaSourceHandle transferred to a worker => our shim is blind.
  if (G.MediaSource && 'handle' in (G.MediaSource.prototype || {})) {
    var hd = Object.getOwnPropertyDescriptor(MediaSource.prototype, 'handle');
    if (hd && hd.get) {
      Object.defineProperty(MediaSource.prototype, 'handle', {
        configurable: true, enumerable: hd.enumerable,
        get: function () { send({ ev: 'EVASION', t: now(), kind: 'MediaSourceHandle.get' }); return hd.get.call(this); }
      });
    }
  }
  // (b) Worker creation — MSE could move there.
  var OrigWorker = G.Worker;
  if (OrigWorker) {
    G.Worker = function (url, opts) {
      send({ ev: 'worker', t: now(), url: String(url).slice(0, 300) });
      return new OrigWorker(url, opts);
    };
    G.Worker.prototype = OrigWorker.prototype;
  }
  // (c) srcObject = MediaSource (in-memory attach, no blob URL)
  try {
    var vd = (typeof HTMLMediaElement !== 'undefined') && Object.getOwnPropertyDescriptor(HTMLMediaElement.prototype, 'srcObject');
    if (vd && vd.set) {
      Object.defineProperty(HTMLMediaElement.prototype, 'srcObject', {
        configurable: true, enumerable: vd.enumerable, get: vd.get,
        set: function (v) {
          send({ ev: 'srcObject', t: now(), kind: v && v.constructor ? v.constructor.name : String(v) });
          return vd.set.call(this, v);
        }
      });
    }
  } catch (e) {}

  send({ ev: 'shim', t: 0, scope: IS_WORKER ? 'worker' : 'window', url: (typeof location !== 'undefined' ? location.href : '?'), hasMS: !!G.MediaSource, hasMMS: !!G.ManagedMediaSource });
})();
