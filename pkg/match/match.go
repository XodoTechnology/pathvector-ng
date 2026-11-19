package match

import (
	"fmt"
	"strings"

	"github.com/XodoTechnology/pathvector-ng/pkg/peeringdb"
)

// CommonIXs gets common IXPs from PeeringDB
func CommonIXs(a uint32, b uint32, yamlFormat bool, queryTimeout uint, apiKey string) (string, error) {
	networkA, err := peeringdb.IXLANs(a, queryTimeout, apiKey)
	if err != nil {
		return "", fmt.Errorf("AS%d: %w", a, err)
	}
	networkB, err := peeringdb.IXLANs(b, queryTimeout, apiKey)
	if err != nil {
		return "", fmt.Errorf("AS%d: %w", b, err)
	}

	networkBInfo, err := peeringdb.NetworkInfo(b, queryTimeout, apiKey, true)
	if err != nil {
		return "", fmt.Errorf("AS%d: %w", b, err)
	}

	out := ""
	for _, ixA := range networkA {
		for _, ixB := range networkB {
			if ixA.IxlanId == ixB.IxlanId {
				if !yamlFormat {
					out += fmt.Sprintf(`%s
  AS%d
  %s
  %s

  AS%d
  %s
  %s

`, ixA.Name, a, ixA.Ipaddr4, ixA.Ipaddr6, b, ixB.Ipaddr4, ixB.Ipaddr6)
				} else {
					out += fmt.Sprintf(`  %s %s:
    asn: %d
    neighbors:
      - %s
      - %s

`, networkBInfo.Name, strings.Split(ixA.Name, ":")[0], b, ixB.Ipaddr4, ixB.Ipaddr6)
				}
			}
		}
	}
	return out, nil
}
