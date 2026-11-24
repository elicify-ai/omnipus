// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !s1seams

package testseam

import "io"

// EgressTokenMinted marks the entry of an egress-token mint. No-op here.
func EgressTokenMinted(string) {}

// CaptureCap returns the foreground output cap unchanged.
func CaptureCap(_ string, capBytes int) int { return capBytes }

// CaptureWriter returns the foreground output buffer unchanged.
func CaptureWriter(_ string, w io.Writer) io.Writer { return w }

// BackgroundPipeReader returns the background pipe's read end unchanged.
func BackgroundPipeReader(_, _ string, r *io.PipeReader) io.Reader { return r }

// BeforeCheckLaunch marks the start of a program account-check launch. No-op.
func BeforeCheckLaunch(string, int64) {}
