# Frontend recovery review corrections

Branch: `browser-improvements`. Release base: `fbcbc5edc9845f1fbecb01b423f15d09fe405f5c`.
Independent frontend review at `f9f019de6` required these corrections.

- Back remains usable while presentation proof is unavailable. The protocol
  command is `navigate_back`; Forward is not a supported protocol action.
- Releasing Control/Command before a shortcut letter sends both releases.
- Video failure releases recorded keys/buttons through the surviving socket.
- Reason-bearing server failures retain the real media session's exponential
  retry delays and five-retry budget. Fresh starts retain known capture identity.
- Retry replaces a failed attachment connection before offering video; an
  established attachment retries media and clears the displayed failure.
- Temporary status/error notices overlay the picture without resizing it.
  Removed the written-only data-channel flag and contradictory control comments.

The real component/gate input fixture reproduced four failures. A separate
fixture composes the real component, socket client and media session, replacing
only browser WebSocket/PeerConnection APIs; it reproduced immediate reoffers,
failed attachment recovery and a persistent error after media Retry.

Focused closeout process 11848 exited 0: reverting the five behavior fixes caught
all seven selected cases; restoring source passed all 50 cases in both affected
test files, without skips. Logs and machine-readable results:
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/frontend-recovery/`.

Real Chromium measurement with compiled application CSS and the reconstructed
component flex boundary found old notice height changes of -43/-29/-29 pixels
at widths 320/560/900. The corrected layout changed zero pixels at every width.
Evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-runtime/evidence/notice-layout.json`.
This isolates the layout mechanism; complete-application layout, actual media,
sustained input, independent re-review, final typecheck/build and CI remain open.

Other accepted independent findings remain separately owned: annotation page
coordinates; encoder shutdown/adaptation lifetime and retired recapture policy;
retired gateway input-test routes and combined degradation-message length.
