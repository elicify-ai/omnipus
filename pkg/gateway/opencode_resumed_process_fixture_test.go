package gateway

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

const (
	opencode1895LiveNativeID = "native-frame-conversation-1895"
	opencode1895LiveFollowup = "person's live follow-up 1895"
	opencode1895LiveAnswer   = "RESUMED helper answer streamed live 1895"
)

// Both invocations are real processes. The first announces its conversation
// before an observable tool event, then can end only through interruption.
// Resume validates the retained native id/new prompt and prints its answer
// before waiting at a separate exit barrier. No model or web service is used.
func opencodeFix1895LiveFollowupProcess(addr, resumeAddr, nativeID string) int {
	resumed := nativeID != ""
	if resumed {
		prompt := os.Args[len(os.Args)-1]
		if nativeID != opencode1895LiveNativeID || strings.Count(prompt, opencode1895LiveFollowup) != 1 ||
			strings.Contains(prompt, "answer in the helper's own chat") {
			fmt.Println(`{"type":"error","message":"fixture: live resume lost the native id or the person's new input"}`)
			return 2
		}
		addr = resumeAddr
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "live CLI fixture connection:", err)
		return 2
	}
	defer func() { _ = conn.Close() }()
	fmt.Printf("{\"type\":\"step_start\",\"sessionID\":%q,\"part\":{\"type\":\"step-start\"}}\n", opencode1895LiveNativeID)
	if resumed {
		fmt.Printf("{\"type\":\"text\",\"sessionID\":%q,\"part\":{\"type\":\"text\",\"text\":%q}}\n", opencode1895LiveNativeID, opencode1895LiveAnswer)
	} else {
		fmt.Println(`{"type":"tool_use","part":{"type":"tool","id":"fixture-ready-1895","tool":"read_file","state":{"status":"completed","input":{"path":"fixture.txt"},"output":"ready"}}}`)
	}
	reader := bufio.NewReader(conn)
	for {
		command, readErr := reader.ReadString('\n')
		if readErr != nil {
			fmt.Fprintln(os.Stderr, "live CLI fixture release:", readErr)
			return 2
		}
		switch command {
		case "probe\n":
			if _, err = fmt.Fprintln(conn, "live-process-still-paused"); err != nil {
				return 2
			}
		case "finish\n":
			if resumed {
				return 0
			}
			fmt.Println(`{"type":"error","message":"fixture: original run must be interrupted, not released naturally"}`)
			return 2
		default:
			fmt.Fprintln(os.Stderr, "unexpected live CLI fixture command:", command)
			return 2
		}
	}
}
