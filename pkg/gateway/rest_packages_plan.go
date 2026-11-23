// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_packages_plan.go: the export review ("plan") routes of epic #1094
// slice B1 (spec docs/internal/specs/export-import-b1-spec.md, "API and Data",
// FR-001…FR-009): POST /api/v1/packages/export/plan and
// GET /api/v1/packages/export/plan/{plan_id}, over an in-memory plan store.
// The review writes nothing, writes no audit entry and uses no upload slot.

package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
)

const (
	// packagePlanStoreCap is the most reviews the gateway holds (FR-008).
	packagePlanStoreCap = 32
	// packageCodePlanNotFound answers an unknown, malformed or expired review.
	packageCodePlanNotFound = "plan_not_found"
	// packagePlanReadPrefix is the read route's subtree prefix.
	packagePlanReadPrefix = "/api/v1/packages/export/plan/"
)

// packagePlanStore holds reviews in memory, keyed by plan_id. It keeps only
// the response (the generated wire value): never bundle bytes. Plans do not
// survive a gateway restart.
type packagePlanStore struct {
	plans map[string]gen.PackageExportPlan
}

// put stores p, first making room when the store is full: expired reviews
// are dropped, then the oldest by created_at until one place is free (FR-008).
func (s *packagePlanStore) put(p gen.PackageExportPlan) {
	if s.plans == nil {
		s.plans = map[string]gen.PackageExportPlan{}
	}
	if len(s.plans) >= packagePlanStoreCap {
		now := packageNow()
		for id, held := range s.plans {
			if !now.Before(held.ExpiresAt) {
				delete(s.plans, id)
			}
		}
	}
	if over := len(s.plans) - packagePlanStoreCap + 1; over > 0 {
		ids := make([]string, 0, len(s.plans))
		for id := range s.plans {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			return s.plans[ids[i]].CreatedAt.Before(s.plans[ids[j]].CreatedAt)
		})
		for _, id := range ids[:over] {
			delete(s.plans, id)
		}
	}
	s.plans[p.PlanId.String()] = p
}

// get returns the review while now is before its expires_at (FR-007). An
// expired review is dropped on the way.
func (s *packagePlanStore) get(id string) (gen.PackageExportPlan, bool) {
	p, ok := s.plans[id]
	if !ok {
		return gen.PackageExportPlan{}, false
	}
	if !packageNow().Before(p.ExpiresAt) {
		delete(s.plans, id)
		return gen.PackageExportPlan{}, false
	}
	return p, true
}

func (a *restAPI) storePackagePlan(p gen.PackageExportPlan) {
	a.packages.mu.Lock()
	defer a.packages.mu.Unlock()
	a.packages.plans.put(p)
}

func (a *restAPI) loadPackagePlan(id string) (gen.PackageExportPlan, bool) {
	a.packages.mu.Lock()
	defer a.packages.mu.Unlock()
	return a.packages.plans.get(id)
}

// handlePackageExportPlanCreate is POST /api/v1/packages/export/plan (FR-001,
// FR-002): the same capture, cleaning and refusals as the export, answered as
// 201 PackageExportPlan.
func (a *restAPI) handlePackageExportPlanCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req gen.PackageExportPlanRequest
	if tooLarge, err := decodePackagePlanRequest(r, &req); err != nil {
		if tooLarge {
			jsonErr(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		jsonErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if !req.Scope.Valid() {
		jsonErrField(w, http.StatusBadRequest, `scope is required and must be "agents"`, "scope")
		return
	}
	if len(req.AgentIds) != 1 {
		writePackageError(w, packageErr(omnpkg.CodeExportSelectionInvalid, "reason", "exactly one agent must be selected"), nil)
		return
	}
	if packageT5.PlanOpenSecretFile != nil {
		packageT5.PlanOpenSecretFile()
	}
	src := &packageExportSource{a: a}
	plan, err := omnpkg.BuildExportPlan(src, omnpkg.Selection{AgentIDs: req.AgentIds}, packageNow())
	if err != nil {
		writePackageErr(w, "export plan", err)
		return
	}
	wire, err := packagePlanToWire(plan)
	if err != nil {
		writePackageErr(w, "export plan", err)
		return
	}
	a.storePackagePlan(wire)
	writeJSON(w, http.StatusCreated, wire)
}

// handlePackageExportPlanRead is GET /api/v1/packages/export/plan/{plan_id}
// (FR-007): the stored review unchanged while held, else 404 plan_not_found.
func (a *restAPI) handlePackageExportPlanRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, _ := strings.CutPrefix(r.URL.Path, packagePlanReadPrefix)
	plan, ok := a.loadPackagePlan(id)
	if !ok {
		writePackageError(w, packageErr(packageCodePlanNotFound), nil)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// decodePackagePlanRequest decodes the review request strictly (an unknown
// property is a 400) and then reads the rest of the body through the route's
// 1 MiB limiter. tooLarge reports that limit.
func decodePackagePlanRequest(r *http.Request, dst *gen.PackageExportPlanRequest) (tooLarge bool, err error) {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err = dec.Decode(dst)
	if err == nil {
		if _, err = io.Copy(io.Discard, r.Body); err != nil {
			err = fmt.Errorf("read request body: %w", err)
		}
	}
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe), err
}

// packagePlanToWire maps the engine's review onto the generated wire type.
// The two share their JSON field names; decoding strictly makes any drift
// between them an error rather than a silently dropped field.
func packagePlanToWire(plan *omnpkg.ExportPlan) (gen.PackageExportPlan, error) {
	var wire gen.PackageExportPlan
	raw, err := json.Marshal(plan)
	if err != nil {
		return wire, fmt.Errorf("encode export plan: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return wire, fmt.Errorf("map export plan to its wire type: %w", err)
	}
	return wire, nil
}
