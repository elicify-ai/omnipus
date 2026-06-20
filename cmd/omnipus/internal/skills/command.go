package skills

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal"
	"github.com/dapicom-ai/omnipus/pkg/skills"
)

type deps struct {
	workspace    string
	installer    *skills.SkillInstaller
	skillsLoader *skills.SkillsLoader
}

func NewSkillsCommand() *cobra.Command {
	var d deps

	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Manage skills",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := internal.LoadConfig()
			if err != nil {
				return fmt.Errorf("error loading config: %w", err)
			}

			d.workspace = cfg.WorkspacePath()
			githubToken, githubProxy := skills.FirstGitHubMarketplaceCreds(cfg, os.Getenv)
			installer, err := skills.NewSkillInstaller(
				d.workspace,
				githubToken,
				githubProxy,
			)
			if err != nil {
				return fmt.Errorf("error creating skills installer: %w", err)
			}
			d.installer = installer

			// get global config directory and builtin skills directory
			globalDir := filepath.Dir(internal.GetConfigPath())
			globalSkillsDir := filepath.Join(globalDir, "skills")
			builtinSkillsDir := filepath.Join(globalDir, "omnipus", "skills")
			d.skillsLoader = skills.NewSkillsLoader(d.workspace, globalSkillsDir, builtinSkillsDir)

			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	installerFn := func() (*skills.SkillInstaller, error) {
		if d.installer == nil {
			return nil, fmt.Errorf("skills installer is not initialized")
		}
		return d.installer, nil
	}

	loaderFn := func() (*skills.SkillsLoader, error) {
		if d.skillsLoader == nil {
			return nil, fmt.Errorf("skills loader is not initialized")
		}
		return d.skillsLoader, nil
	}

	workspaceFn := func() (string, error) {
		if d.workspace == "" {
			return "", fmt.Errorf("workspace is not initialized")
		}
		return d.workspace, nil
	}

	cmd.AddCommand(
		newListCommand(loaderFn),
		newInstallCommand(installerFn),
		newInstallBuiltinCommand(workspaceFn),
		newListBuiltinCommand(),
		newRemoveCommand(installerFn),
		newUpdateCommand(installerFn),
		newSearchCommand(),
		newShowCommand(loaderFn),
	)

	return cmd
}
