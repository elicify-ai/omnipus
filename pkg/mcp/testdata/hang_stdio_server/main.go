// hang_stdio_server is a stdio process that spawns successfully but never
// speaks the MCP protocol: it drains and discards stdin, writes nothing to
// stdout, and only exits once stdin reaches EOF (or it is signaled).
//
// It exists to exercise ConnectServer's handshake-timeout / no-leak path
// without depending on a shell "hang forever" one-liner: shells commonly
// tail-call-exec a script's final simple command (replacing their own argv
// with the child command's), which would silently drop any identifying
// marker passed on the command line and make external process-table
// verification (pgrep -f) unreliable. A dedicated binary keeps its own argv
// (here, just its own unique build path under a per-test t.TempDir()) for
// its entire lifetime, which is what pkg/mcp's leak-regression test polls
// for with pgrep.
//
// Build:
//
//	go build -o hang_stdio_server ./pkg/mcp/testdata/hang_stdio_server/
//
// The test builds it on demand via exec.Command("go", "build", ...).
//
// License: MIT — Copyright (c) 2026 Omnipus contributors
package main

import (
	"io"
	"os"
)

func main() {
	// Block until the client closes its side of the pipe (EOF), producing
	// zero stdout output the whole time — no MCP handshake response is ever
	// sent, so any caller waiting on the handshake blocks until its own ctx
	// deadline fires.
	_, _ = io.Copy(io.Discard, os.Stdin)
}
