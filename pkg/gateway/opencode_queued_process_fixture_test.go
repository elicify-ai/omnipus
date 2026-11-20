package gateway

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"time"
)

// The starting run is alive but announces no native id until its natural-exit
// command. It emits no answer, so its ordinary done has no message id. The
// resumed process reuses the existing fully-silent output barrier/validation.
func opencode1895QueuedFollowupProcess(addr, resumeAddr, nativeID string) int {
	if nativeID != "" {
		return opencodeFix1895LiveFollowupProcess(addr, resumeAddr, nativeID, true)
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "queued CLI fixture connection:", err)
		return 2
	}
	defer func() { _ = conn.Close() }()
	fmt.Println(`{"type":"tool_use","part":{"type":"tool","id":"fixture-ready-1895","tool":"read_file","state":{"status":"completed","input":{"path":"fixture.txt"},"output":"ready"}}}`)
	reader := bufio.NewReader(conn)
	for {
		command, readErr := reader.ReadString('\n')
		if readErr != nil {
			fmt.Fprintln(os.Stderr, "queued CLI fixture command:", readErr)
			return 2
		}
		switch command {
		case "probe\n":
			if _, err = fmt.Fprintln(conn, "still-starting-without-native-id"); err != nil {
				return 2
			}
		case "finish\n":
			fmt.Printf("{\"type\":\"step_start\",\"sessionID\":%q,\"part\":{\"type\":\"step-start\"}}\n", opencode1895LiveNativeID)
			return 0 // Only the test can release this natural, successful completion.
		default:
			fmt.Fprintln(os.Stderr, "unexpected queued CLI fixture command:", command)
			return 2
		}
	}
}
