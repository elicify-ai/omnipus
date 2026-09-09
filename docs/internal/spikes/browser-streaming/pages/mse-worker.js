// MSE entirely inside a dedicated worker (MediaSource in Worker + MediaSourceHandle).
// This is the adversarial case for a main-thread-only shim.
let ms, sbs = {};
self.onmessage = async (e) => {
  const { run } = e.data;
  const man = await (await fetch(`/out/${run}/manifest.json`)).json();
  ms = new MediaSource();
  const handle = ms.handle;                    // transferable
  self.postMessage({ handle }, [handle]);
  await new Promise(r => ms.addEventListener('sourceopen', r, { once: true }));
  self.postMessage({ log: 'worker sourceopen' });

  const wait = sb => sb.updating ? new Promise(r => sb.addEventListener('updateend', r, { once: true })) : Promise.resolve();
  let n = 0, bytes = 0;
  for (const ev of man.events) {
    if (ev.ev === 'addSourceBuffer') { sbs[ev.sb] = ms.addSourceBuffer(ev.mime); }
    else if (ev.ev === 'append' && ev.file) {
      const sb = sbs[ev.sb]; if (!sb) continue;
      const buf = await (await fetch(`/out/${run}/${ev.file}`)).arrayBuffer();
      await wait(sb); sb.appendBuffer(buf); await wait(sb);
      n++; bytes += buf.byteLength;
    }
  }
  self.postMessage({ log: `worker appended ${n} buffers, ${bytes} bytes` });
};
