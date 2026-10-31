// effort_red_scaffold_timing_seam.go — RED SCAFFOLDING for the effort-levels
// feature (train T7, branch landing/effort). Spec: effort-levels-spec-r10.md
// "Recap timing rules" + scenario S-97: "the timing values reach callCandidates
// as time.Duration from one place (the settings getters convert seconds and
// milliseconds there), so tests pass short durations through an unexported
// seam and assert received context deadlines, call order and elapsed time with
// a tolerance."
//
// GREEN implements the real seam inside session_end.go (callCandidates reads
// recap_timeout_seconds, recap_stream_retries and recap_retry_pause_ms from
// the settings getters). Until then this method delegates to today's
// implementation and ignores the injected values, so the S-97 timing
// assertions fail for the right reason (the injected time limit and retry
// schedule are not honoured).

package agent

import "time"

// callCandidatesWithTiming is the unexported test seam for S-97: it runs the
// recap candidate chain with an injected overall time limit, retry count and
// first-retry pause. The pause for retry n is n × retryPause.
func (rr *agentLoopRunRecap) callCandidatesWithTiming(timeLimit time.Duration, streamRetries int, retryPause time.Duration) bool {
	_ = timeLimit
	_ = streamRetries
	_ = retryPause
	return rr.callCandidates()
}
