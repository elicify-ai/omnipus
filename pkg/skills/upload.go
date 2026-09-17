package skills

import (
	"fmt"
	"os"
	"path/filepath"
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
		return "", "", fmt.Errorf("uploaded skill exceeds %d bytes", MaxUploadedSkillPackageBytes)
	}
	ext := strings.ToLower(filepath.Ext(uploadPath))
	slug = strings.TrimSuffix(filepath.Base(uploadPath), filepath.Ext(uploadPath))
	if !ValidSlug(slug) {
		return "", "", fmt.Errorf("uploaded filename must provide a valid skill id")
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
			return "", "", err
		}
		err = os.WriteFile(filepath.Join(stagedDir, "SKILL.md"), data, 0o644)
	case ".zip":
		err = utils.ExtractZipFileWithLimits(uploadPath, stagedDir, utils.ZipExtractionLimits{
			MaxEntries:       MaxUploadedSkillArchiveEntries,
			MaxExpandedBytes: MaxUploadedSkillPackageBytes,
		})
		if err == nil {
			data, readErr := os.ReadFile(filepath.Join(stagedDir, "SKILL.md"))
			if readErr != nil {
				err = fmt.Errorf("ZIP must contain SKILL.md at its root: %w", readErr)
			} else {
				err = ValidateSkillMarkdown(slug, string(data))
			}
		}
	default:
		return "", "", fmt.Errorf("unsupported uploaded skill type %q; use .md or .zip", ext)
	}
	if err != nil {
		return "", "", err
	}
	ok = true
	return slug, stagedDir, nil
}
