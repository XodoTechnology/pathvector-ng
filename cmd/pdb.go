package cmd

import (
	"fmt"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/XodoTechnology/pathvector-ng/pkg/peeringdb"
)

var pdbIXLANs bool

func init() {
	pdbCmd.Flags().BoolVar(&pdbIXLANs, "ixlans", false, "show PeeringDB IX LAN attachments instead of network info")
	rootCmd.AddCommand(pdbCmd)
}

var pdbCmd = &cobra.Command{
	Use:   "pdb ASN",
	Short: "Show PeeringDB data for an ASN",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadConfig()
		if err != nil {
			log.Fatal(err)
		}

		asn, err := strconv.ParseUint(strings.TrimPrefix(strings.ToUpper(args[0]), "AS"), 10, 32)
		if err != nil || asn == 0 {
			log.Fatalf("invalid ASN %q", args[0])
		}

		if pdbIXLANs {
			lanes, err := peeringdb.IXLANs(uint32(asn), c.PeeringDBQueryTimeout, c.PeeringDBAPIKey)
			if err != nil {
				log.Fatalf("PeeringDB IX LANs for AS%d: %s", asn, err)
			}
			fmt.Printf("AS%d IX LANs: %d\n", asn, len(lanes))
			for _, l := range lanes {
				status := ""
				if l.Operational {
					status = " (operational)"
				}
				fmt.Printf("  ixlan %d ix %d %s%s\n", l.IxlanId, l.IxId, l.Name, status)
			}
			return
		}

		d, err := peeringdb.NetworkInfoWithFallback(uint32(asn), c.PeeringDBQueryTimeout, c.PeeringDBAPIKey, c.PeeringDBCache, c.CacheDirectory, false)
		if err != nil {
			log.Fatalf("PeeringDB network info for AS%d: %s", asn, err)
		}
		fmt.Printf("name:          %s\n", d.Name)
		fmt.Printf("asn:           AS%d\n", d.ASN)
		fmt.Printf("as-set:        %s\n", d.ASSet)
		fmt.Printf("info-type:     %s\n", d.InfoType)
		fmt.Printf("import-limit4: %d\n", d.ImportLimit4)
		fmt.Printf("import-limit6: %d\n", d.ImportLimit6)
	},
}
