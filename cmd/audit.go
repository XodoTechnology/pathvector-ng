package cmd

import (
	"fmt"
	"net/netip"
	"sort"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/XodoTechnology/pathvector-ng/pkg/config"
	"github.com/XodoTechnology/pathvector-ng/pkg/irr"
)

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Audit originated prefixes against IRR route objects",
	Long: `Checks every prefix pathvector is configured to originate (top-level
prefixes and kernel.statics) against the IRR. Reports prefixes that have no
route object for the local ASN — the most common cause of routes being dropped
by peers' IRR filters.`,
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadConfig()
		if err != nil {
			log.Fatalf("loading config: %s", err)
		}

		prefixes := originatedPrefixes(c)
		if len(prefixes) == 0 {
			fmt.Println("no originated prefixes configured (prefixes/kernel.statics are empty)")
			return
		}

		// Query the IRR for everything our ASN originates (!g/!6 queries), then
		// normalize keys to canonical prefix form for comparison.
		irrRaw, err := irr.OriginPrefixes(c.IRRServerList(), []uint32{uint32(c.ASN)}, c.IRRQueryTimeout, c.BGPQArgs)
		if err != nil {
			log.Fatalf("IRR query failed: %s", err)
		}
		irrSet := make(map[string]struct{}, len(irrRaw))
		for p := range irrRaw {
			if np, err := netip.ParsePrefix(p); err == nil {
				irrSet[np.Masked().String()] = struct{}{}
			} else {
				irrSet[p] = struct{}{}
			}
		}

		var ok, covered, missing []string
		for _, p := range prefixes {
			if _, found := irrSet[p.String()]; found {
				ok = append(ok, p.String())
				continue
			}
			// A less-specific route object may still cover this prefix for peers
			// that accept child prefixes — check progressively shorter masks.
			found := false
			for bits := p.Bits() - 1; bits >= 0; bits-- {
				if _, found = irrSet[netip.PrefixFrom(p.Addr(), bits).Masked().String()]; found {
					break
				}
			}
			if found {
				covered = append(covered, p.String())
			} else {
				missing = append(missing, p.String())
			}
		}

		sort.Strings(ok)
		sort.Strings(covered)
		sort.Strings(missing)

		for _, p := range ok {
			fmt.Printf("ok       %s\n", p)
		}
		for _, p := range covered {
			fmt.Printf("covered  %s  (only via a less-specific route object)\n", p)
		}
		for _, p := range missing {
			fmt.Printf("MISSING  %s\n", p)
		}
		fmt.Printf("\n%d authorized, %d covered by parent object, %d missing IRR route objects (AS%d)\n",
			len(ok), len(covered), len(missing), c.ASN)
		if len(missing) > 0 {
			log.Fatalf("%d originated prefix(es) have no IRR route object for AS%d", len(missing), c.ASN)
		}
	},
}

// originatedPrefixes returns the unique set of prefixes pathvector originates:
// the top-level `prefixes` list plus the keys of kernel.statics.
func originatedPrefixes(c *config.Config) []netip.Prefix {
	seen := make(map[netip.Prefix]struct{})
	add := func(s string) {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			log.Warnf("ignoring unparsable originated prefix %q: %s", s, err)
			return
		}
		seen[p.Masked()] = struct{}{}
	}
	for _, p := range c.Prefixes {
		add(p)
	}
	if c.Kernel != nil {
		for p := range c.Kernel.Statics {
			add(p)
		}
	}
	out := make([]netip.Prefix, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func init() {
	rootCmd.AddCommand(auditCmd)
}
