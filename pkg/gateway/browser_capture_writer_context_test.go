package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func captureWriterPair(t *testing.T) (*captureIngestConn, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			accepted <- conn
		}
	}))
	t.Cleanup(server.Close)
	client, response, err := websocket.DefaultDialer.Dial("ws"+server.URL[len("http"):], nil)
	if response != nil {
		response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	select {
	case serverConn := <-accepted:
		t.Cleanup(func() { serverConn.Close() })
		return &captureIngestConn{conn: serverConn}, client
	case <-time.After(time.Second):
		t.Fatal("capture writer fixture did not upgrade")
		return nil, nil
	}
}

func TestCaptureWriterRejectsRetiredCommand(t *testing.T) {
	for _, kind := range []string{"canceled", "superseded"} {
		t.Run(kind, func(t *testing.T) {
			writer, _ := captureWriterPair(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "canceled" {
				cancel()
			}
			if err := writer.sendJSONContext(ctx, map[string]string{"action": "recapture"}, func() bool { return kind != "superseded" }); err == nil {
				t.Error("retired capture command was admitted to the socket")
			}
		})
	}
}

func TestCaptureWriterCancellationReleasesWaitingCommand(t *testing.T) {
	writer, _ := captureWriterPair(t)
	release, err := writer.acquireWrite(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- writer.sendJSONContext(ctx, map[string]string{"action": "recapture"}, nil) }()
	cancel()
	select {
	case err := <-result:
		release()
		if err == nil {
			t.Error("canceled waiting command was admitted")
		}
	case <-time.After(200 * time.Millisecond):
		release()
		<-result
		t.Error("canceled capture command remained blocked behind the writer")
	}
}

func TestCaptureWriterRechecksStateAfterEncoding(t *testing.T) {
	writer, client := captureWriterPair(t)
	var current atomic.Bool
	current.Store(true)
	value := captureWriterEncodingBarrier{retire: func() { current.Store(false) }}
	if err := writer.sendJSONContext(context.Background(), value, current.Load); err == nil {
		t.Error("encoding completion revived a superseded capture command")
	}
	if err := writer.sendJSONContext(context.Background(), map[string]string{"action": "marker"}, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	client.SetReadDeadline(time.Now().Add(time.Second))
	_, data, err := client.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"action":"marker"}` {
		t.Fatalf("superseded command reached transport before marker: %s", data)
	}
}

func TestCaptureWriterRechecksOriginalStateAfterAdmissionWait(t *testing.T) {
	writer, _ := captureWriterPair(t)
	release, err := writer.acquireWrite(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entered, resume := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	current := func() bool {
		if calls.Add(1) == 1 {
			close(entered)
			<-resume
			return true // The originally checked state was current.
		}
		return false // It retired before this command could own the writer.
	}
	result := make(chan error, 1)
	go func() {
		result <- writer.sendJSONContext(context.Background(), map[string]string{"action": "recapture"}, current)
	}()
	select {
	case <-entered:
	case <-time.After(200 * time.Millisecond):
		t.Error("capture writer did not validate the original state before waiting")
	}
	close(resume)
	release()
	if err := <-result; err == nil {
		t.Error("retired state survived capture writer admission")
	}
}

type captureWriterEncodingBarrier struct{ retire func() }

func (v captureWriterEncodingBarrier) MarshalJSON() ([]byte, error) {
	v.retire()
	return []byte(`{"action":"recapture"}`), nil
}
