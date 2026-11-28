// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package doctor

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/app/internal"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
)

// checkDocumentPreview reports the document preview components and limits
// (office preview spec AD-4, AD-7, §3.1 D1.4): a damaged or agent-writable
// LibreOffice or PDF renderer, and a Linux memory limit the kernel does not
// enforce. A component that is not installed is silent. Read-only: it resolves and fingerprints the
// components and never runs them.
func checkDocumentPreview(cfg *config.Config) []warning {
	home := internal.GetOmnipusHome()
	var warnings []warning
	resolver, err := component.NewResolver(home)
	if err != nil {
		warnings = append(warnings, warning{
			code:    "WARN-PREVIEW-000",
			message: "Document preview components could not be checked (the Omnipus executable path is unknown: " + err.Error() + ").",
		})
	} else {
		roots := preview.ConfiguredWriteRoots(home, cfg, nil)
		for _, c := range []struct {
			kind      component.Kind
			code      string
			name      string
			effect    string
			reinstall string
			insideOwn string
		}{
			{component.KindPDFRenderer, "WARN-PREVIEW-001", "PDF renderer",
				"PDF previews send the PDF or its text, without page images",
				"register an unpacked renderer release with `omnipus pdf-renderer register <folder>`",
				"register an unpacked renderer release"},
			{component.KindOffice, "WARN-PREVIEW-002", "LibreOffice",
				"Office previews are refused; PDF previews still work",
				"register LibreOffice 26.8 with `omnipus office register <folder>`",
				"register LibreOffice 26.8"},
		} {
			if msg := componentProblem(resolver, roots, c.kind, c.name, c.effect, c.reinstall, c.insideOwn); msg != "" {
				warnings = append(warnings, warning{code: c.code, message: msg})
			}
		}
	}
	if reason, _ := preview.HostMemoryLimitUnenforced(); reason != "" {
		warnings = append(warnings, warning{
			code:    "WARN-PREVIEW-003",
			message: "Document preview memory limit not enforced: " + reason + ". Preview programs still run with the limit set; the kernel does not apply it.",
		})
	}
	return warnings
}

// componentProblem returns kind's doctor warning, or "". insideOwn is the
// register step named when a Linux registration lies outside Omnipus's own
// folder (office-preview spec §3.9 OF-4).
func componentProblem(r *component.Resolver, roots []component.WriteRoot, kind component.Kind, name, effect, reinstall, insideOwn string) string {
	comp, problem := r.Resolve(kind)
	if problem != nil {
		switch problem.Status {
		case component.StatusMissing:
			// Not installed is a supported install choice, not a doctor
			// finding (AD-4, architect ruling 2026-11-01): previews report
			// it when they are used.
			return ""
		case component.StatusOutsideOwn:
			return fmt.Sprintf("%s is registered outside Omnipus's own folder (%s; on Linux only folders inside %s are used): %s. Remove the registration with `omnipus %s unregister`, or %s from a folder inside %s.",
				name, problem.Root, problem.Own, effect, kind, insideOwn, problem.Own)
		}
		return fmt.Sprintf("%s damaged (%s): %s. Reinstall it or %s.", name, problem.Reason, effect, reinstall)
	}
	if overlap := component.FirstOverlap(comp.Root, roots); overlap != nil {
		return fmt.Sprintf("%s is in a folder an agent can write (%s): %s until that access is removed.", name, overlap.Grant, effect)
	}
	return ""
}
