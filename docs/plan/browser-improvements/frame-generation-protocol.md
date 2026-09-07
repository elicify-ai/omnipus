# Displayed-frame identity protocol

Status: implementation design; not yet verified in a running application.

A page or CSS viewport transition invalidates input before the mutation begins. The server allocates a positive generation number, scoped to the capture session, and tells every viewer that the picture is transitioning. A generation is limited to JavaScript's exact integer range. Capture-session replacement invalidates all prior generation claims even if numbering restarts.

The capture offer carries the requested generation and the actual CDP target identity. Echoing a requested target is insufficient: the encoder must prove its Chrome tab selection corresponds to that target. Read-only `chrome.debugger.getTargets()` was verified on Chrome 151 with duplicate-URL tabs and a different foreground tab: capturing the requested red tab produced a red frame while the blue tab was foreground. The managed extension requires the debugger permission for this identity lookup, but never attaches another debugger. No URL-based matching or foreground guessing is acceptable.

The relay binds each ingest connection to immutable generation and target values. Target or CSS geometry changes create a fresh ingest connection. Track replacement on the same connection remains available only while target and geometry are unchanged. A callback publishes the first forwarded video timestamp for the new connection after retiring all old-source writes. That timestamp must be strictly after every old forwarded timestamp, including out-of-order input packets and timestamp wrap.

Viewers keep input locked until a video-frame callback identifies the current stream and a timestamp at or after the authoritative boundary, and that frame's expected display time has arrived. A late boundary message may validate an already presented cached frame. Generation, stream, or boundary changes invalidate earlier proof. A missing presentation timestamp never becomes an arbitrary timeout unlock.

For browsers without received RTP timestamps, the viewer requests a fresh connection for the confirmed generation after the boundary is committed. The server validates that generation during negotiation and includes it in the answer. Only an actual presented frame from that newly negotiated connection can authorize input. A new MediaStream wrapper around old tracks is not fresh lineage. A transition during negotiation invalidates the answer. Long-lived captures need a current boundary at attach because 32-bit timestamp comparisons become ambiguous across half the timestamp space.

Ordinary pointer and keyboard actions carry the displayed generation. The server validates it against the live target and geometry before dispatch. Stale actions receive an operation-level rejection without destroying a healthy picture. Cleanup releases use server-owned held-state records so rejecting stale input cannot leave a key or button pressed. Tab selection and navigation initiate transitions rather than claiming a new picture is already visible.

## Wire fields

- `capture_generation`: positive exact integer, maximum 9007199254740991, on browser input, capture control/offer, viewer offer/answer and video health.
- `target_id`: bounded CDP target identity on capture control/offer and video health; internal selection must verify it.
- `rtp_timestamp`: unsigned 32-bit boundary on video health, zero valid.
- `capture_id`: non-secret capture-session identity on input, video health and viewer negotiation; the pair of capture ID and generation scopes every input claim.
- `offer_id`: positive exact integer identifying one negotiation attempt, echoed in the corresponding answer. Both viewer and ingest negotiations require this even when the generation is unchanged.
- Video health adds `transitioning`; a committed boundary accompanies `recovered`.

Fields remain schema-optional to stage the protocol across existing constructors, but new capture sessions must enforce generation validation before accepting human input. Optional schema fields are not permission to bypass identity checks. Initial viewer negotiation may omit a generation because it starts the capture; the server must return the assigned generation before any input is enabled.

## Required evidence

Tests must cover stale input after switch and resize; same-size tabs with distinct visual fixtures; duplicate URLs and wrong foreground; delayed old RTP; sequence/timestamp wrap; delayed boundary messages on static pages; missing RTP and presentation metadata; fallback negotiation racing another transition; session replacement; and release cleanup across generations. Runtime acceptance additionally checks corner/edge clicks for padding introduced by capture alignment and measures resize-to-present latency against the two-second requirement.
