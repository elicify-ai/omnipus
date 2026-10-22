// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package channels

import (
	"context"
	"errors"
	"net"
)

// User-visible pairing messages a chat card and Connectors show verbatim
// (gate r4; texts agreed with chief for frontend-lead). They never carry ids,
// paths or raw platform text.
const (
	// PairingInterruptedMessage ends an active pairing a configuration change,
	// disable or reload cut off.
	PairingInterruptedMessage = "This pairing was interrupted because the WhatsApp connection was changed or restarted. " +
		"Press Retry to get a new code."
	// PairingStopPendingMessage reports a Stop whose teardown has not finished.
	PairingStopPendingMessage = "Stopping this pairing has not finished yet. Omnipus keeps trying; " +
		"a new pairing can start once it has stopped."
	// PairingAlreadyLinkedMessage reports an admitted start whose device was
	// already linked: there is nothing to pair.
	PairingAlreadyLinkedMessage = "This WhatsApp device is already linked."

	pairingStartFailedLocalData = "WhatsApp could not open its local session data. " +
		"Check that the Omnipus data directory is readable and writable, then press Retry."
	pairingStartFailedNetwork = "WhatsApp could not be reached. Check the network connection, then press Retry."
	pairingStartFailedOther   = "The WhatsApp pairing could not start. Details are in the server log. Press Retry to try again."
)

// PairingStartFailureMessage turns a pairing start failure into its
// value-free cause and repair (gate r4 T2-SF-16): local session data, the
// network, or anything else (the server log names it).
func PairingStartFailureMessage(err error) string {
	var netErr net.Error
	switch {
	case errors.Is(err, ErrLocalState):
		return pairingStartFailedLocalData
	case errors.As(err, &netErr), errors.Is(err, context.DeadlineExceeded):
		return pairingStartFailedNetwork
	default:
		return pairingStartFailedOther
	}
}
