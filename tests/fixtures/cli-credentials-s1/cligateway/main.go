// Test-build entry point for S136/S146. CLI/gateway assembly and exit codes
// are app.Main, exactly the public entry point used by cmd/omnipus/main.go.
// Only the external gh process and the exported transaction test seam vary.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/elicify-ai/omnipus/pkg/app"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

func main() {
	if name := filepath.Base(os.Args[0]); name == "gh" || name == "gh.exe" {
		os.Exit(accountCheck())
	}
	if dir := os.Getenv("S1_CLI_OBSERVE_DIR"); dir != "" {
		credentials.TransactHook = func(stage string) {
			if stage == "admission" || stage == "after-read" {
				if err := os.WriteFile(filepath.Join(dir, stage), []byte("observed"), 0600); err != nil {
					fmt.Fprintln(os.Stderr, "fixture transaction observer:", err)
					os.Exit(91)
				}
			}
			if stage != "after-read" || os.Getenv("S1_CLI_HOLD") != "true" {
				return
			}
			deadline := time.Now().Add(30 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(dir, "release")); err == nil {
					return
				}
				if time.Now().After(deadline) {
					fmt.Fprintln(os.Stderr, "fixture inside-read barrier was not released")
					os.Exit(92)
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
	}
	app.Main()
}

func accountCheck() int {
	if len(os.Args) != 5 || os.Args[1] != "api" || os.Args[2] != "user" || os.Args[3] != "--jq" || os.Args[4] != ".login" {
		return 93
	}
	exe, err := os.Executable()
	if err != nil {
		return 94
	}
	starts, err := os.OpenFile(filepath.Join(filepath.Dir(exe), "gh-starts"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return 95
	}
	_, writeErr := starts.WriteString("started\n")
	closeErr := starts.Close()
	if writeErr != nil || closeErr != nil {
		return 96
	}
	switch os.Getenv("GH_TOKEN") {
	case "tok_valid_12345":
		fmt.Println("octocat")
	case "tok_new_67890":
		fmt.Println("hubot")
	default:
		fmt.Fprintln(os.Stderr, "HTTP 401: Bad credentials")
		return 1
	}
	return 0
}
