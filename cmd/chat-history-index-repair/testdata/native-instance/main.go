// Native child for the repair CLI acceptance tests. This is not a product
// command, process-probe mock or stopped-state override.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/daemon"
)

func run(input io.Reader, output, diagnostic io.Writer, args []string) int {
	flags := flag.NewFlagSet("native-instance-fixture", flag.ContinueOnError)
	flags.SetOutput(diagnostic)
	root := flags.String("data-root", "", "owned canonical fixture root")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || !filepath.IsAbs(*root) {
		fmt.Fprintln(diagnostic, "fixture requires one absolute --data-root")
		return 2
	}
	if err := daemon.WritePID(*root, os.Getpid()); err != nil {
		fmt.Fprintln(diagnostic, err)
		return 1
	}
	if _, err := fmt.Fprintf(output, "READY pid=%d\n", os.Getpid()); err != nil {
		daemon.RemovePID(*root)
		fmt.Fprintln(diagnostic, err)
		return 1
	}
	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil {
		daemon.RemovePID(*root)
		fmt.Fprintln(diagnostic, "fixture control read:", err)
		return 1
	}
	switch strings.TrimSuffix(line, "\n") {
	case "stop":
		daemon.RemovePID(*root)
		if _, err := os.Stat(daemon.PIDPath(*root)); !os.IsNotExist(err) {
			fmt.Fprintln(diagnostic, "fixture clean stop did not remove PID:", err)
			return 1
		}
		return 0
	case "crash":
		return 86 // L/D13/G pins an actual unclean exit with the stale PID intact.
	default:
		daemon.RemovePID(*root)
		fmt.Fprintln(diagnostic, "unknown fixture control command")
		return 2
	}
}

func main() {
	os.Exit(run(os.Stdin, os.Stdout, os.Stderr, os.Args[1:]))
}
