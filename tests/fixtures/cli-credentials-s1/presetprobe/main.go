// Test-build probe: all product behavior remains in pkg/programs. The compiler
// overlay adds only example.json to the shipped embed directory, no Go change.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/programs"
)

func main() {
	if err := probe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func probe() error {
	if filepath.Base(os.Args[0]) == "exampletool" || filepath.Base(os.Args[0]) == "exampletool.exe" {
		if len(os.Args) != 2 || os.Args[1] != "whoami" || os.Getenv("EXAMPLE_TOKEN") != "example_tok_123456" {
			return fmt.Errorf("fixture: actual recipe argv/env differed")
		}
		fmt.Println("ex-user")
		return nil
	}
	entries, err := programs.Presets()
	if err != nil {
		return err
	}
	home, err := os.MkdirTemp("", "s1-preset-probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(home)
	bin := filepath.Join(home, "bin")
	if err = os.Mkdir(bin, 0700); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	body, err := os.ReadFile(exe)
	if err != nil {
		return err
	}
	name := "exampletool"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err = os.WriteFile(filepath.Join(bin, name), body, 0700); err != nil {
		return err
	}
	if err = os.Setenv("PATH", bin); err != nil {
		return err
	}
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	defer store.Close()
	if err = store.UnlockWithKey(bytes.Repeat([]byte{0x52}, 32)); err != nil {
		return err
	}
	cfg := &config.Config{}
	svc := programs.NewService(func() *config.Config { return cfg }, func() (*credentials.Store, error) { return store, nil }, filepath.Join(home, "connections.json"))
	_, err = svc.Publish("example", "example_tok_123456", nil, nil)
	if err != nil {
		return err
	}
	svc.Wait()
	// Test-only probe output, not a gateway wire format. The tags repeat the
	// field names, so the keys are the ones the untagged fields already had.
	return json.NewEncoder(os.Stdout).Encode(struct {
		Entries    []programs.ProgramEntry `json:"Entries"`
		Connection programs.Connection     `json:"Connection"`
	}{entries, svc.Connection("example")})
}
