# ADR-062: Universal live-browser connectivity — embedded TURN, no external service

- **Status:** **Proposed — 2026-08-15** (under adversarial review; see "Open questions").
- **Deciders:** Daniel Piatkowski (operator). Direction stated 2026-08-15: connectivity that
  works universally, no external provider, no additional configuration for the user; and the
  closing constraint on scope — *"if the normal web page cannot be reached, no browser is
  needed anyway."*
- **Builds on:** ADR-047 (WebRTC live view), ADR-061 (WebRTC is the ONLY video path; failures
  must be visible). Closes issue #621 (VPN clients cannot establish WebRTC).
- **Evidence level:** 1 — every claim in Context was MEASURED on 2026-08-15 with both ends of
  the connection observed in a single run (bare Pion peer inside the Fly machine, real Chrome
  on the client), plus raw-socket probes. Two earlier confident diagnoses on this exact topic
  were wrong until measured; that is why this ADR leads with the evidence.

## Context

The live-browser viewer leg is established by ICE using one STUN server and nothing else
(`pkg/tools/browser/webrtc/session.go`: bare `SettingEngine`, `ICEServers = [stun]`). That
is peer-to-peer hole-punching. It works when viewer and gateway share a host or an open LAN,
and it fails on any hosted deployment, because **no hosted provider delivers inbound UDP to
an undeclared ephemeral port**. Since ADR-061 removed the JPEG fallback, that failure is now
visible as `ice-disconnected-timeout` (Fly UAT, 2026-08-15) instead of silently degrading.

What was measured, and what it rules out:

| Measurement (2026-08-15) | Result | Rules out |
|---|---|---|
| STUN from inside the Fly machine | public mapping obtained | "outbound UDP blocked" |
| Same local port to two STUN servers | identical mapped port | "Fly NAT is symmetric" |
| Fly UDP service declared + dedicated IPv4 + bind `fly-global-services`; raw datagrams client→machine→client | delivered both ways, sizes ≤1200B, STUN-formatted replies included | "Fly can't route UDP" (this claim was made twice and was wrong twice) |
| Browser ICE requests to that socket | 239 arrived intact | "Fly filters STUN" |
| Same run: browser `responsesReceived` | **0**, while a Python client on the same Mac received identical replies | isolates a client-side filter: a VPN system extension eating Chrome's ICE replies — issue #621's class |
| Bare Pion peer + real Chrome, same signalling, on the Mac | connected | "the probe/method is wrong" |

Two independent problems were therefore stacked: (1) the gateway's ephemeral-port hole-punching
never works on a hosted provider — the production gap; (2) some clients' networks filter raw UDP
for browsers — the #621 gap. A universal design must close both.

**The structural insight:** Omnipus is client-to-server, not peer-to-peer. The gateway always has
a fixed public address (`gateway.public_url`, already set by anyone behind a domain). We do not
need hole-punching; we need the gateway to *listen somewhere reachable and say where*.

## Decision (proposed)

Three connectivity tiers inside the single Go binary, offered together; ICE selects the best that
works. No external service; no new runtime dependency (`pion/turn` is already a transitive
dependency); no new user configuration.

| Tier | Mechanism | Serves |
|---|---|---|
| 1 | Direct UDP on ONE fixed port via `SetICEUDPMux`; gateway advertises its public address (`SetNAT1To1IPs`, sourced from `public_url`) as a host candidate | any provider that routes UDP; VPS; home lab. Fastest. |
| 2 | ICE-TCP on a fixed port (`SetICETCPMux`) | UDP blocked at provider or client |
| 3 | Embedded TURN (`pion/turn`) on **its own port** — see the correction below | corporate firewalls, hostile VPNs, "only HTTPS out". Closes #621. |

Scope: **viewer leg only** — the encoder leg (gateway ↔ its own headless Chrome) is loopback and
never needs relay. Fly-specific needs (bind `fly-global-services`, declare a UDP service) are a
deployment recipe (`fly-uat.toml`, committed), not core code.

Wire contract (Constraint #8): the SPA currently hard-codes its ICE servers
(`src/lib/browserWebRTC.ts:173`). The gateway must deliver ICE servers — including short-lived,
per-viewer TURN credentials — via the existing `browser_webrtc_state` frame. Schema first.

Diagnosability: on ICE failure the gateway logs the candidate pairs tried, and the panel's error
path (ADR-061) names the tier that failed. Today's day-long hunt must not repeat.

## Adversarial review (2026-08-15) — verdict BLOCK on tiers 2–3, tier 1 proceeded

The five claims the author trusted least were reviewed against the libraries and the repo.
Three were wrong. The corrections are recorded here rather than quietly edited away.

**CORRECTION 1 (critical) — "TURN over TLS on the SAME 443 as the web UI" is IMPOSSIBLE as
written, in every topology this product has.** The gateway has no TLS listener anywhere: no
`ListenAndServeTLS`, no `tls.Config`, no certificate handling (verified across `pkg/gateway/`
and `cmd/`). Locally the UI is plain HTTP. On Fly, 443 belongs to the edge proxy, which
terminates TLS and forwards HTTP only — TURN bytes would never reach the gateway — and a second
service cannot claim the same port. Reverse proxies behave identically. **Tier 3 therefore needs
its OWN port**, and the "nothing extra to open" promise must be stated as "one port, the same one
you already expose for the UI, plus one for media" rather than implied away. Acceptance criterion
2 as originally written was unsatisfiable on the very platform that motivated this ADR.

**CORRECTION 2 (critical) — embedded TURN would default to an authenticated OPEN RELAY.**
pion/turn's `DefaultPermissionHandler` admits every peer address, so a credential holder could
relay UDP to `localhost:5000`, the Fly 6PN, or cloud metadata endpoints — defeating the
internal-CIDR egress blocking planned in #155. The permission handler must admit only the
gateway's own ICE agent addresses. Related: pion/turn exposes no per-allocation teardown
(`AllocationCount`/`Close` only), so "revoked on detach" is not achievable — credentials must be
short-lived TURN-REST with a stated residual window, not claimed as revocable.

**CORRECTION 3 (major) — two defects in tier 1's first implementation**, both of which would
have broken things that currently work, and both now fixed with non-vacuous tests:
one `SettingEngine` serves BOTH legs, so the public-address rewrite would have reached the
loopback encoder leg and broken capture on every install; and a Session exists per AGENT, so a
per-Session bind of the fixed port would have left every agent after the first silently on an
ephemeral port.

**Confirmed sound:** deriving the advertised address from `public_url` (with the hostname
non-resolution caveat), and one mux socket serving N ICE agents (Pion demultiplexes by ufrag) —
provided the socket is gateway-owned, which it now is.

## Status of each tier

- **Tier 1 — IMPLEMENTED and committed.** Measured working on Fly (see Acceptance).
- **Tier 2 (ICE-TCP) — CODE IMPLEMENTED, DISABLED EVERYWHERE, 2026-08-17.** `SetICETCPMux` +
  `SetNetworkTypes(UDP4,UDP6,TCP4)` behind `tools.browser.webrtc_media_tcp_port` (0 = off, the
  default). It is **off in both Fly configs** because enabling it made things measurably worse,
  not better: Fly's TCP proxy resets the media connection (`could not proxy TCP data to/from
  instance … Connection reset by peer`) and a TCP media path head-of-line blocks scroll and
  click input. Do not re-enable on Fly without solving the proxy behaviour first.
  Covered by `pkg/tools/browser/webrtc/ice_tcp_test.go` (strict `a=candidate: … tcp …
  tcptype passive` assertion — an earlier substring check for `"tcp"` was vacuous because
  every Pion answer contains `rtcp`).
  Known gap while disabled: a failed ICE-TCP bind logs ERROR only and has no panel surface,
  unlike the UDP sibling's `mediaPortFallback` notice.

### Viewer-leg ICE posture (2026-08-17, wider blast radius than tier 2)

With a **declared public address** (`PublicIPs` non-empty) the viewer leg is now **ICE-Lite with
no STUN URL**: we listen and say where, the viewer checks toward us. This removes the
server-reflexive candidates a hosted box gathers but no viewer can reach — the Fly answer dump
carried `138.199.24.232:36534` and a 6PN address next to the real one, and Chrome burned its
attempt on them and never completed DTLS.

Two constraints this must keep:
- **One predicate for both settings.** ICE-Lite and skipping STUN are gated by the same
  `hostedViewerLeg()`. pion rejects a lite agent that is also handed an ICE URL
  (`ErrUselessUrlsProvided`), and `CreateOffer` then fails outright — no offer, no nameable
  failure. Guarded by `TestSession_TCPOnlyWithStun_StillNegotiates`.
- **Gated on the public address alone, never on a fixed media port.** A self-hoster who pins a
  port for a firewall forward but declares no public address still needs srflx (their NAT
  mapping is the only way in). Guarded by `TestSession_SelfHostedNoPublicIP_KeepsSrflx`.
  Network types are widened (not narrowed) only when a TCP mux exists; an earlier revision
  narrowed to UDP4-only, which silently removed every IPv6 host candidate.
- **Tier 3 (embedded TURN) — IMPLEMENTED 2026-08-19**, with all three blockers answered rather
  than deferred (`pkg/tools/browser/webrtc/turnserver.go`):
  - **Correction 1 (its own port).** `tools.browser.webrtc_turn_udp_port` (0 = off, the default),
    with an optional `webrtc_turn_tcp_port`. No pretence of sharing 443: there is still no TLS
    listener in this product, so the operator declares a port, exactly as for tier 1.
  - **Correction 2 (not an open relay).** The permission handler admits ONLY this gateway's own
    media address. `TestAllowedRelayPeer_AdmitsOnlyTheGatewayItself` proves loopback, cloud
    metadata (169.254.169.254), provider-internal ranges and the open internet are all refused —
    pion's `DefaultPermissionHandler` would have admitted every one of them.
  - **Credential lifecycle.** Short-lived TURN-REST credentials minted per viewer
    (`GenerateLongTermTURNRESTCredentials`), expiry encoded in the username so the server
    enforces it. pion/turn exposes no per-allocation teardown, so the honest guarantee is a
    bounded residual window (10 min), stated here and in the code rather than claimed as
    revocation.
  - **Wire contract.** `browser_webrtc_state.ice_servers` carries them to the SPA, which no
    longer relies solely on a hard-coded public STUN server (that can only ever help a client
    already able to hole-punch). STUN is kept alongside; ICE still prefers a direct path.
  - **Deployment caveat, measured:** on a provider whose TCP proxy is not a transparent byte
    pipe the TURN/TCP listener is subject to the same reset that disabled tier 2 on Fly. TURN/UDP
    on a declared port is the usable path there.

## Non-goals

- No coturn / managed TURN. Nothing that runs as a second service.
- Not making a client's VPN pass raw UDP — tier 3 serves that client over TLS instead.

## Acceptance

1. Fly UAT: live browser connects with no manual steps beyond the deployment recipe — measured.
2. Same client with the VPN extension ON: connects via tier 3 — DEFERRED with tier 3.
3. Local macOS: unchanged, still direct.
4. ICE failure yields a named-tier error, never a silent hang (ADR-061 discipline).
5. CI green; contract-first for every wire change; regression tests for each tier's selection.

## Verification of tier 1 (2026-08-15, measured end to end)

Both legs were driven over the public internet from a real Chromium on the
operator's Mac against the deployed UAT gateway (`uat-omnipus.fly.dev`), using
the gateway's own machine-local service token (`$OMNIPUS_HOME/cli.token`) for
the browser WebSocket's in-band auth frame — no human credential involved. The
probe speaks the same frame sequence the SPA panel does: `auth` →
`browser_attach` → `browser_tab_action{open}` → `browser_webrtc_offer` →
`browser_webrtc_answer`.

Result — the selected ICE candidate pair is the fixed-port host candidate this
ADR introduces:

| Measurement | Value |
|---|---|
| ICE connection state | `connected` (`new → checking → connected`) |
| Selected pair, remote | `109.105.222.208:50000`, **udp**, type **host** |
| Round-trip time | 216 ms |
| Inbound video | 20 754 bytes, 4 frames decoded, 12 fps (blank tab — low motion by design) |
| Audio track negotiated | yes (`has_audio: true`) |

The server also offered `srflx` candidates on ephemeral ports
(`216.246.119.120:58967`, `[2605:4c40:119:f110::1192]:55212`). Those are the
only candidates the pre-ADR-062 build could offer, and neither was selected —
Fly routes nothing to them. That is the failure this tier fixes, and the
candidate that won is the one the fix adds.

Regression note observed during the run: a stale capture session belonging to
another agent used to make the gateway answer `browser_webrtc_state{available:false,
reason:"error"}` with the real cause only in the gateway log
(`capture denied — another agent's capture session is actively viewed`,
ADR-048 condition 2). **FIXED 2026-08-16** (`05bccea6`): the wire reason is now
`multi_agent_capture_denied` (contract enum + SPA translator).

Local macOS re-test after UAT passed: `tests/e2e/browser-live-video.spec.ts`
green in 40.6 s with 143 ms measured end-to-end input latency — the direct path
is unaffected by the shared-socket change.

## Follow-up finding (2026-08-15): tier 1 connects, but the stream is not yet usable on a hosted install

With tier 1 in place the UAT install connects reliably, and the operator then
reported the remaining symptoms: sluggish scrolling and clicks that do not
work. Both were reproduced and measured. They are two independent defects, and
neither is a build difference — the deployed Linux binary was fingerprinted and
contains every fix, and its seeded `encoder.js` is byte-identical (md5
`1e76742dac52f729c84aba5e56969b77`) to the one in the repo.

### Finding 1 — clicks landed ~21% too high (FIXED)

Fixed in the commit that follows this ADR; see its message and
`pkg/tools/browser/live_viewport_basis_test.go` for the measured geometry.
Summary: the cached CSS layout viewport (633×543) did not describe the
captured surface (1266×1372 = 633×686 CSS), and input coordinates were mapped
through it anyway.

### Finding 2 — the encoder congestion-controls against the wrong link (OPEN)

The relay forwards only `PictureLossIndication`/`FullIntraRequest` from the
viewer leg to the encoder leg (`pkg/tools/browser/webrtc/viewer.go`,
`ingest.go`). No REMB, no transport-wide feedback, no bandwidth estimate. Chrome
therefore measures the **loopback** ingest hop — infinite bandwidth, zero loss —
and encodes for it, up to `maxBitrate` = 6 Mbps × deviceScaleFactor² = 24 Mbps
at a retina viewer's DPR 2. The gateway then relays that to a viewer whose real
path cannot carry it.

Measured from macOS to the `ams` UAT machine, viewer-side `getStats`:

| Measurement | Value |
|---|---|
| Packet loss | **27.6%** over the sampled window (452 received, 172 lost) |
| Jitter | 180–330 ms |
| Round-trip time | 166–223 ms |
| Delivered rate | 355–1021 kbps |
| Frame rate | 1–6 fps |
| Resolution | pinned at 1266×1372 throughout |
| Freezes | 7, totalling ~6 s |
| Scroll feedback latency | 202 ms – 3.7 s |
| Machine load | 1.5 on 4 cores (~37%) — **not** encoder starvation |

Resolution stays pinned because `encoder.js` sets `contentHint='detail'` with
`scaleResolutionDownBy=1` — deliberately, to fix a 2026-07-31 resolution
collapse — so frame rate is the only thing left that can give under pressure.
That trade is right on loopback and wrong on a real link.

**RESOLVED 2026-08-19** via the second option: the gateway now derives a target
from the viewer leg's own RTCP receiver reports and pushes it over the existing
`browser_capture_control` channel as a new `set_bitrate` action carrying
`max_bitrate` (contract-first; `pkg/tools/browser/webrtc/bitrate.go`).

- The drain that previously discarded everything except PLI/FIR now also reads
  `rtcp.ReceiverReport` and takes the WORST fraction-lost block in it.
- The policy is AIMD with hysteresis — multiplicative decrease above 5% loss,
  additive increase below 2%, hold in between — bounded by a floor (300 kbps)
  and by the encoder's own ceiling, and it is a pure function so it is tested
  exhaustively without a network (`bitrate_test.go`).
- Pushes are throttled (2s) and suppressed when the target has not actually
  moved, so a healthy link generates no control traffic at all.
- `applyVideoSenderConstraints` CLAMPS its locally-computed ceiling to the
  reported one; a guard test fails if the value is stored but not applied,
  which is the green-but-broken shape this finding was.

Not done: inverting `degradationPreference` on a constrained link. The encoder
already runs a bounded resolution-adaptation loop on its own CPU self-report,
and changing the preference from underneath it needs its own measurement.
