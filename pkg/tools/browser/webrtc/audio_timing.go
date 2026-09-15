package webrtc

import (
	"log/slog"
	"os"
	"sync"
	"time"
)

const audioTimingEnv = "OMNIPUS_BROWSER_AUDIO_TIMING"

type audioTimingSummary struct {
	Packets, Intervals, Reports, Reordered                               uint64
	ReadCompletionGapTotal, ReadCompletionGapMax, SourceTotal, SourceMax time.Duration
	RTPLockTotal, RTPLockMax, RTPWriteTotal, RTPWriteMax                 time.Duration
	RTCPLockTotal, RTCPLockMax, RTCPWriteTotal, RTCPWriteMax             time.Duration
}

type audioTiming struct {
	mu                sync.Mutex
	emit              func(audioTimingSummary)
	summary           audioTimingSummary
	started, lastRead time.Time
	lastTimestamp     uint32
	haveTimestamp     bool
}

func newAudioTiming(emit func(audioTimingSummary)) *audioTiming {
	if os.Getenv(audioTimingEnv) != "1" {
		return nil
	}
	return &audioTiming{emit: emit}
}

func addAudioTiming(total, maximum *time.Duration, value time.Duration) {
	if value < 0 {
		return
	}
	*total += value
	if value > *maximum {
		*maximum = value
	}
}

// Both record methods run after the media writer unlocks. The emitter also
// runs outside this aggregate's lock; it must never contain media payloads.
func (d *audioTiming) recordRTP(at time.Time, timestamp uint32, rate uint32, lockWait, writeDuration time.Duration) {
	if d == nil {
		return
	}
	d.mu.Lock()
	if d.started.IsZero() {
		d.started = at
	}
	s := &d.summary
	s.Packets++
	if !d.lastRead.IsZero() {
		s.Intervals++
		addAudioTiming(&s.ReadCompletionGapTotal, &s.ReadCompletionGapMax, at.Sub(d.lastRead))
	}
	d.lastRead = at
	if d.haveTimestamp {
		delta := timestamp - d.lastTimestamp
		if delta >= 0x80000000 {
			s.Reordered++
		} else {
			if rate > 0 {
				addAudioTiming(&s.SourceTotal, &s.SourceMax, time.Duration(uint64(delta)*uint64(time.Second)/uint64(rate)))
			}
			d.lastTimestamp = timestamp
		}
	} else {
		d.lastTimestamp = timestamp
		d.haveTimestamp = true
	}
	addAudioTiming(&s.RTPLockTotal, &s.RTPLockMax, lockWait)
	addAudioTiming(&s.RTPWriteTotal, &s.RTPWriteMax, writeDuration)
	result, emit := d.flushLocked(at)
	d.mu.Unlock()
	if emit && d.emit != nil {
		d.emit(result)
	}
}

func (d *audioTiming) recordRTCP(at time.Time, lockWait, writeDuration time.Duration) {
	if d == nil {
		return
	}
	d.mu.Lock()
	if d.started.IsZero() {
		d.started = at
	}
	s := &d.summary
	s.Reports++
	addAudioTiming(&s.RTCPLockTotal, &s.RTCPLockMax, lockWait)
	addAudioTiming(&s.RTCPWriteTotal, &s.RTCPWriteMax, writeDuration)
	result, emit := d.flushLocked(at)
	d.mu.Unlock()
	if emit && d.emit != nil {
		d.emit(result)
	}
}

func (d *audioTiming) flushLocked(at time.Time) (audioTimingSummary, bool) {
	if at.Sub(d.started) < 5*time.Second {
		return audioTimingSummary{}, false
	}
	result := d.summary
	d.summary = audioTimingSummary{}
	d.started = at
	return result, true
}

// No periodic goroutine: completed operations emit at most once per five
// seconds per feed. A permanently blocked writer cannot emit its own duration.
func logAudioTiming(feed int64, rate uint32, s audioTimingSummary) {
	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
	slog.Info("browser audio timing", "feed", feed, "clock_rate", rate,
		"packets", s.Packets, "read_intervals", s.Intervals, "reports", s.Reports, "reordered", s.Reordered,
		"read_completion_gap_total_ms", ms(s.ReadCompletionGapTotal), "read_completion_gap_max_ms", ms(s.ReadCompletionGapMax),
		"source_delta_total_ms", ms(s.SourceTotal), "source_delta_max_ms", ms(s.SourceMax),
		"rtp_lock_total_ms", ms(s.RTPLockTotal), "rtp_lock_max_ms", ms(s.RTPLockMax),
		"rtp_write_total_ms", ms(s.RTPWriteTotal), "rtp_write_max_ms", ms(s.RTPWriteMax),
		"rtcp_lock_total_ms", ms(s.RTCPLockTotal), "rtcp_lock_max_ms", ms(s.RTCPLockMax),
		"rtcp_fanout_total_ms", ms(s.RTCPWriteTotal), "rtcp_fanout_max_ms", ms(s.RTCPWriteMax))
}
