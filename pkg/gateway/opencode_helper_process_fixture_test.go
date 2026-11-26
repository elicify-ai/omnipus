package gateway

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/tests/fixtures/t11combined"
	"github.com/stretchr/testify/require"
)

// The copied test executable impersonates only the external CLI process. It
// never runs a second test suite, calls a model or shells out. Using a copy of
// this binary keeps the fixture portable across Linux, macOS and Windows.
func init() {
	if strings.HasPrefix(filepath.Base(os.Args[0]), "opencode-fix1895-process") {
		os.Exit(opencodeFix1895Process())
	}
}

func opencodeFix1895Process() int {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("2.2.6")
		return 0
	}
	var addr, resumeAddr, mode, nativeID string
	for i, arg := range os.Args[1:] {
		if arg == "--session" && i+2 < len(os.Args) {
			nativeID = os.Args[i+2]
		}
		if strings.HasPrefix(arg, "--fixture-address=") {
			addr = strings.TrimPrefix(arg, "--fixture-address=")
		}
		if strings.HasPrefix(arg, "--fixture-mode=") {
			mode = strings.TrimPrefix(arg, "--fixture-mode=")
		}
		if strings.HasPrefix(arg, "--fixture-resume-address=") {
			resumeAddr = strings.TrimPrefix(arg, "--fixture-resume-address=")
		}
	}
	if mode == "queued_save_failure" {
		return opencodeGateNarrationProcess(addr, resumeAddr, nativeID)
	}
	if mode == "queued_followup_pending" || mode == "queued_followup_text_pending" {
		return opencode1895QueuedFollowupProcess(addr, resumeAddr, nativeID)
	}
	if mode == "live_followup" || mode == "live_followup_pending" || mode == "live_followup_text" {
		return opencodeFix1895LiveFollowupProcess(addr, resumeAddr, nativeID, mode != "live_followup")
	}
	if mode == "startup_followup" && nativeID != "" {
		prompt := os.Args[len(os.Args)-1]
		if nativeID != "native-frame-conversation-1895" ||
			strings.Count(prompt, "real startup follow-up 1899") != 1 || strings.Contains(prompt, "answer in the helper's own chat") {
			fmt.Println(`{"type":"error","message":"fixture: resume must use the original native id and new input only"}`)
			return 2
		}
		fmt.Println(`{"type":"text","sessionID":"native-frame-conversation-1895","part":{"type":"text","text":"real startup follow-up delivered 1899"}}`)
		return 0
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "CLI fixture connection:", err)
		return 2
	}
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	command, err := reader.ReadString('\n')
	if err == nil && mode == "startup_followup" && command == "probe\n" {
		if _, err = fmt.Fprintln(conn, "still-starting-without-native-id"); err == nil {
			command, err = reader.ReadString('\n')
		}
	}
	if err != nil || command != "finish\n" {
		fmt.Fprintln(os.Stderr, "CLI fixture release:", command, err)
		return 2
	}
	// This tool event is an instrument control: real hub-bound subscribers
	// must receive its frames even on the pre-fix branch missing final text.
	fmt.Println(`{"type":"tool_use","part":{"type":"tool","id":"fixture-tool-1895","tool":"read_file","state":{"status":"completed","input":{"path":"fixture.txt"},"output":"fixture"}}}`)
	switch mode {
	case "success", "startup_followup":
		fmt.Println(`{"type":"text","sessionID":"native-frame-conversation-1895","part":{"type":"text","text":"external helper answer 1895"}}`)
	case "runner_error":
		fmt.Println(`{"type":"error","message":"authentication failed in CLI fixture 1895","code":"auth"}`)
	case "captured_unknown_1x":
		fmt.Println(opencodeGateCapturedUnknown)
	case "resume_refusal":
		// A legitimate run can finish without announcing a native id. A later
		// Resume must then visibly refuse rather than invent a conversation.
		fmt.Println(`{"type":"content.delta","delta":{"type":"text_delta","text":"first answer without native id"}}`)
	default:
		fmt.Fprintln(os.Stderr, "unrecognized CLI fixture mode:", mode)
		return 2
	}
	return 0
}

func opencodeFix1895Program(t *testing.T) string {
	t.Helper()
	source, err := os.Executable()
	require.NoError(t, err)
	in, err := os.Open(source)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, in.Close()) })
	// The real program check rejects agent-writable runtime temp. Use the
	// existing checkout-local trusted-program fixture; never bypass the check.
	program := filepath.Join(t11combined.TrustedProgramDir(t), "opencode-fix1895-process.exe")
	out, err := os.OpenFile(program, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o700)
	require.NoError(t, err)
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	require.NoError(t, copyErr)
	require.NoError(t, closeErr)
	return program
}

// The listener is a deterministic process barrier, not a clock assertion.
func opencodeFix1895Gate(t *testing.T) (string, func() net.Conn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, listener.Close()) })
	accepted := make(chan net.Conn, 1)
	failed := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			failed <- err
			return
		}
		accepted <- conn
	}()
	return listener.Addr().String(), func() net.Conn {
		t.Helper()
		select {
		case conn := <-accepted:
			t.Cleanup(func() { require.NoError(t, conn.Close()) })
			return conn
		case err := <-failed:
			t.Fatalf("SETUP: CLI process gate accept failed: %v", err)
		case <-time.After(10 * time.Second):
			t.Fatal("SETUP: real external driver never started the CLI fixture")
		}
		return nil
	}
}
