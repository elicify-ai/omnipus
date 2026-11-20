package gateway

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"time"
)

const (
	opencode1895LiveNativeID = "native-frame-conversation-1895"
	opencode1895LiveFollowup = "person's live follow-up 1895"
	opencode1895LiveAnswer   = "RESUMED helper answer streamed live 1895"
	opencode1895FirstAnswer  = "Original helper text before the person interrupts 1895"
)

// Both invocations are real processes. The first announces its conversation
// before an observable tool event, then can end only through interruption.
// Resume validates the retained native id/new prompt. The pending mode waits
// before ANY stdout; other modes print immediately. Both then wait at an exit
// barrier. No model or web service is used.
func opencodeFix1895LiveFollowupProcess(addr, resumeAddr, nativeID string, holdOutput bool) int {
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
	emit := func() {
		fmt.Printf("{\"type\":\"step_start\",\"sessionID\":%q,\"part\":{\"type\":\"step-start\"}}\n", opencode1895LiveNativeID)
		if resumed {
			fmt.Printf("{\"type\":\"text\",\"sessionID\":%q,\"part\":{\"type\":\"text\",\"text\":%q}}\n", opencode1895LiveNativeID, opencode1895LiveAnswer)
		} else {
			if slices.Contains(os.Args, "--fixture-mode=live_followup_text") {
				fmt.Printf("{\"type\":\"text\",\"sessionID\":%q,\"part\":{\"type\":\"text\",\"text\":%q}}\n", opencode1895LiveNativeID, opencode1895FirstAnswer)
			}
			fmt.Println(`{"type":"tool_use","part":{"type":"tool","id":"fixture-ready-1895","tool":"read_file","state":{"status":"completed","input":{"path":"fixture.txt"},"output":"ready"}}}`)
		}
	}
	emitted := !resumed || !holdOutput
	if emitted {
		emit()
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
			reply := "live-process-still-paused"
			if !emitted {
				reply = "live-process-paused-before-output"
			}
			if _, err = fmt.Fprintln(conn, reply); err != nil {
				return 2
			}
		case "answer\n":
			if !resumed || emitted {
				fmt.Fprintln(os.Stderr, "fixture: answer release requires a silent resumed run")
				return 2
			}
			emit()
			emitted = true
		case "finish\n":
			if resumed && emitted {
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
