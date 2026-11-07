package cmd

import (
	"fmt"
	"strings"

	"github.com/fatih/color"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"

	"github.com/XodoTechnology/pathvector-ng/pkg/bird"
	"github.com/XodoTechnology/pathvector-ng/pkg/cairn"
	"github.com/XodoTechnology/pathvector-ng/pkg/config"
	"github.com/XodoTechnology/pathvector-ng/pkg/frr"
)

var compatCmd = &cobra.Command{
	Use:   "compat",
	Short: "Check the routing daemon's version against the tested range",
	Long: `Query the configured routing daemon for its version and compare it
against the releases covered by Pathvector's test matrices. Reports whether
the daemon is within the verified range, newer than anything tested, or older
than the minimum supported version.`,
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadConfig()
		if err != nil {
			log.Warnf("Error loading config, assuming bird backend: %s", err)
			c = nil
		}
		runCompat(c)
	},
}

func init() {
	rootCmd.AddCommand(compatCmd)
}

// compatStatus classifies a detected daemon version against the tested range.
func compatStatus(detected, minV, maxV string) string {
	nv := normalizeSemver(detected)
	if nv == "" {
		return "unknown"
	}
	if semver.Compare(nv, "v"+minV) < 0 {
		return "older-than-tested"
	}
	if semver.Compare(nv, "v"+maxV) > 0 {
		return "newer-than-tested"
	}
	return "supported"
}

// normalizeSemver turns a daemon-reported version ("2.14", "10.2.6",
// "0.1.1+git7129.83a42fb0") into a golang.org/x/mod/semver string, or ""
// when it can't be parsed.
func normalizeSemver(v string) string {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	if semver.IsValid("v" + v) {
		return "v" + v
	}
	// Fall back to the numeric core, dropping any -prerelease/+build suffix.
	if i := strings.IndexAny(v, "+-"); i >= 0 {
		v = v[:i]
	}
	if semver.IsValid("v" + v) {
		return "v" + v
	}
	return ""
}

func runCompat(c *config.Config) {
	backend := "bird"
	if isCairnBackend(c) {
		backend = "cairn"
	} else if isFRRBackend(c) {
		backend = "frr"
	}

	var daemon, detected, minV, maxV string
	var err error

	switch backend {
	case "cairn":
		daemon = "Cairn"
		minV, maxV = cairn.TestedMin, cairn.TestedMax
		detected, err = cairn.DaemonVersion(c)
	case "frr":
		daemon = "FRR"
		minV, maxV = frr.TestedMin, frr.TestedMax
		detected, err = frr.DaemonVersion(c)
	default:
		daemon = "BIRD"
		minV, maxV = bird.TestedMin, bird.TestedMax
		socket, _ := birdPaths(c)
		detected, err = bird.DaemonVersion(socket)
	}

	status := "supported"
	switch {
	case err != nil:
		status = "unreachable"
	case detected == "" || detected == bird.UnknownVersion:
		status = "unknown"
	default:
		status = compatStatus(detected, minV, maxV)
	}

	fmt.Printf("Backend:      %s\n", backend)
	fmt.Printf("Daemon:       %s\n", daemon)
	if err != nil {
		fmt.Printf("Version:      unreachable — %v\n", err)
	} else {
		fmt.Printf("Version:      %s\n", detected)
	}
	fmt.Printf("Tested range: v%s – v%s\n", minV, maxV)

	statusColor := color.New(color.FgGreen)
	switch status {
	case "supported":
		// green
	case "newer-than-tested":
		statusColor = color.New(color.FgYellow)
	case "older-than-tested", "unreachable":
		statusColor = color.New(color.FgRed)
	default: // unknown
		statusColor = color.New(color.FgYellow)
	}
	fmt.Printf("Status:       %s\n", statusColor.Sprint(status))

	if status == "newer-than-tested" {
		fmt.Println()
		fmt.Println("The daemon is newer than anything in the test matrix. It will")
		fmt.Println("probably work, but isn't covered by CI — please report any issues.")
	} else if status == "older-than-tested" {
		fmt.Println()
		fmt.Println("The daemon is older than the minimum tested version. Upgrade it,")
		fmt.Println("or expect unsupported behavior.")
	}
}
