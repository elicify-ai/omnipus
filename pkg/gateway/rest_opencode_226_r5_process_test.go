package gateway

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/tests/fixtures/t11combined"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Portable process-edge fixture, like the round-two runner fixture. Only a
// uniquely named executable COPY in a trusted test directory can enter this
// branch. The normal gateway test binary always performs ordinary collection.
func init() {
	name := filepath.Base(os.Args[0])
	if name != "oc226-r5-process" && name != "oc226-r5-process.exe" {
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "run") {
		os.Exit(opencode226R5ServeProcess())
	}
}

// not-wire-format: private test-process handshake, never a gateway/SPA type.
// Capture only the harmless fixture env value, never the full child env.
type opencode226R5ProcessObservation struct {
	Args     []string `json:"args"`
	EnvValue string   `json:"env_value"`
	PID      int      `json:"pid"`
}

func opencode226R5ServeProcess() int {
	if os.Args[1] == "--version" {
		if _, err := fmt.Fprintln(os.Stdout, "opencode v2.2.6"); err != nil {
			return 2
		}
		return 0
	}
	dir := filepath.Dir(os.Args[0])
	address, err := os.ReadFile(filepath.Join(dir, "control.addr"))
	if err != nil {
		return 2
	}
	conn, err := net.Dial("tcp", string(address))
	if err != nil {
		return 2
	}
	defer func() { _ = conn.Close() }()
	observe := func() error {
		return json.NewEncoder(conn).Encode(opencode226R5ProcessObservation{
			Args: os.Args[1:], EnvValue: os.Getenv(opencode226R5EnvKey), PID: os.Getpid(),
		})
	}
	if observeErr := observe(); observeErr != nil {
		return 2
	}
	// No wall-clock script/sleep: the real child cannot finish until the
	// test releases it AFTER the real executor/model PUT has returned.
	command, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || command != "finish\n" {
		return 2
	}
	if observeErr := observe(); observeErr != nil {
		return 2
	}
	capture, err := os.ReadFile(filepath.Join(dir, "capture.ndjson"))
	if err != nil {
		return 2
	}
	if _, err := os.Stdout.Write(capture); err != nil {
		return 2
	}
	return 0
}

type opencode226R5ProcessFixture struct {
	program  string
	listener net.Listener
	accepted chan net.Conn
	stopped  chan struct{}
	wg       sync.WaitGroup
	mu       sync.Mutex
	conns    []net.Conn
	runs     atomic.Int32
}

func opencode226R5NewProcessFixture(t *testing.T) *opencode226R5ProcessFixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	f := &opencode226R5ProcessFixture{
		listener: listener, accepted: make(chan net.Conn, 8), stopped: make(chan struct{}),
	}
	t.Cleanup(func() {
		close(f.stopped)
		_ = listener.Close()
		f.wg.Wait()
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, conn := range f.conns {
			_ = conn.Close()
		}
	})
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				if !errors.Is(acceptErr, net.ErrClosed) {
					t.Errorf("fixture listener failed: %v", acceptErr)
				}
				return
			}
			f.runs.Add(1)
			f.mu.Lock()
			f.conns = append(f.conns, conn)
			f.mu.Unlock()
			select {
			case f.accepted <- conn:
			case <-f.stopped:
				return
			}
		}
	}()
	name := "oc226-r5-process"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dir := t11combined.TrustedProgramDir(t)
	f.program = filepath.Join(dir, name)
	sourcePath, err := os.Executable()
	require.NoError(t, err)
	source, err := os.Open(sourcePath)
	require.NoError(t, err)
	defer func() { _ = source.Close() }()
	destination, err := os.OpenFile(f.program, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	require.NoError(t, err)
	_, copyErr := io.Copy(destination, source)
	closeErr := destination.Close()
	require.NoError(t, copyErr)
	require.NoError(t, closeErr)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "control.addr"), []byte(listener.Addr().String()), 0o600))
	capture, err := os.ReadFile(filepath.Join("..", "agent", "runner", "testdata", "opencode-2.2.6", "plain.stdout.ndjson"))
	require.NoError(t, err, "replay the real 2.2.6 plain capture, not invented stream vocabulary")
	require.Contains(t, string(capture), "CAPTURE-OK", "instrument: the supplied answer is present in the capture")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "capture.ndjson"), capture, 0o600))
	return f
}

func (f *opencode226R5ProcessFixture) accept(t *testing.T) (*json.Decoder, net.Conn, opencode226R5ProcessObservation) {
	t.Helper()
	var conn net.Conn
	select {
	case conn = <-f.accepted:
	case <-time.After(10 * time.Second):
		t.Fatal("real delegated CLI never reached the signaled process boundary")
	}
	require.NoError(t, conn.SetDeadline(time.Now().Add(10*time.Second))) // liveness guard, not an elapsed-time assertion
	decoder := json.NewDecoder(conn)
	var observation opencode226R5ProcessObservation
	require.NoError(t, decoder.Decode(&observation), "receive actual process argv/env before permitting completion")
	require.Positive(t, observation.PID, "instrument: the fixture is a real child process")
	return decoder, conn, observation
}

func opencode226R5FinishProcess(t *testing.T, decoder *json.Decoder, conn net.Conn, before opencode226R5ProcessObservation) {
	t.Helper()
	_, err := io.WriteString(conn, "finish\n")
	require.NoError(t, err, "settings save must not kill the blocked original process")
	var after opencode226R5ProcessObservation
	require.NoError(t, decoder.Decode(&after), "the SAME running process must acknowledge release after the save")
	assert.Equal(t, before, after, "in-flight argv/env/PID must stay unchanged through the settings update")
}

func opencode226R5SaveProgram(t *testing.T, api *restAPI, program string) {
	t.Helper()
	t11bSaveProgram(t, api, "opencode", program)
	// The following real REST PUT refreshes config from disk. Persist this
	// fixture's Settings row too, so that refresh cannot erase its program.
	data, err := json.Marshal(api.agentLoop.GetConfig())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(api.configPath(), data, 0o600))
	require.False(t, strings.Contains(program, api.homePath), "fixture program must not be planted inside the Omnipus data directory")
}
