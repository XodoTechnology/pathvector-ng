package cmd

import (
	"fmt"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/XodoTechnology/pathvector-ng/pkg/irr"
)

func init() {
	rootCmd.AddCommand(irrCmd)
	irrCmd.AddCommand(irrASSetCmd)
	irrCmd.AddCommand(irrAutNumCmd)
}

var irrCmd = &cobra.Command{
	Use:   "irr",
	Short: "Query IRR data",
}

var irrASSetCmd = &cobra.Command{
	Use:   "as-set NAME",
	Short: "Show AS members and announced prefixes of an IRR as-set",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadConfig()
		if err != nil {
			log.Fatal(err)
		}

		servers := c.IRRServerList()
		name := args[0]

		members, err := irr.ASMembers(name, servers, c.IRRQueryTimeout, c.BGPQArgs)
		if err != nil {
			log.Fatalf("IRR as-set members for %s: %s", name, err)
		}

		pfx4, err := irr.PrefixSet(name, 4, servers, c.IRRQueryTimeout, c.BGPQArgs)
		if err != nil {
			log.Fatalf("IRR IPv4 prefixes for %s: %s", name, err)
		}
		pfx6, err := irr.PrefixSet(name, 6, servers, c.IRRQueryTimeout, c.BGPQArgs)
		if err != nil {
			log.Fatalf("IRR IPv6 prefixes for %s: %s", name, err)
		}

		fmt.Printf("AS-SET %s\n", name)
		fmt.Printf("  members: %d\n", len(members))
		for _, m := range members {
			fmt.Printf("    AS%d\n", m)
		}
		fmt.Printf("  ipv4 prefixes: %d\n", len(pfx4))
		for _, p := range pfx4 {
			fmt.Printf("    %s\n", p)
		}
		fmt.Printf("  ipv6 prefixes: %d\n", len(pfx6))
		for _, p := range pfx6 {
			fmt.Printf("    %s\n", p)
		}
	},
}

var irrAutNumCmd = &cobra.Command{
	Use:   "aut-num ASN",
	Short: "Show the IRR aut-num object for an ASN",
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

		var lastErr error
		for _, srv := range c.IRRServerList() {
			out, err := irr.QueryAutNum(srv, uint32(asn), c.IRRQueryTimeout, c.BGPQArgs)
			if err == nil {
				fmt.Println(out)
				return
			}
			lastErr = err
		}
		log.Fatalf("IRR aut-num query for AS%d failed on all servers: %s", asn, lastErr)
	},
}
