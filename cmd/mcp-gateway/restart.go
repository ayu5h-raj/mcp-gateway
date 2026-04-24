package main

import (
	"github.com/spf13/cobra"
)

func newRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Stop then start the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := newStopCmd().RunE(cmd, args); err != nil {
				// Tolerate "no daemon running" — proceed to start.
			}
			return newStartCmd().RunE(cmd, args)
		},
	}
}
