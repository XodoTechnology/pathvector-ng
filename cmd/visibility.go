package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// ripeStatBase is the RIPEstat API base URL; overridable for testing.
var ripeStatBase = "https://stat.ripe.net"

type ripeRoutingStatus struct {
	Status string `json:"status"`
	Data   struct {
		Resource   string `json:"resource"`
		Visibility struct {
			V4 struct {
				Seeing int `json:"ris_peers_seeing"`
				Total  int `json:"total_ris_peers"`
			} `json:"v4"`
			V6 struct {
				Seeing int `json:"ris_peers_seeing"`
				Total  int `json:"total_ris_peers"`
			} `json:"v6"`
		} `json:"visibility"`
		Origins []struct {
			Origin       int      `json:"origin"`
			RouteObjects []string `json:"route_objects"`
		} `json:"origins"`
	} `json:"data"`
}

var visibilityCmd = &cobra.Command{
	Use:   "visibility",
	Short: "Check originated prefixes against RIPEstat routing visibility",
	Long: `Queries RIPEstat for each originated prefix (top-level prefixes and
kernel.statics) and reports BGP visibility across RIS peers plus the origin
ASNs currently announcing it. A prefix whose origins do not include the local
ASN may be hijacked or simply not announced.`,
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

		client := &http.Client{Timeout: 20 * time.Second}
		var visible, hidden, foreign int
		for _, p := range prefixes {
			st, err := fetchRoutingStatus(client, p.String())
			if err != nil {
				fmt.Printf("ERROR    %-18s %s\n", p, err)
				continue
			}
			seeing, total := st.Data.Visibility.V4.Seeing, st.Data.Visibility.V4.Total
			if p.Addr().Is6() {
				seeing, total = st.Data.Visibility.V6.Seeing, st.Data.Visibility.V6.Total
			}
			origins := make([]int, 0, len(st.Data.Origins))
			ours := false
			for _, o := range st.Data.Origins {
				origins = append(origins, o.Origin)
				if o.Origin == c.ASN {
					ours = true
				}
			}
			sort.Ints(origins)
			originStr := "none"
			if len(origins) > 0 {
				originStr = fmt.Sprint(origins)
			}

			switch {
			case !ours && len(origins) > 0:
				foreign++
				fmt.Printf("FOREIGN  %-18s %d/%d peers  origins=%v (local AS%d absent)\n", p, seeing, total, originStr, c.ASN)
			case seeing == 0:
				hidden++
				fmt.Printf("HIDDEN   %-18s %d/%d peers  origins=%v\n", p, seeing, total, originStr)
			default:
				visible++
				fmt.Printf("VISIBLE  %-18s %d/%d peers  origins=%v\n", p, seeing, total, originStr)
			}
		}
		fmt.Printf("\n%d visible, %d hidden, %d originated by other ASNs\n", visible, hidden, foreign)
	},
}

func fetchRoutingStatus(client *http.Client, prefix string) (*ripeRoutingStatus, error) {
	u := fmt.Sprintf("%s/data/routing-status/data.json?resource=%s", ripeStatBase, url.QueryEscape(prefix))
	resp, err := client.Get(u)
	if err != nil {
		return nil, fmt.Errorf("querying RIPEstat: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("RIPEstat returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading RIPEstat response: %w", err)
	}
	var st ripeRoutingStatus
	if err := json.Unmarshal(body, &st); err != nil {
		return nil, fmt.Errorf("parsing RIPEstat response: %w", err)
	}
	if st.Status != "ok" {
		return nil, fmt.Errorf("RIPEstat status: %s", st.Status)
	}
	return &st, nil
}

func init() {
	rootCmd.AddCommand(visibilityCmd)
}
