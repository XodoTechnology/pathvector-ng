package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"

	"github.com/creasty/defaults"
	log "github.com/sirupsen/logrus"

	"github.com/XodoTechnology/pathvector-ng/pkg/config"
	"github.com/XodoTechnology/pathvector-ng/pkg/templating"
)

// birdPaths returns the BIRD control socket and BIRD config directory from c,
// falling back to the config defaults when c is nil or the values are unset
func birdPaths(c *config.Config) (socket string, directory string) {
	var def config.Config
	if err := defaults.Set(&def); err != nil {
		log.Warnf("Setting config defaults: %v", err)
	}
	socket, directory = def.BIRDSocket, def.BIRDDirectory
	if c != nil {
		if c.BIRDSocket != "" {
			socket = c.BIRDSocket
		}
		if c.BIRDDirectory != "" {
			directory = c.BIRDDirectory
		}
	}
	return socket, directory
}

// cairnPaths returns the cairnd control socket and Cairn config directory
// from c, falling back to config defaults when unset.
func cairnPaths(c *config.Config) (socket string, directory string) {
	var def config.Config
	if err := defaults.Set(&def); err != nil {
		log.Warnf("Setting config defaults: %v", err)
	}
	socket, directory = def.CairnSocket, def.CairnDirectory
	if c != nil {
		if c.CairnSocket != "" {
			socket = c.CairnSocket
		}
		if c.CairnDirectory != "" {
			directory = c.CairnDirectory
		}
	}
	return socket, directory
}

// peerNeighborAddrs returns the configured neighbor addresses of a peer with
// any IPv6 zone suffix stripped, for mapping BGP neighbor addresses back to
// pathvector peer names.
func peerNeighborAddrs(p *config.Peer) []string {
	if p.NeighborIPs == nil {
		return nil
	}
	out := make([]string, 0, len(*p.NeighborIPs))
	for _, nb := range *p.NeighborIPs {
		out = append(out, strings.SplitN(nb, "%", 2)[0])
	}
	return out
}

// isCairnBackend reports whether the loaded config selects the Cairn backend.
func isCairnBackend(c *config.Config) bool {
	return c != nil && c.Backend == "cairn"
}

// isFRRBackend reports whether the loaded config selects the FRR backend.
func isFRRBackend(c *config.Config) bool {
	return c != nil && c.Backend == "frr"
}

// frrPaths returns the FRR config directory and vtysh binary path from c,
// falling back to config defaults when unset.
func frrPaths(c *config.Config) (directory, vtysh string) {
	var def config.Config
	if err := defaults.Set(&def); err != nil {
		log.Warnf("Setting config defaults: %v", err)
	}
	directory, vtysh = def.FRRDirectory, def.VtyshBinary
	if c != nil {
		if c.FRRDirectory != "" {
			directory = c.FRRDirectory
		}
		if c.VtyshBinary != "" {
			vtysh = c.VtyshBinary
		}
	}
	return directory, vtysh
}

// ctrlBinary returns the cairnctrl binary path from config (or the default).
func ctrlBinary(c *config.Config) string {
	if c != nil && c.CairnCtrlBinary != "" {
		return c.CairnCtrlBinary
	}
	var def config.Config
	if err := defaults.Set(&def); err == nil {
		return def.CairnCtrlBinary
	}
	return "/usr/bin/cairnctrl"
}

// cairnCtrl runs cairnctrl with the given arguments and returns stdout.
func cairnCtrl(binary string, args ...string) (string, error) {
	cmd := exec.Command(binary, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// readProtocolNames reads the BIRD protocol name to peer name map (protocols.json) from the BIRD directory
func readProtocolNames(birdDirectory string) (map[string]*templating.Protocol, error) {
	contents, err := os.ReadFile(path.Join(birdDirectory, "protocols.json"))
	if err != nil {
		return nil, err
	}
	var protocols map[string]*templating.Protocol
	if err := json.Unmarshal(contents, &protocols); err != nil {
		return nil, err
	}
	return protocols, nil
}

// resolveProtocols maps each name to BIRD protocol names. A name matching a peer name from
// protocols maps to all BIRD protocols of that peer; any other name is taken as a BIRD protocol name.
// The result is deduplicated and keeps the order of names.
func resolveProtocols(names []string, protocols map[string]*templating.Protocol) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}

	for _, name := range names {
		var matches []string
		for birdName, p := range protocols {
			if p != nil && p.Name == name {
				matches = append(matches, birdName)
			}
		}
		sort.Strings(matches)
		for _, m := range matches {
			add(m)
		}
		// Real BIRD protocol name (or no matching peer name)
		if _, isBIRDName := protocols[name]; isBIRDName || len(matches) == 0 {
			add(name)
		}
	}
	return out
}
