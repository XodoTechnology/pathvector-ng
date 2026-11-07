// Package cairn renders Pathvector NG configuration into Cairn's native
// configuration format (cairnd.toml + policy.conf + staticd.toml) and drives
// the cairnd control channel through cairnctrl.
//
// Cairn's policy language is BIRD-inspired: prefix-lists, community-lists,
// as-path access-lists, define'd prefix sets, filter { } blocks. Prefix-list
// names are shared across families — an "ip prefix-list X" and an
// "ipv6 prefix-list X" form one logical list X that matches whichever
// family applies, so a single filter serves both address families.
package cairn

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/XodoTechnology/pathvector-ng/pkg/config"
	"github.com/XodoTechnology/pathvector-ng/pkg/util"
)

// policyName returns a Cairn policy identifier for a peer direction.
// Peer names may contain characters invalid in policy names; sanitize hard.
func policyName(peerName, suffix string) string {
	var b strings.Builder
	b.WriteString("PV_")
	for _, r := range strings.ToUpper(peerName) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	b.WriteString(suffix)
	return b.String()
}

func isV6(ip string) bool { return strings.Contains(ip, ":") }

// splitFamilies splits a mixed IPv4/IPv6 prefix list into per-family lists.
func splitFamilies(prefixes []string) (v4, v6 []string) {
	for _, p := range prefixes {
		if strings.Contains(p, ":") {
			v6 = append(v6, p)
		} else {
			v4 = append(v4, p)
		}
	}
	return v4, v6
}

// communityTuple renders a "asn,val" or "asn,val,val" community (the form
// pathvector stores after sortCommunities) as a Cairn tuple "(asn, val)".
func communityTuple(comm string) (string, bool) {
	comm = strings.TrimSpace(comm)
	parts := strings.Split(comm, ",")
	switch len(parts) {
	case 2:
		return fmt.Sprintf("(%s, %s)", parts[0], parts[1]), true
	case 3:
		return fmt.Sprintf("(%s, %s, %s)", parts[0], parts[1], parts[2]), true
	}
	return "", false
}

// never4/never6 are permit entries that match no real route; they keep a
// prefix-list defined when a feature applies to only one family.
const (
	never4 = "0.0.0.0/32"
	never6 = "::/128"
)

// prefixListEntry converts a BIRD prefix pattern (PREFIX/LEN{min,max}) into
// prefix-list ge/le syntax. Plain prefixes pass through unchanged.
func prefixListEntry(p string) string {
	i := strings.Index(p, "{")
	if i < 0 {
		return p
	}
	base := strings.TrimSpace(p[:i])
	rng := strings.TrimSuffix(strings.TrimSpace(p[i+1:]), "}")
	parts := strings.SplitN(rng, ",", 2)
	out := base
	if parts[0] != "" {
		out += " ge " + parts[0]
	}
	if len(parts) == 2 && parts[1] != "" {
		out += " le " + parts[1]
	}
	return out
}

// writePrefixList emits an ip and an ipv6 prefix-list under one shared
// name, using a never-match placeholder for an empty family so references
// to the name always resolve.
func writePrefixList(b *strings.Builder, name string, v4, v6 []string) {
	if len(v4) == 0 {
		v4 = []string{never4}
	}
	if len(v6) == 0 {
		v6 = []string{never6}
	}
	sort.Strings(v4)
	sort.Strings(v6)
	for _, p := range v4 {
		fmt.Fprintf(b, "ip prefix-list %s permit %s\n", name, prefixListEntry(p))
	}
	for _, p := range v6 {
		fmt.Fprintf(b, "ipv6 prefix-list %s permit %s\n", name, prefixListEntry(p))
	}
}

func quoteJoin(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(quoted, ", ")
}

// Nil-safe dereference helpers: config fields tagged default:"-" stay nil
// unless the user sets them, so every peer option must be read through these.
func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefBool(p *bool) bool { return p != nil && *p }

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func derefStrSlice(p *[]string) []string {
	if p == nil {
		return nil
	}
	return *p
}

func derefUint32Slice(p *[]uint32) []uint32 {
	if p == nil {
		return nil
	}
	return *p
}

func derefUint32Map(p *map[uint32]uint32) map[uint32]uint32 {
	if p == nil {
		return nil
	}
	return *p
}

func derefStrUintMap(p *map[string]uint32) map[string]uint32 {
	if p == nil {
		return nil
	}
	return *p
}

// sortedKeys returns map keys in sorted order for deterministic rendering.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedUintKeys(m map[uint32]uint32) []uint32 {
	keys := make([]uint32, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func derefStrSliceMap(p *map[string][]string) map[string][]string {
	if p == nil {
		return nil
	}
	return *p
}

func staticPrefixes(c *config.Config) (v4, v6 []string) {
	if c.Kernel == nil {
		return nil, nil
	}
	for p := range c.Kernel.Statics4 {
		v4 = append(v4, p)
	}
	for p := range c.Kernel.Statics6 {
		v6 = append(v6, p)
	}
	return v4, v6
}

// localPrefixes returns our originated prefixes + kernel statics, split by family.
func localPrefixes(c *config.Config) (v4, v6 []string) {
	s4, s6 := staticPrefixes(c)
	all := append(append([]string{}, c.Prefixes...), append(s4, s6...)...)
	return splitFamilies(all)
}

func unique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// GlobalHeader renders the non-neighbor sections of cairnd.toml.
func GlobalHeader(c *config.Config) string {
	var b strings.Builder
	b.WriteString("# Generated by Pathvector NG — do not edit\n\n")

	b.WriteString("[global]\n")
	fmt.Fprintf(&b, "asn = %d\n", c.ASN)
	fmt.Fprintf(&b, "router_id = %q\n", c.RouterID)
	b.WriteString("listen = [\"0.0.0.0:179\", \"[::]:179\"]\n")
	b.WriteString("policy_file = \"policy.conf\"\n")
	fmt.Fprintf(&b, "api_socket = %q\n", c.CairnSocket)
	b.WriteString("ebgp_requires_policy = true\n")
	b.WriteString("honor_graceful_shutdown = true\n")
	b.WriteString("\n")

	// Best-path: BIRD does single-path unless merge-paths asks for ECMP.
	b.WriteString("[decision]\n")
	if c.MergePaths {
		b.WriteString("max_paths = 64\n")
		b.WriteString("multipath_relax = true\n")
	} else {
		b.WriteString("max_paths = 1\n")
	}
	b.WriteString("\n")

	b.WriteString("[fib]\n")
	b.WriteString("enabled = true\n")
	if c.Kernel != nil {
		fmt.Fprintf(&b, "table = %d\n", c.Kernel.Table)
	}
	b.WriteString("metric = 20\n")
	b.WriteString("resolve_via_default = false\n")
	if c.Source4 != "" {
		fmt.Fprintf(&b, "source_ipv4 = %q\n", c.Source4)
	}
	if c.Source6 != "" {
		fmt.Fprintf(&b, "source_ipv6 = %q\n", c.Source6)
	}
	b.WriteString("\n")

	// Prometheus metrics — Cairn serves /metrics natively.
	metricsListen := c.MetricsListen
	if metricsListen == "" || metricsListen == "-" {
		metricsListen = "127.0.0.1:10065"
	}
	fmt.Fprintf(&b, "[metrics]\nlisten = [%q]\nneighbors = true\n\n", metricsListen)

	// RPKI RTR cache.
	if c.RPKIEnable && c.RTRServer != "" {
		fmt.Fprintf(&b, "[[rpki.cache]]\naddress = %q\n\n", c.RTRServer)
	}

	// Locally originated prefixes: [[network]] announces them to neighbors.
	for _, p := range c.Prefixes {
		fmt.Fprintf(&b, "[[network]]\nprefix = %q\n", p)
		comms := append(append([]string{}, c.OriginCommunities...), c.LocalCommunities...)
		if len(comms) > 0 {
			fmt.Fprintf(&b, "communities = [%s]\n", quoteJoin(comms))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// NeighborBlocks renders the [[neighbor]] TOML entries for one peer —
// one per neighbor address, matching BIRD's per-session protocols.
func NeighborBlocks(peerName string, p *config.Peer, c *config.Config) (string, []string) {
	var b strings.Builder
	var unsupported []string

	inPolicy := policyName(peerName, "_IN")
	outPolicy := policyName(peerName, "_OUT")

	if p.NeighborIPs == nil {
		return "", []string{"no neighbor addresses"}
	}
	for _, neighbor := range *p.NeighborIPs {
		addr := strings.SplitN(neighbor, "%", 2)[0]
		af := "ipv4-unicast"
		if isV6(addr) {
			af = "ipv6-unicast"
		}
		families := []string{af}
		if derefBool(p.MPUnicast46) {
			families = []string{"ipv4-unicast", "ipv6-unicast"}
		}

		b.WriteString("[[neighbor]]\n")
		fmt.Fprintf(&b, "address = %q\n", addr)
		fmt.Fprintf(&b, "remote_as = %d\n", derefInt(p.ASN))
		desc := derefStr(p.Description)
		if desc == "" {
			desc = peerName
		}
		fmt.Fprintf(&b, "description = %q\n", desc)
		if derefBool(p.Passive) {
			b.WriteString("passive = true\n")
		}
		// GTSM (ttl_security) always sends TTL 255; cairn rejects the combo.
		if derefBool(p.Multihop) && !derefBool(p.TTLSecurity) {
			b.WriteString("multihop = 255\n")
		}
		// Session source address: listen4 for v4 neighbors, listen6 for v6.
		if af == "ipv4-unicast" && derefStr(p.Listen4) != "" {
			fmt.Fprintf(&b, "local_address = %q\n", derefStr(p.Listen4))
		} else if af == "ipv6-unicast" && derefStr(p.Listen6) != "" {
			fmt.Fprintf(&b, "local_address = %q\n", derefStr(p.Listen6))
		}
		if derefInt(p.LocalPort) != 179 || derefInt(p.NeighborPort) != 179 {
			unsupported = append(unsupported, "local-port/neighbor-port")
		}
		if derefStr(p.Password) != "" {
			fmt.Fprintf(&b, "password = %q\n", derefStr(p.Password))
		}
		// Policies are always attached; the filters decide accept/reject so
		// import:false / no-accept still behave correctly.
		fmt.Fprintf(&b, "import = %q\n", inPolicy)
		fmt.Fprintf(&b, "export = %q\n", outPolicy)
		fmt.Fprintf(&b, "families = [%s]\n", quoteJoin(families))

		// next-hop-self: peer-level flag, or per-AF eBGP/iBGP variants.
		ebgp := derefInt(p.ASN) != c.ASN
		nhs := derefBool(p.NextHopSelf) ||
			(derefBool(p.NextHopSelfEBGP) && ebgp) ||
			(derefBool(p.NextHopSelfIBGP) && !ebgp)
		if nhs {
			b.WriteString("next_hop_self = true\n")
		}
		// Cairn enforces session-role validity: RR client is iBGP-only,
		// route-server client and allowas-in are eBGP-only.
		if derefBool(p.RRClient) {
			if ebgp {
				unsupported = append(unsupported, "rr-client on eBGP session (iBGP only)")
			} else {
				b.WriteString("route_reflector_client = true\n")
			}
		}
		if derefBool(p.RSClient) {
			if !ebgp {
				unsupported = append(unsupported, "rs-client on iBGP session (eBGP only)")
			} else {
				b.WriteString("route_server_client = true\n")
			}
		}
		if derefBool(p.AllowLocalAS) {
			if !ebgp {
				unsupported = append(unsupported, "allow-local-as on iBGP session (eBGP only)")
			} else {
				b.WriteString("allowas_in = 1\n")
			}
		}
		if derefBool(p.AddPathTx) {
			b.WriteString("add_path_send = \"all\"\n")
		}
		if derefBool(p.AddPathRx) {
			b.WriteString("add_path_receive = true\n")
		}
		if derefBool(p.TTLSecurity) {
			b.WriteString("ttl_security = 255\n")
		}
		if derefBool(p.EnforceFirstAS) {
			b.WriteString("enforce_first_as = true\n")
		}
		if derefBool(p.Disabled) {
			unsupported = append(unsupported, "disabled (use cairnctrl shutdown)")
		}
		if derefBool(p.BFD) {
			unsupported = append(unsupported, "bfd (requires holo)")
		}
		if derefBool(p.ConfederationMember) || derefInt(p.Confederation) != 0 {
			unsupported = append(unsupported, "confederation")
		}
		if derefStr(p.Gateway) != "" || derefInt(p.Cost) != 0 {
			unsupported = append(unsupported, "gateway/cost")
		}
		if derefBool(p.ExtendedNextHop) {
			unsupported = append(unsupported, "extended-next-hop")
		}
		if derefBool(p.L3VPN) {
			unsupported = append(unsupported, "l3vpn")
		}
		// Import limits → session prefix limit (approximation: cairn counts
		// received prefixes, pathvector counts post-filter RIB entries).
		limit := derefInt(p.ImportLimit4)
		if af == "ipv6-unicast" {
			limit = derefInt(p.ImportLimit6)
		}
		if limit > 0 {
			fmt.Fprintf(&b, "max_prefixes = %d\n", limit)
			// BIRD's import-limit requires keep-filtered so rejected routes
			// stay inspectable; cairn's equivalent keeps received routes.
			b.WriteString("keep_received_routes = true\n")
			if derefStr(p.ImportLimitTripAction) == "warn" {
				b.WriteString("max_prefixes_warning_only = true\n")
			}
		}
		if derefInt(p.ReceiveLimit4) > 0 || derefInt(p.ReceiveLimit6) > 0 {
			unsupported = append(unsupported, "receive-limit")
		}
		if p.ExportLimit4 != nil || p.ExportLimit6 != nil {
			unsupported = append(unsupported, "export-limit")
		}
		if derefStr(p.ImportLimitTripAction) != "disable" &&
			derefStr(p.ImportLimitTripAction) != "" ||
			derefStr(p.ReceiveLimitTripAction) != "disable" &&
				derefStr(p.ReceiveLimitTripAction) != "" ||
			derefStr(p.ExportLimitTripAction) != "disable" &&
				derefStr(p.ExportLimitTripAction) != "" {
			unsupported = append(unsupported, "limit-violation actions other than disable")
		}

		b.WriteString("\n")
	}

	return b.String(), unique(unsupported)
}

// PolicyHeader renders the shared prefix-lists and as-path lists used by
// every peer's import/export filters.
func PolicyHeader(c *config.Config) string {
	var b strings.Builder
	b.WriteString("! Generated by Pathvector NG — do not edit\n")
	b.WriteString("! Backend: cairn\n\n")

	fmt.Fprintf(&b, "define MY_AS = %d;\n\n", c.ASN)

	// Family matchers for per-address-family conditions.
	b.WriteString("ip prefix-list PV_V4ANY permit 0.0.0.0/0 le 32\n")
	b.WriteString("ipv6 prefix-list PV_V6ANY permit ::/0 le 128\n\n")

	// Default route matcher for announce-default.
	b.WriteString("ip prefix-list PV_DEFAULT permit 0.0.0.0/0\n")
	b.WriteString("ipv6 prefix-list PV_DEFAULT permit ::/0\n\n")

	// Bogon filters (filter-bogon-routes), one shared name per family set.
	b4, _ := splitFamilies(c.Bogons4)
	_, b6 := splitFamilies(c.Bogons6)
	writePrefixList(&b, "PV_BOGON", b4, b6)
	b.WriteString("\n")

	// Bogon AS paths (filter-bogon-asns): unanchored contains-match.
	if len(c.BogonASNs) > 0 {
		fmt.Fprintf(&b, "bgp as-path access-list PV_BOGON_ASNS permit (^| )(%s)( |$)\n\n",
			strings.Join(c.BogonASNs, "|"))
	}

	// Global blocklist.
	if len(c.BlocklistPrefixes) > 0 {
		lb4, lb6 := splitFamilies(c.BlocklistPrefixes)
		writePrefixList(&b, "PV_BLOCKLIST", lb4, lb6)
		b.WriteString("\n")
	}
	if len(c.BlocklistASNs) > 0 {
		asns := make([]string, len(c.BlocklistASNs))
		for i, a := range c.BlocklistASNs {
			asns[i] = fmt.Sprint(a)
		}
		fmt.Fprintf(&b, "bgp as-path access-list PV_BLOCKLIST_ASNS permit (^| )(%s)( |$)\n\n",
			strings.Join(asns, "|"))
	}

	// Out-of-bounds prefix lengths (filter-prefix-length):
	// IPv4 rejects len < 8 or len > 24; IPv6 len < 12 or len > 48.
	b.WriteString("ip prefix-list PV_BADLEN permit 0.0.0.0/0 le 7\n")
	b.WriteString("ip prefix-list PV_BADLEN permit 0.0.0.0/0 ge 25\n")
	b.WriteString("ipv6 prefix-list PV_BADLEN permit ::/0 le 11\n")
	b.WriteString("ipv6 prefix-list PV_BADLEN permit ::/0 ge 49\n\n")

	// Locally originated prefixes (announce-originated).
	lv4, lv6 := localPrefixes(c)
	if len(lv4)+len(lv6) > 0 {
		writePrefixList(&b, "PV_LOCAL", lv4, lv6)
		b.WriteString("\n")
	}

	return b.String()
}

// PeerPolicy renders the import and export filters for one peer.
func PeerPolicy(peerName string, p *config.Peer, c *config.Config) (string, []string) {
	var b strings.Builder

	inName := policyName(peerName, "_IN")
	outName := policyName(peerName, "_OUT")

	// The combined accept set: user prefixes merged with IRR results at load.
	set4, set6 := splitFamilies(derefStrSlice(p.PrefixSet4))
	irr6, _ := splitFamilies(derefStrSlice(p.PrefixSet6))
	set6 = append(set6, irr6...)

	dont4, dont6 := splitFamilies(derefStrSlice(p.DontAnnounce))
	only4, only6 := splitFamilies(derefStrSlice(p.OnlyAnnounce))

	// ---------- IMPORT ----------
	fmt.Fprintf(&b, "! ---- %s import ----\n", peerName)
	if len(set4)+len(set6) > 0 {
		writePrefixList(&b, inName+"_ALLOW", set4, set6)
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "filter %s {\n", inName)
	if c.NoAccept || !derefBool(p.Import) {
		b.WriteString("    reject;\n")
	} else {
		// Accept-list: route must be inside the peer's prefix set.
		if len(set4)+len(set6) > 0 {
			b.WriteString("    if net !~ " + inName + "_ALLOW then reject;\n")
		}
		// Sanity filters (same order as the BIRD template).
		if derefBool(p.FilterBogonRoutes) {
			b.WriteString("    if net ~ PV_BOGON then reject;\n")
		}
		if derefBool(p.FilterBogonASNs) && len(c.BogonASNs) > 0 {
			b.WriteString("    if bgp_path ~ PV_BOGON_ASNS then reject;\n")
		}
		if derefBool(p.FilterPrefixLength) {
			b.WriteString("    if net ~ PV_BADLEN then reject;\n")
		}
		if derefBool(p.FilterBlocklist) {
			if len(c.BlocklistPrefixes) > 0 {
				b.WriteString("    if net ~ PV_BLOCKLIST then reject;\n")
			}
			if len(c.BlocklistASNs) > 0 {
				b.WriteString("    if bgp_path ~ PV_BLOCKLIST_ASNS then reject;\n")
			}
		}
		// Local preference: base, then per-family, then community/as/prefix prefs.
		// Later assignments win, matching BIRD filter order.
		basePref := derefInt(p.LocalPref)
		if derefInt(p.DefaultLocalPref) != 0 {
			basePref = derefInt(p.DefaultLocalPref)
		}
		if derefBool(p.SetLocalPref) || derefInt(p.DefaultLocalPref) != 0 {
			fmt.Fprintf(&b, "    bgp_local_pref = %d;\n", basePref)
		}
		if derefInt(p.LocalPref4) != 0 {
			fmt.Fprintf(&b, "    if net ~ PV_V4ANY then bgp_local_pref = %d;\n", derefInt(p.LocalPref4))
		}
		if derefInt(p.LocalPref6) != 0 {
			fmt.Fprintf(&b, "    if net ~ PV_V6ANY then bgp_local_pref = %d;\n", derefInt(p.LocalPref6))
		}
		for _, comm := range sortedKeys(derefStrUintMap(p.StandardCommunityPrefs)) {
			if t, ok := communityTuple(comm); ok {
				fmt.Fprintf(&b, "    if %s ~ bgp_community then bgp_local_pref = %d;\n", t, derefStrUintMap(p.StandardCommunityPrefs)[comm])
			}
		}
		for _, comm := range sortedKeys(derefStrUintMap(p.LargeCommunityPrefs)) {
			if t, ok := communityTuple(comm); ok {
				fmt.Fprintf(&b, "    if %s ~ bgp_large_community then bgp_local_pref = %d;\n", t, derefStrUintMap(p.LargeCommunityPrefs)[comm])
			}
		}
		for _, asn := range sortedUintKeys(derefUint32Map(p.ASPrefs)) {
			fmt.Fprintf(&b, "    if %d ~ bgp_path then bgp_local_pref = %d;\n", asn, derefUint32Map(p.ASPrefs)[asn])
		}
		for _, asn := range sortedUintKeys(derefUint32Map(p.ASPrefs4)) {
			fmt.Fprintf(&b, "    if %d ~ bgp_path && net ~ PV_V4ANY then bgp_local_pref = %d;\n", asn, derefUint32Map(p.ASPrefs4)[asn])
		}
		for _, asn := range sortedUintKeys(derefUint32Map(p.ASPrefs6)) {
			fmt.Fprintf(&b, "    if %d ~ bgp_path && net ~ PV_V6ANY then bgp_local_pref = %d;\n", asn, derefUint32Map(p.ASPrefs6)[asn])
		}
		for _, prefix := range sortedKeys(derefStrUintMap(p.PrefixPrefs)) {
			fmt.Fprintf(&b, "    if net ~ [ %s ] then bgp_local_pref = %d;\n", prefix, derefStrUintMap(p.PrefixPrefs)[prefix])
		}
		// Community removal/addition.
		for _, comm := range derefStrSlice(p.RemoveStandardCommunities) {
			if t, ok := communityTuple(comm); ok {
				fmt.Fprintf(&b, "    bgp_community.delete([%s]);\n", t)
			}
		}
		for _, comm := range derefStrSlice(p.RemoveLargeCommunities) {
			if t, ok := communityTuple(comm); ok {
				fmt.Fprintf(&b, "    bgp_large_community.delete([%s]);\n", t)
			}
		}
		if derefInt(p.RemoveAllCommunities) != 0 {
			fmt.Fprintf(&b, "    bgp_community.delete([(%d, *)]);\n", derefInt(p.RemoveAllCommunities))
			fmt.Fprintf(&b, "    bgp_large_community.delete([(%d, *, *)]);\n", derefInt(p.RemoveAllCommunities))
		}
		for _, comm := range derefStrSlice(p.ImportStandardCommunities) {
			if t, ok := communityTuple(comm); ok {
				fmt.Fprintf(&b, "    bgp_community.add(%s);\n", t)
			}
		}
		for _, comm := range derefStrSlice(p.ImportLargeCommunities) {
			if t, ok := communityTuple(comm); ok {
				fmt.Fprintf(&b, "    bgp_large_community.add(%s);\n", t)
			}
		}
		for _, comm := range c.ImportStandardCommunities {
			if t, ok := communityTuple(comm); ok {
				fmt.Fprintf(&b, "    bgp_community.add(%s);\n", t)
			}
		}
		for _, comm := range c.ImportLargeCommunities {
			if t, ok := communityTuple(comm); ok {
				fmt.Fprintf(&b, "    bgp_large_community.add(%s);\n", t)
			}
		}
		// Next-hop handling.
		if derefBool(p.ForcePeerNexthop) {
			b.WriteString("    bgp_next_hop = from;\n")
		}
		if derefStr(p.ImportNextHop) != "" {
			fmt.Fprintf(&b, "    bgp_next_hop = %s;\n", derefStr(p.ImportNextHop))
		}
		// enforce-peer-nexthop: next hop must be one of the peer's addresses.
		if derefBool(p.EnforcePeerNexthop) && p.NeighborIPs != nil && len(*p.NeighborIPs) > 0 {
			var conds []string
			for _, nb := range *p.NeighborIPs {
				addr := strings.SplitN(nb, "%", 2)[0]
				conds = append(conds, fmt.Sprintf("bgp_next_hop != %s", addr))
			}
			fmt.Fprintf(&b, "    if %s then reject;\n", strings.Join(conds, " && "))
		}
		// RPKI.
		if derefBool(p.FilterRPKI) {
			b.WriteString("    if roa_check() = ROA_INVALID then reject;\n")
		}
		if derefBool(p.StrictRPKI) {
			b.WriteString("    if roa_check() != ROA_VALID then reject;\n")
		}
		b.WriteString("    accept;\n")
	}
	b.WriteString("}\n\n")

	// ---------- EXPORT ----------
	fmt.Fprintf(&b, "! ---- %s export ----\n", peerName)
	if len(dont4)+len(dont6) > 0 || len(only4)+len(only6) > 0 {
		writePrefixList(&b, outName+"_DENY", dont4, dont6)
		writePrefixList(&b, outName+"_ONLY", only4, only6)
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "filter %s {\n", outName)
	if c.NoAnnounce || !derefBool(p.Export) {
		b.WriteString("    reject;\n")
	} else {
		if len(dont4)+len(dont6) > 0 {
			b.WriteString("    if net ~ " + outName + "_DENY then reject;\n")
		}
		if len(only4)+len(only6) > 0 {
			b.WriteString("    if net !~ " + outName + "_ONLY then reject;\n")
		}
		// Per-prefix communities.
		for _, prefix := range sortedKeys(derefStrSliceMap(p.PrefixStandardCommunities)) {
			for _, comm := range derefStrSliceMap(p.PrefixStandardCommunities)[prefix] {
				if t, ok := communityTuple(comm); ok {
					fmt.Fprintf(&b, "    if net ~ [ %s ] then bgp_community.add(%s);\n", prefix, t)
				}
			}
		}
		for _, prefix := range sortedKeys(derefStrSliceMap(p.PrefixLargeCommunities)) {
			for _, comm := range derefStrSliceMap(p.PrefixLargeCommunities)[prefix] {
				if t, ok := communityTuple(comm); ok {
					fmt.Fprintf(&b, "    if net ~ [ %s ] then bgp_large_community.add(%s);\n", prefix, t)
				}
			}
		}
		// Export communities (peer then global).
		for _, comm := range derefStrSlice(p.ExportStandardCommunities) {
			if t, ok := communityTuple(comm); ok {
				fmt.Fprintf(&b, "    bgp_community.add(%s);\n", t)
			}
		}
		for _, comm := range derefStrSlice(p.ExportLargeCommunities) {
			if t, ok := communityTuple(comm); ok {
				fmt.Fprintf(&b, "    bgp_large_community.add(%s);\n", t)
			}
		}
		for _, comm := range c.ExportStandardCommunities {
			if t, ok := communityTuple(comm); ok {
				fmt.Fprintf(&b, "    bgp_community.add(%s);\n", t)
			}
		}
		for _, comm := range c.ExportLargeCommunities {
			if t, ok := communityTuple(comm); ok {
				fmt.Fprintf(&b, "    bgp_large_community.add(%s);\n", t)
			}
		}
		// Private-AS stripping + path manipulation.
		if derefBool(p.RemovePrivateASNs) {
			b.WriteString("    bgp_path.delete(PRIVATE_ASNS);\n")
		}
		if p.Prepends != nil {
			for range *p.Prepends {
				b.WriteString("    bgp_path.prepend(MY_AS);\n")
			}
		}
		for _, asn := range derefUint32Slice(p.PrependPath) {
			fmt.Fprintf(&b, "    bgp_path.prepend(%d);\n", asn)
		}
		if derefStr(p.ExportNextHop) != "" {
			fmt.Fprintf(&b, "    bgp_next_hop = %s;\n", derefStr(p.ExportNextHop))
		}
		// announce-originated: our own prefixes + kernel statics.
		if derefBool(p.AnnounceOriginated) {
			lv4, lv6 := localPrefixes(c)
			if len(lv4)+len(lv6) > 0 {
				b.WriteString("    if net ~ PV_LOCAL then accept;\n")
			}
		}
		// announce communities: routes carrying them are exported.
		for _, comm := range derefStrSlice(p.AnnounceStandardCommunities) {
			if t, ok := communityTuple(comm); ok {
				fmt.Fprintf(&b, "    if %s ~ bgp_community then accept;\n", t)
			}
		}
		for _, comm := range derefStrSlice(p.AnnounceLargeCommunities) {
			if t, ok := communityTuple(comm); ok {
				fmt.Fprintf(&b, "    if %s ~ bgp_large_community then accept;\n", t)
			}
		}
		// announce-default.
		if derefBool(p.AnnounceDefault) {
			b.WriteString("    if net ~ PV_DEFAULT then accept;\n")
		}
		if derefBool(p.AnnounceAll) {
			b.WriteString("    accept;\n")
		} else {
			b.WriteString("    reject;\n")
		}
	}
	b.WriteString("}\n")

	return b.String(), unsupportedFeatures(p)
}

// PolicyConf assembles the complete policy.conf: header + all peer policies.
func PolicyConf(c *config.Config, peerPolicies map[string]string) string {
	var b strings.Builder
	b.WriteString(PolicyHeader(c))

	names := make([]string, 0, len(peerPolicies))
	for n := range peerPolicies {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		b.WriteString(peerPolicies[n])
		b.WriteString("\n")
	}
	return b.String()
}

// Staticd renders staticd.toml for kernel static routes.
func Staticd(c *config.Config) string {
	var b strings.Builder
	b.WriteString("# Generated by Pathvector NG — do not edit\n\n")
	b.WriteString("[fib]\n")
	b.WriteString("protocol = 200\n")
	b.WriteString("metric = 1\n")
	b.WriteString("nexthop_id_base = 0x20000000\n")
	b.WriteString("resolve_via_bgp = true\n")
	b.WriteString("resolve_via_default = false\n")
	if c.Source4 != "" {
		fmt.Fprintf(&b, "source_ipv4 = %q\n", c.Source4)
	}
	if c.Source6 != "" {
		fmt.Fprintf(&b, "source_ipv6 = %q\n", c.Source6)
	}
	if c.Kernel != nil {
		for prefix, nh := range c.Kernel.Statics4 {
			fmt.Fprintf(&b, "\n[[route]]\nprefix = %q\ntable = %d\nnext_hop = [{ address = %q }]\n", prefix, c.Kernel.Table, nh)
		}
		for prefix, nh := range c.Kernel.Statics6 {
			fmt.Fprintf(&b, "\n[[route]]\nprefix = %q\ntable = %d\nnext_hop = [{ address = %q }]\n", prefix, c.Kernel.Table, nh)
		}
	}
	return b.String()
}

// Check runs `cairnd --check` against a rendered config file.
func Check(binary, configPath string) error {
	cmd := exec.Command(binary, "--check", "-c", configPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cairnd --check: %s: %s", err, strings.TrimSpace(string(out)))
	}
	log.Infof("%s", strings.TrimSpace(string(out)))
	return nil
}

// StaticdCheck runs `staticd --check` against a rendered config file.
func StaticdCheck(binary, configPath string) error {
	cmd := exec.Command(binary, "--check", "-c", configPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("staticd --check: %s: %s", err, strings.TrimSpace(string(out)))
	}
	log.Infof("%s", strings.TrimSpace(string(out)))
	return nil
}

// TestedMin and TestedMax bound the Cairn releases covered by the end-to-end
// suite (tests/cairn-e2e). Cairn is early-release/private-beta, so the tested
// range is a single version.
const (
	TestedMin = "0.1.1"
	TestedMax = "0.1.1"
)

// cairnVersionRegex matches `cairnd --version` output, e.g.
// "cairnd 0.1.1+git7129.83a42fb0".
var cairnVersionRegex = regexp.MustCompile(`(?i)cairn\w*\s+v?([0-9]+\.[0-9]+\.[0-9]+[^\s]*)`)

// DaemonVersion reports the installed Cairn version by running `cairnd
// --version`. It uses the configured cairnd binary.
func DaemonVersion(c *config.Config) (string, error) {
	bin := "/usr/sbin/cairnd"
	if c != nil && c.CairndBinary != "" {
		bin = c.CairndBinary
	}
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("cairnd --version: %w: %s", err, strings.TrimSpace(string(out)))
	}
	m := cairnVersionRegex.FindStringSubmatch(string(out))
	if len(m) < 2 {
		return "", fmt.Errorf("could not parse Cairn version from cairnd --version output")
	}
	return m[1], nil
}

// Ctrl runs cairnctrl with the given arguments and returns stdout.
func Ctrl(c *config.Config, args ...string) (string, error) {
	full := append([]string{"-s", c.CairnSocket}, args...)
	cmd := exec.Command(c.CairnCtrlBinary, full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("cairnctrl %s: %s: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Deploy copies rendered files into the Cairn config directory and asks
// cairnd to reload (hot reload — no restart needed).
func Deploy(c *config.Config, cacheDir string, noConfigure bool) error {
	if err := os.MkdirAll(c.CairnDirectory, os.FileMode(0755)); err != nil {
		return fmt.Errorf("create cairn directory: %w", err)
	}
	files := []string{"cairnd.toml", "policy.conf", "staticd.toml", "pathvector.yml"}
	for _, f := range files {
		src := filepath.Join(cacheDir, f)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := util.CopyFile(src, filepath.Join(c.CairnDirectory, f)); err != nil {
			return fmt.Errorf("deploy %s: %w", f, err)
		}
	}
	if noConfigure {
		return nil
	}
	if _, err := Ctrl(c, "reload"); err != nil {
		log.Warnf("cairnctrl reload failed (is cairnd running?): %v", err)
	}
	return nil
}

// ---------- unsupported feature reporting ----------

// unsupportedFeatures lists peer options Cairn cannot express (yet).
func unsupportedFeatures(p *config.Peer) []string {
	var u []string
	if derefStr(p.SessionGlobal) != "" || derefStr(p.PreImportFilter) != "" ||
		derefStr(p.PostImportFilter) != "" || derefStr(p.PreExport) != "" ||
		derefStr(p.PreExportFinal) != "" || derefStr(p.PreImportAccept) != "" {
		u = append(u, "raw BIRD filter snippets (session-global/pre/post-import-filter/pre-export)")
	}
	if derefBool(p.FilterTransitASNs) {
		u = append(u, "filter-transit-asns")
	}
	if derefBool(p.FilterASSet) {
		u = append(u, "filter-as-set")
	}
	if derefBool(p.FilterASPA) {
		u = append(u, "filter-aspa")
	}
	if derefBool(p.BlackholeIn) || derefBool(p.BlackholeOut) || derefBool(p.AllowBlackholeCommunity) {
		u = append(u, "blackhole communities")
	}
	if derefBool(p.FilterNeverViaRouteServers) {
		u = append(u, "filter-never-via-route-servers")
	}
	if derefStr(p.Role) != "" || derefBool(p.RequireRoles) {
		u = append(u, "bgp roles (RFC 9234)")
	}
	if derefBool(p.PreferOlderRoutes) || derefBool(p.DisableAfterError) ||
		derefBool(p.AdvertiseHostname) || !derefBool(p.InterpretCommunities) {
		u = append(u, "misc BIRD session options")
	}
	if derefBool(p.Direct) {
		u = append(u, "direct")
	}
	if derefBool(p.Multihop) && derefBool(p.TTLSecurity) {
		u = append(u, "multihop+ttl-security (ttl_security wins; multihop dropped)")
	}
	if derefStr(p.Source4) != "" || derefStr(p.Source6) != "" {
		u = append(u, "per-peer source4/source6")
	}
	if derefBool(p.ClearPath) {
		u = append(u, "clear-path")
	}
	if derefStr(p.Gateway) != "" {
		u = append(u, "gateway mode")
	}
	return u
}
