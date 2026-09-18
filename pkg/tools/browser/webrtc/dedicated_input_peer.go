package webrtc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	pion "github.com/pion/webrtc/v4"
)

var dedicatedPreparation ingestPreparationPool

// DedicatedInputPeer owns a data-only connection. Its channel/source lifetime is
// independent of media capture. Closed joins dispatch and native cleanup, so replacement can
// bound retiring resources without blocking the signaling reader.
type DedicatedInputPeer struct {
	ctx             context.Context
	cancel          context.CancelFunc
	queue           *dedicatedInputQueue
	validate        func([]byte) error
	state           func(string)
	controlFailure  func(int, string)
	cfg             Config
	mu              sync.Mutex
	channels        map[string]*pion.DataChannel
	opened          map[string]bool
	failed          bool
	answered        bool
	setupTimer      *time.Timer
	nativeMu        sync.Mutex
	pc              *pion.PeerConnection
	closeOnce       sync.Once
	nativeCloseOnce sync.Once
	closed          chan struct{}
}

func NewDedicatedInputPeer(parent context.Context, cfg Config, epoch, control int, sink func(context.Context, generated.BrowserInputFrame), validate func([]byte) error, state func(string)) *DedicatedInputPeer {
	ctx, cancel := context.WithCancel(parent)
	p := &DedicatedInputPeer{ctx: ctx, cancel: cancel, cfg: cfg, validate: validate, state: state, channels: make(map[string]*pion.DataChannel), opened: make(map[string]bool), closed: make(chan struct{})}
	p.queue = newDedicatedInputQueue(ctx, epoch, control, sink, p.fail)
	p.queue.mu.Lock()
	p.queue.controlFailure = p.reportControlFailure
	p.queue.mu.Unlock()
	context.AfterFunc(ctx, p.Close)
	return p
}
func (p *DedicatedInputPeer) Closed() <-chan struct{} { return p.closed }

// RetiredSource returns the canceled input source after Close. Callers join
// Closed and drain this source's held input before admitting a replacement.
func (p *DedicatedInputPeer) RetiredSource() context.Context {
	p.queue.mu.Lock()
	defer p.queue.mu.Unlock()
	return p.queue.ctx
}
func (p *DedicatedInputPeer) Close() {
	p.closeOnce.Do(func() {
		p.cancel()
		p.queue.close()
		p.mu.Lock()
		if p.setupTimer != nil {
			p.setupTimer.Stop()
		}
		p.mu.Unlock()
		go p.closeNative()
	})
}
func (p *DedicatedInputPeer) closeNative() {
	p.nativeCloseOnce.Do(func() {
		// Preparation can fail before the outer Answer caller invokes Close.
		// Cancel dispatch here as well before joining its worker.
		p.cancel()
		p.queue.close()
		p.nativeMu.Lock()
		if p.pc != nil {
			if err := p.pc.Close(); err != nil {
				slog.Warn("browser input peer cleanup failed", "error", err)
			}
		}
		p.nativeMu.Unlock()
		p.queue.mu.Lock()
		done := p.queue.done
		p.queue.mu.Unlock()
		<-done
		close(p.closed)
	})
}
func (p *DedicatedInputPeer) fail(reason string) {
	p.mu.Lock()
	if p.failed || p.ctx.Err() != nil {
		p.mu.Unlock()
		return
	}
	p.failed = true
	p.mu.Unlock()
	p.Close()
	if p.state != nil {
		p.state(reason)
	}
}

// SetControlFailureHandler allows an expired control source to be retired while
// retaining its healthy peer. Install before Answer. Callers must fence the
// supplied control epoch and explicitly advance control after joining/releasing
// the old source; no queued action is replayed. Without a handler expiry remains
// a visible fatal failure rather than silently leaving a caller paused. Expiry
// may invoke the handler from the queue's timer goroutine, after internal locks
// are released; handlers must synchronize access to any shared state.
func (p *DedicatedInputPeer) SetControlFailureHandler(handler func(int, string)) {
	p.mu.Lock()
	p.controlFailure = handler
	p.mu.Unlock()
}
func (p *DedicatedInputPeer) reportControlFailure(control int, reason string) {
	p.mu.Lock()
	handler := p.controlFailure
	stopped := p.failed || p.ctx.Err() != nil
	p.mu.Unlock()
	if stopped {
		return
	}
	if handler == nil {
		p.fail(reason)
		return
	}
	handler(control, reason)
}

// SetActiveDispatchBudget supplies the browser's existing per-input bound.
func (p *DedicatedInputPeer) SetActiveDispatchBudget(budget time.Duration) {
	p.queue.setActiveDispatchBudget(budget)
}

// SetQueueTimingObserver observes admission time immediately before serial
// dispatch without changing the input source context or the wire frame.
func (p *DedicatedInputPeer) SetQueueTimingObserver(observer func(generated.BrowserInputFrame, InputQueueTiming)) {
	p.queue.setTimingObserver(observer)
}

func (p *DedicatedInputPeer) PauseControl(next int) (context.Context, <-chan struct{}, error) {
	return p.queue.pause(next)
}
func (p *DedicatedInputPeer) ResumeControl(next int) error { return p.queue.resume(next) }
func (p *DedicatedInputPeer) Answer(caller context.Context, sdp string) (string, error) {
	p.mu.Lock()
	if p.answered || p.ctx.Err() != nil {
		p.mu.Unlock()
		return "", errors.New("input peer already negotiated or closed")
	}
	p.answered = true
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(caller, gatherTimeout)
	defer cancel()
	stop := context.AfterFunc(p.ctx, cancel)
	defer stop()
	if p.ctx.Err() != nil {
		cancel()
	}
	answer, err := dedicatedPreparation.runWithCleanup(ctx, func() (string, func(), error) {
		answer, err := p.answerNative(ctx, sdp)
		if err != nil {
			return "", p.closeNative, err
		}
		return answer, nil, nil
	})
	if err != nil {
		p.Close()
	}
	return answer, err
}
func (p *DedicatedInputPeer) answerNative(ctx context.Context, sdp string) (string, error) {
	p.nativeMu.Lock()
	defer p.nativeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := p.ctx.Err(); err != nil {
		return "", err
	}
	// A dedicated offer must contain only an application section.
	applications := 0
	for _, line := range strings.Split(sdp, "\n") {
		if strings.HasPrefix(line, "m=") {
			if !strings.HasPrefix(line, "m=application ") {
				return "", errors.New("input offer must be data-only")
			}
			applications++
		}
	}
	if applications != 1 {
		return "", errors.New("input offer must have one application section")
	}
	// Warn, not Info: the gateway's default log level is "warn"
	// (pkg/config/defaults.go's LogLevel), so an Info line here is invisible in
	// every default deployment — which is exactly how this peer's ICE
	// instrumentation sat merged, tested, and silent while three lanes argued
	// its defect from absence. The lines are one-shot per negotiation (offer
	// summary, one line per candidate, state transitions), so Warn-level noise
	// is bounded the way pion's own ICE warnings already are.
	logf := func(format string, args ...any) {
		slog.Warn(fmt.Sprintf("browser dedicated input: "+format, args...))
	}
	session := NewSession(p.cfg, nil, logf)
	pc, err := session.buildPeerConnection(session.apiViewer, true)
	if err != nil {
		return "", err
	}
	p.pc = pc
	prefix := fmt.Sprintf("[input-%d]", p.queue.peer)
	diag := newICEDiag(prefix, "input", logf)
	pc.OnICECandidate(diag.noteLocalCandidate)
	pc.OnICEGatheringStateChange(diag.noteGatheringState)
	pc.OnICEConnectionStateChange(func(state pion.ICEConnectionState) {
		logf("%s ICE connection state -> %s", prefix, state.String())
		diag.noteICEState(state, pc)
	})
	pc.OnDataChannel(p.bindChannel)
	pc.OnConnectionStateChange(func(state pion.PeerConnectionState) {
		switch state {
		case pion.PeerConnectionStateFailed, pion.PeerConnectionStateDisconnected, pion.PeerConnectionStateClosed:
			p.fail("input connection closed")
		}
	})
	diag.noteRemoteOffer(sdp)
	if err = pc.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeOffer, SDP: sdp}); err != nil {
		return "", fmt.Errorf("input offer: %w", err)
	}
	gathered := pion.GatheringCompletePromise(pc)
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return "", err
	}
	if err = pc.SetLocalDescription(answer); err != nil {
		return "", err
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-p.ctx.Done():
		return "", p.ctx.Err()
	case <-gathered:
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := p.ctx.Err(); err != nil {
		return "", err
	}
	local := pc.LocalDescription()
	if local == nil {
		return "", errors.New("input answer unavailable")
	}
	// Channels may fail to arrive even after SDP succeeds. Bound that setup too;
	// this timer is never an operation deadline on an accepted input source.
	p.mu.Lock()
	p.setupTimer = time.AfterFunc(gatherTimeout, func() {
		p.mu.Lock()
		ready := len(p.opened) == 2
		p.mu.Unlock()
		if !ready {
			p.fail("input channels did not open")
		}
	})
	if p.ctx.Err() != nil || len(p.opened) == 2 {
		p.setupTimer.Stop()
	}
	p.mu.Unlock()
	return local.SDP, nil
}
func (p *DedicatedInputPeer) bindChannel(dc *pion.DataChannel) {
	hover := dc.Label() == "input-hover"
	valid := dc.Label() == "input-reliable" || hover
	valid = valid && dc.Protocol() == InputBinaryProtocol && !dc.Negotiated() && dc.MaxPacketLifeTime() == nil
	if hover {
		valid = valid && !dc.Ordered() && dc.MaxRetransmits() != nil && *dc.MaxRetransmits() == 0
	} else {
		valid = valid && dc.Ordered() && dc.MaxRetransmits() == nil
	}
	p.mu.Lock()
	if !valid || p.channels[dc.Label()] != nil || p.ctx.Err() != nil {
		duplicate := p.channels[dc.Label()] != nil
		ctxErr := p.ctx.Err()
		p.mu.Unlock()
		// Say WHICH predicate rejected the channel. This branch surfaces to the
		// operator as "Could not negotiate the input connection", and until now
		// it named nothing — so a peer whose ICE connects fine, and whose input
		// then silently never arrives, gave the investigator no way to tell a
		// label mismatch from a protocol mismatch from a duplicate. That
		// ambiguity cost several rounds on the ui-browser shard.
		retx := "nil"
		if v := dc.MaxRetransmits(); v != nil {
			retx = fmt.Sprintf("%d", *v)
		}
		life := "nil"
		if v := dc.MaxPacketLifeTime(); v != nil {
			life = fmt.Sprintf("%d", *v)
		}
		dedicatedInputLogf("input data channel REJECTED: label=%q protocol=%q negotiated=%t ordered=%t max_retransmits=%s max_packet_lifetime=%s duplicate=%t ctx_err=%v (want label input-reliable|input-hover, protocol %q, negotiated=false, lifetime=nil; reliable: ordered=true retransmits=nil; hover: ordered=false retransmits=0)",
			dc.Label(), dc.Protocol(), dc.Negotiated(), dc.Ordered(), retx, life, duplicate, ctxErr, InputBinaryProtocol)
		p.fail("invalid input data channel")
		return
	}
	dedicatedInputLogf("input data channel accepted: label=%q protocol=%q ordered=%t", dc.Label(), dc.Protocol(), dc.Ordered())
	p.channels[dc.Label()] = dc
	p.mu.Unlock()
	dc.OnOpen(func() {
		p.mu.Lock()
		if p.ctx.Err() != nil {
			p.mu.Unlock()
			return
		}
		p.opened[dc.Label()] = true
		ready := len(p.opened) == 2
		if ready && p.setupTimer != nil {
			p.setupTimer.Stop()
		}
		p.mu.Unlock()
		if ready && p.state != nil {
			p.state("ready")
		}
	})
	dc.OnClose(func() { p.fail("input data channel closed") })
	dc.OnError(func(error) { p.fail("input data channel failed") })
	dc.OnMessage(func(message pion.DataChannelMessage) {
		if p.ctx.Err() != nil {
			return
		}
		p.mu.Lock()
		ready := len(p.opened) == 2
		p.mu.Unlock()
		if !ready {
			p.fail("input channels not ready")
			return
		}
		if message.IsString || len(message.Data) > inputBinaryMaxBytes {
			p.fail("invalid input message")
			return
		}
		frame, err := DecodeInputPacket(message.Data)
		if err != nil {
			p.fail("invalid input payload")
			return
		}
		if p.validate != nil {
			raw, err := json.Marshal(frame)
			if err != nil {
				p.fail("invalid input payload")
				return
			}
			if err := p.validate(raw); err != nil {
				p.fail("invalid input payload")
				return
			}
		}
		if frame.Type != "browser_input" {
			p.fail("invalid input payload")
			return
		}
		if (frame.Kind == "mouse_down" || frame.Kind == "mouse_up") && (frame.X == nil || frame.Y == nil) {
			p.fail("button input requires coordinates")
			return
		}
		p.queue.submit(hover, frame)
	})
}

// dedicatedInputLogf mirrors the peer's existing Warn-level logging shape (see
// the logf closure built in Answer). bindChannel is reached from pion's
// OnDataChannel callback, which has no access to that closure, and the gateway
// filters below Warn — so Info here would be invisible, which is the exact trap
// the ICE diagnostics fell into.
func dedicatedInputLogf(format string, args ...any) {
	slog.Warn(fmt.Sprintf("browser dedicated input: "+format, args...))
}
