// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package programs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Minimum git that honours GIT_CONFIG_COUNT/KEY/VALUE (FR-014).
const (
	minGitMajor = 2
	minGitMinor = 31
)

// GitEnv returns the environment-only git configuration of the managed
// logins (FR-013): per host, an empty `credential.https://<host>.helper`
// (which empties the helper list collected so far, so no saved login answers),
// then a fixed get-only helper that prints the username and reads the token
// from the entry's first variable. The token is never in configuration text,
// a file or a command line; store and erase are ignored. Nil when there is no
// login.
func (p *TurnPrograms) GitEnv() []string {
	if p == nil || len(p.gitLogins) == 0 {
		return nil
	}
	var keys, values []string
	for _, l := range p.gitLogins {
		key := "credential.https://" + l.Host + ".helper"
		keys = append(keys, key, key)
		values = append(values, "", gitHelper(l))
	}
	env := []string{"GIT_CONFIG_COUNT=" + strconv.Itoa(len(keys))}
	for i := range keys {
		env = append(env,
			fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, keys[i]),
			fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, values[i]))
	}
	return env
}

// gitHelper is the fixed get-only helper. The username matches
// ^[A-Za-z0-9._@-]{1,64}$ and the variable name is a validated environment
// name, so neither needs quoting beyond the single quotes and "${...}" used.
func gitHelper(l gitLogin) string {
	return `!f() { test "$1" = get || exit 0; printf 'username=%s\npassword=%s\n' '` +
		l.Username + `' "${` + l.Variable + `}"; }; f`
}

// IsGitConfigName reports whether an environment entry name is one of the
// reserved git configuration names (FR-013).
func IsGitConfigName(name string) bool {
	return name == "GIT_CONFIG_COUNT" || name == "GIT_CONFIG_PARAMETERS" ||
		strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_")
}

// StripGitConfig removes inherited GIT_CONFIG_* entries from env and reports
// whether any was removed (God Mode: managed entries replace them).
func StripGitConfig(env []string) ([]string, bool) {
	out := env[:0:0]
	removed := false
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if IsGitConfigName(name) {
			removed = true
			continue
		}
		out = append(out, kv)
	}
	return out, removed
}

// GitTarget names the git the command will run (FR-014): an explicit path
// among its program words, else the first `git` on the child's PATH. found is
// false when there is no git at all. cwd resolves a relative explicit path.
func GitTarget(programWords []string, childPATH, cwd string) (path string, found bool) {
	for _, w := range programWords {
		if ProgramWordName(w) != "git" || !strings.Contains(w, "/") {
			continue
		}
		if !filepath.IsAbs(w) {
			w = filepath.Join(cwd, w)
		}
		if isExecutableFile(w) {
			return w, true
		}
		return "", false
	}
	for _, dir := range filepath.SplitList(childPATH) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, gitExecutableName())
		if isExecutableFile(candidate) {
			return candidate, true
		}
	}
	return "", false
}

func isExecutableFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// gitVersionPattern reads "git version 2.43.0" (and vendor suffixes).
var gitVersionPattern = regexp.MustCompile(`git version (\d+)\.(\d+)(\.[0-9A-Za-z.]+)?`)

// ParseGitVersion returns the version text of `git --version` output and
// whether it supports environment configuration (2.31 or later). ok is false
// when the output names no version.
func ParseGitVersion(out string) (version string, supported, ok bool) {
	m := gitVersionPattern.FindStringSubmatch(out)
	if m == nil {
		return "", false, false
	}
	major, err1 := strconv.Atoi(m[1])
	minor, err2 := strconv.Atoi(m[2])
	if err1 != nil || err2 != nil {
		return "", false, false
	}
	version = m[1] + "." + m[2] + m[3]
	return version, major > minGitMajor || (major == minGitMajor && minor >= minGitMinor), true
}

// GitProbe runs `<git> --version` as an ordinary child of the command's turn
// and sandbox and returns its (masked) standard output.
type GitProbe func(gitPath string) (string, error)

// gitVersionCache holds probe results per (path, size, mtime) (FR-014).
var gitVersionCache = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

func gitCacheKey(path string) (string, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("%s\x00%d\x00%d", path, st.Size(), st.ModTime().UnixNano()), true
}

// GitSupport decides the managed git entries for one command (FR-014): the
// GitEnv when the target git is 2.31 or later or there is no git, nothing
// plus the old-git notice when it is older. A probe that fails or prints no
// version adds no entries and reports the failure (never silently).
func (p *TurnPrograms) GitSupport(programWords []string, childPATH, cwd string, probe GitProbe) (env []string, notice string, err error) {
	if !p.HasGitLogins() {
		return nil, "", nil
	}
	path, found := GitTarget(programWords, childPATH, cwd)
	if !found {
		return p.GitEnv(), "", nil
	}
	key, keyed := gitCacheKey(path)
	var out string
	if keyed {
		gitVersionCache.Lock()
		out = gitVersionCache.m[key]
		gitVersionCache.Unlock()
	}
	if out == "" {
		if probe == nil {
			return nil, "", fmt.Errorf("programs: no git version probe for %s", path)
		}
		out, err = probe(path)
		if err != nil {
			return nil, "", fmt.Errorf("programs: git version probe: %w", err)
		}
	}
	version, supported, ok := ParseGitVersion(out)
	if !ok {
		return nil, "", fmt.Errorf("programs: git version probe printed no version")
	}
	if keyed {
		gitVersionCache.Lock()
		gitVersionCache.m[key] = out
		gitVersionCache.Unlock()
	}
	if !supported {
		return nil, fmt.Sprintf(msgOldGit, version), nil
	}
	return p.GitEnv(), "", nil
}
