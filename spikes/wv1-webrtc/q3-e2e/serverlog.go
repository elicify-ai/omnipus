package main

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// logBuffer is a small in-memory ring-ish buffer (unbounded for spike
// purposes) exposed at GET /serverlog so the viewer/q1 pages can render the
// server's perspective alongside the browser console. Copied verbatim in
// spirit from q1-connectivity/main.go's logBuffer.
type logBuffer struct {
	mu    sync.Mutex
	lines []string
}

func (lb *logBuffer) Add(format string, args ...interface{}) {
	ts := time.Now().Format("15:04:05.000")
	line := fmt.Sprintf("[%s] %s", ts, fmt.Sprintf(format, args...))
	lb.mu.Lock()
	lb.lines = append(lb.lines, line)
	// Cap at 4000 lines so a long-running demo doesn't grow unbounded.
	if len(lb.lines) > 4000 {
		lb.lines = lb.lines[len(lb.lines)-4000:]
	}
	lb.mu.Unlock()
	log.Println(line)
}

func (lb *logBuffer) Text() string {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	out := ""
	for _, l := range lb.lines {
		out += l + "\n"
	}
	return out
}

var serverLog = &logBuffer{}

var connCounter int64
var connCounterMu sync.Mutex

func nextConnID() int64 {
	connCounterMu.Lock()
	defer connCounterMu.Unlock()
	connCounter++
	return connCounter
}
