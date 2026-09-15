# Mixed-input endurance with controlled audio

The Amsterdam candidate `3be6cea27d52857d262340de2e60ee1e679914ac` passed the 20-minute mixed-input diagnostic on 2026-09-15. Audio latency under CPU pressure remains unresolved. This diagnostic supplements the two earlier normal endurance passes; it is not classified as full-feature acceptance.

## Verified run

- Workload: 06:33:02.939–06:53:06.859 UTC (1203.920 seconds), 111 rounds, nine resizes.
- 10,491 binary-v1 input events: 4,945 moves, 3,330 wheel events, 333 mouse downs, 333 mouse ups, 1,108 key downs, 331 key ups, 111 text events.
- Playwright: one test passed, terminal exit 0. Existing per-round and per-minute input assertions remained active.
- Independently decoded final page state matched expected state: 111 clicks, 1,221 counted downs, 444 counted ups, 111 drags, held=0, scroll=300, errors=0, text=`@é`.
- Test-only audio schedule: eight minutes with no graph, four with a running zero-gain graph, four with a quiet tone, four after closing the graph. The same deliberate 120 ms key and 75 ms wheel handler delays remained active throughout.
- The tone was verified at the source and receiver. Thirteen settled final-phase samples confirmed absent/closed source context and a cleared clock.
- Installed and running binary hashes, image and unchanged Amsterdam machine configuration were reverified. No production build or deployment was performed for this diagnostic.

## Findings

| Phase | Mean interval audio buffer | CPU steal, weighted | Native audio clock/wall ratio, mean |
| --- | ---: | ---: | ---: |
| No graph | 32 ms | 0.8% | Not applicable |
| Running, zero gain | 698 ms | 34.5% | 0.61 |
| Tone | 1,013 ms | 38.6% | 0.59 |
| Closed graph | 870 ms | 30.6% | Not applicable |

At onset, 06:42:14–06:44:06 UTC, the browser's native AudioContext advanced only 44.881 seconds during 112 seconds of source wall time (40.1% speed), while reporting running. This source observation is encoded into the displayed video independently of relay logs. CPU steal rose to roughly 40–57% at the same onset. Twenty-one relay windows recorded 47.88 seconds of source audio during 105.921 seconds, with only 225 ms total forwarding-write time. These observations locate a substantial delay before encoding/forwarding, in remote audio rendering/output scheduling. They do not yet distinguish the browser audio thread from the host audio driver, or CPU quota effects from host contention.

Starting a real tone did not restore normal clock speed; closing the graph did not clear receiver delay under continued pressure. Merely changing audio activity is not an established fix. No synthetic silent audio workaround was introduced.

## Limits and next investigation

The phase order is fixed, so audio state is confounded with elapsed time and changing CPU availability. The fixture deliberately blocks page handlers and is not representative of every ordinary page. Buffer measurements are interval averages, not end-to-end input latency. The test proves continued input delivery under this workload, not flawless audio/video or absence of all future bugs.

Next isolate native browser audio scheduling from host audio output scheduling using bounded, read-only process/thread observations before proposing production changes. Preserve location, machine resources and WebRTC-only input transport.

Read-only follow-up found no `/dev/snd`, `/etc/asound.conf`, or `/etc/pulse` on Amsterdam. The observed `AudioWorkerThre` thread used ordinary scheduling (policy 0, nice 0). This is consistent with the existing headless/no-device architecture; it is not proof that thread priority is defective. Chromium's [current FakeAudioWorker implementation](https://raw.githubusercontent.com/chromium/chromium/main/media/base/fake_audio_worker.cc) invokes the source callback once when delayed, then skips its scheduling deadline forward to the next interval. This is a plausible mechanism for a sample-driven clock falling behind wall time; it has not been traced in the deployed Chrome build. Chromium's [output controller](https://raw.githubusercontent.com/chromium/chromium/main/services/audio/output_controller.cc) also uses fake output when local output is disabled, so installing a sound server is not an established remedy.

The existing input-pressure controller lowers both capture and sender ceilings to 20 then 15 FPS. Encoder-resolution adaptation requires reported encoder CPU pressure and sufficient source frame demand; it intentionally does not infer overload from a static page. Neither policy uses audio starvation. The next bounded experiment should compare actual capture/sender ceilings at 30, 10, then 30 FPS under a fixed synthetic workload and tone, recording native audio-clock rate and actual source/encoded frame deltas. It must remain diagnostic-only and must not replace the mixed-input acceptance run. No production adaptation policy has been changed on the strength of this hypothesis.

## Validation of the diagnostic

### Isolated native capture follow-up

The four-phase native Linux diagnostic completed with terminal exit 0 and all phase markers verified. Same Chrome/capture/audio context; manual adaptation frozen; encoded dimensions stayed 1344×672. After eight minutes of warmup, mean native audio-clock/wall ratios were 0.27 at requested 30 FPS, 0.32 at 10 FPS, 0.33 after restoring 30 FPS, and 0.38 after disabling the artificial busy loop (animation remained active). Actual encoded frame rates were only 3.3, 3.9, 3.4 and 5.9 FPS respectively. Lowering the ceiling did not restore audio; actual encoding was already below either ceiling. This does not establish that video cost is irrelevant, or that ordinary pages suffer the same slowdown.

The CPU collector for this follow-up started after the experiment ended because its execution approval was delayed. Its samples must not be correlated with the experiment. Earlier full-endurance CPU evidence remains valid. This isolated run has no remote viewer and provides no receiver-quality result. The first launch failed because the diagnostic TMPDIR made Chrome's Unix socket path too long; only the private runner path was corrected before repeating the same binary.

Private native log: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/audio-capture-result.log`; summary: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/audio-capture-analysis.json`.

Seventeen decoder tests passed, as did a native local browser lifecycle/pixel check and focused TypeScript checking. Compile-valid mutations of signature, phase and fault decoding were caught; native fixture boundary, tone gain and close mutations were caught. Independent review found a missing final-phase closure assertion. It was added after the live run and replayed against its recorded samples; negative controls for a still-running context, an uncleared clock and missing observations all failed as required. The strengthened assertion has not been rerun in a new live session.

Private evidence directory: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/audio-pressure-3be6cea27`.

Private phase analysis: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/audio-pressure-3be6cea27-analysis.json`.
