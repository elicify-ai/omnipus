# Collection State

`CollectionState` is caller-controlled across `initial-loading`, `refreshing`, `empty`, `partial`, `ready`, and `error`. Initial loading and refreshing mount the supplied placeholder immediately to reserve layout, then expose it after the shared 400 ms delay. Once exposed, loading content remains visible for at least 300 ms even when the caller moves to a completed state — any non-loading terminal presentation (`empty`, `partial`, `ready`, or `error`). A quick operation that finishes before 400 ms never exposes its loading content.

Cached children remain visible throughout refreshing. Before the delay, controls inside the supplied loading placeholder are inert and hidden from the accessibility tree; they become accessible when that loading region becomes visible. Cached children remain available during refresh. `aria-busy` follows only the caller’s current loading state, while the visual dwell may finish afterward. Callers retain ownership of collection data, actions, and retries.

A persistent polite status region is a sibling of the busy content. It announces the caller-controlled lifecycle state without inheriting `aria-busy`, changing layout, or repeating caller-provided loading, error, or action content.
