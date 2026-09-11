# Annotation coordinates and source lifetime

The screenshot crop remains in decoded video pixels. Optional element inspection now maps its center through the confirmed CSS aspect ratio, including encoder padding. A decoded 640×360 image of a 1280×720 page therefore inspects its center at CSS (640,360), rather than (320,180).

The component snapshots the presented capture identity, generation, RTP boundary, stream object and confirmed geometry before asynchronous canvas encoding. Upload and inspection completion recheck that original snapshot. Retired or unavailable proof suppresses optional element enrichment while preserving screenshot/comment delivery. This is client-observed retirement protection, not a new server-side atomic inspection contract.

## Focused evidence

- Red 24707: incorrect scaling/padding, retired identity and upload/inspection retirement assertions failed.
- Initial green 68861 exposed a test isolation error: unused one-shot inspection mocks leaked into later cases because clearAllMocks does not clear queued implementations. Corrected the new mocks without changing production behavior to accommodate them.
- Green 32170: 30 affected tests passed, including actual component drag → canvas boundary → popover → submit. The canvas mock verifies unchanged encoded crop coordinates; the real frame gate and mapper verify CSS coordinates and retirement during canvas encoding.
- Mutation driver 43323: replacing component CSS mapping with the raw crop center was caught; removing the post-inspection retirement check was caught. Restored selection passed all 30 tests, no skips (17.87 s). No Go or broad CI suite was run.
- Existing-symbol impacts were LOW for finalizeSelection, handleSendAnnotation, PendingAnnotation, SubmitAnnotationParams and submitAnnotation. The written-only controlledByOtherRef was unindexed; manual inspection found only its declaration/sync effect. Removed it and obsolete control-gating comments, preserving controlledByOther presentation state.

Full integrated frontend type/build validation and real-browser acceptance remain owned by the parent integration lane.
