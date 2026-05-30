// Omnipus - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal"
	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal/agent"
	auditcmd "github.com/dapicom-ai/omnipus/cmd/omnipus/internal/audit"
	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal/auth"
	credcmd "github.com/dapicom-ai/omnipus/cmd/omnipus/internal/credentials"
	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal/cron"
	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal/doctor"
	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal/gateway"
	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal/migrate"
	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal/model"
	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal/onboard"
	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal/skills"
	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal/status"
	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal/version"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

func NewOmnipusCommand() *cobra.Command {
	short := fmt.Sprintf("%s omnipus - Personal AI Assistant v%s\n\n", internal.Logo, config.GetVersion())

	cmd := &cobra.Command{
		Use:     "omnipus",
		Short:   short,
		Example: "omnipus version",
	}

	cmd.AddCommand(
		onboard.NewOnboardCommand(),
		agent.NewAgentCommand(),
		auditcmd.NewAuditCommand(),
		auth.NewAuthCommand(),
		credcmd.NewCredentialsCommand(),
		doctor.NewDoctorCommand(),
		gateway.NewGatewayCommand(),
		status.NewStatusCommand(),
		cron.NewCronCommand(),
		migrate.NewMigrateCommand(),
		skills.NewSkillsCommand(),
		model.NewModelCommand(),
		version.NewVersionCommand(),
	)

	return cmd
}

func main() {
	cmd := NewOmnipusCommand()
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
