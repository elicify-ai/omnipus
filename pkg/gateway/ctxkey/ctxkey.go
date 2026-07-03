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

// CLITokenContextKey marks a request as authenticated via the machine-only
// Gateway.CLIToken credential rather than a real Gateway.Users account row.
// checkBearerAuth and withOptionalAuth set this to true (via
// context.WithValue) specifically in their Gateway.CLIToken success branch,
// never in the Gateway.Users loop branch. (authenticateWS's WebSocket
// equivalent is the wsConn.isCLIToken bool field — a WS connection has no
// per-request context to carry this key into.) The synthetic
// UserConfig{Username: "cli"} those branches also inject under
// UserContextKey is NOT backed by any Gateway.Users row, so any handler that
// treats UserContextKey's Username as a key into Gateway.Users (e.g.
// HandleLogout's token-revocation lookup) must check this key first and
// take a different path — see HandleLogout in pkg/gateway/rest_auth.go.
type CLITokenContextKey struct{}

// ConfigContextKey stores a snapshotted *config.Config in the request context
// so all handlers within a single request see a consistent config even during
// hot-reload.
type ConfigContextKey struct{}
