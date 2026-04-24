package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ayu5h-raj/mcp-gateway/internal/admin"
	"github.com/ayu5h-raj/mcp-gateway/internal/adminclient"
)

func newAddCmd() *cobra.Command {
	var (
		command  string
		args     []string
		envs     []string
		prefix   string
		disabled bool
	)
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add an MCP server (writes config + reconciles)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, posArgs []string) error {
			if command == "" {
				return errors.New("--command is required")
			}
			env := map[string]string{}
			for _, e := range envs {
				k, v, ok := strings.Cut(e, "=")
				if !ok {
					return fmt.Errorf("invalid --env %q (must be KEY=VALUE)", e)
				}
				env[k] = v
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			sock := filepath.Join(home, ".mcp-gateway", "sock")
			c := adminclient.New(sock)
			spec := admin.ServerSpec{
				Name:    posArgs[0],
				Command: command,
				Args:    args,
				Env:     env,
				Prefix:  prefix,
				Enabled: !disabled,
			}
			if err := c.Post("/admin/servers", spec, nil); err != nil {
				return err
			}
			fmt.Printf("added %s\n", posArgs[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&command, "command", "", "executable to run (required)")
	cmd.Flags().StringArrayVar(&args, "arg", nil, "argument to pass (repeatable)")
	cmd.Flags().StringArrayVar(&envs, "env", nil, "KEY=VALUE env var (repeatable; use ${secret:NAME} for keychain refs)")
	cmd.Flags().StringVar(&prefix, "prefix", "", "tool prefix (default: server name)")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "add but don't start")
	return cmd
}
