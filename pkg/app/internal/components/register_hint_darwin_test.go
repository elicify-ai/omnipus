// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build darwin

package components_test

// T-129 macOS twin (office-preview spec §3.8 BR-2.7; B-88): on macOS the
// BR-2.7 restart hint is never printed — not even under the confined model.
// This is a CONTROL: it passes on the pre-change code (no hint exists yet)
// and must still pass once the Linux hint lands, proving the hint is gated
// to Linux rather than sprayed from shared code.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/app/internal/components"
	"github.com/stretchr/testify/require"
)

// t129DarwinRDB services and plist literals match the darwin fixtures of
// pkg/preview/component's packaged-manifest contract tests: the services
// registry is real converter-registry XML the AD-3 filter accepts, the plist
// carries CFBundleShortVersionString 26.8.1.
const (
	t129DarwinRDBFiltered = `<components xmlns="http://openoffice.org/2010/uno-components"><component loader="com.sun.star.loader.SharedLibrary" uri="vnd.sun.star.expand:$LO_LIB_DIR/libmergedlo.dylib"><implementation name="org.openoffice.lingu.MacOSXSpellChecker" constructor="lingucomponent_MacSpellChecker_get_implementation" single-instance="true"><service name="com.sun.star.linguistic2.SpellChecker"/></implementation><implementation name="org.openoffice.lingu.MySpellSpellChecker" constructor="lingucomponent_MySpellChecker_get_implementation"><service name="com.sun.star.linguistic2.SpellChecker"/></implementation></component></components>`
	t129DarwinPlist       = `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>CFBundleShortVersionString</key><string>26.8.1</string></dict></plist>`
)

func TestT129_DarwinRegisterPrintsNoHintEvenWhenConfined(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_CONFIG", filepath.Join(home, "config.json"))
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"),
		[]byte(`{"version":1,"sandbox":{"filesystem_model":"confined"}}`), 0o600))

	// The install folder holding LibreOffice.app (locateOffice accepts both
	// spellings; the command records the .app as the root).
	folder := t.TempDir()
	contents := filepath.Join(folder, "LibreOffice.app", "Contents")
	require.NoError(t, os.MkdirAll(filepath.Join(contents, "MacOS"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(contents, "Resources", "services"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(contents, "MacOS", "soffice"), []byte("t129 fixture program"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(t129DarwinPlist), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(contents, "Resources", "services", "services.rdb"), []byte(t129DarwinRDBFiltered), 0o644))
	app, err := filepath.EvalSymlinks(filepath.Join(folder, "LibreOffice.app"))
	require.NoError(t, err)

	var out, errOut bytes.Buffer
	cmd := components.NewOfficeCommand()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"register", folder})
	require.NoError(t, cmd.Execute(), "T-129 darwin twin: the registration itself must succeed")

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	require.Len(t, lines, 1, "T-129 darwin twin: exactly the Registered line; got %q", out.String())
	require.Equal(t, "Registered office 26.8.1 from "+app, lines[0])
	require.FileExists(t, filepath.Join(home, "system", "components", "office", "manifest.json"))
	require.Empty(t, errOut)
}
