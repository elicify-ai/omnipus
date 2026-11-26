package gateway

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"time"
)

const opencodeGateLostNarration = "Original narration displayed while its actual archive append fails"
const opencodeGateSaveBarrier = "gate-original-append-attempted"

// A process-edge barrier, not a mocked persistence method. Startup input is
// accepted first; the test then breaks storage and releases narration. A later
// tool event proves the real dispatcher finished processing that text event.
func opencodeGateNarrationProcess(addr, resumeAddr, nativeID string) int {
	if nativeID != "" {
		return opencodeFix1895LiveFollowupProcess(addr, resumeAddr, nativeID, true)
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "save-failure process connection:", err)
		return 2
	}
	defer func() { _ = conn.Close() }()
	fmt.Println(`{"type":"tool_use","part":{"type":"tool","id":"fixture-ready-1895","tool":"read_file","state":{"status":"completed","input":{"path":"fixture.txt"},"output":"ready"}}}`)
	reader := bufio.NewReader(conn)
	narrated := false
	for {
		command, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintln(os.Stderr, "save-failure process command:", err)
			return 2
		}
		switch command {
		case "probe\n":
			if _, err := fmt.Fprintln(conn, "still-starting-without-native-id"); err != nil {
				return 2
			}
		case "narrate\n":
			if narrated {
				return 2
			}
			fmt.Printf("{\"type\":\"step_start\",\"sessionID\":%q,\"part\":{\"type\":\"step-start\"}}\n", opencode1895LiveNativeID)
			fmt.Printf("{\"type\":\"text\",\"sessionID\":%q,\"part\":{\"type\":\"text\",\"text\":%q}}\n", opencode1895LiveNativeID, opencodeGateLostNarration)
			fmt.Printf("{\"type\":\"tool_use\",\"part\":{\"type\":\"tool\",\"id\":%q,\"tool\":\"read_file\",\"state\":{\"status\":\"completed\",\"input\":{},\"output\":\"after narration\"}}}\n", opencodeGateSaveBarrier)
			narrated = true
		case "finish\n":
			if narrated {
				return 0
			}
			return 2
		default:
			return 2
		}
	}
}
