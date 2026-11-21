// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package components_test

// T-129 Windows twin (office-preview spec §3.8 BR-2.7; B-88): on Windows the
// BR-2.7 restart hint is never printed — not even under the confined model.
// Control twin of register_hint_darwin_test.go; compiles in the GOOS=windows
// vet gate.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/app/internal/components"
	"github.com/stretchr/testify/require"
)

func TestT129_WindowsRegisterPrintsNoHintEvenWhenConfined(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_CONFIG", filepath.Join(home, "config.json"))
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"),
		[]byte(`{"version":1,"sandbox":{"filesystem_model":"confined"}}`), 0o600))

	// A Windows LibreOffice install: program\soffice.exe and soffice.bin,
	// version from program\version.ini.
	folder := t.TempDir()
	program := filepath.Join(folder, "program")
	require.NoError(t, os.MkdirAll(program, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(program, "soffice.exe"), []byte("t129 fixture program"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(program, "soffice.bin"), []byte("t129 fixture program"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(program, "version.ini"), []byte("ProductVersion=26.8.1.1\n"), 0o644))
	root, err := filepath.EvalSymlinks(folder)
	require.NoError(t, err)

	var out, errOut bytes.Buffer
	cmd := components.NewOfficeCommand()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"register", folder})
	require.NoError(t, cmd.Execute(), "T-129 windows twin: the registration itself must succeed")

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	require.Len(t, lines, 1, "T-129 windows twin: exactly the Registered line; got %q", out.String())
	require.Equal(t, "Registered office 26.8.1.1 from "+root, lines[0])
	require.FileExists(t, filepath.Join(home, "system", "components", "office", "manifest.json"))
	require.Empty(t, errOut)
}
