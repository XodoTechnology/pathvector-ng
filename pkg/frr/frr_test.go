package frr

import (
	"strings"
	"testing"

	"github.com/creasty/defaults"

	"github.com/XodoTechnology/pathvector-ng/pkg/config"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	c := &config.Config{
		ASN:      65000,
		RouterID: "192.0.2.1",
	}
	if err := defaults.Set(c); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if c.Kernel == nil {
		c.Kernel = &config.Kernel{}
		// defaults.Set on Config leaves a nil *Kernel unallocated, so apply
		// the Kernel defaults (export: true, ...) explicitly like process.Load.
		if err := defaults.Set(c.Kernel); err != nil {
			t.Fatalf("kernel defaults: %v", err)
		}
	}
	// Derived fields populated by process.Load, set here directly.
	c.Prefixes4 = []string{"203.0.113.0/24"}
	c.Prefixes6 = []string{"2001:db8::/48"}
	c.RTRServerHost = "rtr.example.net"
	c.RTRServerPort = 3233
	return c
}

func testPeer(t *testing.T, ips []string, mutate func(*config.Peer)) *config.Peer {
	t.Helper()
	p := &config.Peer{ASN: intp(64512)}
	if err := defaults.Set(p); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	p.NeighborIPs = &ips
	if mutate != nil {
		mutate(p)
	}
	return p
}

func intp(i int) *int       { return &i }
func boolp(b bool) *bool    { return &b }
func strp(s string) *string { return &s }

func TestSplitFamilies(t *testing.T) {
	v4, v6 := splitFamilies([]string{"10.0.0.0/8", "2001:db8::/32", "192.0.2.0/24"})
	if len(v4) != 2 || len(v6) != 1 {
		t.Fatalf("splitFamilies = %v / %v", v4, v6)
	}
	if v6[0] != "2001:db8::/32" {
		t.Fatalf("v6 = %v", v6)
	}
}

func TestPrefixListEntry(t *testing.T) {
	cases := map[string]string{
		"10.0.0.0/8":            "10.0.0.0/8",
		"10.0.0.0/8{16,24}":     "10.0.0.0/8 ge 16 le 24",
		"10.0.0.0/8{8,}":        "10.0.0.0/8 ge 8",
		"10.0.0.0/8{,24}":       "10.0.0.0/8 le 24",
		"2001:db8::/32{32,128}": "2001:db8::/32 ge 32 le 128",
	}
	for in, want := range cases {
		if got := prefixListEntry(in); got != want {
			t.Errorf("prefixListEntry(%q) = %q, want %q", in, got, want)
		}
	}
}

// Regression: the IPv6 prefix-set must reach the v6 route-map. A bug once
// discarded the v6 half of splitFamilies so IPv6 allow-lists were dropped.
func TestPeerConfigV6AllowList(t *testing.T) {
	c := testConfig(t)
	set6 := []string{"2001:db8:1::/48"}
	p := testPeer(t, []string{"2001:db8::12"}, func(p *config.Peer) {
		p.PrefixSet6 = &set6
	})
	pb, _ := PeerConfig("peer", p, c)
	if !strings.Contains(pb.Objects, "ipv6 prefix-list PV_PEER_ALLOW_V6 permit 2001:db8:1::/48") {
		t.Errorf("missing v6 allow prefix-list:\n%s", pb.Objects)
	}
	if !strings.Contains(pb.Objects, "match ipv6 address prefix-list PV_PEER_ALLOW_V6") {
		t.Error("v6 allow-list not referenced as a required match")
	}
	// The v6 neighbor belongs in the ipv6 address-family block, not ipv4.
	if !strings.Contains(pb.AF6, "neighbor 2001:db8::12") {
		t.Errorf("v6 neighbor missing from AF6:\n%s", pb.AF6)
	}
	if strings.Contains(pb.AF4, "2001:db8::12") {
		t.Error("v6 neighbor leaked into AF4")
	}
}

// Regression: route-map names must carry a single PV_ prefix.
func TestPeerConfigRouteMapNaming(t *testing.T) {
	c := testConfig(t)
	p := testPeer(t, []string{"203.0.113.12"}, nil)
	pb, _ := PeerConfig("Example Peer", p, c)
	if strings.Contains(pb.Objects, "PV_PV_") {
		t.Errorf("doubled PV_ prefix in route-map names:\n%s", pb.Objects)
	}
	if !strings.Contains(pb.Objects, "route-map PV_EXAMPLE_PEER_0_INV4") {
		t.Errorf("expected sanitized route-map name:\n%s", pb.Objects)
	}
}

// Regression: the v4 neighbor must land in address-family ipv4 unicast and
// the v6 neighbor in ipv6 unicast (a suffix-matching bug once put all
// neighbors under ipv4).
func TestPeerConfigFamilyPlacement(t *testing.T) {
	c := testConfig(t)
	p := testPeer(t, []string{"203.0.113.12", "2001:db8::12"}, func(p *config.Peer) {
		*p.MPUnicast46 = true
	})
	pb, _ := PeerConfig("dual", p, c)
	if !strings.Contains(pb.AF4, "neighbor 203.0.113.12 activate") {
		t.Error("v4 neighbor missing from AF4")
	}
	if !strings.Contains(pb.AF6, "neighbor 2001:db8::12 activate") {
		t.Error("v6 neighbor missing from AF6")
	}
	// mp-unicast-46 renders both families for each neighbor.
	if !strings.Contains(pb.AF4, "neighbor 2001:db8::12 activate") {
		t.Error("mp-unicast-46 v4 neighbor missing v6 family in AF4 block")
	}
}

// allow-list (prefix-set) and as-set are negated matches in BIRD and cannot
// be route-map denies; they must be required matches on the terminal accept.
func TestPeerConfigAllowAsSetTerminal(t *testing.T) {
	c := testConfig(t)
	set4 := []string{"198.51.100.0/24"}
	members := []uint32{64512, 64513}
	p := testPeer(t, []string{"203.0.113.12"}, func(p *config.Peer) {
		p.PrefixSet4 = &set4
		p.FilterASSet = boolp(true)
		p.ASSetMembers = &members
	})
	pb, _ := PeerConfig("aset", p, c)
	// Find the final permit sequence of the import route-map and check both
	// required matches are on it.
	idx := strings.LastIndex(pb.Objects, "route-map PV_ASET_0_INV4 permit")
	if idx < 0 {
		t.Fatalf("no terminal import permit found:\n%s", pb.Objects)
	}
	tail := pb.Objects[idx:]
	if !strings.Contains(tail, "match ip address prefix-list PV_ASET_ALLOW_V4") {
		t.Error("allow-list missing from terminal accept")
	}
	if !strings.Contains(tail, "match as-path PV_ASET_ASSET") {
		t.Error("as-set missing from terminal accept")
	}
}

// clear-path must precede prepend-path/prepends in the export sequence,
// matching BIRD's ordering.
func TestPeerConfigClearPathPrepend(t *testing.T) {
	c := testConfig(t)
	prepends := []uint32{64513, 64514}
	p := testPeer(t, []string{"203.0.113.12"}, func(p *config.Peer) {
		*p.ClearPath = true
		p.PrependPath = &prepends
		p.Prepends = intp(2)
	})
	pb, _ := PeerConfig("cp", p, c)
	outIdx := strings.Index(pb.Objects, "route-map PV_CP_0_OUTV4")
	if outIdx < 0 {
		t.Fatalf("no export route-map:\n%s", pb.Objects)
	}
	seg := pb.Objects[outIdx:]
	exIdx := strings.Index(seg, "set as-path exclude all")
	ppIdx := strings.Index(seg, "set as-path prepend")
	if exIdx < 0 || ppIdx < 0 {
		t.Fatalf("missing clear-path or prepend in export:\n%s", seg)
	}
	if exIdx > ppIdx {
		t.Error("clear-path must come before prepend-path")
	}
}

// blackhole-out is a terminal accept in BIRD (set_blackhole calls accept),
// so it must be its own permit sequence, not folded into later policy.
func TestPeerConfigBlackholeOut(t *testing.T) {
	c := testConfig(t)
	p := testPeer(t, []string{"203.0.113.12"}, func(p *config.Peer) {
		*p.BlackholeOut = true
	})
	pb, _ := PeerConfig("bh", p, c)
	if !strings.Contains(pb.Objects, "route-map PV_BH_0_OUTV4 permit") {
		t.Error("blackhole-out did not produce a permit sequence")
	}
	if !strings.Contains(pb.Objects, "set ip next-hop") {
		t.Error("blackhole-out missing blackhole next-hop set")
	}
}

func TestKernelExportSRD(t *testing.T) {
	c := testConfig(t)
	c.Kernel.SRDStandardCommunities = []string{"65000:100"}
	c.Kernel.SRDLargeCommunities = []string{"65000:1:100"}
	out := KernelExport(c)
	// Standard and large SRD matches need separate permit sequences (a single
	// sequence ANDs its match clauses; BIRD accepts on either).
	stdIdx := strings.Index(out, "match community PV_KRT_SRD_C")
	largeIdx := strings.Index(out, "match large-community PV_KRT_SRD_L")
	if stdIdx < 0 || largeIdx < 0 {
		t.Fatalf("SRD matches missing:\n%s", out)
	}
	// They must be in different route-map sequences: a "route-map" header
	// between the two match lines is the sequence boundary. If both matches
	// were in one sequence there would be no header between them (ANDed).
	if !strings.Contains(out[stdIdx:largeIdx], "route-map") {
		t.Error("standard and large SRD matches share one sequence (ANDed)")
	}
	// Locally originated prefixes are denied to BGP export.
	if !strings.Contains(out, "deny") || !strings.Contains(out, "PV_LOCAL_V4") {
		t.Error("kernel export missing local-prefix deny")
	}
}

func TestKernelExportDisabled(t *testing.T) {
	c := testConfig(t)
	c.Kernel.Export = false
	out := KernelExport(c)
	if !strings.Contains(out, "route-map PV_KRT4 deny") {
		t.Error("export:false should render a deny-all kernel route-map")
	}
}

func TestConfigDeterministicAndHeader(t *testing.T) {
	c := testConfig(t)
	mk := func(ips ...string) PeerBlocks {
		pb, _ := PeerConfig("p", testPeer(t, ips, nil), c)
		return pb
	}
	blocks := map[string]PeerBlocks{
		"b-peer": mk("203.0.113.2"),
		"a-peer": mk("203.0.113.1"),
	}
	out := Config(c, blocks)
	ia := strings.Index(out, "neighbor 203.0.113.1")
	ib := strings.Index(out, "neighbor 203.0.113.2")
	if ia < 0 || ib < 0 || ia > ib {
		t.Error("peers not emitted in sorted order")
	}
	if !strings.Contains(out, "frr defaults traditional") {
		t.Error("missing frr defaults header")
	}
	if strings.Contains(out, "frr version") {
		t.Error("hardcoded frr version should not be emitted")
	}
}

func TestGlobalObjectsRPKI(t *testing.T) {
	c := testConfig(t)
	out := GlobalObjects(c)
	if !strings.Contains(out, "rpki cache tcp rtr.example.net 3233") {
		t.Errorf("RPKI cache missing host/port:\n%s", out)
	}
}

func TestUnsupportedWarnings(t *testing.T) {
	c := testConfig(t)
	p := testPeer(t, []string{"203.0.113.12"}, func(p *config.Peer) {
		*p.PreferOlderRoutes = true
		*p.ConfederationMember = true
		*p.FilterASPA = true
	})
	_, warns := PeerConfig("w", p, c)
	joined := strings.Join(warns, ",")
	for _, want := range []string{"prefer-older-routes", "confederation", "filter-aspa"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing unsupported warning %q in %v", want, warns)
		}
	}
}

// Regression: FRR's command parser treats a "| " (pipe+space) sequence as a
// pipe action and rejects the config. The old boundary wrapper "(^| )...( |$)"
// contained "| "; the "_" ASN-element boundary must be used instead.
func TestAsPathRegexNoPipeAction(t *testing.T) {
	contains := asPathContainsRe([]string{"174", "701..703", "64512..65534"})
	if contains == "" {
		t.Fatal("asPathContainsRe returned empty")
	}
	if !strings.HasPrefix(contains, "_(") || !strings.HasSuffix(contains, ")_") {
		t.Errorf("as-path contains regex must use _ boundaries, got %q", contains)
	}
	origin := asPathOriginRe([]uint32{65510, 65520})
	if !strings.HasPrefix(origin, "_(") || !strings.HasSuffix(origin, ")$") {
		t.Errorf("as-path origin regex must use _ boundary + end anchor, got %q", origin)
	}
	for name, re := range map[string]string{"contains": contains, "origin": origin} {
		if strings.Contains(re, "| ") {
			t.Errorf("%s regex contains '| ' pipe-action sequence: %q", name, re)
		}
		if strings.Contains(re, "(^|") {
			t.Errorf("%s regex uses literal (^| boundary instead of '_': %q", name, re)
		}
	}
}

// Regression: generated as-path access-lists (bogon/transit/blocklist) must
// also carry the "_" boundary and no "| " sequence.
func TestGlobalObjectsAsPathNoPipeAction(t *testing.T) {
	c := testConfig(t)
	c.BogonASNs = []string{"0", "23456", "64512..65534"}
	c.TransitASNs = []uint32{174, 701, 1299}
	c.BlocklistASNs = []uint32{65530, 65520}
	out := GlobalObjects(c)
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "bgp as-path access-list") {
			continue
		}
		if strings.Contains(line, "| ") {
			t.Errorf("as-path access-list contains '| ' pipe-action sequence:\n%s", line)
		}
		if !strings.Contains(line, " permit _(") {
			t.Errorf("as-path access-list missing '_' boundary:\n%s", line)
		}
	}
}

// Regression: peer route-maps are referenced by `neighbor ... route-map` inside
// `router bgp`, so their definitions (and the objects they match) must be
// emitted BEFORE the router bgp block. Emitting them after makes bgpd warn
// "route-map does not exist" and skip applying the policy.
func TestConfigRouteMapsBeforeRouterBgp(t *testing.T) {
	c := testConfig(t)
	p := testPeer(t, []string{"203.0.113.9"}, func(p *config.Peer) {
		p.ASN = intp(64512)
	})
	pb, _ := PeerConfig("Ordering", p, c)
	out := Config(c, map[string]PeerBlocks{"Ordering": pb})

	rmIdx := strings.Index(out, "route-map PV_ORDERING_")
	bgpIdx := strings.Index(out, "router bgp")
	if rmIdx < 0 {
		t.Fatalf("no peer route-map emitted:\n%s", out)
	}
	if bgpIdx < 0 {
		t.Fatal("no router bgp block emitted")
	}
	if rmIdx > bgpIdx {
		t.Errorf("peer route-map (offset %d) emitted AFTER router bgp (offset %d)", rmIdx, bgpIdx)
	}
	// The neighbor must reference a route-map that is defined earlier.
	refIdx := strings.Index(out, "route-map PV_ORDERING_0_INV4 in")
	if refIdx < 0 {
		t.Fatal("neighbor does not reference import route-map")
	}
	defIdx := strings.Index(out, "route-map PV_ORDERING_0_INV4 permit")
	if defIdx < 0 || defIdx > refIdx {
		t.Errorf("import route-map not defined before neighbor reference (def=%d ref=%d)", defIdx, refIdx)
	}
}

func TestParseNeighbors(t *testing.T) {
	out := `{
  "ipv4-unicast": {"peers": {"10.0.0.2": {"remoteAs": 65002, "state": "Established", "pfxRcd": 3, "pfxSnt": 5, "peerUptime": "00:10:00"}}},
  "ipv6-unicast": {"peers": {"fd00::2": {"remoteAs": 65002, "state": "Active", "pfxRcd": 0, "pfxSnt": 1, "peerUptime": "00:00:30"}}}
}`
	nameByAddr := map[string]string{"10.0.0.2": "Peer", "fd00::2": "Peer"}
	neighbors, err := parseNeighbors(out, nameByAddr)
	if err != nil {
		t.Fatalf("parseNeighbors: %v", err)
	}
	if len(neighbors) != 2 {
		t.Fatalf("expected 2 neighbors, got %d", len(neighbors))
	}
	byAFI := map[string]NeighborInfo{}
	for _, n := range neighbors {
		byAFI[n.AFI] = n
	}
	v4 := byAFI["ipv4-unicast"]
	if v4.Name != "Peer" || v4.Neighbor != "10.0.0.2" || v4.ASN != 65002 ||
		v4.State != "Established" || v4.Imported != 3 || v4.Exported != 5 {
		t.Fatalf("unexpected v4 neighbor: %+v", v4)
	}
	v6 := byAFI["ipv6-unicast"]
	if v6.State != "Active" || v6.Imported != 0 {
		t.Fatalf("unexpected v6 neighbor: %+v", v6)
	}
}

func TestParseNeighborsUnknownAddress(t *testing.T) {
	out := `{"ipv4-unicast": {"peers": {"10.9.9.9": {"remoteAs": 65003, "state": "Idle", "pfxRcd": -1, "pfxSnt": -1, "peerUptime": ""}}}}`
	neighbors, err := parseNeighbors(out, map[string]string{})
	if err != nil {
		t.Fatalf("parseNeighbors: %v", err)
	}
	if len(neighbors) != 1 || neighbors[0].Name != "10.9.9.9" {
		t.Fatalf("expected neighbor named by address, got %+v", neighbors)
	}
}

func TestParseNeighborsEmpty(t *testing.T) {
	neighbors, err := parseNeighbors(`{}`, map[string]string{})
	if err != nil {
		t.Fatalf("parseNeighbors: %v", err)
	}
	if len(neighbors) != 0 {
		t.Fatalf("expected no neighbors, got %+v", neighbors)
	}
}

// Regression: enforce-peer-nexthop must reject routes whose next hop is NOT
// the peer. The prefix-list is inverted (deny peer addr, permit rest) so the
// route-map's `deny ... match ip next-hop prefix-list` fires only on foreign
// next hops; the old form permitted the peer addr, denying every valid route.
func TestPeerConfigEnforcePeerNexthop(t *testing.T) {
	c := testConfig(t)
	p := testPeer(t, []string{"203.0.113.12"}, func(p *config.Peer) {
		p.EnforcePeerNexthop = boolp(true)
	})
	pb, _ := PeerConfig("nhpeer", p, c)
	if !strings.Contains(pb.Objects, "seq 5 deny 203.0.113.12/32") {
		t.Errorf("next-hop list must deny the peer address:\n%s", pb.Objects)
	}
	if !strings.Contains(pb.Objects, "seq 10 permit 0.0.0.0/0 le 32") {
		t.Errorf("next-hop list must permit everything else:\n%s", pb.Objects)
	}
	if strings.Contains(pb.Objects, "permit 203.0.113.12/32") {
		t.Error("next-hop list must not permit the peer address")
	}
	if !strings.Contains(pb.Objects, "match ip next-hop prefix-list") {
		t.Error("import route-map does not reference the next-hop prefix-list")
	}
}

// Regression: soft-reconfiguration inbound must be rendered inside each
// address-family block — the neighbor-level form only covers IPv4 unicast,
// leaving IPv6 received/filtered routes invisible.
func TestPeerConfigSoftReconfiguration(t *testing.T) {
	c := testConfig(t)
	p := testPeer(t, []string{"203.0.113.12", "2001:db8::12"}, nil)
	pb, _ := PeerConfig("dual", p, c)
	if !strings.Contains(pb.AF4, "neighbor 203.0.113.12 soft-reconfiguration inbound") {
		t.Errorf("v4 neighbor missing per-AF soft-reconfiguration:\n%s", pb.AF4)
	}
	if !strings.Contains(pb.AF6, "neighbor 2001:db8::12 soft-reconfiguration inbound") {
		t.Errorf("v6 neighbor missing per-AF soft-reconfiguration:\n%s", pb.AF6)
	}
}
