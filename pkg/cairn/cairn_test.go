package cairn

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
	return c
}

func testPeer(t *testing.T, mutate func(*config.Peer)) *config.Peer {
	t.Helper()
	p := &config.Peer{ASN: intp(64512)}
	if err := defaults.Set(p); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	ips := []string{"198.51.100.1"}
	p.NeighborIPs = &ips
	if mutate != nil {
		mutate(p)
	}
	return p
}

func intp(i int) *int { return &i }

func TestPolicyNameSanitizes(t *testing.T) {
	if got := policyName("my peer.1/x", "_IN"); got != "PV_MY_PEER_1_X_IN" {
		t.Fatalf("policyName = %q", got)
	}
}

func TestPrefixListEntry(t *testing.T) {
	cases := map[string]string{
		"10.0.0.0/8":            "10.0.0.0/8",
		"10.0.0.0/8{16,24}":     "10.0.0.0/8 ge 16 le 24",
		"10.0.0.0/8{8,32}":      "10.0.0.0/8 ge 8 le 32",
		"2001:db8::/32{32,128}": "2001:db8::/32 ge 32 le 128",
		"192.0.2.0/24{24,}":     "192.0.2.0/24 ge 24",
	}
	for in, want := range cases {
		if got := prefixListEntry(in); got != want {
			t.Errorf("prefixListEntry(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCommunityTuple(t *testing.T) {
	if got, ok := communityTuple("64512,100"); !ok || got != "(64512, 100)" {
		t.Fatalf("standard community = %q ok=%v", got, ok)
	}
	// large community form is "asn,val,val" after pathvector normalizes ":" to ","
	if got, ok := communityTuple("65000,1,2"); !ok || got != "(65000, 1, 2)" {
		t.Fatalf("large community = %q ok=%v", got, ok)
	}
	if _, ok := communityTuple("bogus"); ok {
		t.Fatal("bogus community should not parse")
	}
}

func TestSplitFamilies(t *testing.T) {
	v4, v6 := splitFamilies([]string{"10.0.0.0/8", "2001:db8::/32", "192.0.2.0/24"})
	if len(v4) != 2 || len(v6) != 1 {
		t.Fatalf("splitFamilies = %v / %v", v4, v6)
	}
}

func TestGlobalHeader(t *testing.T) {
	c := testConfig(t)
	c.Prefixes = []string{"203.0.113.0/24"}
	c.MergePaths = true
	out := GlobalHeader(c)
	for _, want := range []string{
		"asn = 65000",
		`router_id = "192.0.2.1"`,
		"max_paths = 64",
		`[[network]]`,
		`prefix = "203.0.113.0/24"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("GlobalHeader missing %q", want)
		}
	}
}

func TestNeighborBlocksBasic(t *testing.T) {
	c := testConfig(t)
	p := testPeer(t, nil)
	out, uns := NeighborBlocks("transit", p, c)
	if len(uns) != 0 {
		t.Fatalf("unexpected unsupported: %v", uns)
	}
	for _, want := range []string{
		`address = "198.51.100.1"`,
		"remote_as = 64512",
		`import = "PV_TRANSIT_IN"`,
		`export = "PV_TRANSIT_OUT"`,
		`families = ["ipv4-unicast"]`,
		"enforce_first_as = true",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("NeighborBlocks missing %q", want)
		}
	}
}

func TestNeighborBlocksRoles(t *testing.T) {
	c := testConfig(t)
	// eBGP peer: rr-client invalid, rs-client + allowas-in valid
	p := testPeer(t, func(p *config.Peer) {
		*p.RRClient = true
		*p.RSClient = true
		*p.AllowLocalAS = true
	})
	out, uns := NeighborBlocks("ebgp", p, c)
	if strings.Contains(out, "route_reflector_client") {
		t.Error("rr-client emitted on eBGP session")
	}
	if !strings.Contains(out, "route_server_client = true") {
		t.Error("rs-client missing on eBGP session")
	}
	if !strings.Contains(out, "allowas_in = 1") {
		t.Error("allowas_in missing on eBGP session")
	}
	found := false
	for _, u := range uns {
		if strings.Contains(u, "rr-client") {
			found = true
		}
	}
	if !found {
		t.Error("expected rr-client unsupported warning")
	}

	// iBGP peer (asn == local): rr-client valid, rs-client/allowas invalid
	p2 := testPeer(t, func(p *config.Peer) {
		*p.ASN = 65000
		*p.RRClient = true
		*p.RSClient = true
		*p.AllowLocalAS = true
	})
	out2, uns2 := NeighborBlocks("ibgp", p2, c)
	if !strings.Contains(out2, "route_reflector_client = true") {
		t.Error("rr-client missing on iBGP session")
	}
	if strings.Contains(out2, "route_server_client") || strings.Contains(out2, "allowas_in") {
		t.Error("eBGP-only options emitted on iBGP session")
	}
	if len(uns2) == 0 {
		t.Error("expected unsupported warnings for rs-client/allowas-in")
	}
}

func TestNeighborBlocksTTLMultihopConflict(t *testing.T) {
	c := testConfig(t)
	p := testPeer(t, func(p *config.Peer) {
		*p.Multihop = true
		*p.TTLSecurity = true
	})
	out, _ := NeighborBlocks("conflict", p, c)
	if strings.Contains(out, "multihop") {
		t.Error("multihop emitted alongside ttl_security")
	}
	if !strings.Contains(out, "ttl_security = 1") {
		t.Error("ttl_security missing (hop count)")
	}
	// the conflict warning is reported by PeerPolicy's unsupported list
	_, uns := PeerPolicy("conflict", p, c)
	found := false
	for _, u := range uns {
		if strings.Contains(u, "multihop") {
			found = true
		}
	}
	if !found {
		t.Error("expected multihop conflict warning")
	}
}

func TestPeerPolicyImportExport(t *testing.T) {
	c := testConfig(t)
	c.Prefixes = []string{"203.0.113.0/24"}
	p := testPeer(t, func(p *config.Peer) {
		ips := []string{"198.51.100.1"}
		p.NeighborIPs = &ips
		prefixes := []string{"198.18.0.0/15"}
		p.PrefixSet4 = &prefixes
		*p.LocalPref = 200
		*p.SetLocalPref = true
		*p.AnnounceDefault = true
		*p.AnnounceAll = true
		*p.RemovePrivateASNs = true
		prepends := []uint32{64513}
		p.PrependPath = &prepends
	})
	out, _ := PeerPolicy("transit", p, c)
	for _, want := range []string{
		"filter PV_TRANSIT_IN {",
		"if net !~ PV_TRANSIT_IN_ALLOW then reject;",
		"bgp_local_pref = 200;",
		"filter PV_TRANSIT_OUT {",
		"bgp_path.delete(PRIVATE_ASNS);",
		"bgp_path.prepend(64513);",
		"if net ~ PV_LOCAL then accept;",
		"if net ~ PV_DEFAULT then accept;",
		"accept;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("PeerPolicy missing %q", want)
		}
	}
}

func TestNeighborBlocksSessionFields(t *testing.T) {
	c := testConfig(t)
	p := testPeer(t, func(p *config.Peer) {
		*p.EnforceFirstAS = false
		*p.AnnounceDefault = true
		p.LocalASN = intp(65001)
		*p.TTLSecurity = true
	})
	out, _ := NeighborBlocks("sess", p, c)
	for _, want := range []string{
		"enforce_first_as = false", // explicit false must be emitted (cairn default is true)
		"default_originate = true",
		"local_as = 65001",
		"ttl_security = 1",
		"keep_received_routes = true", // always on, not just with import-limit
	} {
		if !strings.Contains(out, want) {
			t.Errorf("NeighborBlocks missing %q", want)
		}
	}
}

func TestPolicyHeaderAsPathDefines(t *testing.T) {
	c := testConfig(t)
	c.BogonASNs = []string{"0", "64496..64511", "64512..65534"}
	c.BlocklistASNs = []uint32{64497, 64498}
	out := PolicyHeader(c)
	if !strings.Contains(out, "define PV_BOGON_ASNS = [ 0, 64496..64511, 64512..65534 ];") {
		t.Errorf("bogon ASNs not emitted as number-set define:\n%s", out)
	}
	if !strings.Contains(out, "define PV_BLOCKLIST_ASNS = [ 64497, 64498 ];") {
		t.Errorf("blocklist ASNs not emitted as number-set define:\n%s", out)
	}
	if strings.Contains(out, "as-path access-list") {
		t.Error("as-path access-list regex must not be used for ASN sets")
	}
}

func TestPeerPolicyNoAnnounce(t *testing.T) {
	c := testConfig(t)
	c.NoAnnounce = true
	p := testPeer(t, nil)
	out, _ := PeerPolicy("x", p, c)
	if !strings.Contains(out, "filter PV_X_OUT {\n    reject;\n}") {
		t.Error("no-announce should render a bare reject export filter")
	}
}

func TestPeerPolicyEnforcePeerNexthop(t *testing.T) {
	c := testConfig(t)
	p := testPeer(t, func(p *config.Peer) {
		ips := []string{"198.51.100.1", "198.51.100.2"}
		p.NeighborIPs = &ips
	})
	out, _ := PeerPolicy("nh", p, c)
	want := "if bgp_next_hop != 198.51.100.1 && bgp_next_hop != 198.51.100.2 then reject;"
	if !strings.Contains(out, want) {
		t.Errorf("enforce-peer-nexthop missing/wrong:\n%s", out)
	}
}

func TestPolicyConfOrdering(t *testing.T) {
	c := testConfig(t)
	pols := map[string]string{
		"b-peer": "filter PV_B_IN {\n    accept;\n}\n",
		"a-peer": "filter PV_A_IN {\n    accept;\n}\n",
	}
	out := PolicyConf(c, pols)
	ia := strings.Index(out, "PV_A_IN")
	ib := strings.Index(out, "PV_B_IN")
	if ia < 0 || ib < 0 || ia > ib {
		t.Error("peer policies not sorted by name")
	}
	if !strings.HasPrefix(out, "! Generated by Pathvector NG") {
		t.Error("missing header")
	}
}
