package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// This follows wu24VersionBinary's portable process-edge fixture pattern. Only
// a uniquely named executable copy invoked as a CLI enters this branch: the
// ordinary runner.test process always goes through normal test collection.
func init() {
	name := filepath.Base(os.Args[0])
	if name != "oc226-r2-process" && name != "oc226-r2-process.exe" {
		return
	}
	if len(os.Args) < 2 || (os.Args[1] != "--version" && os.Args[1] != "run") {
		return
	}
	os.Exit(opencode226R2ServeProcess())
}

// The sidecar is test-fixture configuration, not a gateway/SPA wire format.
// Keep it beside the executable: version probing really scrubs the environment.
type opencode226R2ProcessConfig struct {
	VersionOutput string
	Stdout        []byte
	ExitCode      int
}

func opencode226R2ServeProcess() int {
	dir := filepath.Dir(os.Args[0])
	data, err := os.ReadFile(filepath.Join(dir, "fixture.json"))
	var cfg opencode226R2ProcessConfig
	if err == nil {
		err = json.Unmarshal(data, &cfg)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "OpenCode fixture configuration:", err)
		return 2
	}
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		if _, err := fmt.Fprintln(os.Stdout, cfg.VersionOutput); err != nil {
			return 2
		}
		return 0
	}
	log, err := os.OpenFile(filepath.Join(dir, "argv.ndjson"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "OpenCode fixture argv log:", err)
		return 2
	}
	encodeErr := json.NewEncoder(log).Encode(os.Args[1:])
	closeErr := log.Close()
	if encodeErr != nil || closeErr != nil {
		fmt.Fprintln(os.Stderr, "OpenCode fixture argv write:", encodeErr, closeErr)
		return 2
	}
	if _, err := os.Stdout.Write(cfg.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "OpenCode fixture replay:", err)
		return 2
	}
	return cfg.ExitCode
}

func opencode226R2Stub(t *testing.T, stdout []byte, exitCode int, version string) (RunOptions, string) {
	t.Helper()
	dir := t.TempDir()
	name := "oc226-r2-process"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve runner test executable: %v", err)
	}
	source, err := os.Open(executable)
	if err != nil {
		t.Fatalf("open process fixture source: %v", err)
	}
	defer func() { _ = source.Close() }()
	binary := filepath.Join(dir, name)
	destination, err := os.OpenFile(binary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatalf("create process fixture: %v", err)
	}
	_, copyErr := io.Copy(destination, source)
	closeErr := destination.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("copy process fixture: copy=%v close=%v", copyErr, closeErr)
	}
	data, err := json.Marshal(opencode226R2ProcessConfig{VersionOutput: version, Stdout: stdout, ExitCode: exitCode})
	if err != nil {
		t.Fatalf("encode process fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture.json"), data, 0o600); err != nil {
		t.Fatalf("write process fixture: %v", err)
	}
	return RunOptions{
		CLIPath: binary, WorkDir: dir, RunID: "oc226-r2", Input: "fixture task",
		// Two is the longest supplied capture's step count, not a hidden default.
		MaxTurns: 2,
	}, filepath.Join(dir, "argv.ndjson")
}

func opencode226R2Read(t *testing.T, folder, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", folder, name))
	if err != nil {
		t.Fatalf("read OpenCode capture %q: %v", name, err)
	}
	return data
}

func opencode226R2Context(t *testing.T) context.Context {
	t.Helper()
	// A fixture liveness guard only; elapsed time is never a behavior oracle.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func opencode226R2Run(t *testing.T, driver *OpencodeDriver, opts RunOptions) []RunEvent {
	t.Helper()
	ctx := opencode226R2Context(t)
	t.Cleanup(driver.Cancel)
	ch, err := driver.Run(ctx, opts)
	if err != nil {
		t.Fatalf("real OpencodeDriver.Run with process fixture: %v", err)
	}
	return opencode226R2Collect(t, ctx, ch, opts.RunID)
}

func opencode226R2Collect(t *testing.T, ctx context.Context, ch <-chan RunEvent, runID string) []RunEvent {
	t.Helper()
	var events []RunEvent
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			if ev.RunID != runID {
				t.Errorf("%s event run id = %q, want %q", ev.Kind, ev.RunID, runID)
			}
			events = append(events, ev)
		case <-ctx.Done():
			t.Fatalf("process fixture did not close the public event channel: %v", ctx.Err())
		}
	}
}

func opencode226R2Argv(t *testing.T, path string) [][]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read actual CLI invocations: %v", err)
	}
	var calls [][]string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		var args []string
		if err := json.Unmarshal(scanner.Bytes(), &args); err != nil {
			t.Fatalf("decode actual CLI invocation: %v", err)
		}
		calls = append(calls, args)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan actual CLI invocations: %v", err)
	}
	return calls
}
