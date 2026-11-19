package cmd

import (
	"fmt"
	"os"
	"reflect"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/XodoTechnology/pathvector-ng/pkg/bird"
	"github.com/XodoTechnology/pathvector-ng/pkg/cairn"
	"github.com/XodoTechnology/pathvector-ng/pkg/config"
	"github.com/XodoTechnology/pathvector-ng/pkg/frr"
	"github.com/XodoTechnology/pathvector-ng/pkg/plugin"
	"github.com/XodoTechnology/pathvector-ng/pkg/process"
)

func init() {
	rootCmd.AddCommand(versionCmd)
}

func versionBanner() string {
	buf := fmt.Sprintf(`Pathvector NG %s
Built %s on %s
`, version, commit, date)
	if len(plugin.Get()) > 0 {
		buf += "Plugins:\n"
		for name, p := range plugin.Get() {
			buf += fmt.Sprintf("  %s - %s [%s]\n", name, p.Description(), reflect.TypeOf(p).PkgPath())
		}
	} else {
		buf += "No plugins"
	}

	return buf
}

func printVersionBanner() {
	fmt.Println(versionBanner())
}

// queryDaemonVersion queries the configured backend's daemon for its version
func queryDaemonVersion(c *config.Config) (name string, detected string, err error) {
	switch {
	case isCairnBackend(c):
		name = "Cairn"
		detected, err = cairn.DaemonVersion(c)
	case isFRRBackend(c):
		name = "FRR"
		detected, err = frr.DaemonVersion(c)
	default:
		name = "BIRD"
		socket, _ := birdPaths(c)
		detected, err = bird.DaemonVersion(socket)
	}
	return name, detected, err
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Run: func(cmd *cobra.Command, args []string) {
		printVersionBanner()

		log.Debugf("Loading config from %s", configFile)
		configFile, err := os.ReadFile(configFile)
		if err != nil {
			log.Fatalf("Reading config file: %s", err)
		}
		c, err := process.Load(configFile)
		if err != nil {
			log.Fatal(err)
		}
		log.Debug("Finished loading config")

		name, detected, err := queryDaemonVersion(c)
		if err != nil {
			log.Fatalf("Querying %s version: %s", name, err)
		}
		fmt.Printf("%s: %s\n", name, detected)
	},
}
