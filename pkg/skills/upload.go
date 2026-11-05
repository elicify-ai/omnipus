package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/utils"
)

const (
	MaxUploadedSkillPackageBytes   = 50 * 1024 * 1024
	MaxUploadedSkillArchiveEntries = 1024
)

// StageUploadedSkill validates an authorized local upload and materializes a
// package in stagingRoot. Only a single Markdown SKILL.md or a bounded ZIP
// package is supported; JSON and arbitrary filesystem paths are deliberately
// absent from this API.
func StageUploadedSkill(uploadPath, stagingRoot string) (slug, stagedDir string, err error) {
	info, err := os.Stat(uploadPath)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("uploaded skill is not a regular file")
	}
	if info.Size() > MaxUploadedSkillPackageBytes {
		return "", "", refuseUpload(fmt.Sprintf("uploaded skill exceeds %d bytes", MaxUploadedSkillPackageBytes))
	}
	ext := strings.ToLower(filepath.Ext(uploadPath))
	slug = stripUploadRenameSuffix(strings.TrimSuffix(filepath.Base(uploadPath), filepath.Ext(uploadPath)))
	if !ValidSlug(slug) {
		return "", "", refuseUpload("uploaded filename must provide a valid skill id")
	}
	if err = os.MkdirAll(stagingRoot, 0o755); err != nil {
		return "", "", err
	}
	stagedDir, err = os.MkdirTemp(stagingRoot, slug+".upload-")
	if err != nil {
		return "", "", err
	}
	cleanupDir := stagedDir
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(cleanupDir)
		}
	}()
	switch ext {
	case ".md":
		data, readErr := os.ReadFile(uploadPath)
		if readErr != nil {
			return "", "", readErr
		}
		if err = ValidateSkillMarkdown(slug, string(data)); err != nil {
			return "", "", refuseUploadErr(err)
		}
		err = os.WriteFile(filepath.Join(stagedDir, "SKILL.md"), data, 0o644)
	case ".zip":
		err = utils.ExtractZipFileWithLimits(uploadPath, stagedDir, utils.ZipExtractionLimits{
			MaxEntries:       MaxUploadedSkillArchiveEntries,
			MaxExpandedBytes: MaxUploadedSkillPackageBytes,
			OwnerExecDir:     "scripts", // UAT run 2 R2-4: scripts stay runnable
		})
		if err == nil {
			err = validateStagedZipInstruction(stagedDir, slug)
		}
	default:
		return "", "", refuseUpload(fmt.Sprintf("unsupported uploaded skill type %q; use .md or .zip", ext))
	}
	if err != nil {
		return "", "", err
	}
	ok = true
	return slug, stagedDir, nil
}

// ErrUploadRefused marks a staging failure caused by the uploaded file itself
// (its name, type, size, or its SKILL.md), as opposed to a storage fault. The
// install handler answers it 400 with the error's text (UAT run 2 R2-2).
var ErrUploadRefused = errors.New("uploaded skill refused")

// uploadRefusalError is an ErrUploadRefused whose text is the user-facing reason.
type uploadRefusalError struct {
	text  string
	cause error
}

func (e *uploadRefusalError) Error() string        { return e.text }
func (e *uploadRefusalError) Unwrap() error        { return e.cause }
func (e *uploadRefusalError) Is(target error) bool { return target == ErrUploadRefused }

func refuseUpload(text string) error { return &uploadRefusalError{text: text} }

func refuseUploadErr(err error) error { return &uploadRefusalError{text: err.Error(), cause: err} }

// validateStagedZipInstruction checks the extracted zip's root SKILL.md. A
// missing one is the uploader's mistake (often a zip whose files sit inside
// one top-level folder, which is not accepted); any other read failure is a
// storage fault.
func validateStagedZipInstruction(stagedDir, slug string) error {
	data, err := os.ReadFile(filepath.Join(stagedDir, "SKILL.md"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return refuseUpload("ZIP must contain SKILL.md at its root; a zip whose files sit inside one top-level folder is not accepted")
	case err != nil:
		return fmt.Errorf("read staged SKILL.md: %w", err)
	}
	if err := ValidateSkillMarkdown(slug, string(data)); err != nil {
		return refuseUploadErr(err)
	}
	return nil
}

// uploadRenameSuffix is the `_<unix nanoseconds>` the upload handler adds to
// a repeated same-named upload (pkg/gateway/rest_uploads.go): exactly 19
// digits (UAT run 2 R2-3, chief ruling option B).
var uploadRenameSuffix = regexp.MustCompile(`_[0-9]{19}$`)

// stripUploadRenameSuffix removes exactly that suffix; any other name is
// returned unchanged, so `good-skill_123` is still refused by the slug check.
func stripUploadRenameSuffix(base string) string {
	return uploadRenameSuffix.ReplaceAllString(base, "")
}
