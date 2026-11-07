package cmd

import (
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/XodoTechnology/pathvector-ng/pkg/bird"
	"github.com/XodoTechnology/pathvector-ng/pkg/config"
	"github.com/XodoTechnology/pathvector-ng/pkg/frr"
)

var restartAll bool

func init() {
	restartCmd.Flags().BoolVarP(&restartAll, "all", "a", false, "restart all BIRD protocols")
	rootCmd.AddCommand(restartCmd)
}

// birdErrorResponses are substrings of BIRD responses that indicate a restart failed
var birdErrorResponses = []string{"No protocols match", "syntax error"}

var restartCmd = &cobra.Command{
	Use:   "restart [name...]",
	Short: "Restart BIRD protocols",
	Long: `Restart BIRD protocols.

Each name may be a peer name as shown by "pathvector status" (restarting all of the peer's
BIRD protocols, e.g. both IPv4 and IPv6 sessions) or a BIRD protocol name as shown by
"pathvector status --real-protocol-names".`,
	Run: func(cmd *cobra.Command, args []string) {
		if !restartAll && len(args) == 0 {
			log.Fatal("Usage: pathvector restart <name> [name...] (or --all)")
		}
		if restartAll && len(args) > 0 {
			log.Fatal("Protocol names can't be combined with --all")
		}

		c, err := loadConfig()
		if err != nil {
			log.Warnf("Error loading config, using default BIRD socket and directory: %s", err)
		}

		if isCairnBackend(c) {
			runCairnRestart(c, args, restartAll)
			return
		}
		if isFRRBackend(c) {
			runFRRRestart(c, args, restartAll)
			return
		}

		birdSocket, birdDirectory := birdPaths(c)

		var targets []string
		if restartAll {
			targets = []string{"all"}
		} else {
			protocols, err := readProtocolNames(birdDirectory)
			if err != nil {
				log.Warnf("Reading protocol names, treating names as BIRD protocol names: %v", err)
			}
			targets = resolveProtocols(args, protocols)
		}

		failed := 0
		for _, target := range targets {
			command := "restart all"
			if !restartAll {
				// Quote the name so BIRD treats it as a protocol pattern: an unquoted unknown
				// name is a syntax error ("unexpected CF_SYM_UNDEFINED"), whereas a quoted one
				// gives a clear "No protocols match". Reject characters that would break the quoting.
				if strings.ContainsAny(target, "\" \t\r\n") {
					log.Errorf("Invalid protocol name %q", target)
					failed++
					continue
				}
				command = fmt.Sprintf("restart \"%s\"", target)
			}

			log.Debugf("Restarting %s", target)
			resp, _, err := bird.RunCommand(command, birdSocket)
			if err != nil {
				log.Fatal(err)
			}
			resp = strings.TrimSpace(resp)

			isError := false
			for _, e := range birdErrorResponses {
				if strings.Contains(resp, e) {
					isError = true
				}
			}
			if isError {
				log.Errorf("%s: %s", target, resp)
				failed++
				continue
			}
			for _, line := range strings.Split(resp, "\n") {
				fmt.Println(strings.TrimSpace(line))
			}
		}

		if failed > 0 {
			log.Fatalf("Failed to restart %d of %d protocols", failed, len(targets))
		}
	},
}

// runCairnRestart resets cairnd sessions via `cairnctrl reset`. Arguments may
// be peer names (all of the peer's neighbor addresses are reset) or raw
// neighbor addresses. --all resets every configured neighbor.
func runCairnRestart(c *config.Config, args []string, all bool) {
	socket, _ := cairnPaths(c)
	ctrl := ctrlBinary(c)

	// Build peer name → neighbor addresses and address → peer name maps.
	peerAddrs := map[string][]string{}
	addrPeer := map[string]string{}
	var allAddrs []string
	for name, p := range c.Peers {
		if p.NeighborIPs == nil {
			continue
		}
		for _, nb := range *p.NeighborIPs {
			addr := strings.SplitN(nb, "%", 2)[0]
			peerAddrs[name] = append(peerAddrs[name], addr)
			addrPeer[addr] = name
			allAddrs = append(allAddrs, addr)
		}
	}

	var targets []string
	switch {
	case all:
		targets = allAddrs
	case len(args) == 0:
		log.Fatal("Usage: pathvector restart <name|address> [name|address...] (or --all)")
	default:
		seen := map[string]bool{}
		for _, arg := range args {
			addrs, ok := peerAddrs[arg]
			if !ok {
				addrs = []string{arg} // treat as a raw neighbor address
			}
			for _, a := range addrs {
				if !seen[a] {
					seen[a] = true
					targets = append(targets, a)
				}
			}
		}
	}

	failed := 0
	for _, addr := range targets {
		log.Debugf("Resetting %s", addr)
		out, err := cairnCtrl(ctrl, "-s", socket, "reset", addr)
		if err != nil {
			log.Errorf("%s: %v: %s", addr, err, strings.TrimSpace(out))
			failed++
			continue
		}
		fmt.Print(out)
	}
	if failed > 0 {
		log.Fatalf("Failed to reset %d of %d sessions", failed, len(targets))
	}
}

// runFRRRestart bounces FRR BGP sessions via `vtysh -c "clear bgp <addr>"`.
// Arguments may be peer names (all of the peer's neighbor addresses are
// cleared) or raw neighbor addresses. --all clears every BGP session.
func runFRRRestart(c *config.Config, args []string, all bool) {
	// Build peer name → neighbor addresses map (same shape as the Cairn path).
	peerAddrs := map[string][]string{}
	var allAddrs []string
	for name, p := range c.Peers {
		for _, addr := range peerNeighborAddrs(p) {
			peerAddrs[name] = append(peerAddrs[name], addr)
			allAddrs = append(allAddrs, addr)
		}
	}

	var targets []string
	switch {
	case all:
		targets = []string{"*"}
	case len(args) == 0:
		log.Fatal("Usage: pathvector restart <name|address> [name|address...] (or --all)")
	default:
		seen := map[string]bool{}
		for _, arg := range args {
			addrs, ok := peerAddrs[arg]
			if !ok {
				addrs = []string{arg} // treat as a raw neighbor address
			}
			for _, a := range addrs {
				if !seen[a] {
					seen[a] = true
					targets = append(targets, a)
				}
			}
		}
	}

	failed := 0
	for _, addr := range targets {
		log.Debugf("Clearing BGP session %s", addr)
		if _, err := frr.Show(c, "-c", "clear bgp "+addr); err != nil {
			log.Errorf("%s: %v", addr, err)
			failed++
			continue
		}
		fmt.Printf("Cleared BGP session %s\n", addr)
	}
	if failed > 0 {
		log.Fatalf("Failed to clear %d of %d sessions", failed, len(targets))
	}
}
