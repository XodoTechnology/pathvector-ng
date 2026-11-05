package cmd

import (
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/XodoTechnology/pathvector-ng/pkg/process"
)

func init() {
	checkCmd.Flags().BoolVar(&offline, "offline", false, "Don't query IRR or PeeringDB, only use data cached by previous runs")
	rootCmd.AddCommand(checkCmd)
}

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Validate configuration: render and BIRD-validate without applying",
	Long: `Renders the config and runs 'bird -p' validation against it without writing
to the BIRD directory or reconfiguring BIRD. Useful as a pre-commit or CI lint
for pathvector.yml — exits non-zero if the config is invalid.`,
	Run: func(cmd *cobra.Command, args []string) {
		// dryRun + noConfigure force a validate-only run
		if err := process.Run(configFile, lockFile, version, true, true, false, offline); err != nil {
			log.Fatal(err)
		}
	},
}
