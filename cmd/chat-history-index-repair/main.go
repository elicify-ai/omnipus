// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// An operator-built command only. It is never wired into the product CLI,
// installers, gateway startup or any agent tool catalog.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := runRepair(ctx, os.Args[1:], os.Stdout)
	stop()
	os.Exit(code)
}
