package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/XodoTechnology/pathvector-ng/pkg/bird"
	"github.com/XodoTechnology/pathvector-ng/pkg/cairn"
	"github.com/XodoTechnology/pathvector-ng/pkg/config"
	"github.com/XodoTechnology/pathvector-ng/pkg/frr"
	"github.com/XodoTechnology/pathvector-ng/pkg/peeringdb"
)

var doctorJSON bool

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "output results as JSON")
	rootCmd.AddCommand(doctorCmd)
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check external dependencies (daemon, IRR, RTR, PeeringDB)",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadConfig()
		if err != nil {
			log.Fatalf("config: %s", err)
		}

		var results []doctorResult
		results = append(results, doctorDaemon(c))
		results = append(results, doctorBGPQ4(c)...)
		results = append(results, doctorIRR(c)...)
		results = append(results, doctorRTR(c)...)
		results = append(results, doctorPeeringDB(c)...)

		failed := 0
		for _, r := range results {
			if r.Status != "ok" {
				failed++
			}
		}

		if doctorJSON {
			out, _ := json.MarshalIndent(results, "", "  ")
			fmt.Println(string(out))
		} else {
			for _, r := range results {
				mark := "ok"
				if r.Status != "ok" {
					mark = "FAIL"
				}
				fmt.Printf("%-6s %-12s %s\n", mark, r.Name, r.Detail)
			}
		}

		if failed > 0 {
			log.Fatalf("%d check(s) failed", failed)
		}
	},
}

type doctorResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func doctorDaemon(c *config.Config) doctorResult {
	var version string
	var err error
	switch c.Backend {
	case "frr":
		version, err = frr.DaemonVersion(c)
	case "cairn":
		version, err = cairn.DaemonVersion(c)
	default:
		version, err = bird.DaemonVersion(c.BIRDSocket)
	}
	if err != nil {
		return doctorResult{Name: "daemon", Status: "fail", Detail: fmt.Sprintf("%s: %s", c.Backend, err)}
	}
	return doctorResult{Name: "daemon", Status: "ok", Detail: fmt.Sprintf("%s %s", c.Backend, version)}
}

func doctorBGPQ4(c *config.Config) []doctorResult {
	if len(c.IRRServerList()) == 0 {
		return nil
	}
	if _, err := exec.LookPath("bgpq4"); err != nil {
		return []doctorResult{{Name: "bgpq4", Status: "fail", Detail: "binary not found in PATH (required for IRR queries)"}}
	}
	return []doctorResult{{Name: "bgpq4", Status: "ok", Detail: "found in PATH"}}
}

func doctorIRR(c *config.Config) []doctorResult {
	servers := c.IRRServerList()
	if len(servers) == 0 {
		return nil
	}
	var out []doctorResult
	for i, srv := range servers {
		host, port, err := net.SplitHostPort(srv)
		if err != nil {
			host, port = srv, "43"
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 5*time.Second)
		if err != nil {
			out = append(out, doctorResult{Name: fmt.Sprintf("irr[%d]", i), Status: "fail", Detail: fmt.Sprintf("%s unreachable: %s", srv, err)})
			continue
		}
		conn.Close()
		out = append(out, doctorResult{Name: fmt.Sprintf("irr[%d]", i), Status: "ok", Detail: srv})
	}
	return out
}

func doctorRTR(c *config.Config) []doctorResult {
	var out []doctorResult
	for i, ep := range c.EffectiveRTREndpoints() {
		addr := net.JoinHostPort(ep.Host, fmt.Sprint(ep.Port))
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			out = append(out, doctorResult{Name: fmt.Sprintf("rtr[%d]", i), Status: "fail", Detail: fmt.Sprintf("%s unreachable: %s", addr, err)})
			continue
		}
		conn.Close()
		out = append(out, doctorResult{Name: fmt.Sprintf("rtr[%d]", i), Status: "ok", Detail: addr})
	}
	return out
}

func doctorPeeringDB(c *config.Config) []doctorResult {
	client := http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, peeringdb.Endpoint+"/net?asn=64496", nil)
	if err != nil {
		return []doctorResult{{Name: "peeringdb", Status: "fail", Detail: err.Error()}}
	}
	res, err := client.Do(req)
	if err != nil {
		return []doctorResult{{Name: "peeringdb", Status: "fail", Detail: err.Error()}}
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return []doctorResult{{Name: "peeringdb", Status: "fail", Detail: "HTTP " + res.Status}}
	}
	return []doctorResult{{Name: "peeringdb", Status: "ok", Detail: peeringdb.Endpoint}}
}
