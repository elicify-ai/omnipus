// Package cdppipe drives Chromium's DevTools Protocol (CDP) over an inherited
// OS pipe (Chromium's --remote-debugging-pipe mode) instead of a TCP debugging
// port, and adapts that pipe so chromedp's high-level actions keep working
// unchanged.
//
// # Why a pipe (the security property — EC-3 / CRIT-001)
//
// The default chromedp transport talks to Chrome over a websocket bound to a
// loopback TCP port (--remote-debugging-port), whose address is discoverable
// via the /json HTTP endpoint. On a shared host, ANY co-tenant process (or an
// agent-driven bash command) can connect to that port, drive CDP, and read
// whatever a page holds — including the per-stream ingest token that the
// live-browser-video-streaming feature injects into its encoder page. Landlock
// connect-rules only close that hole on Linux 6.7+; below that they silently
// degrade.
//
// --remote-debugging-pipe removes the surface entirely. Chrome speaks CDP only
// over two file descriptors it inherits at launch:
//
//	fd 3 — messages INTO the browser  (the browser READS commands here)
//	fd 4 — messages OUT of the browser (the browser WRITES events/results here)
//
// There is NO TCP port, NO /json HTTP endpoint, and NO ws:// URL anywhere.
// A co-tenant process does not inherit fd 3/4, so it cannot reach CDP on ANY
// kernel — which is exactly what makes the ingest token unrecoverable
// (spec §Gate 0 / EC-3, FR-013; ADR-044 §6.0.3).
//
// # Wire framing
//
// Each CDP message is a JSON document written verbatim and terminated by a
// single NUL byte (0x00). A raw NUL never appears inside well-formed JSON (the
// grammar requires U+0000 to be escaped as \u0000), so NUL is an unambiguous
// record separator. See frame.go.
//
// # How chromedp is fed a custom transport
//
// chromedp v0.15.1 cannot be handed a custom transport through its public API:
// chromedp.Browser.conn is unexported, chromedp.NewBrowser hardcodes a gobwas
// websocket dial, and there is no WithTransport / WithAllocator option. This
// package bridges that gap using ONLY public seams — no unsafe, no fork:
//
//  1. Chrome is launched here with --remote-debugging-pipe and fd 3/4 wired via
//     exec.Cmd.ExtraFiles. PipeConn (pipeconn.go) implements chromedp.Transport
//     directly over those fds (NUL-delimited JSON).
//  2. To satisfy chromedp.NewBrowser's insistence on dialing a ws:// URL, an
//     IN-MEMORY net.Pipe (no socket, no fd, no port — invisible to other
//     processes) is bridged to the Chrome pipe by a tiny gobwas websocket
//     server (allocator.go / dialer.go). chromedp dials a synthetic host that
//     the ws.DefaultDialer.NetDial hook resolves to that in-memory pipe. The
//     double framing lives entirely in-process and adds NO externally reachable
//     surface, preserving the no-TCP guarantee end to end.
//  3. The resulting *chromedp.Browser is bound onto a chromedp context via the
//     exported chromedp.FromContext(ctx).Browser field, so chromedp.Run and
//     chromedp.NewContext (child contexts multiplexed over the one pipe) work
//     exactly as with NewExecAllocator + NewContext.
//
// NewPipeAllocator is the entry point. Because chromedp did not spawn the
// process, *chromedp.Browser.Process() returns nil under this allocator; obtain
// the PID / crash handle from the *exec.Cmd captured via PipeOptions.ModifyCmd
// (the coordinator's existing chromedp.ModifyCmdFunc pattern). Crash detection
// still works through the standard chromedp.Browser.LostConnection channel,
// which closes when the pipe drops.
package cdppipe
