package webrtc

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pion/logging"
	"github.com/pion/webrtc/v4"
)

// ---------------------------------------------------------------------------
// Why this file exists.
//
// On 2026-09-05 a CI run (33943602552) left the live panel permanently black.
// The gateway's ENTIRE record of the incident was three lines: five "PLI send
// failed" symptoms, "[ingest-15] ICE connection state -> failed", and the
// eviction that followed. Nothing said which candidates either side offered,
// how long gathering took, which pair (if any) was ever selected, or what
// pion's own ICE agent thought was going wrong -- so the incident could not be
// diagnosed after the fact, only speculated about.
//
// Two independent blind spots produced that:
//
//  1. This package logged STATE NAMES and nothing else. A state name tells you
//     that a connection failed, never why. The failure modes it has to
//     distinguish -- "the box was too slow to finish checks in time", "Chrome
//     obfuscated its host candidates as mDNS .local names this container
//     cannot resolve", "the public STUN server was unreachable and gathering
//     burned its whole budget on it" -- all render identically as "-> failed".
//
//  2. Pion's OWN diagnostics were switched off. pion/logging's default factory
//     is LogLevelError (logging.NewDefaultLoggerFactory), and every message
//     that would have identified cause (2) above is logged at WARN:
//     "Failed to discover mDNS candidate %s: %v" (pion/ice agent.go,
//     resolveAndAddMulticastCandidate). It fired, or did not fire, entirely
//     invisibly.
//
// Both are closed here, and deliberately on the SUCCESS path too: a failing
// run's candidate set means nothing without a healthy run's candidate set from
// the same machine to compare it against.
// ---------------------------------------------------------------------------

// pionLogEnv names the environment variable that sets how much of pion's own
// internal logging (ICE agent, DTLS, SCTP, mux) is forwarded into the
// gateway's log through the bridge below.
//
// Accepted values: disable, error, warn (default), info, debug, trace.
//
// The default is WARN rather than pion's own ERROR default because the single
// most diagnostic line pion emits about a loopback ICE failure -- the mDNS
// resolution warning quoted in this file's header -- is a Warn, and losing it
// is what made the CI incident undiagnosable. Warn is quiet on a healthy
// connection (a full local capture start emits none), so this costs nothing
// in the ordinary case. info/debug/trace are for a live investigation only:
// debug is roughly a line per connectivity check.
const pionLogEnv = "OMNIPUS_WEBRTC_PION_LOG"

// pionLogLevel resolves pionLogEnv into a pion log level, defaulting to Warn.
// An unrecognised value is treated as the default rather than an error: this
// is a diagnostic knob, and refusing to start a capture because someone typed
// "verbose" would be a worse outcome than logging slightly less than they
// hoped.
func pionLogLevel() logging.LogLevel {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(pionLogEnv))) {
	case "disable", "disabled", "off", "none":
		return logging.LogLevelDisabled
	case "error":
		return logging.LogLevelError
	case "info":
		return logging.LogLevelInfo
	case "debug":
		return logging.LogLevelDebug
	case "trace":
		return logging.LogLevelTrace
	default:
		return logging.LogLevelWarn
	}
}

// pionLogBridge is a pion logging.LoggerFactory that forwards pion's internal
// messages into this Session's own logf, so they land in gateway.log next to
// the [ingest-N]/[viewer-N] lines they explain instead of on a stderr nobody
// is reading.
//
// Level filtering is done HERE rather than by handing pion its own
// DefaultLoggerFactory with a level set, because that factory writes to
// os.Stderr through the standard log package and cannot be redirected into a
// per-Session sink.
type pionLogBridge struct {
	level logging.LogLevel
	logf  func(string, ...any)
}

// NewLogger implements logging.LoggerFactory.
func (b *pionLogBridge) NewLogger(scope string) logging.LeveledLogger {
	return &pionScopedLogger{bridge: b, scope: scope}
}

// pionScopedLogger is one pion subsystem's ("ice", "dtls", "sctp", …) view of
// the bridge.
type pionScopedLogger struct {
	bridge *pionLogBridge
	scope  string
}

func (l *pionScopedLogger) emitf(level logging.LogLevel, levelName, format string, args ...any) {
	if l.bridge.level < level || l.bridge.level == logging.LogLevelDisabled {
		return
	}
	l.bridge.logf("pion/%s %s: %s", l.scope, levelName, fmt.Sprintf(format, args...))
}

func (l *pionScopedLogger) Trace(msg string) { l.emitf(logging.LogLevelTrace, "TRACE", "%s", msg) }
func (l *pionScopedLogger) Tracef(format string, args ...any) {
	l.emitf(logging.LogLevelTrace, "TRACE", format, args...)
}
func (l *pionScopedLogger) Debug(msg string) { l.emitf(logging.LogLevelDebug, "DEBUG", "%s", msg) }
func (l *pionScopedLogger) Debugf(format string, args ...any) {
	l.emitf(logging.LogLevelDebug, "DEBUG", format, args...)
}
func (l *pionScopedLogger) Info(msg string) { l.emitf(logging.LogLevelInfo, "INFO", "%s", msg) }
func (l *pionScopedLogger) Infof(format string, args ...any) {
	l.emitf(logging.LogLevelInfo, "INFO", format, args...)
}
func (l *pionScopedLogger) Warn(msg string) { l.emitf(logging.LogLevelWarn, "WARN", "%s", msg) }
func (l *pionScopedLogger) Warnf(format string, args ...any) {
	l.emitf(logging.LogLevelWarn, "WARN", format, args...)
}
func (l *pionScopedLogger) Error(msg string) { l.emitf(logging.LogLevelError, "ERROR", "%s", msg) }
func (l *pionScopedLogger) Errorf(format string, args ...any) {
	l.emitf(logging.LogLevelError, "ERROR", format, args...)
}

// ---------------------------------------------------------------------------
// Per-connection ICE diagnostics.
// ---------------------------------------------------------------------------

// iceDiag records, for ONE PeerConnection, everything needed to tell a healthy
// startup apart from a failed one after the fact: every local candidate with
// the time it was gathered, the gathering duration, every ICE/peer-connection
// state transition with its offset from the offer, the remote candidate set,
// and the pair that was ultimately selected.
//
// All timings are relative to the moment the offer was received, which is the
// only clock a reader of gateway.log can correlate across the two legs.
//
// It is safe for concurrent use: pion delivers OnICECandidate from the
// gatherer's goroutine and the state callbacks from the agent's, and both can
// overlap with the signaling goroutine calling summariseAt.
type iceDiag struct {
	prefix string // "[ingest-15]" — matches this package's existing log prefixes
	leg    string // "ingest" | "viewer"
	logf   func(string, ...any)
	start  time.Time

	mu             sync.Mutex
	localCands     []string
	gatherComplete time.Time
	transitions    []string
	pairLogged     bool
	dumped         bool
}

func newICEDiag(prefix, leg string, logf func(string, ...any)) *iceDiag {
	return &iceDiag{prefix: prefix, leg: leg, logf: logf, start: time.Now()}
}

// since renders the offset from the offer with the resolution that matters
// here (milliseconds): a 30ms host-candidate gather and a 5,020ms one are the
// difference between two entirely different diagnoses, and both round to "0s".
func (d *iceDiag) since() string {
	return fmt.Sprintf("+%dms", time.Since(d.start).Milliseconds())
}

// noteLocalCandidate is the OnICECandidate handler. Pion calls it once per
// gathered candidate and once with nil to signal the end of gathering.
//
// Each candidate is logged INDIVIDUALLY, with its offset, because the offsets
// are the measurement: a srflx candidate that appears 5,000ms after the host
// candidates is a STUN round trip that timed out, and no aggregate ("gathering
// took 5s") distinguishes that from a slow machine.
func (d *iceDiag) noteLocalCandidate(c *webrtc.ICECandidate) {
	if c == nil {
		d.mu.Lock()
		d.gatherComplete = time.Now()
		n := len(d.localCands)
		d.mu.Unlock()
		d.logf("%s ice-diag %s local gathering complete %s (%d candidates)", d.prefix, d.leg, d.since(), n)
		return
	}
	line := c.ToJSON().Candidate
	d.mu.Lock()
	d.localCands = append(d.localCands, line)
	d.mu.Unlock()
	d.logf("%s ice-diag %s local candidate %s: %s", d.prefix, d.leg, d.since(), line)
}

// noteGatheringState logs ICE gathering state transitions (new -> gathering ->
// complete) with their offsets.
func (d *iceDiag) noteGatheringState(st webrtc.ICEGatheringState) {
	d.logf("%s ice-diag %s gathering state -> %s %s", d.prefix, d.leg, st.String(), d.since())
}

// noteICEState records an ICE connection state transition and, at the two
// points where the outcome is decided, dumps the evidence:
//
//   - connected/completed: the SELECTED PAIR plus both candidate sets. Logged
//     on success deliberately. A failure's candidate set is uninterpretable
//     without a success's candidate set from the same machine to compare it
//     against -- that comparison is the whole diagnostic method here, and it
//     is impossible if only failures are recorded.
//   - failed: the same dump, so the two are directly comparable line for line.
func (d *iceDiag) noteICEState(st webrtc.ICEConnectionState, pc *webrtc.PeerConnection) {
	d.mu.Lock()
	d.transitions = append(d.transitions, fmt.Sprintf("%s@%s", st.String(), d.since()))
	d.mu.Unlock()

	switch st {
	case webrtc.ICEConnectionStateConnected, webrtc.ICEConnectionStateCompleted:
		d.logSelectedPair(pc)
		d.dumpCandidates(pc, "connected")
	case webrtc.ICEConnectionStateFailed:
		d.dumpCandidates(pc, "failed")
	}
}

// logSelectedPair reports the candidate pair ICE actually nominated. Logged
// once per connection (a completed->connected flap must not repeat it).
//
// The pair is the single most compressed statement of what happened: a
// host/host pair over loopback is the intended path; a srflx or prflx pair on
// the LOOPBACK ingest leg would mean the host pair was unusable and the
// connection only survived by accident, which is a defect even when it works.
func (d *iceDiag) logSelectedPair(pc *webrtc.PeerConnection) {
	d.mu.Lock()
	if d.pairLogged {
		d.mu.Unlock()
		return
	}
	d.pairLogged = true
	d.mu.Unlock()

	sctp := pc.SCTP()
	if sctp == nil {
		return
	}
	transport := sctp.Transport()
	if transport == nil {
		return
	}
	ice := transport.ICETransport()
	if ice == nil {
		return
	}
	pair, err := ice.GetSelectedCandidatePair()
	if err != nil {
		d.logf("%s ice-diag %s selected pair unavailable %s: %v", d.prefix, d.leg, d.since(), err)
		return
	}
	if pair == nil {
		d.logf("%s ice-diag %s selected pair unavailable %s: none reported", d.prefix, d.leg, d.since())
		return
	}
	d.logf("%s ice-diag %s selected pair %s: local=%s:%d/%s remote=%s:%d/%s",
		d.prefix, d.leg, d.since(),
		pair.Local.Address, pair.Local.Port, pair.Local.Typ.String(),
		pair.Remote.Address, pair.Remote.Port, pair.Remote.Typ.String())
}

// dumpCandidates writes the full candidate strings both sides put on the wire,
// plus the transition timeline and the gathering duration. Emitted at most
// once per connection (outcome is terminal in both directions this is called
// from).
//
// The FULL strings, not a summary: the decisive fact about the mDNS hypothesis
// is whether the connection-address field of a remote host candidate is an IP
// or a "<uuid>.local" name, and no count can carry that.
func (d *iceDiag) dumpCandidates(pc *webrtc.PeerConnection, outcome string) {
	d.mu.Lock()
	if d.dumped {
		d.mu.Unlock()
		return
	}
	d.dumped = true
	gather := d.gatherComplete
	transitions := strings.Join(d.transitions, " ")
	d.mu.Unlock()

	gatherMS := int64(-1)
	if !gather.IsZero() {
		gatherMS = gather.Sub(d.start).Milliseconds()
	}
	d.logf("%s ice-diag %s outcome=%s %s gather_ms=%d transitions=[%s]",
		d.prefix, d.leg, outcome, d.since(), gatherMS, transitions)

	localSDP := descriptionSDP(pc.LocalDescription())
	remoteSDP := descriptionSDP(pc.RemoteDescription())
	d.logf("%s ice-diag %s outcome=%s local summary: %s", d.prefix, d.leg, outcome, describeSDPCandidates(localSDP))
	for _, line := range sdpCandidateLines(localSDP) {
		d.logf("%s ice-diag %s outcome=%s local: %s", d.prefix, d.leg, outcome, line)
	}
	d.logf("%s ice-diag %s outcome=%s remote summary: %s", d.prefix, d.leg, outcome, describeSDPCandidates(remoteSDP))
	for _, line := range sdpCandidateLines(remoteSDP) {
		d.logf("%s ice-diag %s outcome=%s remote: %s", d.prefix, d.leg, outcome, line)
	}
}

// noteRemoteOffer logs what the far side offered, at the moment it is applied
// -- BEFORE any ICE outcome is known.
//
// This is the one dump that must not be conditional on the outcome. The
// question "did Chrome send .local host candidates on this run?" has to be
// answerable for runs that SUCCEEDED as well, because the failure is
// intermittent: if the same Chrome sends .local names on every run and only
// some runs fail, mDNS obfuscation cannot be the whole cause, and that is a
// conclusion only a success-path log can support.
func (d *iceDiag) noteRemoteOffer(sdp string) {
	d.logf("%s ice-diag %s remote offer summary: %s", d.prefix, d.leg, describeSDPCandidates(sdp))
	for _, line := range sdpCandidateLines(sdp) {
		d.logf("%s ice-diag %s remote offer: %s", d.prefix, d.leg, line)
	}
}

// sdpCandidateLines returns the full "candidate:..." attribute value of every
// a=candidate line in an SDP, in wire order.
//
// Returns nil (not an error) for an SDP with no candidates -- including the
// empty-string case a nil SessionDescription produces -- because "the far side
// offered nothing" is a legitimate, and highly diagnostic, observation rather
// than a failure to parse.
func sdpCandidateLines(sdp string) []string {
	var out []string
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimSpace(line)
		attr, ok := strings.CutPrefix(line, "a=candidate:")
		if !ok {
			continue
		}
		out = append(out, "candidate:"+attr)
	}
	return out
}

// ErrOfferHasNoUsableCandidates is returned by HandleIngestOffer for an offer
// that carries no ICE candidate the agent could ever check against.
//
// A sentinel rather than a bare fmt.Errorf so the gateway (and tests) can
// recognise this specific, fully-determined-at-arrival condition and say
// something true about it, instead of reporting the generic negotiation
// failure that every other malformed offer produces.
var ErrOfferHasNoUsableCandidates = errors.New(
	"the encoder offered no usable ICE candidate — its Chrome found no non-loopback network " +
		"interface to gather one from (and no STUN server it could reach); this connection could " +
		"never have been established, so it is refused now rather than after ICE's 30s timeout")

// usableRemoteCandidateCount counts the candidates in an SDP that this agent
// could actually mount a connectivity check against.
//
// "Usable" excludes TCP candidates whose tcptype is `active`, and that
// exclusion is not a judgement call -- it mirrors pion/ice's own behaviour
// exactly (agent.go's addRemoteCandidate: "Ignoring remote candidate with
// tcpType active", because an active candidate only ever DIALS, it never
// listens, so pairing one with our own candidate is meaningless). Chrome sends
// one alongside its UDP candidates on every offer, so counting raw a=candidate
// lines would report an offer as usable on the strength of the one candidate
// pion is guaranteed to throw away.
func usableRemoteCandidateCount(sdp string) int {
	n := 0
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimSpace(line)
		attr, ok := strings.CutPrefix(line, "a=candidate:")
		if !ok {
			continue
		}
		fields := strings.Fields(attr)
		// RFC 5245 §15.1: <foundation> <component> <transport> <priority>
		// <connection-address> <port> typ <type> [extensions...]
		if len(fields) < 8 || fields[6] != "typ" {
			continue
		}
		if strings.EqualFold(fields[2], "tcp") && hasTCPTypeActive(fields[8:]) {
			continue
		}
		n++
	}
	return n
}

// hasTCPTypeActive reports whether an SDP candidate's trailing extension
// attributes (name/value pairs) declare tcptype active.
func hasTCPTypeActive(ext []string) bool {
	for i := 0; i+1 < len(ext); i += 2 {
		if strings.EqualFold(ext[i], "tcptype") && strings.EqualFold(ext[i+1], "active") {
			return true
		}
	}
	return false
}
