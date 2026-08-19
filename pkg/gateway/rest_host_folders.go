// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// maxHostFolderEntries caps one listing. A directory with tens of thousands of
// subdirectories (a node_modules root, a Time Machine volume) would otherwise
// build an enormous response for a picker nobody can scroll. The cap is a
// display concern, not a security one — the operator can always navigate INTO a
// directory to see its own children.
const maxHostFolderEntries = 500

// hostFolderEntryWire is a type ALIAS (not a definition) for the anonymous
// struct oapi-codegen inlines into HostFolderListing.Entries.
//
// The generator inlines any schema referenced across files, so the element type
// has no name of its own. An alias to the identical shape is assignable to it
// while letting this file read in terms of the concept rather than a five-field
// literal repeated at every construction site. It must stay byte-identical to
// the generated shape — `make verify-contracts` fails the build if the contract
// changes without this following.
type hostFolderEntryWire = struct {
	Broad     *bool   `json:"broad,omitempty"`
	Mountable bool    `json:"mountable"`
	Name      string  `json:"name"`
	Path      string  `json:"path"`
	Reason    *string `json:"reason,omitempty"`
}

// HandleSystemFolders handles GET /api/v1/system/folders.
//
// Lists the directories inside a path on the operator's own machine so they can
// pick one to mount into a workspace.
//
// # Why the gateway does this at all
//
// A web page cannot open the native folder picker and learn a real filesystem
// path — the browser deliberately withholds it, and `<input webkitdirectory>`
// yields file contents rather than a location. Without this the only way to add
// a mount is to type an absolute path from memory, in a control whose entire
// job is to be deliberate.
//
// # Why this is not new exposure
//
// Post-ADR-062 reading is open: any agent on this installation can already read
// anywhere on this machine. This hands the OPERATOR the same view their own
// agents have. It is admin-authenticated (withAuth at registration) and is
// deliberately not reachable from any agent tool — an agent that wants a folder
// asks for it and the operator approves, rather than browsing for one itself.
//
// Only directories are returned. A mount target is always a folder, and listing
// files would bury the operator in rows none of which they can pick.
func (a *restAPI) HandleSystemFolders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	requested := strings.TrimSpace(r.URL.Query().Get("path"))
	if requested == "" {
		// A picker should open where the operator's own files are, not at "/".
		home, err := os.UserHomeDir()
		if err != nil {
			jsonErr(w, http.StatusBadRequest, "cannot determine your home directory; pass ?path=")
			return
		}
		requested = home
	}
	if !filepath.IsAbs(requested) {
		jsonErr(w, http.StatusBadRequest, "path must be absolute")
		return
	}

	// Resolve symlinks so the path shown, the path stored, and the path the
	// mount rules are evaluated against are all the same string. Skipping this
	// is how "/tmp" and "/private/tmp" end up disagreeing on macOS.
	resolved, err := filepath.EvalSymlinks(filepath.Clean(requested))
	if err != nil {
		if os.IsNotExist(err) {
			jsonErr(w, http.StatusNotFound, "no such directory")
			return
		}
		jsonErr(w, http.StatusBadRequest, "cannot read that path")
		return
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		jsonErr(w, http.StatusNotFound, "no such directory")
		return
	}
	if !fi.IsDir() {
		jsonErr(w, http.StatusBadRequest, "that path is a file, not a folder")
		return
	}

	dirEntries, err := os.ReadDir(resolved)
	if err != nil {
		// Unreadable (permissions) is reported as an empty listing rather than
		// an error: the operator can still navigate elsewhere, and a hard
		// failure on one protected directory would strand the whole picker.
		dirEntries = nil
	}

	entries := make([]hostFolderEntryWire, 0, len(dirEntries))
	for _, de := range dirEntries {
		if !de.IsDir() || strings.HasPrefix(de.Name(), ".") {
			// Hidden directories are omitted: they are overwhelmingly tooling
			// state, and $OMNIPUS_HOME itself is conventionally one of them —
			// which is refused below in any case, so this is tidiness, not the
			// boundary.
			continue
		}
		full := filepath.Join(resolved, de.Name())
		entries = append(entries, a.classifyHostFolder(full, de.Name()))
		if len(entries) >= maxHostFolderEntries {
			break
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	resp := gen.HostFolderListing{Path: resolved, Entries: entries}
	if parent := filepath.Dir(resolved); parent != resolved {
		resp.Parent = strPtr(parent)
	}
	jsonOK(w, resp)
}

// classifyHostFolder decides, for one candidate directory, whether it may be
// mounted and whether it is a broad grant.
//
// The verdict travels WITH the row so the client can disable the choice at the
// point of selection. Letting the operator pick a folder and only then telling
// them no is a worse control, and it teaches them to ignore the refusal.
//
// The two answers come from the SAME functions the create path uses
// (workspace.CheckMountTarget for the refusal, workspace.IsBroadMountTarget for
// the breadth), rather than a second copy of the rules. A picker that greys out
// a different set from the one the API refuses is a picker that lies.
func (a *restAPI) classifyHostFolder(full, name string) hostFolderEntryWire {
	entry := hostFolderEntryWire{
		Name:      name,
		Path:      full,
		Mountable: true,
	}

	// Match the REFUSAL sentinel specifically. CheckMountTarget also fails for
	// unrelated reasons (an unresolvable path, a race with a delete), and
	// reporting those as "inside the Omnipus data directory" would state a
	// confident, wrong reason in the one control where the reason is the point.
	if _, _, err := workspace.CheckMountTarget(full, a.homePath); err != nil {
		if errors.Is(err, workspace.ErrMountRefused) {
			entry.Mountable = false
			entry.Reason = strPtr("Inside the Omnipus data directory — mounting it would expose your keys and let an agent disable its own sandbox.")
			return entry
		}
		// Anything else: leave it selectable and let the create call give the
		// real error. Greying out a folder for a transient reason would be a
		// silent, unexplained refusal.
		return entry
	}

	if workspace.IsBroadMountTarget(full) {
		broad := true
		entry.Broad = &broad
		entry.Reason = strPtr("A broad grant — the agent could write anywhere inside this folder.")
	}
	return entry
}
