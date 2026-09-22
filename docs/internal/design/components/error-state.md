# ErrorState

`ErrorState` is the compact shared error presentation. It announces its message as an alert and can expose a Retry button. The caller owns error detection and retry behavior. The legacy `ListStates` export is a presentation-compatible adapter.

The danger color remains unchanged in normal mode. Forced-colors mode maps the message to `CanvasText` against `Canvas` so the alert text remains readable.
