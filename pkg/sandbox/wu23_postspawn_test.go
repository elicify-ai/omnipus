//go:build linux || darwin

package sandbox

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fixture "github.com/elicify-ai/omnipus/tests/l1_red/protectedfixture"
)

func wu23PostSpawn(t *testing.T, wantDenied bool) {
	f := fixture.New(t)
	policy := wu23KernelPolicy(t, f, f.Work)
	future := filepath.Join(f.Home, "post-spawn-unclassified", "value.txt")
	cmd := wu23KernelCommand(t, policy, []string{"/bin/sh", "-c", `cat "$1" >/dev/null || exit 90
printf control > "$4/control-after-spawn.txt" || exit 91
if cat "$2" >/dev/null 2>/dev/null; then printf 'EXISTING=reachable\n'; else printf 'EXISTING=denied\n'; fi
printf 'READY\n'
read ready || exit 92
if cat "$3" >/dev/null 2>/dev/null; then printf 'FUTURE=reachable\n'; else printf 'FUTURE=denied\n'; fi`, "wu23", filepath.Join(f.Work, "allowed.txt"), filepath.Join(f.Home, "sessions", "private.txt"), future, f.Work})
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if startErr := cmd.Start(); startErr != nil {
		t.Fatal(startErr)
	}
	reader := bufio.NewReader(stdout)
	existing, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("child never produced existing probe: %v stderr=%s", err, stderr.String())
	}
	ready, err := reader.ReadString('\n')
	if err != nil || ready != "READY\n" {
		t.Fatalf("same-child control/ready handshake failed: got %q err=%v", ready, err)
	}
	fixture.Write(t, future, fixture.Sentinel)
	if _, writeErr := io.WriteString(stdin, "go\n"); writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr := stdin.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child failed: %v stderr=%s", err, stderr.String())
	}
	t.Run("positive_control", func(t *testing.T) {
		b, err := os.ReadFile(filepath.Join(f.Work, "control-after-spawn.txt"))
		if err != nil || string(b) != "control" {
			t.Fatalf("same-child control bytes=%q err=%v", b, err)
		}
	})
	if existing != "EXISTING=denied\n" {
		t.Errorf("D2: existing sessions file was reachable before handshake: %q", existing)
	}
	want := "FUTURE=reachable"
	if wantDenied {
		want = "FUTURE=denied"
	}
	if strings.TrimSpace(string(out)) != want {
		t.Errorf("D5/D8 %s post-spawn entry: got %q want %q", wu23Platform(), out, want)
	}
	t.Logf("documented post-spawn outcome: %s", strings.TrimSpace(string(out)))
}
