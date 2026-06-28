//go:build cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — CGO stub for NewGatewayCommand.
//
// The gateway package (pkg/gateway) is entirely //go:build !cgo because the
// project requires a pure-Go binary (CLAUDE.md hard-constraint #2). Under
// CGO_ENABLED=1 (e.g. race-detector builds, goolm/whatsapp_native builds) the
// pkg/gateway package is excluded, so command.go's //go:build !cgo variant is
// also excluded, leaving NewGatewayCommand undefined and breaking cmd/omnipus.
//
// This stub provides a compile-time placeholder so that CGO_ENABLED=1 builds
// link without errors. The command panics at runtime with a clear message if
// someone accidentally invokes it — in practice, all production and release
// builds set CGO_ENABLED=0.

package gateway

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// NewGatewayCommand returns a gateway sub-command stub that explains the
// CGO_ENABLED constraint and exits non-zero. It is only compiled when
// CGO_ENABLED=1 (i.e. when the real gateway is excluded by build constraints).
func NewGatewayCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "start",
		Aliases: []string{"gateway", "g"},
		Short:   "Start Omnipus (unavailable in CGO builds)",
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Fprintln(os.Stderr,
				"Error: the gateway command is unavailable in CGO-enabled builds.\n"+
					"Rebuild with CGO_ENABLED=0 to enable the gateway.",
			)
			os.Exit(1)
			return nil
		},
	}
}
