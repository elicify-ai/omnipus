package sandbox

import "bytes"

// ProgramTestCapture exposes only the real cappedBuffer to the external test
// package. This is a test-only instrument, not a production capture replacement.
type ProgramTestCapture struct {
	cap  cappedBuffer
	seen bytes.Buffer
}

func NewProgramTestCapture(limit int) *ProgramTestCapture {
	return &ProgramTestCapture{cap: cappedBuffer{cap: limit}}
}

func (c *ProgramTestCapture) Write(p []byte) (int, error) {
	_, _ = c.seen.Write(p)
	return c.cap.Write(p)
}

func (c *ProgramTestCapture) Entered() string  { return c.seen.String() }
func (c *ProgramTestCapture) Captured() string { return string(c.cap.Bytes()) }
