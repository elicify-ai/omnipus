package cdppipe

import "time"

// stderrDrainDelay bounds how long teardown's exec.Cmd.Wait keeps draining
// Chrome's stderr after the browser process has exited (exec.Cmd.WaitDelay).
//
// Without a bound, Wait returns only when EVERY holder of the stderr pipe's
// write end has closed it — and Chrome's helper processes inherit that pipe.
// A helper that outlives the browser (measured: a renderer re-parented to
// launchd on a loaded macOS host, still running more than 10 minutes after
// teardown began) therefore
// blocked teardown, and every caller of the allocator's CancelFunc with it,
// for as long as the helper lived. teardown also kills Chrome's process group
// (killProcessGroup), which normally closes the pipe at once; this bound is
// the backstop for a helper that escaped the group (e.g. a daemonized crash
// handler), so teardown returns in bounded time either way.
//
// Two seconds is ample for Chrome's last stderr lines to be forwarded — they
// are already written to the pipe by the time the process has exited.
const stderrDrainDelay = 2 * time.Second
