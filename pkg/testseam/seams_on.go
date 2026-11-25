// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build s1seams

package testseam

import (
	"io"
	"sync/atomic"
)

// ---- H43: egress-token mint entry -----------------------------------------

type mintObserver struct{ fn func(kind string) }

var mintHook atomic.Pointer[mintObserver]

// ObserveEgressTokenMints registers fn to be called, synchronously, at the
// entry of every egress-token mint (kind is EgressTokenOnce or
// EgressTokenSession). It is called before the random bytes are drawn, so a
// mint that later fails or whose token is discarded is still counted. fn never
// receives token bytes. restore puts back the previous registration; call it
// in t.Cleanup.
func ObserveEgressTokenMints(fn func(kind string)) (restore func()) {
	prev := mintHook.Swap(&mintObserver{fn: fn})
	return func() { mintHook.Store(prev) }
}

// EgressTokenMinted marks the entry of an egress-token mint.
func EgressTokenMinted(kind string) {
	if o := mintHook.Load(); o != nil && o.fn != nil {
		o.fn(kind)
	}
}

// ---- H44: foreground capture cap and input observer -----------------------

type captureConfig struct {
	capBytes int
	observe  func(stream string, p []byte)
}

var captureHook atomic.Pointer[captureConfig]

// SetForegroundCapture changes the foreground command's output capture
// (sandbox.runOnCurrentThread, the real cappedBuffer for "stdout" and
// "stderr"). capBytes > 0 replaces the 4 MiB cap; capBytes <= 0 keeps it.
// observe, when non-nil, is called synchronously with a copy of every byte
// slice written into the capture buffer, before the cap truncates it: these
// are the bytes after programs.Attach's masking writer. The setting is read
// when a command starts. restore puts back the previous setting.
func SetForegroundCapture(capBytes int, observe func(stream string, p []byte)) (restore func()) {
	prev := captureHook.Swap(&captureConfig{capBytes: capBytes, observe: observe})
	return func() { captureHook.Store(prev) }
}

// CaptureCap returns the cap for one foreground stream.
func CaptureCap(_ string, capBytes int) int {
	if c := captureHook.Load(); c != nil && c.capBytes > 0 {
		return c.capBytes
	}
	return capBytes
}

// CaptureWriter wraps one foreground stream's capture buffer with the
// registered observer, if any.
func CaptureWriter(stream string, w io.Writer) io.Writer {
	c := captureHook.Load()
	if c == nil || c.observe == nil {
		return w
	}
	return &observedWriter{stream: stream, w: w, observe: c.observe}
}

type observedWriter struct {
	stream  string
	w       io.Writer
	observe func(stream string, p []byte)
}

func (o *observedWriter) Write(p []byte) (int, error) {
	o.observe(o.stream, append([]byte(nil), p...))
	return o.w.Write(p)
}

// ---- H45: background pipe read fault --------------------------------------

type pipeFault struct {
	fn func(sessionID, stream string, readSoFar int64) error
}

var pipeFaultHook atomic.Pointer[pipeFault]

// SetBackgroundPipeReadFault registers fn on the read end of every background
// command's output pipe (tools.ExecTool.runBackground), for pipes created
// while it is registered. A background child's stdout and stderr share ONE
// merged pipe (so the child's write order is kept), which carries the stream
// label "stdout". After every read that returned bytes, fn is called with the
// session id, that stream label and the total bytes read so far on the pipe.
// A non-nil error is a read fault: the
// bytes of that read are still delivered, the read returns the error (so the
// product's read loop sees a non-EOF error), and the pipe's read end is closed
// with that error, so the child's further writes fail instead of blocking.
// restore puts back the previous registration.
func SetBackgroundPipeReadFault(fn func(sessionID, stream string, readSoFar int64) error) (restore func()) {
	prev := pipeFaultHook.Swap(&pipeFault{fn: fn})
	return func() { pipeFaultHook.Store(prev) }
}

// BackgroundPipeReader wraps one background pipe's read end with the
// registered fault hook, if any.
func BackgroundPipeReader(sessionID, stream string, r *io.PipeReader) io.Reader {
	f := pipeFaultHook.Load()
	if f == nil || f.fn == nil {
		return r
	}
	return &faultingReader{sessionID: sessionID, stream: stream, r: r, fn: f.fn}
}

type faultingReader struct {
	sessionID string
	stream    string
	r         *io.PipeReader
	fn        func(sessionID, stream string, readSoFar int64) error
	read      int64
}

func (f *faultingReader) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	if n > 0 {
		f.read += int64(n)
		if ferr := f.fn(f.sessionID, f.stream, f.read); ferr != nil {
			_ = f.r.CloseWithError(ferr) // io.PipeReader.CloseWithError always returns nil
			return n, ferr
		}
	}
	return n, err
}

// ---- H42: program account-check launch barrier ----------------------------

type checkLaunchHook struct {
	fn func(entryID string, generation int64)
}

var checkLaunchBarrier atomic.Pointer[checkLaunchHook]

// SetBeforeCheckLaunch registers fn at the start of every program
// account-check launch goroutine (programs.Service.launchLocked): after the
// reservation is recorded and the service lock is released, before any store
// read and before the check process can start. fn runs on the launch
// goroutine, so blocking in it pauses only that launch; another paste can
// commit, reserve and launch meanwhile. entryID is the program entry id and
// generation the connection generation that launch was reserved for. restore
// puts back the previous registration.
func SetBeforeCheckLaunch(fn func(entryID string, generation int64)) (restore func()) {
	prev := checkLaunchBarrier.Swap(&checkLaunchHook{fn: fn})
	return func() { checkLaunchBarrier.Store(prev) }
}

// BeforeCheckLaunch calls the registered launch barrier, if any.
func BeforeCheckLaunch(entryID string, generation int64) {
	if h := checkLaunchBarrier.Load(); h != nil && h.fn != nil {
		h.fn(entryID, generation)
	}
}
