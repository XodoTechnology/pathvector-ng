package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/fatih/color"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/XodoTechnology/pathvector-ng/pkg/bird"
	"github.com/XodoTechnology/pathvector-ng/pkg/config"
	"github.com/XodoTechnology/pathvector-ng/pkg/frr"
	"github.com/XodoTechnology/pathvector-ng/pkg/templating"
	"github.com/XodoTechnology/pathvector-ng/pkg/util"
)

var (
	realProtocolNames bool
	onlyBGP           bool
	showTags          bool
	jsonOut           bool
	tagFilter         []string
)

func init() {
	statusCmd.Flags().BoolVarP(&realProtocolNames, "real-protocol-names", "r", false, "use real protocol names")
	statusCmd.Flags().BoolVarP(&onlyBGP, "bgp", "b", false, "only show BGP protocols")
	statusCmd.Flags().BoolVar(&showTags, "tags", false, "show tags column")
	statusCmd.Flags().BoolVar(&jsonOut, "json", false, "output as JSON")
	statusCmd.Flags().StringArrayVarP(&tagFilter, "filter", "f", []string{}, "tags to filter by")
	rootCmd.AddCommand(statusCmd)
}

// Define the prefix order for sorting (case insensitive)
var prefixOrder = []string{
	"internal",
	"transit",
	"collector",
	"rs",
	"peer",
	"client",
	"downstream",
}

// getPrefixPriority returns the priority of a protocol name based on its prefix (case insensitive)
func getPrefixPriority(name string) int {
	lowerName := strings.ToLower(name)
	for i, prefix := range prefixOrder {
		if strings.HasPrefix(lowerName, prefix) {
			return i
		}
	}
	// If no prefix matches, put it at the end
	return len(prefixOrder)
}

var statusCmd = &cobra.Command{
	Use:     "status",
	Aliases: []string{"s", "status"},
	Short:   "Show protocol status",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadConfig()
		if err != nil {
			log.Warnf("Error loading config, falling back to no-config output parsing: %s", err)
		}

		if isCairnBackend(c) {
			runCairnStatus(c)
			return
		}
		if isFRRBackend(c) {
			runFRRStatus(c)
			return
		}

		birdSocket, birdDirectory := birdPaths(c)

		commandOutput, _, err := bird.RunCommand("show protocols all", birdSocket)
		if err != nil {
			log.Fatal(err)
		}

		// Read protocol names map
		var protocols map[string]*templating.Protocol
		if !realProtocolNames {
			protocols, err = readProtocolNames(birdDirectory)
			if err != nil {
				log.Fatalf("Reading protocol names: %v", err)
			}
		}

		protocolStates, err := bird.ParseProtocols(commandOutput)
		if err != nil {
			log.Fatal(err)
		}

		// Sort protocol states: non-BGP first (in original order), then BGP (by prefix and alphabetically)
		sort.SliceStable(protocolStates, func(i, j int) bool {
			// Check if protocols are BGP or non-BGP
			iIsBGP := protocolStates[i].BGP != nil
			jIsBGP := protocolStates[j].BGP != nil

			// Non-BGP protocols always come first
			if !iIsBGP && jIsBGP {
				return true
			}
			if iIsBGP && !jIsBGP {
				return false
			}

			// If both are non-BGP, keep original order (stable sort handles this)
			if !iIsBGP && !jIsBGP {
				return false
			}

			// Both are BGP protocols - sort by prefix priority, then alphabetically
			priorityI := getPrefixPriority(protocolStates[i].Name)
			priorityJ := getPrefixPriority(protocolStates[j].Name)

			// First compare by prefix priority
			if priorityI != priorityJ {
				return priorityI < priorityJ
			}

			// If same prefix priority, sort alphabetically (case insensitive)
			return strings.ToLower(protocolStates[i].Name) < strings.ToLower(protocolStates[j].Name)
		})

		if jsonOut {
			type session struct {
				Name     string   `json:"name"`
				Protocol string   `json:"protocol"`
				Neighbor string   `json:"neighbor,omitempty"`
				ASN      int      `json:"asn,omitempty"`
				State    string   `json:"state"`
				Imported int      `json:"routes_imported,omitempty"`
				Exported int      `json:"routes_exported,omitempty"`
				Since    string   `json:"since"`
				Info     string   `json:"info"`
				Tags     []string `json:"tags,omitempty"`
			}
			out := []session{}
			for _, st := range protocolStates {
				if onlyBGP && st.BGP == nil {
					continue
				}
				se := session{Name: st.Name, Protocol: st.Proto, State: st.State, Since: st.Since, Info: st.Info}
				if st.BGP != nil {
					se.Neighbor = st.BGP.NeighborAddress
					se.ASN = st.BGP.NeighborAS
				}
				if st.Routes != nil {
					se.Imported = st.Routes.Imported
					se.Exported = st.Routes.Exported
				}
				if p, found := protocols[st.Name]; found {
					se.Tags = p.Tags
					if !realProtocolNames {
						se.Name = p.Name
					}
				}
				if len(tagFilter) == 0 || containsAny(tagFilter, se.Tags) {
					out = append(out, se)
				}
			}
			j, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				log.Fatal(err)
			}
			fmt.Println(string(j))
			return
		}

		header := []string{"Peer", "AS", "Neighbor", "State", "In", "Out", "Since", "Info"}
		if showTags {
			header = append(header, "Tags")
		}
		util.PrintTable(header, func() [][]string {
			var table [][]string
			for _, protocolState := range protocolStates {
				if !onlyBGP || protocolState.BGP != nil {
					neighborAddr, neighborAS := "-", "-"
					if protocolState.BGP != nil {
						neighborAS = parseTableInt(protocolState.BGP.NeighborAS)
						neighborAddr = protocolState.BGP.NeighborAddress
					}

					// Lookup peer in protocol JSON
					protocolName := protocolState.Name
					var tags []string
					if p, found := protocols[protocolState.Name]; found {
						protocolName = p.Name
						tags = p.Tags
					}

					if len(tagFilter) == 0 || containsAny(tagFilter, tags) {
						row := []string{
							protocolName,
							neighborAS,
							neighborAddr,
							colorStatus(protocolState.State),
							parseTableInt(protocolState.Routes.Imported),
							parseTableInt(protocolState.Routes.Exported),
							protocolState.Since,
							colorStatus(protocolState.Info),
						}
						if showTags {
							row = append(row, strings.Join(tags, ", "))
						}
						table = append(table, row)
					}
				}
			}
			return table
		}())
	},
}

// runCairnStatus delegates to cairnctrl: the daemon's own neighbor view,
// which carries state, per-family route counts and policy names.
func runCairnStatus(c *config.Config) {
	socket, _ := cairnPaths(c)
	ctrl := ctrlBinary(c)
	if !jsonOut {
		if out, err := cairnCtrl(ctrl, "-s", socket, "status"); err == nil {
			fmt.Print(out)
		} else {
			log.Warnf("cairnctrl status: %v", err)
		}
	}
	args := []string{"-s", socket, "neighbors"}
	if jsonOut {
		args = append(args, "--json")
	}
	out, err := cairnCtrl(ctrl, args...)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(out)
}

// frrPeerJSON is the per-neighbor subset of `show bgp summary json` output.
type frrPeerJSON struct {
	RemoteAs int    `json:"remoteAs"`
	State    string `json:"state"`
	PfxRcd   int    `json:"pfxRcd"`
	PfxSnt   int    `json:"pfxSnt"`
	Uptime   string `json:"peerUptime"`
}

// frrSession mirrors the JSON session shape emitted by the bird backend.
type frrSession struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Neighbor string `json:"neighbor,omitempty"`
	ASN      int    `json:"asn,omitempty"`
	State    string `json:"state"`
	Imported int    `json:"routes_imported,omitempty"`
	Exported int    `json:"routes_exported,omitempty"`
	Since    string `json:"since"`
	Info     string `json:"info"`
}

// runFRRStatus reads BGP neighbor state from FRR via `vtysh -c "show bgp
// summary json"` and renders the same table/JSON shape as the other backends.
// Neighbor addresses are mapped back to pathvector peer names using the
// configured neighbor addresses.
func runFRRStatus(c *config.Config) {
	nameByAddr := map[string]string{}
	for name, p := range c.Peers {
		for _, addr := range peerNeighborAddrs(p) {
			nameByAddr[addr] = name
		}
	}

	out, err := frr.Show(c, "-c", "show bgp summary json")
	if err != nil {
		log.Fatal(err)
	}

	// `show bgp summary json` returns one object per AFI/SAFI that has
	// neighbors, e.g. {"ipv4-unicast": {"peers": {...}}, "ipv6-unicast": {...}}.
	var byAF map[string]struct {
		Peers map[string]frrPeerJSON `json:"peers"`
	}
	if err := json.Unmarshal([]byte(out), &byAF); err != nil {
		log.Fatalf("parsing vtysh JSON: %v", err)
	}

	sessions := []frrSession{}
	for afi, sec := range byAF {
		for addr, pj := range sec.Peers {
			name := nameByAddr[addr]
			if name == "" {
				name = addr
			}
			sessions = append(sessions, frrSession{
				Name:     name,
				Protocol: "bgp",
				Neighbor: addr,
				ASN:      pj.RemoteAs,
				State:    pj.State,
				Imported: pj.PfxRcd,
				Exported: pj.PfxSnt,
				Since:    pj.Uptime,
				Info:     afi,
			})
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Name < sessions[j].Name })

	if jsonOut {
		j, err := json.MarshalIndent(sessions, "", "  ")
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(string(j))
		return
	}

	util.PrintTable([]string{"Peer", "AS", "Neighbor", "State", "In", "Out", "Since", "Info"}, func() [][]string {
		var rows [][]string
		for _, s := range sessions {
			rows = append(rows, []string{
				s.Name,
				strconv.Itoa(s.ASN),
				s.Neighbor,
				colorStatus(s.State),
				strconv.Itoa(s.Imported),
				strconv.Itoa(s.Exported),
				s.Since,
				s.Info,
			})
		}
		return rows
	}())
}

// containsAny checks if two string slices contain any of the same elements
func containsAny(a []string, b []string) bool {
	for _, i := range a {
		for _, j := range b {
			if i == j {
				return true
			}
		}
	}
	return false
}

func parseTableInt(i int) string {
	if i == -1 {
		return ""
	}
	return fmt.Sprintf("%d", i)
}

func colorStatus(s string) string {
	if s == "up" || s == "Established" {
		return color.GreenString(s)
	} else if strings.Contains(s, "Error") || s == "down" {
		return color.RedString(s)
	} else if strings.Contains(s, "Connect") || s == "start" {
		return color.YellowString(s)
	}
	return s
}
