// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package ctxkey defines typed context keys shared between the gateway package
// and its sub-packages (e.g. middleware). Keeping them in a dedicated leaf
// package avoids circular imports while guaranteeing key identity (context
// lookup uses the exact concrete type, so the key type must be shared).
package ctxkey

// UserContextKey is the context key for storing the authenticated *config.UserConfig.
type UserContextKey struct{}

// ConfigContextKey stores a snapshotted *config.Config in the request context
// so all handlers within a single request see a consistent config even during
// hot-reload.
type ConfigContextKey struct{}
