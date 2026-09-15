# Pending capture geometry admission

Status: implemented; focused green, mutation, and race verification complete.

FR-009 and the picture-to-input protocol require a layout to be known before
its video can authorize coordinates. A tab change may reserve a generation
with dimensions 0/0 while measuring the new target, but a packet from that
generation must not commit it or authorize input. A later measured layout
must receive a newer generation and may commit normally. The minimum valid
measured dimensions are 1/1 CSS pixels.

The test uses the real frame tracker without mocks. It checks rejection and
unchanged pending state at 0/0, then exact generation advancement and successful
boundary/input authorization at 1/1. Mutations removed the unknown-size
guard, rejected the valid minimum, and reported rejection as acceptance. Existing
timestamp wrap tests must use measured dimensions while retaining their exact
timestamp assertions. Live tab/viewport transition integration remains separate.

Run 9268 exited 1 in 1.660 seconds: the unmeasured layout committed its packet
boundary, authorized input, and changed pending state to Ready. The patch rejects
this boundary; checking width is sufficient because begin enforces paired zero
or positive width/height. The timestamp-wrap fixture now provides measured
800x600 geometry; all timestamp expectations are preserved.

GitNexus impact for the new tracker commit method is UNKNOWN/not indexed. Manual
production scope is CaptureSession.CommitFrameBoundary and the relay's video
boundary callback. No other production tracker commit caller exists. The required
full release review and runtime transition acceptance remain outstanding.

Driver 29375 exited 0: the focused `TestCaptureFrame*` selection initially
passed in 1.713 seconds; all three mutations failed the intended assertions;
restored tests passed in 1.535 seconds, followed by race detection and shuffled
execution in 2.849 seconds. No mutated source remained during the final runs.
This covers the frame tracker and adapters, not actual tab/viewport integration
or live browser acceptance.

Staged GitNexus check 14825 exited 0 with no indexed changes; the new tracker
is absent from its graph. Manual staged inspection confirms the single boundary
guard, measured timestamp fixture, new regression, and this evidence file.
