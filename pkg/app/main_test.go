//go:build goolm && stdjson

package app

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #877: Main used to bind the command error and drop it — os.Exit(1)
// with nothing printed by this function. runMain now owns the single stderr
// print (the root command sets SilenceErrors, so cobra's own print is off)
// and maps the command result to the process exit code. A failing command
// must surface its error text and exit 1.

func TestRunMainPrintsErrorAndReturnsOne(t *testing.T) {
	var stderr bytes.Buffer
	cmd := &cobra.Command{
		Use: "boom",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("boom: config unreadable")
		},
	}
	cmd.SetErr(&stderr)

	code := runMain(context.Background(), cmd)

	require.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "boom: config unreadable")
}

func TestRunMainCleanRunReturnsZero(t *testing.T) {
	var stderr bytes.Buffer
	cmd := &cobra.Command{
		Use: "ok",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
	cmd.SetErr(&stderr)

	code := runMain(context.Background(), cmd)

	require.Equal(t, 0, code)
	assert.Empty(t, stderr.String())
}

// The single-print contract: cobra must stay quiet (SilenceErrors on the
// root — cobra applies it to every subcommand) because runMain owns the
// print; otherwise every returned error is printed twice.
func TestNewRootCommandSilencesCobraErrorPrinting(t *testing.T) {
	root := NewRootCommand()
	assert.True(t, root.SilenceErrors, "root must set SilenceErrors: runMain owns the error print (issue #877)")
	assert.True(t, root.SilenceUsage, "root must keep SilenceUsage")
}
