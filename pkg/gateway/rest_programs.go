// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Settings → Programs REST surface (connected-programs spec S1): the program
// list, custom entries, the web-chat switch, and per-entry token save, check
// and disconnect. Every route is person-session only (FR-028): the route is
// wrapped withAuth → RequirePersonSession → requireSessionCookieAuth, so a
// CLI token, a bearer token, the development bypass and agent calls are
// answered 403 before the body is read.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/programs"
	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
)

// programsBodyLimit is the Programs request body cap (D-1 row 10).
const programsBodyLimit = 65536

// programsHolder lazily owns the restAPI's programs service.
type programsHolder struct {
	once  sync.Once
	svc   *programs.Service
	mu    sync.Mutex
	store *credentials.Store
}

// programService returns the connected-programs service, building it on
// first use; checks left running by an earlier process run again (S-115).
// publishProgramCard stores a submitted program card's token through the
// Programs service, so the card's commit starts the same account check as a
// paste in Settings → Programs (FR-051).
func (a *restAPI) publishProgramCard(programID, value string, receipt *credentials.CardReceipt) (int64, error) {
	conn, err := a.programService().Publish(programID, value, nil, receipt)
	return conn.Generation, err
}

func (a *restAPI) programService() *programs.Service {
	a.programs.once.Do(func() {
		a.programs.svc = programs.NewService(a.agentLoop.GetConfig, a.programStore,
			filepath.Join(a.homePath, "programs", "connections.json"))
		a.programs.svc.SetSupersededSink(a.auditProgramSuperseded)
		a.programs.svc.SetResultSink(a.auditProgramCheckResult)
		// FR-005/S-08: the account check's program passes the shared
		// third-party program check over every agent-writable folder.
		a.programs.svc.SetProgramPathCheck(func(word, found string, cfg *config.Config) (string, error) {
			roots, rootsErr := thirdpartycli.AgentWritableRoots(a.homePath, cfg)
			return thirdpartycli.CheckProgramPath(word, found, roots, rootsErr)
		})
		a.programs.svc.ResumeChecks()
	})
	return a.programs.svc
}

// programStore returns the unlocked credential store the programs service
// writes through: the injected boot store, or one opened from homePath.
func (a *restAPI) programStore() (*credentials.Store, error) {
	if a.credStore != nil {
		return a.credStore, nil
	}
	a.programs.mu.Lock()
	defer a.programs.mu.Unlock()
	if a.programs.store != nil {
		return a.programs.store, nil
	}
	store := credentials.NewStore(a.credentialsStorePath())
	if err := credentials.Unlock(store); err != nil {
		return nil, fmt.Errorf("credential store locked: %w", err)
	}
	a.programs.store = store
	return store, nil
}

// programsHandler is the person-session-only wrapper of every Programs route.
func (a *restAPI) programsHandler() http.HandlerFunc {
	return a.withAuth(personOnly(a.HandlePrograms))
}

// HandlePrograms routes /api/v1/programs, /api/v1/programs/settings,
// /api/v1/programs/{id}, /api/v1/programs/{id}/credential and
// /api/v1/programs/{id}/check.
func (a *restAPI) HandlePrograms(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/programs"), "/")
	parts := strings.Split(rest, "/")
	switch {
	case rest == "":
		switch r.Method {
		case http.MethodGet:
			a.listPrograms(w)
		case http.MethodPost:
			a.createProgram(w, r)
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case rest == "settings":
		if r.Method != http.MethodPut {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.putProgramSettings(w, r)
	case len(parts) == 1:
		switch r.Method {
		case http.MethodPut:
			a.editProgram(w, r, parts[0])
		case http.MethodDelete:
			a.deleteProgram(w, parts[0])
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case len(parts) == 2 && parts[1] == "credential":
		switch r.Method {
		case http.MethodPut:
			a.saveProgramCredential(w, r, parts[0])
		case http.MethodDelete:
			a.disconnectProgram(w, parts[0])
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case len(parts) == 2 && parts[1] == "check":
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.recheckProgram(w, parts[0])
	default:
		jsonErr(w, http.StatusNotFound, "not found")
	}
}

// readProgramsBody reads a Programs request body under the 65,536-byte cap
// and decodes it strictly into dst (unknown fields are refused).
func readProgramsBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, programsBodyLimit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			jsonErr(w, http.StatusRequestEntityTooLarge, "request body too large")
		} else {
			jsonErr(w, http.StatusBadRequest, "could not read request body")
		}
		return false
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

// programsRevision is the ConfigurationRevision of the programs settings.
func programsRevision(cfg *config.Config) string {
	return contractRevision(config.RevisionHash(cfg.Programs))
}

// channelProgramsRevision is the ConfigurationRevision of one channel's
// programs setting: it changes whenever the channel's configuration does.
func channelProgramsRevision(cfg *config.Config, id string) string {
	return contractRevision(config.ChannelConfigRevision(cfg, id))
}

// contractRevision widens a short config revision token to the contract's
// ConfigurationRevision form, 64 lowercase hex characters (SHA-256), which
// the SPA's generated schema enforces. Equal tokens stay equal and different
// tokens stay different, so the write precondition is unchanged.
func contractRevision(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// programEntryView renders one entry with its connection (value-free).
func (a *restAPI) programEntryView(cfg *config.Config, e programs.ProgramEntry, conn programs.Connection) (gen.ProgramEntry, error) {
	var out gen.ProgramEntry
	raw, err := json.Marshal(e)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	if out.NetworkSites == nil {
		out.NetworkSites = []string{}
	}
	out.EnabledAgentIds = programs.EnabledAgentIDs(cfg, e.ID)
	out.Connection.State = gen.ProgramEntryConnectionState(conn.State)
	out.Connection.Generation = conn.Generation
	if conn.AccountLabel != "" && conn.State == programs.StateConnected {
		label := conn.AccountLabel
		out.Connection.AccountLabel = &label
	}
	if conn.Message != "" {
		msg := conn.Message
		out.Connection.Message = &msg
	}
	if !conn.CheckedAt.IsZero() {
		at := conn.CheckedAt
		out.Connection.CheckedAt = &at
	}
	return out, nil
}

func (a *restAPI) writeProgramEntry(w http.ResponseWriter, status int, id string, conn *programs.Connection) {
	cfg := a.agentLoop.GetConfig()
	e, found, err := programs.Lookup(cfg, id)
	if err != nil || !found {
		jsonErr(w, http.StatusNotFound, "program not found")
		return
	}
	c := a.programService().Connection(id)
	if conn != nil {
		c = *conn
	}
	view, err := a.programEntryView(cfg, e, c)
	if err != nil {
		slog.Error("rest: render program entry", "program", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, status, view)
}

func (a *restAPI) listPrograms(w http.ResponseWriter) {
	cfg := a.agentLoop.GetConfig()
	entries, err := programs.Entries(cfg)
	if err != nil {
		slog.Error("rest: program catalog unreadable", "error", err)
		jsonErr(w, http.StatusInternalServerError, "the program catalog could not be read")
		return
	}
	svc := a.programService()
	views := make([]gen.ProgramEntry, 0, len(entries))
	for _, e := range entries {
		v, verr := a.programEntryView(cfg, e, svc.Connection(e.ID))
		if verr != nil {
			slog.Error("rest: render program entry", "program", e.ID, "error", verr)
			jsonErr(w, http.StatusInternalServerError, "internal server error")
			return
		}
		views = append(views, v)
	}
	var list gen.ProgramList
	raw, err := json.Marshal(views)
	if err == nil {
		err = json.Unmarshal(raw, &list.Entries)
	}
	if err != nil {
		slog.Error("rest: render program list", "error", err)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	list.WebChatAllowed = cfg.Programs.WebChatAllowed
	list.Revision = programsRevision(cfg)
	jsonOK(w, list)
}

// customFromWire converts the generated request into the domain request.
func customFromWire(req gen.ProgramCustomRequest) (programs.CustomRequest, error) {
	var out programs.CustomRequest
	raw, err := json.Marshal(req)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}

func writeProgramValidation(w http.ResponseWriter, err error) {
	var verr *programs.ValidationError
	if errors.As(err, &verr) {
		status := http.StatusBadRequest
		if verr.Conflict {
			status = http.StatusConflict
		}
		jsonErr(w, status, verr.Message)
		return
	}
	slog.Error("rest: programs", "error", err)
	jsonErr(w, http.StatusInternalServerError, "internal server error")
}

// programsWriteError carries a refusal decided inside a config write.
type programsWriteError struct {
	status  int
	message string
}

func (e *programsWriteError) Error() string { return e.message }

// writeProgramsSection replaces config.json's programs section with section.
func writeProgramsSection(m map[string]any, section config.ProgramsConfig) error {
	raw, err := json.Marshal(section)
	if err != nil {
		return err
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		return err
	}
	if len(asMap) == 0 {
		delete(m, "programs")
		return nil
	}
	m["programs"] = asMap
	return nil
}

func (a *restAPI) createProgram(w http.ResponseWriter, r *http.Request) {
	var wire gen.ProgramCustomRequest
	if !readProgramsBody(w, r, &wire) {
		return
	}
	req, err := customFromWire(wire)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var newID string
	err = a.safeUpdateProgramsConfigJSON(func(m map[string]any) error {
		cfg := a.agentLoop.GetConfig()
		entries, lerr := programs.Entries(cfg)
		if lerr != nil {
			return lerr
		}
		norm, verr := programs.NormalizeCustomRequest(req, entries)
		if verr != nil {
			return verr
		}
		newID = programs.NewCustomID(norm.DisplayName, entries)
		section := cfg.Programs
		section.Entries = append(slices.Clone(section.Entries), programs.ConfigFromRequest(newID, norm, 1))
		return writeProgramsSection(m, section)
	})
	if err != nil {
		writeProgramValidation(w, err)
		return
	}
	a.auditProgram(auditProgramCustomChanged, audit.DecisionAllow, "", map[string]any{"program_id": newID, "change": "created"})
	a.writeProgramEntry(w, http.StatusCreated, newID, nil)
}

func (a *restAPI) presetRefusal(w http.ResponseWriter, e programs.ProgramEntry) bool {
	if e.Kind != programs.KindPreset {
		return false
	}
	jsonErr(w, http.StatusUnprocessableEntity, e.DisplayName+" is a preset and cannot be changed or deleted.")
	return true
}

func (a *restAPI) editProgram(w http.ResponseWriter, r *http.Request, id string) {
	var wire gen.ProgramCustomRequest
	cfg := a.agentLoop.GetConfig()
	e, found, err := programs.Lookup(cfg, id)
	if err != nil || !found {
		jsonErr(w, http.StatusNotFound, "program not found")
		return
	}
	if !readProgramsBody(w, r, &wire) {
		return
	}
	if a.presetRefusal(w, e) {
		return
	}
	if wire.Revision == nil {
		jsonErr(w, http.StatusBadRequest, "revision is required")
		return
	}
	checkRelevant := false
	err = a.safeUpdateProgramsConfigJSON(func(m map[string]any) error {
		cur := a.agentLoop.GetConfig()
		entries, lerr := programs.Entries(cur)
		if lerr != nil {
			return lerr
		}
		idx := slices.IndexFunc(cur.Programs.Entries, func(c config.ProgramEntryConfig) bool { return c.ID == id })
		if idx < 0 {
			return &programsWriteError{status: http.StatusNotFound, message: "program not found"}
		}
		stored := cur.Programs.Entries[idx]
		if *wire.Revision != stored.Revision {
			return &programsWriteError{status: http.StatusConflict,
				message: "This program changed since this page was loaded. Check the current state before saving again."}
		}
		merged := mergeCustomEdit(stored, wire)
		others := slices.DeleteFunc(slices.Clone(entries), func(x programs.ProgramEntry) bool { return x.ID == id })
		norm, verr := programs.NormalizeCustomRequest(merged, others)
		if verr != nil {
			return verr
		}
		section := cur.Programs
		section.Entries = slices.Clone(section.Entries)
		section.Entries[idx] = programs.ConfigFromRequest(id, norm, stored.Revision+1)
		if cerr := programs.EditGitHostConflict(cur, section.Entries[idx]); cerr != nil {
			return cerr
		}
		checkRelevant = checkRelevantChange(stored, section.Entries[idx])
		return writeProgramsSection(m, section)
	})
	var werr *programsWriteError
	switch {
	case errors.As(err, &werr):
		jsonErr(w, werr.status, werr.message)
		return
	case err != nil:
		writeProgramValidation(w, err)
		return
	}
	a.auditProgram(auditProgramCustomChanged, audit.DecisionAllow, "", map[string]any{"program_id": id, "change": "edited"})
	if checkRelevant {
		// R3-M3/FR-047: the committed edit changes how the account check runs,
		// so check_revision advances and, with a saved credential, a new check
		// replaces the running one (S-116). The entry edit itself is saved.
		if _, cerr := a.programService().CheckRelevantEdited(id); cerr != nil {
			slog.Error("rest: program edit saved but its account check was not restarted", "program_id", id, "error", cerr)
			jsonErr(w, http.StatusInternalServerError, "The change was saved, but the account check could not start. Run the check again.")
			return
		}
	}
	a.writeProgramEntry(w, http.StatusOK, id, nil)
}

// checkRelevantChange reports whether an edit changed a check-relevant field:
// the account check or the token variables (R3-M3). Display name, program
// words, network sites and git leave check_revision unchanged (S-131).
func checkRelevantChange(before, after config.ProgramEntryConfig) bool {
	if !slices.Equal(before.TokenVariables, after.TokenVariables) {
		return true
	}
	if (before.IdentityCheck == nil) != (after.IdentityCheck == nil) {
		return true
	}
	if before.IdentityCheck == nil {
		return false
	}
	return !reflect.DeepEqual(*before.IdentityCheck, *after.IdentityCheck)
}

// mergeCustomEdit applies the fields present in an edit to the stored entry.
func mergeCustomEdit(stored config.ProgramEntryConfig, wire gen.ProgramCustomRequest) programs.CustomRequest {
	out := programs.CustomRequest{
		DisplayName:    stored.DisplayName,
		ProgramWords:   slices.Clone(stored.ProgramWords),
		TokenVariables: slices.Clone(stored.TokenVariables),
		NetworkSites:   slices.Clone(stored.NetworkSites),
	}
	if stored.Git != nil {
		out.Git = &programs.ProgramGit{Host: stored.Git.Host, Username: stored.Git.Username}
	}
	if stored.IdentityCheck != nil {
		out.IdentityCheck = &programs.ProgramIdentityCheck{ProgramWord: stored.IdentityCheck.ProgramWord,
			Args: slices.Clone(stored.IdentityCheck.Args), TimeoutSeconds: stored.IdentityCheck.TimeoutSeconds,
			MaxOutputBytes: stored.IdentityCheck.MaxOutputBytes}
	}
	edit, err := customFromWire(wire)
	if err != nil {
		return out
	}
	if wire.DisplayName != nil {
		out.DisplayName = edit.DisplayName
	}
	if wire.ProgramWords != nil {
		out.ProgramWords = edit.ProgramWords
	}
	if wire.TokenVariables != nil {
		out.TokenVariables = edit.TokenVariables
	}
	if wire.NetworkSites != nil {
		out.NetworkSites = edit.NetworkSites
	}
	if wire.Git != nil {
		out.Git = edit.Git
	}
	if wire.IdentityCheck != nil {
		out.IdentityCheck = edit.IdentityCheck
	}
	return out
}

func (a *restAPI) deleteProgram(w http.ResponseWriter, id string) {
	cfg := a.agentLoop.GetConfig()
	e, found, err := programs.Lookup(cfg, id)
	if err != nil || !found {
		jsonErr(w, http.StatusNotFound, "program not found")
		return
	}
	if a.presetRefusal(w, e) {
		return
	}
	if on := programs.EnabledAgentIDs(cfg, id); len(on) > 0 {
		names := make([]string, 0, len(on))
		for _, agentID := range on {
			names = append(names, programs.AgentName(cfg, agentID))
		}
		jsonErr(w, http.StatusConflict, fmt.Sprintf("%s cannot be deleted: it is switched on for %s. Switch it off first.",
			e.DisplayName, strings.Join(names, ", ")))
		return
	}
	svc := a.programService()
	if _, derr := svc.Disconnect(id); derr != nil {
		writeProgramServiceError(w, derr)
		return
	}
	err = a.safeUpdateProgramsConfigJSON(func(m map[string]any) error {
		cur := a.agentLoop.GetConfig()
		section := cur.Programs
		section.Entries = slices.DeleteFunc(slices.Clone(section.Entries), func(c config.ProgramEntryConfig) bool { return c.ID == id })
		return writeProgramsSection(m, section)
	})
	if err != nil {
		slog.Error("rest: delete program entry", "program", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not save config")
		return
	}
	svc.ForgetEntry(id)
	a.auditProgram(auditProgramCustomChanged, audit.DecisionAllow, "", map[string]any{"program_id": id, "change": "deleted"})
	w.WriteHeader(http.StatusNoContent)
}

func (a *restAPI) putProgramSettings(w http.ResponseWriter, r *http.Request) {
	var req gen.ProgramSettingsUpdate
	if !readProgramsBody(w, r, &req) {
		return
	}
	err := a.safeUpdateProgramsConfigJSON(func(m map[string]any) error {
		cur := a.agentLoop.GetConfig()
		if req.Revision != programsRevision(cur) {
			return &programsWriteError{status: http.StatusConflict,
				message: "The program settings changed since this page was loaded. Check the current state before saving again."}
		}
		section := cur.Programs
		section.WebChatAllowed = req.WebChatAllowed
		return writeProgramsSection(m, section)
	})
	var werr *programsWriteError
	switch {
	case errors.As(err, &werr):
		jsonErr(w, werr.status, werr.message)
		return
	case err != nil:
		slog.Error("rest: save program settings", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not save config")
		return
	}
	a.auditProgram(auditProgramSettingChanged, audit.DecisionAllow, "", map[string]any{
		"setting": "web_chat_allowed", "allowed": req.WebChatAllowed})
	a.listPrograms(w)
}

func writeProgramServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, programs.ErrUnknownProgram):
		jsonErr(w, http.StatusNotFound, "program not found")
	case errors.Is(err, programs.ErrStaleGeneration):
		jsonErr(w, http.StatusConflict, programs.MsgStaleGeneration)
	case errors.Is(err, programs.ErrStoreBusy):
		jsonErr(w, http.StatusServiceUnavailable, programs.MsgStoreBusy)
	case errors.Is(err, programs.ErrNotConnected):
		jsonErr(w, http.StatusConflict, programs.MsgNotConnected)
	default:
		slog.Error("rest: program token not saved", "error", err)
		jsonErr(w, http.StatusInternalServerError, programs.MsgSaveFailed)
	}
}

func (a *restAPI) saveProgramCredential(w http.ResponseWriter, r *http.Request, id string) {
	var req gen.ProgramCredentialRequest
	if !readProgramsBody(w, r, &req) {
		return
	}
	if _, found, err := programs.Lookup(a.agentLoop.GetConfig(), id); err != nil || !found {
		jsonErr(w, http.StatusNotFound, "program not found")
		return
	}
	value, err := programs.ValidateTokenValue(req.Value)
	if err != nil {
		writeProgramValidation(w, err)
		return
	}
	svc := a.programService()
	event := auditProgramConnected
	if svc.Connection(id).State != programs.StateNotConnected {
		event = auditProgramReplaced
	}
	conn, err := svc.Publish(id, value, req.ExpectedGeneration, nil)
	if err != nil {
		writeProgramServiceError(w, err)
		return
	}
	slog.Info("programs.token_saved", "program", id, "generation", conn.Generation)
	a.auditProgram(event, audit.DecisionAllow, "", map[string]any{"program_id": id, "generation": conn.Generation})
	a.writeProgramEntry(w, http.StatusOK, id, &conn)
}

func (a *restAPI) disconnectProgram(w http.ResponseWriter, id string) {
	conn, err := a.programService().Disconnect(id)
	if err != nil {
		writeProgramServiceError(w, err)
		return
	}
	slog.Info("programs.disconnected", "program", id, "generation", conn.Generation)
	a.auditProgram(auditProgramDisconnected, audit.DecisionAllow, "", map[string]any{"program_id": id, "generation": conn.Generation})
	a.writeProgramEntry(w, http.StatusOK, id, &conn)
}

func (a *restAPI) recheckProgram(w http.ResponseWriter, id string) {
	conn, err := a.programService().Recheck(id)
	if err != nil {
		writeProgramServiceError(w, err)
		return
	}
	a.auditProgram(auditProgramCheck, audit.DecisionAllow, "", map[string]any{
		"program_id": id, "generation": conn.Generation, "check_revision": conn.CheckRevision})
	a.writeProgramEntry(w, http.StatusAccepted, id, &conn)
}
