package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/XodoTechnology/pathvector-ng/pkg/autodoc"
)

func init() {
	rootCmd.AddCommand(docsCmd)
}

var docsCmd = &cobra.Command{
	Use:    "docs",
	Short:  "Generate documentation",
	Hidden: true,
	Run: func(cmd *cobra.Command, args []string) {
		if err := autodoc.DocumentConfig(true); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	},
}
