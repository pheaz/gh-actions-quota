package main

import (
	"context"
	"errors"
	"io"

	"github.com/philippwallrafen/gh-actions-quota/internal/action"
	"github.com/philippwallrafen/gh-actions-quota/internal/setup"
	"github.com/spf13/cobra"
)

func newRootCommand(buildVersion string, input io.Reader, output, errOutput io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "gh-actions-quota",
		Short:         "Monitor and gate GitHub Actions quota usage",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		Version:       buildVersion,
		Annotations: map[string]string{
			cobra.CommandDisplayNameAnnotation: "gh actions-quota",
		},
	}
	root.SetIn(input)
	root.SetOut(output)
	root.SetErr(errOutput)
	root.SetVersionTemplate("gh actions-quota {{.Version}}\n")

	root.AddCommand(
		&cobra.Command{
			Use:    "action",
			Short:  "Run the GitHub Action adapter",
			Hidden: true,
			Args:   cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return action.Run(cmd.Context(), cmd.OutOrStdout())
			},
		},
		newQuotaCommand("setup", "Configure quota gating for the current repository", setup.RunWithQuota),
		newQuotaCommand("status", "Show repository setup state and Actions quota usage", setup.StatusWithQuota),
		&cobra.Command{
			Use:   "uninstall",
			Short: "Remove gh-actions-quota setup from the current repository",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return setup.Uninstall(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
			},
		},
		newAuthCommand(),
	)
	root.InitDefaultCompletionCmd()
	return root
}

func newAuthCommand() *cobra.Command {
	auth := &cobra.Command{
		Use:   "auth",
		Short: "Manage gh-actions-quota GitHub App authentication",
		Args:  cobra.NoArgs,
	}
	auth.AddCommand(
		&cobra.Command{
			Use:   "login",
			Short: "Authenticate the quota account for the current context",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return setup.AuthLogin(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show gh-actions-quota authentication state",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				authenticated, err := setup.AuthStatus(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
				if err != nil {
					return err
				}
				if !authenticated {
					return silentError{err: errors.New("not authenticated")}
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "logout",
			Short: "Remove the local gh-actions-quota authorization",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return setup.AuthLogout(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
			},
		},
	)
	return auth
}

func newQuotaCommand(name, description string, run func(context.Context, io.Reader, io.Writer, string) error) *cobra.Command {
	var included string
	cmd := &cobra.Command{Use: name, Short: description, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return run(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), included)
	}}
	cmd.Flags().StringVar(&included, "quota-minutes", "", "Override included monthly Actions minutes with a verified allowance")
	return cmd
}
