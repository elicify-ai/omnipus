// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// MachinePrincipalUsername is the synthetic identity the gateway gives a
// caller authenticated with the machine-only Gateway.CLIToken. It is not a
// configured account (registration reserves the name), so it is never a
// person to attribute anything to.
const MachinePrincipalUsername = "cli"

// OperatorUsername returns the username of the install's operator: the first
// configured account in Gateway.Users with a non-empty username (a legacy
// "cli" row is skipped). It returns "" when no account is configured or c is
// nil. Used to attribute work no person started
// (system-initiated main sessions) to the person running the install.
func (c *Config) OperatorUsername() string {
	if c == nil {
		return ""
	}
	for _, u := range c.Gateway.Users {
		if u.Username != "" && u.Username != MachinePrincipalUsername {
			return u.Username
		}
	}
	return ""
}

// MainOwnerFor returns the owner to record on a main session acted on by
// actor (an authenticated username, or "" when no person is acting): the
// actor itself when it is a person, otherwise the operator. The machine
// principal "cli" is treated as a system actor, never stamped as an owner.
// Safe on a nil *Config (no operator then).
func (c *Config) MainOwnerFor(actor string) string {
	if actor != "" && actor != MachinePrincipalUsername {
		return actor
	}
	return c.OperatorUsername()
}
