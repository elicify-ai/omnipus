// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package programs

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/childenv"
)

// Token value bounds (FR-003, dataset D-1).
const (
	MinTokenBytes = 8
	MaxTokenBytes = 16384
)

// Scope boundaries (spec "Machine-Verifiable Constraints").
const (
	MaxEntries         = 32
	MaxDisplayNameLen  = 64
	MaxProgramWords    = 8
	MaxTokenVariables  = 4
	MaxNetworkSites    = 32
	MaxCheckArgs       = 16
	MaxCheckArgLen     = 256
	MaxProgramsOnAgent = 32
	DefaultGitUsername = "x-access-token"
)

// ValidationError is a refused program input. Conflict marks the 409 class
// (duplicate name, collection full, stale revision); everything else is the
// 400 class.
type ValidationError struct {
	Conflict bool
	Message  string
}

func (e *ValidationError) Error() string { return e.Message }

func invalid(format string, args ...any) error {
	return &ValidationError{Message: fmt.Sprintf(format, args...)}
}

func conflict(format string, args ...any) error {
	return &ValidationError{Conflict: true, Message: fmt.Sprintf(format, args...)}
}

// IsConflict reports whether err is a 409-class validation error.
func IsConflict(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve) && ve.Conflict
}

// ValidateTokenValue enforces the program token rules (FR-003, dataset D-1):
// surrounding whitespace trimmed, then 8–16,384 bytes, inner line breaks and
// NUL refused, empty refused. It returns the trimmed value to store.
func ValidateTokenValue(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	switch {
	case v == "":
		return "", invalid("Enter a token.")
	case strings.ContainsRune(v, 0):
		return "", invalid("The token contains a character that cannot be stored.")
	case strings.ContainsAny(v, "\r\n"):
		return "", invalid("The token contains a line break. Paste the token on one line.")
	case len(v) < MinTokenBytes:
		return "", invalid("The token is too short. A token has at least 8 characters.")
	case len(v) > MaxTokenBytes:
		return "", invalid("The token is too long. A token has at most 16,384 bytes.")
	}
	return v, nil
}

var gitUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)

// hostLabel is one DNS label: letters, digits and inner hyphens.
var hostLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// canonicalHost lower-cases a host and drops one trailing dot, refusing
// schemes, ports, paths, IP addresses and anything that is not a host name.
func canonicalHost(raw string) (string, error) {
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	switch {
	case h == "":
		return "", invalid("A site is empty. Enter a host name such as api.example.com.")
	case strings.Contains(h, "://"):
		return "", invalid("%s has a scheme. Enter only the host name, such as api.example.com.", raw)
	case strings.ContainsAny(h, ":/"):
		return "", invalid("%s has a port or a path. Enter only the host name.", raw)
	case net.ParseIP(h) != nil:
		return "", invalid("%s is an IP address. Enter a host name.", raw)
	case len(h) > 253:
		return "", invalid("%s is too long for a host name.", raw)
	}
	labels := strings.Split(h, ".")
	if len(labels) < 2 {
		return "", invalid("%s is not a full host name.", raw)
	}
	for _, l := range labels {
		if !hostLabel.MatchString(l) {
			return "", invalid("%s is not a valid host name.", raw)
		}
	}
	return h, nil
}

// canonicalSite validates one network site: a host name or a `*.` wildcard
// over a host name. A bare `*` is refused.
func canonicalSite(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "*.") {
		h, err := canonicalHost(s[2:])
		if err != nil {
			return "", err
		}
		return "*." + h, nil
	}
	if strings.Contains(s, "*") {
		return "", invalid("%s is not allowed. A wildcard must be written as *.example.com.", raw)
	}
	return canonicalHost(s)
}

// ValidateGit canonicalises and validates one git section (contract
// ProgramGit): host canonical — lower-case, no trailing dot, no port or
// scheme; username ^[A-Za-z0-9._@-]{1,64}$, default x-access-token.
func ValidateGit(git ProgramGit) (ProgramGit, error) {
	host, err := canonicalHost(git.Host)
	if err != nil {
		return ProgramGit{}, err
	}
	user := git.Username
	if user == "" {
		user = DefaultGitUsername
	}
	if !gitUsernamePattern.MatchString(user) {
		return ProgramGit{}, invalid("The git username %q is not allowed: use 1 to 64 letters, digits, '.', '_', '@' or '-'.", user)
	}
	return ProgramGit{Host: host, Username: user}, nil
}

// bannedProgramWords are shells and wrappers: a program word must name the
// program itself (Scope Boundaries).
var bannedProgramWords = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "fish": true, "env": true, "sudo": true,
	"doas": true, "command": true, "exec": true, "nohup": true, "timeout": true, "xargs": true,
	"nice": true, "time": true, "setsid": true, "stdbuf": true,
}

var programWordPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

// ValidateCustomRequest validates a create or edit of a custom entry against
// dataset D-2 and the scope boundaries (FR-019). existing is every OTHER
// entry of the catalog (presets and custom; for an edit, without the entry
// being edited). A duplicate display name or a full collection is a 409-class
// error; every other refusal is 400-class. On success the request's git and
// identity check are replaced by their canonical forms.
func ValidateCustomRequest(req CustomRequest, existing []ProgramEntry) error {
	return validateCustom(&req, existing)
}

// NormalizeCustomRequest is ValidateCustomRequest that also returns the
// canonical request (canonical git host, default username, default check
// timeout and output cap, canonical sites).
func NormalizeCustomRequest(req CustomRequest, existing []ProgramEntry) (CustomRequest, error) {
	err := validateCustom(&req, existing)
	return req, err
}

func validateCustom(req *CustomRequest, existing []ProgramEntry) error {
	if err := validateFields(req); err != nil {
		return err
	}
	name := strings.TrimSpace(req.DisplayName)
	for _, e := range existing {
		if strings.EqualFold(strings.TrimSpace(e.DisplayName), name) || strings.EqualFold(e.ID, name) {
			return conflict("The name %s is already used by %s. Choose another name.", name, e.DisplayName)
		}
	}
	// The cap counts every entry in Settings → Programs, presets included
	// (scope boundary "≤ 32 entries", D-2 row 31).
	if len(existing) >= MaxEntries {
		return conflict("Settings → Programs already has %d programs, the most allowed. Delete one first.", MaxEntries)
	}
	return nil
}

// validateFields applies every per-field rule of dataset D-2 and normalises
// the request in place.
func validateFields(req *CustomRequest) error {
	name := strings.TrimSpace(req.DisplayName)
	if name == "" {
		return invalid("Enter a name for the program.")
	}
	if utf8.RuneCountInString(name) > MaxDisplayNameLen {
		return invalid("The name is too long. A name has at most %d characters.", MaxDisplayNameLen)
	}
	req.DisplayName = name
	if err := validateWords(req.ProgramWords); err != nil {
		return err
	}
	if err := validateVariables(req.TokenVariables); err != nil {
		return err
	}
	sites, err := validateSites(req.NetworkSites)
	if err != nil {
		return err
	}
	req.NetworkSites = sites
	if req.Git != nil {
		g, err := ValidateGit(*req.Git)
		if err != nil {
			return err
		}
		req.Git = &g
	}
	if req.IdentityCheck != nil {
		c, err := validateCheck(*req.IdentityCheck, req.ProgramWords)
		if err != nil {
			return err
		}
		req.IdentityCheck = &c
	}
	return nil
}

func validateWords(words []string) error {
	if len(words) == 0 {
		return invalid("Enter at least one program name, such as acme.")
	}
	if len(words) > MaxProgramWords {
		return invalid("A program has at most %d program names.", MaxProgramWords)
	}
	seen := map[string]bool{}
	for _, w := range words {
		switch {
		case strings.ContainsAny(w, `/\`):
			return invalid("%s is a path. Enter only the program name, such as acme.", w)
		case bannedProgramWords[w]:
			return invalid("%s is a shell or a wrapper, not a program. Enter the program's own name.", w)
		case !programWordPattern.MatchString(w):
			return invalid("%q is not a valid program name.", w)
		case seen[w]:
			return invalid("%s is listed twice.", w)
		}
		seen[w] = true
	}
	return nil
}

func validateVariables(vars []string) error {
	if len(vars) == 0 {
		return invalid("Enter at least one token variable, such as ACME_TOKEN.")
	}
	if len(vars) > MaxTokenVariables {
		return invalid("A program has at most %d token variables.", MaxTokenVariables)
	}
	seen := map[string]bool{}
	for _, v := range vars {
		if err := childenv.CheckPrivateVariableName(v); err != nil {
			return invalid("%s", err.Error())
		}
		if seen[v] {
			return invalid("%s is listed twice.", v)
		}
		seen[v] = true
	}
	return nil
}

func validateSites(sites []string) ([]string, error) {
	if len(sites) > MaxNetworkSites {
		return nil, invalid("A program has at most %d network sites.", MaxNetworkSites)
	}
	out := make([]string, 0, len(sites))
	seen := map[string]bool{}
	for _, s := range sites {
		c, err := canonicalSite(s)
		if err != nil {
			return nil, err
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out, nil
}

func validateCheck(c ProgramIdentityCheck, words []string) (ProgramIdentityCheck, error) {
	found := false
	for _, w := range words {
		if w == c.ProgramWord {
			found = true
		}
	}
	if !found {
		return c, invalid("The account check must run one of this program's names, not %q.", c.ProgramWord)
	}
	if len(c.Args) > MaxCheckArgs {
		return c, invalid("The account check has at most %d arguments.", MaxCheckArgs)
	}
	for _, a := range c.Args {
		if len(a) > MaxCheckArgLen {
			return c, invalid("An account check argument has at most %d characters.", MaxCheckArgLen)
		}
		if strings.ContainsRune(a, 0) {
			return c, invalid("An account check argument contains a character that cannot be stored.")
		}
	}
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > MaxCheckTimeoutSeconds {
		return c, invalid("The account check timeout must be 1 to %d seconds.", MaxCheckTimeoutSeconds)
	}
	if c.MaxOutputBytes == 0 {
		c.MaxOutputBytes = CheckMaxOutputBytes
	}
	if c.MaxOutputBytes != CheckMaxOutputBytes {
		return c, invalid("The account check output limit is fixed at %d bytes.", CheckMaxOutputBytes)
	}
	if c.Args == nil {
		c.Args = []string{}
	}
	return c, nil
}
