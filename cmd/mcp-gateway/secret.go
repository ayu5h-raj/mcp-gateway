package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/ayu5h-raj/mcp-gateway/internal/admin"
	"github.com/ayu5h-raj/mcp-gateway/internal/adminclient"
)

func newSecretCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Inspect ${env:*} references in the config",
	}
	cmd.AddCommand(newSecretListCmd())
	return cmd
}

func newSecretListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List env vars the config references and whether each is set",
		RunE: func(_ *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			c := adminclient.New(filepath.Join(home, ".mcp-gateway", "sock"))
			var got []admin.SecretInfo
			if err := c.Get("/admin/secret", &got); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ENV VAR\tSET?\tUSED BY")
			for _, s := range got {
				mark := "✗"
				if s.Set {
					mark = "✓"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Name, mark, strings.Join(s.UsedBy, ", "))
			}
			return tw.Flush()
		},
	}
}
