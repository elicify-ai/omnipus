package tools

import (
	"bytes"
	"time"
)

// LastActivityAt returns the most recent real output or completion activity.
// It neither reads nor drains the process output buffer. An untouched session
// has a zero timestamp; merely starting or polling a job is not progress.
func (s *ProcessSession) LastActivityAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastActivityAt
}

// recordActivityLocked is called only while the existing session lock is held.
func (s *ProcessSession) recordActivityLocked() {
	s.lastActivityAt = time.Now().UTC()
}

// appendOutput is the common bounded stdout/stderr writer. Activity is observed
// even after the retained output reaches its cap; no additional buffer is kept.
func (s *ProcessSession) appendOutput(output []byte) {
	if len(output) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordActivityLocked()
	if s.outputBuffer == nil {
		s.outputBuffer = &bytes.Buffer{}
	}
	if s.outputBuffer.Len() >= maxOutputBufferSize {
		if !s.outputTruncated {
			s.outputBuffer.WriteString(outputTruncateMarker)
			s.outputTruncated = true
		}
		return
	}
	s.outputBuffer.Write(output)
}
