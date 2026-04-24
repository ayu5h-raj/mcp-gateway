package main

import (
	"github.com/spf13/cobra"
)

func newRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Stop then start the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Tolerate "no daemon running" from stop — proceed to start anyway.
			_ = newStopCmd().RunE(cmd, args)
			return newStartCmd().RunE(cmd, args)
		},
	}
}
