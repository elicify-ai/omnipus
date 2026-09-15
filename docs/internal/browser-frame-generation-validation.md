# Capture identity validation — 7 September 2026

The server tracker and capture-session adapter implement the state rules in the browser improvement plan. They do not yet connect production target changes, viewer negotiation or input dispatch; that integration remains required.

The tracker tests first failed against an inert implementation. After implementation they passed for initial readiness, target/size/scale changes, stale generations, invalid geometry, timestamp wrap, ambiguous half-range timestamps and exact-integer exhaustion. Three deliberate faults were caught: bypassing readiness, reusing identity across resize, and accepting a stale generation. The restored baseline passed.

The capture-session tests first reproduced missing replacement identity and missing observer notifications. Their implementation then passed the focused tracker/session suite in 2.799 seconds. Removing capture-ID validation and allowing stopped captures each caused the intended test failure; the restored session suite passed in 2.090 seconds. The observer test verifies that callbacks can read session state while executing, with a bounded failure if a callback holds the session lock.

Tests use explicit expected generations and independent capture tokens. Timestamp zero is valid; invalid requests must leave the previous state unchanged. The capture ID is a digest of the per-capture random token, not an authentication credential. Input claims must match both capture identity and generation.

GitNexus reports low impact for the existing capture-session and live-input structures. New tracker/adapter methods are absent from its scan, so their current callers were inspected directly; they are test callers until production integration. Final integrated impact mapping, concurrency tests, independent review and actual browser acceptance remain pending.
