// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

// HistoryPosition names the opaque occurrence-token type. Parsing and encoding
// remain explicitly unavailable until the D2 implementation is supplied.
type HistoryPosition string

func ParseHistoryPosition(raw string) (HistoryPosition, error) {
	return "", ErrChatHistoryNotImplemented
}

func historyPositionForAddress(sessionID string, addr ArchiveAddress) (HistoryPosition, error) {
	return "", ErrChatHistoryNotImplemented
}
