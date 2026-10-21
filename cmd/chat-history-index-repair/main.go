// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// The standalone repair command is deliberately nonfunctional in Step 0. It
// must never look like a successful dry-run, verification or publication.
package main

import (
	"log"
	"os"

	"github.com/elicify-ai/omnipus/pkg/session"
)

func main() {
	log.New(os.Stderr, "chat-history-index-repair: ", 0).Fatal(session.ErrChatHistoryNotImplemented)
}
