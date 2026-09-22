# QueryErrorState

The public `QueryErrorState` is pure presentation with `absolute` and `fill` layouts, an optional Retry action, and the established `testId` default. It does not read authentication state. The existing application adapter in `src/components/shared/QueryErrorState.tsx` exclusively owns forced-logout suppression and preserves the prior API for feature callers.

Forced-colors evidence targets the caller-selected `testId` and verifies the message as `CanvasText` against `Canvas`.
