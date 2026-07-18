# cdppipe — CDP over `--remote-debugging-pipe` (no TCP surface)

Pure-Go transport that drives Chromium's DevTools Protocol over an **inherited
OS pipe** instead of a loopback TCP debugging port, while keeping `chromedp`'s
high-level actions working unchanged.

This is the security-critical heart of **EC-3 / CRIT-001** of the
live-browser-video-streaming epic
(`docs/internal/specs/live-browser-video-streaming-spec.md`, FR-013; ADR-044
§6.0.3).

## The fd 3 / fd 4 protocol

Chrome launched with the bare flag `--remote-debugging-pipe` speaks CDP over two
file descriptors it inherits at launch (wired here via `exec.Cmd.ExtraFiles`):

| fd | direction | who reads / writes |
|----|-----------|--------------------|
| **3** | messages **INTO** the browser | Chrome **reads** commands; the parent **writes** |
| **4** | messages **OUT of** the browser | Chrome **writes** events/results; the parent **reads** |

Each CDP message is a JSON document written verbatim and terminated by a single
**NUL byte (`\0`)**. A raw NUL never appears inside well-formed JSON (U+0000 must
be escaped as `\u0000`), so it is an unambiguous, injection-safe record
separator. `writeFrame` fails closed if a payload already contains a NUL.

## Why there is no TCP surface

The default `chromedp` transport uses `--remote-debugging-port`, a loopback TCP
port whose address leaks through the `/json` HTTP endpoint. Any co-tenant
process (or an agent-driven `bash`) can connect to it, drive CDP, and read a
page's secrets — including the per-stream **ingest token** injected into the
encoder page. Landlock connect-rules only close that hole on Linux 6.7+.

`--remote-debugging-pipe` removes the surface entirely: **no TCP port, no
`/json`, no `ws://`**. CDP is reachable only through fd 3/4, which a co-tenant
does not inherit, so the token is unrecoverable on **any** kernel.

The in-memory `net.Pipe` + gobwas websocket bridge used to satisfy `chromedp`'s
sealed transport (it can only be constructed by dialing a `ws://` URL) lives
entirely in-process — it has no file descriptor, no port, and is invisible to
other processes — so the no-TCP guarantee holds end to end. See `doc.go` for the
full extension-point rationale.

## Entry point

```go
ctx, cancel, err := cdppipe.NewPipeAllocator(context.Background(), execPath, cdppipe.PipeOptions{
    Args:      []string{ /* caller-owned: headful, --window-size, DISPLAY mode, ... */ },
    Env:       []string{ "DISPLAY=:99" },
    ModifyCmd: func(cmd *exec.Cmd) { /* capture *exec.Cmd for PID / crash handling */ },
})
// ctx works with chromedp.Run(ctx, ...) and chromedp.NewContext(ctx, ...) child tabs.
```

`chromedp.Browser.Process()` returns `nil` under this allocator (chromedp did not
spawn the process); get the PID/crash handle from the `*exec.Cmd` captured via
`ModifyCmd`. Crash detection still works through `chromedp.Browser.LostConnection`.

## Tests

- `TestPipeConn_FramingRoundTrip`, `TestPipeConn_ConcurrentRequests`,
  `TestFrame_RoundTripAndNULRejection` — framing, id-matching, event dispatch,
  concurrency; **no real Chrome** (`os.Pipe` fakes).
- `smoke_test.go` (`//go:build cdppipesmoke`) — real-Chrome end-to-end drive of
  the epic's CDP calls + a no-TCP-listener assertion. Excluded from normal CI.
