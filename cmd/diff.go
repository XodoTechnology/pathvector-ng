package cmd

import (
	"os"
	"os/exec"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/XodoTechnology/pathvector-ng/pkg/process"
)

func init() {
	diffCmd.Flags().BoolVar(&offline, "offline", false, "Don't query IRR or PeeringDB, only use data cached by previous runs")
	rootCmd.AddCommand(diffCmd)
}

var diffCmd = &cobra.Command{
	Use:   "diff",
	Short: "Show a unified diff between the rendered config and the live BIRD directory",
	Long: `Renders the configuration into the cache directory (without applying), then
shows a unified diff against the files currently in bird-directory. Exits 0 when
the rendered output matches the live config, 1 when they differ.`,
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadConfig()
		if err != nil {
			log.Fatal(err)
		}

		// Render into the cache directory without applying
		if err := process.Run(configFile, lockFile, version, true, true, false, offline); err != nil {
			log.Fatal(err)
		}

		// diff -urN cache/ live/ — exit status 0 same, 1 differ, >1 error
		// -I ignores the "# Update time:" header; side files that only exist in
		// one directory are excluded.
		//nolint:gosec
		diff := exec.Command("diff", "-urN", "-I", "^# Update time:", "-x", "protocols.json", "-x", "pathvector.yml", c.CacheDirectory, c.BIRDDirectory)
		diff.Stdout = os.Stdout
		diff.Stderr = os.Stderr
		if err := diff.Run(); err != nil {
			if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
				os.Exit(1)
			}
			log.Fatalf("diff: %s", err)
		}
	},
}
