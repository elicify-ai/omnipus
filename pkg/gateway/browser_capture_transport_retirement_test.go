package gateway

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// Keep Gorilla's actual writer and fatal-error state; control only the socket
// write boundary so cancellation can occur after transport admission.
type captureFailingWriteConn struct {
	net.Conn
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *captureFailingWriteConn) Write(data []byte) (int, error) {
	if c.armed.Load() {
		c.once.Do(func() { close(c.entered) })
		<-c.release
		return 0, os.ErrDeadlineExceeded
	}
	return c.Conn.Write(data)
}

type captureFailingHijacker struct {
	http.ResponseWriter
	accepted chan *captureFailingWriteConn
}

func (w captureFailingHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := w.ResponseWriter.(http.Hijacker).Hijack()
	if err != nil {
		return nil, nil, err
	}
	controlled := &captureFailingWriteConn{Conn: conn, entered: make(chan struct{}), release: make(chan struct{})}
	w.accepted <- controlled
	return controlled, rw, nil
}

func captureTransportFixture(t *testing.T) (*browser.CaptureSession, *websocket.Conn, *captureFailingWriteConn, <-chan struct{}) {
	t.Helper()
	relay := &wireIngestRelay{token: 60, entered: make(chan wireIngestOffer, 16)}
	var starts int32
	cs, err := browser.NewCaptureSessionWithDeps(nil, "writer-retirement", relay, fakeEncoderStarter(&starts, nil), nil)
	require.NoError(t, err)
	t.Cleanup(cs.Stop)
	_, err = cs.BeginFrameTransition("page-a", 800, 600, 1)
	require.NoError(t, err)
	accepted := make(chan *captureFailingWriteConn, 1)
	done := make(chan struct{})
	handler := &captureIngestWSHandler{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		conn, err := (&websocket.Upgrader{}).Upgrade(captureFailingHijacker{w, accepted}, r, nil)
		if err != nil {
			return
		}
		handler.serveBoundIngest(conn, cs, "writer-retirement", false)
	}))
	t.Cleanup(srv.Close)
	client, response, err := websocket.DefaultDialer.Dial("ws"+srv.URL[len("http"):], nil)
	require.NoError(t, err)
	if response != nil {
		response.Body.Close()
	}
	controlled := <-accepted
	t.Cleanup(func() {
		client.Close()
		controlled.Conn.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("capture transport handler did not drain")
		}
	})
	ingestWireRead(t, client, "browser_capture_control")
	return cs, client, controlled, done
}

func TestCaptureIngestFatalWriteRetiresCanceledRequestSocket(t *testing.T) {
	for _, retire := range []string{"caller", "deadline"} {
		t.Run(retire, func(t *testing.T) {
			cs, _, transport, handlerDone := captureTransportFixture(t)
			transport.armed.Store(true)
			var once sync.Once
			release := func() { once.Do(func() { close(transport.release) }) }
			defer release()
			request, cancel := context.WithCancel(context.Background())
			if retire == "deadline" {
				cancel()
				request, cancel = context.WithTimeout(context.Background(), 500*time.Millisecond)
			}
			defer cancel()
			result := make(chan bool, 1)
			frame := cs.FrameState()
			go func() { result <- cs.RecaptureFrameContext(request, frame) }()
			select {
			case <-transport.entered:
			case <-time.After(time.Second):
				t.Fatal("recapture did not reach socket write")
			}
			if retire == "caller" {
				cancel()
			} else {
				<-request.Done()
			}
			release()
			select {
			case accepted := <-result:
				require.False(t, accepted, "failed write reported recapture acceptance")
			case <-time.After(time.Second):
				t.Fatal("failed recapture write did not return")
			}
			select {
			case <-handlerDone:
			case <-time.After(200 * time.Millisecond):
				t.Error("fatal writer failure retained original socket after request cancellation")
			}
			epoch, token := cs.CurrentIngestBinding()
			require.Equal(t, uint64(0), epoch, "unusable control socket retained its binding")
			require.Equal(t, uint64(0), token)
			select {
			case <-cs.Done():
				t.Error("retiring one socket stopped the capture session")
			default:
			}
		})
	}
}

func TestCaptureIngestCanceledBeforeWriteKeepsUsableSocket(t *testing.T) {
	cs, client, _, handlerDone := captureTransportFixture(t)
	epoch, token := cs.CurrentIngestBinding()
	require.NotZero(t, epoch)
	request, cancel := context.WithCancel(context.Background())
	cancel()
	frame := cs.FrameState()
	require.False(t, cs.RecaptureFrameContext(request, frame))
	require.True(t, cs.RecaptureFrameContext(context.Background(), frame))
	got := ingestWireRead(t, client, "browser_capture_control")
	require.Equal(t, "recapture", got["action"])
	afterEpoch, afterToken := cs.CurrentIngestBinding()
	require.Equal(t, epoch, afterEpoch)
	require.Equal(t, token, afterToken)
	select {
	case <-handlerDone:
		t.Error("pre-write cancellation retired healthy socket")
	default:
	}
}

func TestCaptureIngestFatalAnswerWriteRetiresSupersededSocket(t *testing.T) {
	cs, client, transport, handlerDone := captureTransportFixture(t)
	transport.armed.Store(true)
	var once sync.Once
	release := func() { once.Do(func() { close(transport.release) }) }
	defer release()
	ingestWireSendOffer(t, client, 1, 1, "page-a")
	select {
	case <-transport.entered:
	case <-time.After(time.Second):
		t.Fatal("ingest answer did not reach socket write")
	}
	_, err := cs.BeginFrameTransition("page-b", 800, 600, 1)
	require.NoError(t, err)
	release()
	select {
	case <-handlerDone:
	case <-time.After(200 * time.Millisecond):
		t.Error("fatal answer write became a harmless stale offer after frame replacement")
	}
	epoch, token := cs.CurrentIngestBinding()
	require.Equal(t, uint64(0), epoch, "failed answer writer retained socket binding")
	require.Equal(t, uint64(0), token)
	select {
	case <-cs.Done():
		t.Error("retiring answer socket stopped the capture session")
	default:
	}
}
