// Package frr renders Pathvector NG configuration into FRR's integrated
// configuration format (frr.conf) and drives the daemon through vtysh.
//
// FRR has no template language: configuration is plain line-based Cisco
// syntax. Pathvector's peer/policy model is translated into:
//
//   - router bgp <asn> with per-neighbor statements and address-family blocks
//   - ip/ipv6 prefix-lists for prefix-set, bogon, blocklist, prefix-prefs,
//     per-prefix communities and only/dont-announce
//   - bgp community-list / large-community-list for community matching
//   - bgp as-path access-lists for AS-path matching (regex, incl. numeric
//     ranges such as 64512..65534)
//   - route-maps for import/export policy
//
// BIRD filters apply every matching rule in order ("last assignment wins").
// FRR route-maps are first-match, so the generated maps use `continue`:
// unconditional set sequences chain into conditional local-preference
// sequences ordered lowest→highest priority, reproducing BIRD semantics.
//
// Route-map names embed the peer name and neighbor index so that
// enforce-peer-nexthop can be expressed per session address.
package frr

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/XodoTechnology/pathvector-ng/pkg/config"
	"github.com/XodoTechnology/pathvector-ng/pkg/util"
)

// ---------- shared helpers (mirrors pkg/cairn) ----------

// policyName returns an FRR object name for a peer, sanitized to the
// characters route-map/prefix-list names accept.
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

// communityColon renders a pathvector community ("asn,val" standard or
// "asn,val1,val2" large, comma separated) in FRR's colon form.
func communityColon(comm string) string {
	return strings.ReplaceAll(strings.TrimSpace(comm), ",", ":")
}

// prefixListEntry converts a BIRD prefix pattern (PREFIX/LEN{min,max}) into
// FRR prefix-list ge/le syntax. Plain prefixes pass through unchanged.
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

func writePrefixList4(b *strings.Builder, name string, entries []string) {
	sort.Strings(entries)
	for _, p := range entries {
		fmt.Fprintf(b, "ip prefix-list %s permit %s\n", name, prefixListEntry(p))
	}
}

func writePrefixList6(b *strings.Builder, name string, entries []string) {
	sort.Strings(entries)
	for _, p := range entries {
		fmt.Fprintf(b, "ipv6 prefix-list %s permit %s\n", name, prefixListEntry(p))
	}
}

// ---------- AS path regex ----------

// asnRangeRe returns an ERE fragment matching the decimal representation of
// any ASN in [lo,hi]. The result is either a literal or a parenthesized
// alternation, safe to embed inside a larger alternation.
func asnRangeRe(lo, hi uint32) string {
	if lo == hi {
		return strconv.FormatUint(uint64(lo), 10)
	}
	l := strconv.FormatUint(uint64(lo), 10)
	h := strconv.FormatUint(uint64(hi), 10)
	if len(l) != len(h) {
		// Split at the smallest power of ten above lo.
		p := uint64(10)
		for p <= uint64(lo) {
			p *= 10
		}
		return "(" + asnRangeRe(lo, uint32(p-1)) + "|" + asnRangeRe(uint32(p), hi) + ")"
	}
	return rangeReFixed(l, h)
}

// rangeReFixed returns an ERE matching fixed-width decimal strings in
// [lo,hi], where lo and hi are equal-length digit strings (leading zeros
// allowed). The result is atomic or parenthesized.
func rangeReFixed(lo, hi string) string {
	if lo == hi {
		return lo
	}
	// Fast path: the full range of fixed-width digit strings collapses to
	// a single character class — without this the recursion below expands
	// exponentially.
	if lo == strings.Repeat("0", len(lo)) && hi == strings.Repeat("9", len(hi)) {
		return "[0-9]{" + strconv.Itoa(len(lo)) + "}"
	}
	i := 0
	for i < len(lo) && lo[i] == hi[i] {
		i++
	}
	pre := lo[:i]
	ld, hd := lo[i]-'0', hi[i]-'0'
	k := len(lo) - i - 1
	if k == 0 {
		return pre + "[" + string(lo[i]) + "-" + string(hi[i]) + "]"
	}
	anySuf := "[0-9]"
	if k > 1 {
		anySuf = "[0-9]{" + strconv.Itoa(k) + "}"
	}
	loSuf, hiSuf := lo[i+1:], hi[i+1:]
	nines := strings.Repeat("9", k)
	zeros := strings.Repeat("0", k)
	parts := []string{
		string(rune('0'+ld)) + rangeReFixed(loSuf, nines),
		string(rune('0'+hd)) + rangeReFixed(zeros, hiSuf),
	}
	mid := make([]string, 0, int(hd-ld-1))
	for d := ld + 1; d < hd; d++ {
		mid = append(mid, string(rune('0'+d))+anySuf)
	}
	all := append([]string{parts[0]}, append(mid, parts[1])...)
	return pre + "(" + strings.Join(all, "|") + ")"
}

// asPathElement parses one ASN spec entry: either "N" or "A..B".
func asPathElement(e string) (string, bool) {
	e = strings.TrimSpace(e)
	if strings.Contains(e, "..") {
		kv := strings.SplitN(e, "..", 2)
		lo, err1 := strconv.ParseUint(kv[0], 10, 32)
		hi, err2 := strconv.ParseUint(kv[1], 10, 32)
		if err1 != nil || err2 != nil || lo > hi {
			return "", false
		}
		return asnRangeRe(uint32(lo), uint32(hi)), true
	}
	n, err := strconv.ParseUint(e, 10, 32)
	if err != nil {
		return "", false
	}
	return strconv.FormatUint(n, 10), true
}

// asPathContainsRe builds the as-path regex matching a path that contains
// any of the given ASN specs as a whole element.
func asPathContainsRe(specs []string) string {
	var alts []string
	for _, s := range specs {
		if a, ok := asPathElement(s); ok {
			alts = append(alts, a)
		}
	}
	if len(alts) == 0 {
		return ""
	}
	// FRR as-path regex uses "_" as the ASN-element boundary (bgp_regcomp expands it
	// to (^|[,{}() ]|$)). A literal "(^| )...( |$)" is NOT used: the "| " sequence
	// is parsed by FRR command layer as a pipe action and breaks config parsing.
	return "_(" + strings.Join(alts, "|") + ")_"
}

// asPathOriginRe builds the as-path regex matching a path whose last
// (origin) element is any of the given ASNs.
func asPathOriginRe(asns []uint32) string {
	alts := make([]string, len(asns))
	for i, a := range asns {
		alts[i] = strconv.FormatUint(uint64(a), 10)
	}
	sort.Strings(alts)
	return "_(" + strings.Join(alts, "|") + ")$"
}

// ---------- nil-safe dereference helpers ----------

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

func derefStrSliceMap(p *map[string][]string) map[string][]string {
	if p == nil {
		return nil
	}
	return *p
}

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

// neighborAddrs returns the peer's neighbor addresses (iface suffix
// stripped), sorted for deterministic output.
func neighborAddrs(p *config.Peer) []string {
	if p.NeighborIPs == nil {
		return nil
	}
	addrs := make([]string, 0, len(*p.NeighborIPs))
	for _, n := range *p.NeighborIPs {
		addrs = append(addrs, strings.SplitN(n, "%", 2)[0])
	}
	sort.Strings(addrs)
	return unique(addrs)
}

// ---------- route-map sequence model ----------

// rmSeq is one route-map sequence with an absolute sequence number.
type rmSeq struct {
	seq     int
	action  string   // "permit" or "deny"
	matches []string // match clauses
	sets    []string // set clauses
	cont    int      // absolute continue target seq (0 = terminate)
}

// writeRouteMap serializes sequences in slice order using their seq field.
func writeRouteMap(b *strings.Builder, name string, seqs []rmSeq) {
	for _, s := range seqs {
		fmt.Fprintf(b, "route-map %s %s %d\n", name, s.action, s.seq)
		for _, m := range s.matches {
			b.WriteString(" " + m + "\n")
		}
		for _, st := range s.sets {
			b.WriteString(" " + st + "\n")
		}
		if s.cont != 0 {
			fmt.Fprintf(b, " continue %d\n", s.cont)
		}
	}
}

// ---------- match/set clause helpers ----------

// addrMatch returns the prefix-list match clause for the address family.
func addrMatch(fam, list string) string {
	if fam == "v6" {
		return "match ipv6 address prefix-list " + list
	}
	return "match ip address prefix-list " + list
}

// nhMatch returns the next-hop prefix-list match clause.
func nhMatch(fam, list string) string {
	if fam == "v6" {
		return "match ipv6 next-hop prefix-list " + list
	}
	return "match ip next-hop prefix-list " + list
}

// setNextHop returns the set clause rewriting the BGP next hop.
func setNextHop(fam, addr string) string {
	if fam == "v6" {
		return "set ipv6 next-hop global " + addr
	}
	return "set ip next-hop " + addr
}

// setPeerNextHop returns the set clause forcing next-hop to the peer address.
func setPeerNextHop(fam string) string {
	if fam == "v6" {
		return "set ipv6 next-hop peer-address"
	}
	return "set ip next-hop peer-address"
}

// blackholeSets returns the next-hop set for blackhole routes: FRR resolves
// the next hop through a `blackhole` static route, approximating BIRD's
// RTD_BLACKHOLE.
func blackholeSets(fam string) []string {
	if fam == "v6" {
		return []string{"set ipv6 next-hop global 100::1"}
	}
	return []string{"set ip next-hop 192.0.2.1"}
}

// ---------- global objects ----------

// GlobalObjects renders the shared prefix-lists, community-lists,
// as-path access-lists and the RPKI section used by every peer.
func GlobalObjects(c *config.Config) string {
	var b strings.Builder
	b.WriteString("! ---- shared policy objects ----\n")

	// RPKI RTR cache (FRR 9+ syntax; requires `bgpd -M rpki` and the
	// frr-rpki-rtrlib package).
	if c.RPKIEnable && c.RTRServerHost != "" {
		b.WriteString("rpki\n")
		fmt.Fprintf(&b, " rpki cache tcp %s %d preference 1\n", c.RTRServerHost, c.RTRServerPort)
		b.WriteString(" exit\n")
	}

	// Family matchers (match everything of the family).
	writePrefixList4(&b, "PV_V4ANY", []string{"0.0.0.0/0 le 32"})
	writePrefixList6(&b, "PV_V6ANY", []string{"::/0 le 128"})
	b.WriteString("\n")

	// Default route matchers.
	writePrefixList4(&b, "PV_DEFAULT_V4", []string{"0.0.0.0/0"})
	writePrefixList6(&b, "PV_DEFAULT_V6", []string{"::/0"})
	b.WriteString("\n")

	// Bogon filters.
	b4, _ := splitFamilies(c.Bogons4)
	_, b6 := splitFamilies(c.Bogons6)
	if len(b4) > 0 {
		writePrefixList4(&b, "PV_BOGON_V4", b4)
	}
	if len(b6) > 0 {
		writePrefixList6(&b, "PV_BOGON_V6", b6)
	}
	if re := asPathContainsRe(c.BogonASNs); re != "" {
		fmt.Fprintf(&b, "bgp as-path access-list PV_BOGON_AS permit %s\n", re)
	}
	b.WriteString("\n")

	// Out-of-bounds prefix lengths (IPv4 len <8 or >24, IPv6 <12 or >48).
	// With accept-default, BIRD exempts the default route; `ge 1 le 7` and
	// `ge 49 le 128` style bounds exclude /0 and ::/0 from the low side.
	if c.AcceptDefault {
		writePrefixList4(&b, "PV_BADLEN_V4", []string{"0.0.0.0/0 ge 1 le 7", "0.0.0.0/0 ge 25 le 32"})
		writePrefixList6(&b, "PV_BADLEN_V6", []string{"::/0 ge 1 le 11", "::/0 ge 49 le 128"})
	} else {
		writePrefixList4(&b, "PV_BADLEN_V4", []string{"0.0.0.0/0 le 7", "0.0.0.0/0 ge 25"})
		writePrefixList6(&b, "PV_BADLEN_V6", []string{"::/0 le 11", "::/0 ge 49"})
	}
	b.WriteString("\n")

	// Global blocklist.
	if len(c.BlocklistPrefixes) > 0 {
		lb4, lb6 := splitFamilies(c.BlocklistPrefixes)
		if len(lb4) > 0 {
			writePrefixList4(&b, "PV_BLOCKLIST_V4", lb4)
		}
		if len(lb6) > 0 {
			writePrefixList6(&b, "PV_BLOCKLIST_V6", lb6)
		}
	}
	if len(c.BlocklistASNs) > 0 {
		asns := make([]string, len(c.BlocklistASNs))
		for i, a := range c.BlocklistASNs {
			asns[i] = strconv.FormatUint(uint64(a), 10)
		}
		if re := asPathContainsRe(asns); re != "" {
			fmt.Fprintf(&b, "bgp as-path access-list PV_BLOCKLIST_AS permit %s\n", re)
		}
	}
	b.WriteString("\n")

	// filter-transit-asns and filter-never-via-route-servers support lists.
	if re := asPathContainsRe(uint32SliceStrings(c.TransitASNs)); re != "" {
		fmt.Fprintf(&b, "bgp as-path access-list PV_TRANSIT_AS permit %s\n", re)
	}
	if c.QueryNVRS && len(c.NVRSASNs) > 0 {
		fmt.Fprintf(&b, "bgp as-path access-list PV_NVRS_AS permit %s\n", asPathContainsRe(uint32SliceStrings(c.NVRSASNs)))
	}
	b.WriteString("\n")

	// Locally originated prefixes (announce-originated match + kernel deny).
	if len(c.Prefixes4) > 0 {
		writePrefixList4(&b, "PV_LOCAL_V4", c.Prefixes4)
	}
	if len(c.Prefixes6) > 0 {
		writePrefixList6(&b, "PV_LOCAL_V6", c.Prefixes6)
	}
	b.WriteString("\n")

	// Blackhole helpers, emitted when any peer/global option needs them.
	if needBlackhole(c) {
		// Communities that trigger blackhole processing (BIRD
		// process_blackholes): (ASN,666) and (65535,666) standard; the
		// (ASN,1,666) large community is the is_blackhole marker.
		fmt.Fprintf(&b, "bgp community-list standard PV_BHSTD permit %d:666\n", c.ASN)
		b.WriteString("bgp community-list standard PV_BHSTD permit 65535:666\n")
		fmt.Fprintf(&b, "bgp large-community-list standard PV_BHLC permit %d:1:666\n", c.ASN)
		// is_blackhole also requires a /32 (v4) or /128 (v6) route.
		writePrefixList4(&b, "PV_BHHOST_V4", []string{"0.0.0.0/0 ge 32"})
		writePrefixList6(&b, "PV_BHHOST_V6", []string{"::/0 ge 128"})
		b.WriteString("\n")
	}

	// Graceful shutdown community: 65535:0 -> local-preference 0.
	b.WriteString("bgp community-list standard PV_GS permit 65535:0\n")

	// Origin communities for the accept_local export path.
	if len(c.OriginStandardCommunities) > 0 || len(c.OriginLargeCommunities) > 0 {
		b.WriteString("\n")
		writeCommunityLists(&b, "PV_ORIGIN_STD", "PV_ORIGIN_L", c.OriginStandardCommunities, c.OriginLargeCommunities)
	}

	return b.String()
}

// writeCommunityLists emits standard + large community-list objects for a
// set of exact communities ("asn,val" / "asn,v1,v2" storage form).
func writeCommunityLists(b *strings.Builder, stdName, largeName string, std, large []string) {
	sort.Strings(std)
	for _, comm := range std {
		fmt.Fprintf(b, "bgp community-list standard %s permit %s\n", stdName, communityColon(comm))
	}
	sort.Strings(large)
	for _, comm := range large {
		fmt.Fprintf(b, "bgp large-community-list standard %s permit %s\n", largeName, communityColon(comm))
	}
}

// uint32SliceStrings renders []uint32 as decimal strings.
func uint32SliceStrings(in []uint32) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = strconv.FormatUint(uint64(v), 10)
	}
	return out
}

// ---------- per-peer rendering ----------

// famRender holds the rendered fragments for one peer+address-family.
type famRender struct {
	fam          string // "v4" or "v6"
	objects      string
	importRM     []rmSeq
	exportRM     []rmSeq
	importRMName string
	exportRMName string
	outPL        string
}

// PeerBlocks holds the rendered fragments for one peer.
type PeerBlocks struct {
	Neighbor string
	AF4      string
	AF6      string
	Objects  string
}

// PeerConfig renders everything for one peer and returns the fragments
// Config assembles into frr.conf, plus unsupported-feature warnings.
func PeerConfig(peerName string, p *config.Peer, c *config.Config) (PeerBlocks, []string) {
	var pb PeerBlocks
	var warns []string
	var nb, a4, a6, ob strings.Builder
	base := policyName(peerName, "")

	addrs := neighborAddrs(p)
	if len(addrs) == 0 {
		return pb, []string{"no neighbor addresses"}
	}

	localASN := derefInt(p.LocalASN)
	if localASN == 0 {
		localASN = c.ASN
	}
	ebgp := derefInt(p.ASN) != localASN

	// Render each neighbor address's address-family data.
	type addrFams struct {
		addr string
		fams []famRender
	}
	var perAddr []addrFams
	for i, addr := range addrs {
		isV6addr := isV6(addr)
		fams := []string{"v4"}
		if isV6addr {
			fams = []string{"v6"}
		}
		if derefBool(p.MPUnicast46) {
			fams = []string{"v4", "v6"}
		}
		var frs []famRender
		for _, fam := range fams {
			fr, w := renderFamily(peerName, i, addr, fam, p, c, ebgp, base)
			warns = append(warns, w...)
			frs = append(frs, fr)
		}
		perAddr = append(perAddr, addrFams{addr: addr, fams: frs})
	}

	// ---------- neighbor-level commands ----------
	for _, addr := range addrs {
		fmt.Fprintf(&nb, " neighbor %s remote-as %d\n", addr, derefInt(p.ASN))
		desc := strings.ReplaceAll(derefStr(p.Description), "\n", " ")
		if desc == "" {
			desc = peerName
		}
		fmt.Fprintf(&nb, " neighbor %s description %s\n", addr, desc)
		if pw := derefStr(p.Password); pw != "" {
			fmt.Fprintf(&nb, " neighbor %s password %s\n", addr, pw)
		}
		if derefBool(p.Disabled) {
			fmt.Fprintf(&nb, " neighbor %s shutdown\n", addr)
		}
		if derefBool(p.Passive) {
			fmt.Fprintf(&nb, " neighbor %s passive\n", addr)
		}
		if derefBool(p.Multihop) {
			if derefBool(p.TTLSecurity) {
				warns = append(warns, "multihop+ttl-security (ttl-security wins; multihop dropped)")
			} else {
				fmt.Fprintf(&nb, " neighbor %s ebgp-multihop 255\n", addr)
			}
		}
		if derefBool(p.TTLSecurity) {
			fmt.Fprintf(&nb, " neighbor %s ttl-security hops 254\n", addr)
		}
		if derefBool(p.EnforceFirstAS) {
			if ebgp {
				fmt.Fprintf(&nb, " neighbor %s enforce-first-as\n", addr)
			} else {
				warns = append(warns, "enforce-first-as on iBGP session (eBGP only)")
			}
		}
		if derefInt(p.LocalASN) != 0 {
			fmt.Fprintf(&nb, " neighbor %s local-as %d\n", addr, localASN)
		}
		if !isV6(addr) && derefStr(p.Listen4) != "" {
			fmt.Fprintf(&nb, " neighbor %s update-source %s\n", addr, derefStr(p.Listen4))
		} else if isV6(addr) && derefStr(p.Listen6) != "" {
			fmt.Fprintf(&nb, " neighbor %s update-source %s\n", addr, derefStr(p.Listen6))
		}
		nhs := derefBool(p.NextHopSelf) ||
			(derefBool(p.NextHopSelfEBGP) && ebgp) ||
			(derefBool(p.NextHopSelfIBGP) && !ebgp)
		if nhs {
			fmt.Fprintf(&nb, " neighbor %s next-hop-self\n", addr)
		}
		if derefBool(p.AllowLocalAS) {
			if ebgp {
				fmt.Fprintf(&nb, " neighbor %s allowas-in 1\n", addr)
			} else {
				warns = append(warns, "allow-local-as on iBGP session (eBGP only)")
			}
		}
		if derefBool(p.RRClient) {
			if ebgp {
				warns = append(warns, "rr-client on eBGP session (iBGP only)")
			} else {
				fmt.Fprintf(&nb, " neighbor %s route-reflector-client\n", addr)
			}
		}
		if derefBool(p.RSClient) {
			if !ebgp {
				warns = append(warns, "rs-client on iBGP session (eBGP only)")
			} else {
				fmt.Fprintf(&nb, " neighbor %s route-server-client\n", addr)
			}
		}
		if derefBool(p.RemovePrivateASNs) {
			if ebgp {
				fmt.Fprintf(&nb, " neighbor %s remove-private-AS all\n", addr)
			} else {
				warns = append(warns, "remove-private-asns on iBGP session (eBGP only)")
			}
		}
		// BIRD advertises all community flavours; FRR must be told explicitly.
		fmt.Fprintf(&nb, " neighbor %s send-community all\n", addr)
		if derefBool(p.AddPathTx) {
			fmt.Fprintf(&nb, " neighbor %s addpath-tx-all-paths\n", addr)
		}
		// add-path-rx has no FRR equivalent: receiving additional paths is
		// automatic when the peer sends them.
		// FRR-native graceful shutdown: `shutdown` sends the RFC 9003
		// community so the peer depreferentiates us — the drain primitive.
		fmt.Fprintf(&nb, " neighbor %s graceful-shutdown\n", addr)
		if derefBool(p.BFD) {
			fmt.Fprintf(&nb, " neighbor %s bfd\n", addr)
		}
		if derefInt(p.LocalPort) != 179 || derefInt(p.NeighborPort) != 179 {
			warns = append(warns, "local-port/neighbor-port")
		}
	}

	warns = append(warns, unsupportedPeerWarnings(p)...)

	// ---------- address-family entries + objects ----------
	for _, af := range perAddr {
		for _, fr := range af.fams {
			fam := fr.fam
			var dst *strings.Builder
			if fam == "v4" {
				dst = &a4
			} else {
				dst = &a6
			}
			fmt.Fprintf(dst, "  neighbor %s activate\n", af.addr)
			fmt.Fprintf(dst, "  neighbor %s route-map %s in\n", af.addr, fr.importRMName)
			fmt.Fprintf(dst, "  neighbor %s route-map %s out\n", af.addr, fr.exportRMName)
			if fr.outPL != "" {
				fmt.Fprintf(dst, "  neighbor %s prefix-list %s out\n", af.addr, fr.outPL)
			}
			limit := derefInt(p.ImportLimit4)
			if fam == "v6" {
				limit = derefInt(p.ImportLimit6)
			}
			if limit > 0 {
				if derefStr(p.ImportLimitTripAction) == "warn" {
					fmt.Fprintf(dst, "  neighbor %s maximum-prefix %d 90 warning-only\n", af.addr, limit)
				} else {
					fmt.Fprintf(dst, "  neighbor %s maximum-prefix %d 90\n", af.addr, limit)
				}
			}
			if derefBool(p.AnnounceDefault) {
				fmt.Fprintf(dst, "  neighbor %s default-originate\n", af.addr)
			}
			ob.WriteString(fr.objects)
			writeRouteMap(&ob, fr.importRMName, fr.importRM)
			writeRouteMap(&ob, fr.exportRMName, fr.exportRM)
		}
	}

	pb.Neighbor = nb.String()
	pb.AF4 = a4.String()
	pb.AF6 = a6.String()
	pb.Objects = ob.String()
	return pb, unique(warns)
}

// unsupportedPeerWarnings reports peer options that have no FRR equivalent.
// These are BIRD-specific features; they are skipped with a warning rather
// than silently dropped.
func unsupportedPeerWarnings(p *config.Peer) []string {
	var w []string
	add := func(set bool, name string) {
		if set {
			w = append(w, name)
		}
	}
	add(derefStr(p.PreImportFilter) != "", "pre-import-filter (raw BIRD config)")
	add(derefStr(p.PostImportFilter) != "", "post-import-filter (raw BIRD config)")
	add(derefStr(p.PreImportAccept) != "", "pre-import-accept (raw BIRD config)")
	add(derefStr(p.PreExport) != "", "pre-export (raw BIRD config)")
	add(derefStr(p.PreExportFinal) != "", "pre-export-final (raw BIRD config)")
	add(derefStr(p.SessionGlobal) != "", "session-global (raw BIRD config)")
	add(derefBool(p.Direct), "direct")
	add(derefStr(p.Gateway) != "", "gateway")
	add(derefInt(p.Cost) != 0, "cost")
	add(derefBool(p.PreferOlderRoutes), "prefer-older-routes")
	add(derefBool(p.AdvertiseHostname), "advertise-hostname")
	add(derefBool(p.ExtendedNextHop), "extended-next-hop")
	add(derefBool(p.DisableAfterError), "disable-after-error")
	add(derefInt(p.Confederation) != 0 || derefBool(p.ConfederationMember), "confederation")
	add(derefBool(p.L3VPN), "l3vpn")
	add(derefStr(p.Role) != "" || derefBool(p.RequireRoles), "bgp roles (RFC 9234)")
	add(p.InterpretCommunities != nil && !*p.InterpretCommunities, "interpret-communities (FRR always interprets well-known communities)")
	add(len(derefStrSlice(p.TransitLock)) > 0, "transit-lock")
	add(derefBool(p.FilterASPA), "filter-aspa")
	add(derefStr(p.Source4) != "" || derefStr(p.Source6) != "", "source4/source6")
	add(derefInt(p.ReceiveLimit4) != 0 || derefInt(p.ReceiveLimit6) != 0, "receive-limit (no FRR equivalent)")
	add(derefInt(p.ExportLimit4) != 0 || derefInt(p.ExportLimit6) != 0, "export-limit (no FRR equivalent)")
	return w
}

// renderFamily renders the policy objects and route-map sequences for one
// peer + neighbor index + address family.
func renderFamily(peerName string, idx int, addr, fam string, p *config.Peer, c *config.Config, ebgp bool, base string) (famRender, []string) {
	var fr famRender
	var warns []string
	v6 := fam == "v6"
	sfx := "V4"
	if v6 {
		sfx = "V6"
	}
	fr.importRMName = fmt.Sprintf("%s_%d_IN%s", base, idx, sfx)
	fr.exportRMName = fmt.Sprintf("%s_%d_OUT%s", base, idx, sfx)
	fr.fam = fam

	var ob strings.Builder

	// ---------- accept-list (prefix-set + IRR) ----------
	set4, set6 := splitFamilies(derefStrSlice(p.PrefixSet4))
	_, irr6 := splitFamilies(derefStrSlice(p.PrefixSet6))
	set6 = append(set6, irr6...)
	allowList := set4
	allowName := ""
	if v6 {
		allowList = set6
	}
	if len(allowList) > 0 {
		allowName = fmt.Sprintf("%s_ALLOW_%s", base, sfx)
		if v6 {
			writePrefixList6(&ob, allowName, allowList)
		} else {
			writePrefixList4(&ob, allowName, allowList)
		}
	}

	// ---------- AS-set origin check ----------
	asSetName := ""
	if derefBool(p.FilterASSet) && p.ASSetMembers != nil && len(*p.ASSetMembers) > 0 {
		asSetName = base + "_ASSET"
		fmt.Fprintf(&ob, "bgp as-path access-list %s permit %s\n", asSetName, asPathOriginRe(*p.ASSetMembers))
	}

	// ---------- enforce-peer-nexthop ----------
	nhName := ""
	if derefBool(p.EnforcePeerNexthop) {
		if v6 {
			if isV6(addr) {
				nhName = fmt.Sprintf("%s_%d_NH_%s", base, idx, sfx)
				writePrefixList6(&ob, nhName, []string{addr + "/128"})
			} else {
				warns = append(warns, "enforce-peer-nexthop with IPv4 session carrying IPv6 routes")
			}
		} else {
			if !isV6(addr) {
				nhName = fmt.Sprintf("%s_%d_NH_%s", base, idx, sfx)
				writePrefixList4(&ob, nhName, []string{addr + "/32"})
			} else {
				warns = append(warns, "enforce-peer-nexthop with IPv6 session carrying IPv4 routes")
			}
		}
	}

	// ---------- dont/only-announce outbound prefix-list ----------
	dont4, dont6 := splitFamilies(derefStrSlice(p.DontAnnounce))
	only4, only6 := splitFamilies(derefStrSlice(p.OnlyAnnounce))
	dont, only := dont4, only4
	if v6 {
		dont, only = dont6, only6
	}
	onlySet := len(derefStrSlice(p.OnlyAnnounce)) > 0
	if len(dont) > 0 || len(only) > 0 || onlySet {
		fr.outPL = fmt.Sprintf("%s_%d_OUTPL_%s", base, idx, sfx)
		renderOutPrefixList(&ob, fr.outPL, dont, only, onlySet, v6)
	}

	// ---------- preference objects + fragments ----------
	prefs := collectPrefFrags(&ob, peerName, sfx, v6, p)

	// ---------- community delete lists ----------
	delC, delL := renderDeleteLists(&ob, peerName, p)

	// ---------- announce community lists ----------
	annC, annL := renderAnnounceLists(&ob, peerName, p)

	// ---------- per-prefix communities ----------
	pcFrags := renderPrefixCommunities(&ob, peerName, sfx, v6, p)

	fr.objects = ob.String()
	fr.importRM = buildImportRM(fam, p, c,
		allowName, asSetName, nhName, prefs, delC, delL)
	fr.exportRM = buildExportRM(fam, p, c, pcFrags, annC, annL)

	return fr, unique(warns)
}

// ---------- preference fragments ----------

// matchSets pairs route-map match clauses with set clauses.
type matchSets struct {
	matches []string
	sets    []string
}

// collectPrefFrags emits objects for all conditional local-preference
// rules and returns fragments in BIRD priority order (lowest first, so
// later/higher-priority seqs override earlier ones).
func collectPrefFrags(b *strings.Builder, peerName, sfx string, v6 bool, p *config.Peer) []matchSets {
	var out []matchSets
	base := policyName(peerName, "")
	fam := famOfV6(v6)

	// 1. local-pref4/6 (family specific, overrides base local-pref).
	if v6 {
		if lp := derefInt(p.LocalPref6); lp != 0 {
			out = append(out, matchSets{
				matches: []string{addrMatch("v6", "PV_V6ANY")},
				sets:    []string{fmt.Sprintf("set local-preference %d", lp)},
			})
		}
	} else {
		if lp := derefInt(p.LocalPref4); lp != 0 {
			out = append(out, matchSets{
				matches: []string{addrMatch("v4", "PV_V4ANY")},
				sets:    []string{fmt.Sprintf("set local-preference %d", lp)},
			})
		}
	}

	// 2. Standard community prefs.
	if m := derefStrUintMap(p.StandardCommunityPrefs); len(m) > 0 {
		j := 0
		for _, comm := range sortedKeys(m) {
			j++
			name := fmt.Sprintf("%s_CPF_%s_%d", base, sfx, j)
			writeCommListEntry(b, "bgp community-list", name, comm, false)
			out = append(out, matchSets{
				matches: []string{"match community " + name},
				sets:    []string{fmt.Sprintf("set local-preference %d", m[comm])},
			})
		}
	}

	// 3. Large community prefs.
	if m := derefStrUintMap(p.LargeCommunityPrefs); len(m) > 0 {
		j := 0
		for _, comm := range sortedKeys(m) {
			j++
			name := fmt.Sprintf("%s_LCPF_%s_%d", base, sfx, j)
			writeCommListEntry(b, "bgp large-community-list", name, comm, true)
			out = append(out, matchSets{
				matches: []string{"match large-community " + name},
				sets:    []string{fmt.Sprintf("set local-preference %d", m[comm])},
			})
		}
	}

	// 4. AS prefs (all families), then 5. family-specific AS prefs.
	if m := derefUint32Map(p.ASPrefs); len(m) > 0 {
		for _, asn := range sortedUintKeys(m) {
			name := fmt.Sprintf("%s_ASP_%s_%d", base, sfx, asn)
			fmt.Fprintf(b, "bgp as-path access-list %s permit %s\n", name,
				asPathContainsRe([]string{strconv.FormatUint(uint64(asn), 10)}))
			out = append(out, matchSets{
				matches: []string{"match as-path " + name},
				sets:    []string{fmt.Sprintf("set local-preference %d", m[asn])},
			})
		}
	}
	var famPrefs map[uint32]uint32
	if v6 {
		famPrefs = derefUint32Map(p.ASPrefs6)
	} else {
		famPrefs = derefUint32Map(p.ASPrefs4)
	}
	if len(famPrefs) > 0 {
		for _, asn := range sortedUintKeys(famPrefs) {
			name := fmt.Sprintf("%s_ASPF_%s_%d", base, sfx, asn)
			fmt.Fprintf(b, "bgp as-path access-list %s permit %s\n", name,
				asPathContainsRe([]string{strconv.FormatUint(uint64(asn), 10)}))
			out = append(out, matchSets{
				matches: []string{"match as-path " + name},
				sets:    []string{fmt.Sprintf("set local-preference %d", famPrefs[asn])},
			})
		}
	}

	// 6. Prefix prefs (family-specific maps populated at load).
	var pp map[string]uint32
	if v6 {
		pp = derefStrUintMap(p.PrefixPrefs6)
	} else {
		pp = derefStrUintMap(p.PrefixPrefs4)
	}
	if len(pp) == 0 && p.PrefixPrefs != nil {
		// Fall back to the mixed map filtered by family (defensive; Load
		// normally populates the per-family maps).
		pp = map[string]uint32{}
		for k, v := range *p.PrefixPrefs {
			if v6 == strings.Contains(k, ":") {
				pp[k] = v
			}
		}
	}
	j := 0
	for _, prefix := range sortedKeys(pp) {
		j++
		name := fmt.Sprintf("%s_PPF_%s_%d", base, sfx, j)
		if v6 {
			writePrefixList6(b, name, []string{prefix})
		} else {
			writePrefixList4(b, name, []string{prefix})
		}
		out = append(out, matchSets{
			matches: []string{addrMatch(fam, name)},
			sets:    []string{fmt.Sprintf("set local-preference %d", pp[prefix])},
		})
	}

	return out
}

func famOfV6(v6 bool) string {
	if v6 {
		return "v6"
	}
	return "v4"
}

// writeCommListEntry emits one community-list entry, choosing standard or
// expanded form depending on wildcards.
func writeCommListEntry(b *strings.Builder, listType, name, comm string, large bool) {
	kind := "standard"
	if commHasWildcard(comm) {
		kind = "expanded"
	}
	if large {
		fmt.Fprintf(b, "bgp large-community-list %s %s permit %s\n", kind, name, commListArg(comm, kind))
	} else {
		fmt.Fprintf(b, "bgp community-list %s %s permit %s\n", kind, name, commListArg(comm, kind))
	}
}

// commListArg renders the permit argument: exact communities in colon form,
// wildcard patterns as _regex_.
func commListArg(comm, kind string) string {
	if kind == "expanded" {
		return "_" + commPatternRe(comm) + "_"
	}
	return communityColon(comm)
}

// commPatternRe converts a community pattern ("asn,val" or "asn,v1,v2",
// possibly containing * / ? wildcards) into an ERE on the colon form.
func commPatternRe(comm string) string {
	parts := strings.Split(strings.TrimSpace(comm), ",")
	for i, p := range parts {
		p = strings.ReplaceAll(p, "*", "[0-9]+")
		p = strings.ReplaceAll(p, "?", "[0-9]")
		parts[i] = p
	}
	return "^" + strings.Join(parts, ":") + "$"
}

func commHasWildcard(comm string) bool {
	return strings.ContainsAny(comm, "*?")
}

// ---------- community delete lists ----------

// renderDeleteLists emits expanded community-lists for communities to strip
// on import. Returns the list names ("" when nothing to delete).
func renderDeleteLists(b *strings.Builder, peerName string, p *config.Peer) (stdName, largeName string) {
	base := policyName(peerName, "")
	std := derefStrSlice(p.RemoveStandardCommunities)
	large := derefStrSlice(p.RemoveLargeCommunities)
	hasStd := len(std) > 0 || derefInt(p.RemoveAllCommunities) != 0
	hasLarge := len(large) > 0 || derefInt(p.RemoveAllCommunities) != 0
	if hasStd {
		stdName = base + "_DELC"
		if n := derefInt(p.RemoveAllCommunities); n != 0 {
			fmt.Fprintf(b, "bgp community-list expanded %s permit _^%d:[0-9]+$_\n", stdName, n)
		}
		for _, comm := range std {
			fmt.Fprintf(b, "bgp community-list expanded %s permit _%s_\n", stdName, commPatternRe(comm))
		}
	}
	if hasLarge {
		largeName = base + "_DELL"
		if n := derefInt(p.RemoveAllCommunities); n != 0 {
			fmt.Fprintf(b, "bgp large-community-list expanded %s permit _^%d:[0-9]+:[0-9]+$_\n", largeName, n)
		}
		for _, comm := range large {
			fmt.Fprintf(b, "bgp large-community-list expanded %s permit _%s_\n", largeName, commPatternRe(comm))
		}
	}
	return stdName, largeName
}

// deleteSetClauses returns the set comm-list delete clauses.
func deleteSetClauses(stdName, largeName string) []string {
	var out []string
	if stdName != "" {
		out = append(out, "set comm-list "+stdName+" delete")
	}
	if largeName != "" {
		out = append(out, "set large-comm-list "+largeName+" delete")
	}
	return out
}

// ---------- announce community lists ----------

// renderAnnounceLists emits community-lists matching announce-communities.
func renderAnnounceLists(b *strings.Builder, peerName string, p *config.Peer) (stdName, largeName string) {
	base := policyName(peerName, "")
	std := derefStrSlice(p.AnnounceStandardCommunities)
	large := derefStrSlice(p.AnnounceLargeCommunities)
	if len(std) > 0 {
		stdName = base + "_ANN"
		for _, comm := range std {
			fmt.Fprintf(b, "bgp community-list standard %s permit %s\n", stdName, communityColon(comm))
		}
	}
	if len(large) > 0 {
		largeName = base + "_ANNL"
		for _, comm := range large {
			fmt.Fprintf(b, "bgp large-community-list standard %s permit %s\n", largeName, communityColon(comm))
		}
	}
	return stdName, largeName
}

// ---------- per-prefix communities ----------

// renderPrefixCommunities emits one prefix-list per prefix-communities key
// and returns match+set fragments for the export route-map.
func renderPrefixCommunities(b *strings.Builder, peerName, sfx string, v6 bool, p *config.Peer) []matchSets {
	var out []matchSets
	base := policyName(peerName, "")
	fam := famOfV6(v6)

	stdMap := derefStrSliceMap(p.PrefixStandardCommunities)
	largeMap := derefStrSliceMap(p.PrefixLargeCommunities)

	keys := map[string]bool{}
	for k := range stdMap {
		keys[k] = true
	}
	for k := range largeMap {
		keys[k] = true
	}
	var prefixes []string
	for k := range keys {
		if v6 == strings.Contains(k, ":") {
			prefixes = append(prefixes, k)
		}
	}
	sort.Strings(prefixes)

	for _, prefix := range prefixes {
		name := fmt.Sprintf("%s_PC_%s_%d", base, sfx, len(out)+1)
		if v6 {
			writePrefixList6(b, name, []string{prefix})
		} else {
			writePrefixList4(b, name, []string{prefix})
		}
		var sets []string
		if comms := stdMap[prefix]; len(comms) > 0 {
			sets = append(sets, "set community "+strings.Join(commColonSorted(comms), " ")+" additive")
		}
		if comms := largeMap[prefix]; len(comms) > 0 {
			sets = append(sets, "set large-community "+strings.Join(commColonSorted(comms), " ")+" additive")
		}
		out = append(out, matchSets{matches: []string{addrMatch(fam, name)}, sets: sets})
	}
	return out
}

// commColonSorted renders communities in FRR colon form, sorted+unique.
func commColonSorted(comms []string) []string {
	vals := make([]string, len(comms))
	for i, c := range comms {
		vals[i] = communityColon(c)
	}
	sort.Strings(vals)
	return unique(vals)
}

// ---------- combined outbound prefix-list ----------

// renderOutPrefixList emits the combined dont/only-announce prefix-list.
// Order: deny dont-announce entries, permit only-announce (or all), deny
// the remainder. An only-announce list that is empty for this family
// denies everything (matching BIRD).
func renderOutPrefixList(b *strings.Builder, name string, dont, only []string, onlyAnnounceSet, v6 bool) {
	denyAll := "0.0.0.0/0 le 32"
	if v6 {
		denyAll = "::/0 le 128"
	}
	type ent struct{ act, pfx string }
	var seq []ent
	for _, d := range dont {
		seq = append(seq, ent{"deny", prefixListEntry(d)})
	}
	if len(only) > 0 {
		for _, o := range only {
			seq = append(seq, ent{"permit", prefixListEntry(o)})
		}
		seq = append(seq, ent{"deny", denyAll})
	} else if onlyAnnounceSet {
		seq = append(seq, ent{"deny", denyAll})
	} else {
		seq = append(seq, ent{"permit", denyAll})
	}
	for _, e := range seq {
		if v6 {
			fmt.Fprintf(b, "ipv6 prefix-list %s %s %s\n", name, e.act, e.pfx)
		} else {
			fmt.Fprintf(b, "ip prefix-list %s %s %s\n", name, e.act, e.pfx)
		}
	}
}

// ---------- IMPORT route-map ----------

// buildImportRM assembles the import route-map sequences for one
// peer+family. Layout (absolute seq numbers, gaps allowed):
//
//	5    is_blackhole exemption -> continue 12 (skips only badlen at 10)
//	10   badlen deny
//	12   bogon-routes deny
//	14   blackhole-bogon-as permit->accept
//	16   bogon-as deny
//	18   prefix-set deny
//	20   as-set deny
//	22   blocklist-prefix deny
//	24   blocklist-as deny
//	26   transit-as deny
//	28   nvrs-as deny
//	30   peer-nexthop deny
//	40   unconditional base sets -> continue 42
//	42   blackhole std-community accept
//	44   blackhole large-community accept
//	46   rpki-invalid deny
//	48   rpki-notfound deny
//	50+  conditional local-pref seqs (low->high priority, chained)
//	GS   graceful-shutdown local-pref 0
//	last final accept
func buildImportRM(fam string, p *config.Peer, c *config.Config,
	allowName, asSetName, nhName string, prefs []matchSets, delC, delL string) []rmSeq {

	v6 := fam == "v6"

	// import:false / no-accept → deny everything.
	if c.NoAccept || !derefBool(p.Import) {
		return []rmSeq{{seq: 10, action: "deny"}}
	}

	var seqs []rmSeq

	// is_blackhole exemption: (ASN,1,666) large community AND a /32//128
	// route skips only the prefix-length check (BIRD is_blackhole()).
	// continue 12 lands past the badlen deny at seq 10.
	if derefBool(p.AllowBlackholeCommunity) {
		hostList := "PV_BHHOST_V4"
		if v6 {
			hostList = "PV_BHHOST_V6"
		}
		seqs = append(seqs, rmSeq{
			seq:     5,
			action:  "permit",
			matches: []string{"match large-community PV_BHLC", addrMatch(fam, hostList)},
			cont:    12,
		})
	}

	// Deny block at fixed slots.
	if derefBool(p.FilterPrefixLength) {
		bl := "PV_BADLEN_V4"
		if v6 {
			bl = "PV_BADLEN_V6"
		}
		seqs = append(seqs, rmSeq{seq: 10, action: "deny", matches: []string{addrMatch(fam, bl)}})
	}
	if derefBool(p.FilterBogonRoutes) {
		bl := "PV_BOGON_V4"
		if v6 {
			bl = "PV_BOGON_V6"
		}
		seqs = append(seqs, rmSeq{seq: 12, action: "deny", matches: []string{addrMatch(fam, bl)}})
	}
	// blackhole-bogon-asns: bogon-AS paths are blackholed+accepted instead
	// of rejected (BIRD set_blackhole inside reject_bogon_asns).
	if c.BlackholeBogonASNs && derefBool(p.FilterBogonASNs) && len(c.BogonASNs) > 0 {
		seqs = append(seqs, rmSeq{
			seq:     14,
			action:  "permit",
			matches: []string{"match as-path PV_BOGON_AS"},
			sets:    blackholeSets(fam),
		})
	}
	if derefBool(p.FilterBogonASNs) && len(c.BogonASNs) > 0 {
		seqs = append(seqs, rmSeq{seq: 16, action: "deny", matches: []string{"match as-path PV_BOGON_AS"}})
	}
	// allow-list (prefix-set) and as-set are NEGATED matches in BIRD
	// (`!(net ~ set) -> reject`), which a route-map deny cannot express.
	// They are enforced as required matches on every terminal accept
	// instead: a route that fails either falls through to the implicit
	// deny at the end of the map.
	termMatches := func(extra []string) []string {
		var m []string
		if allowName != "" {
			m = append(m, addrMatch(fam, allowName))
		}
		if asSetName != "" {
			m = append(m, "match as-path "+asSetName)
		}
		return append(m, extra...)
	}

	if derefBool(p.FilterBlocklist) {
		blExists := false
		bl := "PV_BLOCKLIST_V4"
		if v6 {
			bl = "PV_BLOCKLIST_V6"
			_, b6 := splitFamilies(c.BlocklistPrefixes)
			blExists = len(b6) > 0
		} else {
			b4, _ := splitFamilies(c.BlocklistPrefixes)
			blExists = len(b4) > 0
		}
		if blExists {
			seqs = append(seqs, rmSeq{seq: 22, action: "deny", matches: []string{addrMatch(fam, bl)}})
		}
		if len(c.BlocklistASNs) > 0 {
			seqs = append(seqs, rmSeq{seq: 24, action: "deny", matches: []string{"match as-path PV_BLOCKLIST_AS"}})
		}
	}
	if derefBool(p.FilterTransitASNs) && len(c.TransitASNs) > 0 {
		seqs = append(seqs, rmSeq{seq: 26, action: "deny", matches: []string{"match as-path PV_TRANSIT_AS"}})
	}
	if derefBool(p.FilterNeverViaRouteServers) && c.QueryNVRS && len(c.NVRSASNs) > 0 {
		seqs = append(seqs, rmSeq{seq: 28, action: "deny", matches: []string{"match as-path PV_NVRS_AS"}})
	}
	if nhName != "" {
		seqs = append(seqs, rmSeq{seq: 30, action: "deny", matches: []string{nhMatch(fam, nhName)}})
	}

	// Unconditional base-sets sequence -> continue into the accept region.
	seqs = append(seqs, rmSeq{
		seq:    40,
		action: "permit",
		sets:   importSets(fam, p, c, delC, delL),
		cont:   42,
	})

	// process_blackholes: community-triggered blackhole accept (BIRD
	// set_blackhole accepts immediately, skipping RPKI and later checks).
	// The allow-list/as-set matches still apply — BIRD checks them before
	// process_blackholes.
	if derefBool(p.AllowBlackholeCommunity) {
		bhSets := append([]string{
			"set community " + strconv.Itoa(c.ASN) + ":666 65535:666 additive",
		}, blackholeSets(fam)...)
		seqs = append(seqs,
			rmSeq{seq: 42, action: "permit", matches: termMatches([]string{"match community PV_BHSTD"}), sets: bhSets},
			rmSeq{seq: 44, action: "permit", matches: termMatches([]string{"match large-community PV_BHLC"}), sets: bhSets},
		)
	}

	// RPKI (after blackhole processing, matching BIRD order).
	if c.RPKIEnable {
		if derefBool(p.FilterRPKI) || derefBool(p.StrictRPKI) {
			seqs = append(seqs, rmSeq{seq: 46, action: "deny", matches: []string{"match rpki invalid"}})
		}
		if derefBool(p.StrictRPKI) {
			seqs = append(seqs, rmSeq{seq: 48, action: "deny", matches: []string{"match rpki notfound"}})
		}
	}

	// Conditional local-preference seqs, lowest priority first.
	prefStart := 50
	gsSeq := prefStart + 2*len(prefs)
	finalSeq := gsSeq
	honorGS := derefBool(p.HonorGracefulShutdown)
	if honorGS {
		finalSeq = gsSeq + 2
	}
	for i, pf := range prefs {
		cont := prefStart + 2*(i+1)
		if i == len(prefs)-1 {
			cont = gsSeq
		}
		seqs = append(seqs, rmSeq{
			seq:     prefStart + 2*i,
			action:  "permit",
			matches: pf.matches,
			sets:    pf.sets,
			cont:    cont,
		})
	}

	// honor-graceful-shutdown: community 65535:0 -> local-preference 0,
	// after all other preference rules (BIRD order).
	if honorGS {
		seqs = append(seqs, rmSeq{
			seq:     gsSeq,
			action:  "permit",
			matches: []string{"match community PV_GS"},
			sets:    []string{"set local-preference 0"},
			cont:    finalSeq,
		})
	}

	// Final accept — gated on the allow-list/as-set when configured.
	final := rmSeq{seq: finalSeq, action: "permit"}
	if m := termMatches(nil); len(m) > 0 {
		final.matches = m
	}
	seqs = append(seqs, final)

	return seqs
}

// importSets builds the unconditional set clauses for the import
// route-map's base sequence (seq 40).
func importSets(fam string, p *config.Peer, c *config.Config, delC, delL string) []string {
	var sets []string

	// Base local preference (BIRD: set-local-pref / default-local-pref).
	if derefBool(p.SetLocalPref) || derefInt(p.DefaultLocalPref) != 0 {
		base := derefInt(p.LocalPref)
		if derefInt(p.DefaultLocalPref) != 0 {
			base = derefInt(p.DefaultLocalPref)
		}
		sets = append(sets, fmt.Sprintf("set local-preference %d", base))
	}

	// Community deletes then adds (BIRD order).
	sets = append(sets, deleteSetClauses(delC, delL)...)

	// Import communities: peer-level then global, standard then large.
	var stdVals, largeVals []string
	for _, comm := range derefStrSlice(p.ImportStandardCommunities) {
		stdVals = append(stdVals, communityColon(comm))
	}
	for _, comm := range c.ImportStandardCommunities {
		stdVals = append(stdVals, communityColon(comm))
	}
	for _, comm := range derefStrSlice(p.ImportLargeCommunities) {
		largeVals = append(largeVals, communityColon(comm))
	}
	for _, comm := range c.ImportLargeCommunities {
		largeVals = append(largeVals, communityColon(comm))
	}
	stdVals = unique(stdVals)
	largeVals = unique(largeVals)
	sort.Strings(stdVals)
	sort.Strings(largeVals)
	if len(stdVals) > 0 {
		sets = append(sets, "set community "+strings.Join(stdVals, " ")+" additive")
	}
	if len(largeVals) > 0 {
		sets = append(sets, "set large-community "+strings.Join(largeVals, " ")+" additive")
	}

	// Next-hop handling: import-next-hop wins over force-peer-nexthop
	// (BIRD applies force first, then import-next-hop).
	if nh := derefStr(p.ImportNextHop); nh != "" {
		sets = append(sets, setNextHop(fam, nh))
	} else if derefBool(p.ForcePeerNexthop) {
		sets = append(sets, setPeerNextHop(fam))
	}

	// blackhole-in: all accepted routes become blackhole.
	if derefBool(p.BlackholeIn) {
		sets = append(sets, blackholeSets(fam)...)
	}

	return sets
}

// ---------- EXPORT route-map ----------

// buildExportRM assembles the export route-map sequences.
//
//	10+  per-prefix community seqs (chained)
//	M    unconditional export ops -> continue M+2
//	M+2.. announce-originated/communities/default accepts
//	last announce-all accept or final deny
func buildExportRM(fam string, p *config.Peer, c *config.Config,
	pcFrags []matchSets, annC, annL string) []rmSeq {

	v6 := fam == "v6"

	// export:false / no-announce → deny everything.
	if c.NoAnnounce || !derefBool(p.Export) {
		return []rmSeq{{seq: 10, action: "deny"}}
	}

	var seqs []rmSeq
	seq := 10

	// Per-prefix community additions (continue chain).
	for _, pc := range pcFrags {
		seqs = append(seqs, rmSeq{
			seq:     seq,
			action:  "permit",
			matches: pc.matches,
			sets:    pc.sets,
			cont:    seq + 2,
		})
		seq += 2
	}

	// Main unconditional set sequence.
	mainSeq := seq
	seqs = append(seqs, rmSeq{
		seq:    mainSeq,
		action: "permit",
		sets:   exportSets(fam, p, c),
		cont:   mainSeq + 2,
	})
	seq = mainSeq + 2

	// blackhole-out: BIRD's set_blackhole() accepts immediately, before
	// only-announce/announce checks — every route reaching this point is
	// blackholed and accepted.
	if derefBool(p.BlackholeOut) {
		seqs = append(seqs, rmSeq{
			seq:    seq,
			action: "permit",
			sets:   blackholeSets(fam),
		})
		return seqs
	}

	// announce-originated: routes carrying origin communities are accepted;
	// our own prefixes get local communities then accept.
	if derefBool(p.AnnounceOriginated) {
		if len(c.OriginStandardCommunities) > 0 {
			seqs = append(seqs, rmSeq{seq: seq, action: "permit", matches: []string{"match community PV_ORIGIN_STD"}})
			seq += 2
		}
		if len(c.OriginLargeCommunities) > 0 {
			seqs = append(seqs, rmSeq{seq: seq, action: "permit", matches: []string{"match large-community PV_ORIGIN_L"}})
			seq += 2
		}
		localName := "PV_LOCAL_V4"
		if v6 {
			localName = "PV_LOCAL_V6"
		}
		var localSets []string
		var stdVals, largeVals []string
		for _, comm := range c.LocalStandardCommunities {
			stdVals = append(stdVals, communityColon(comm))
		}
		for _, comm := range c.LocalLargeCommunities {
			largeVals = append(largeVals, communityColon(comm))
		}
		stdVals = unique(stdVals)
		largeVals = unique(largeVals)
		sort.Strings(stdVals)
		sort.Strings(largeVals)
		if len(stdVals) > 0 {
			localSets = append(localSets, "set community "+strings.Join(stdVals, " ")+" additive")
		}
		if len(largeVals) > 0 {
			localSets = append(localSets, "set large-community "+strings.Join(largeVals, " ")+" additive")
		}
		seqs = append(seqs, rmSeq{
			seq:     seq,
			action:  "permit",
			matches: []string{addrMatch(fam, localName)},
			sets:    localSets,
		})
		seq += 2
	}

	// announce-communities.
	if annC != "" {
		seqs = append(seqs, rmSeq{seq: seq, action: "permit", matches: []string{"match community " + annC}})
		seq += 2
	}
	if annL != "" {
		seqs = append(seqs, rmSeq{seq: seq, action: "permit", matches: []string{"match large-community " + annL}})
		seq += 2
	}

	// announce-default: learned defaults match the default prefix-list;
	// originated defaults come from `neighbor default-originate`.
	if derefBool(p.AnnounceDefault) {
		defName := "PV_DEFAULT_V4"
		if v6 {
			defName = "PV_DEFAULT_V6"
		}
		seqs = append(seqs, rmSeq{seq: seq, action: "permit", matches: []string{addrMatch(fam, defName)}})
		seq += 2
	}

	// announce-all → final permit; otherwise explicit deny.
	if derefBool(p.AnnounceAll) {
		seqs = append(seqs, rmSeq{seq: seq, action: "permit"})
	} else {
		seqs = append(seqs, rmSeq{seq: seq, action: "deny"})
	}

	return seqs
}

// exportSets builds unconditional set clauses for the export route-map's
// main sequence.
func exportSets(fam string, p *config.Peer, c *config.Config) []string {
	var sets []string

	// Export communities: peer then global, standard then large.
	var stdVals, largeVals []string
	for _, comm := range derefStrSlice(p.ExportStandardCommunities) {
		stdVals = append(stdVals, communityColon(comm))
	}
	for _, comm := range c.ExportStandardCommunities {
		stdVals = append(stdVals, communityColon(comm))
	}
	for _, comm := range derefStrSlice(p.ExportLargeCommunities) {
		largeVals = append(largeVals, communityColon(comm))
	}
	for _, comm := range c.ExportLargeCommunities {
		largeVals = append(largeVals, communityColon(comm))
	}
	stdVals = unique(stdVals)
	largeVals = unique(largeVals)
	sort.Strings(stdVals)
	sort.Strings(largeVals)
	if len(stdVals) > 0 {
		sets = append(sets, "set community "+strings.Join(stdVals, " ")+" additive")
	}
	if len(largeVals) > 0 {
		sets = append(sets, "set large-community "+strings.Join(largeVals, " ")+" additive")
	}

	// AS path manipulation. BIRD clears the path first (clear-path), then
	// prepends the local ASN Prepends times, then each prepend-path entry.
	// FRR applies set clauses in order within a sequence, so emitting
	// exclude-all before prepend reproduces that.
	if derefBool(p.ClearPath) {
		sets = append(sets, "set as-path exclude all")
	}
	{
		// BIRD prepends each element to the front of the AS path, so the
		// last prepend-path entry ends up leftmost. FRR's `set as-path
		// prepend` announces its arguments left-to-right, so emit the
		// prepend-path entries reversed, followed by the local-AS prepends.
		var prependArgs []string
		pp := derefUint32Slice(p.PrependPath)
		for i := len(pp) - 1; i >= 0; i-- {
			prependArgs = append(prependArgs, strconv.FormatUint(uint64(pp[i]), 10))
		}
		if n := derefInt(p.Prepends); n > 0 {
			asn := strconv.Itoa(c.ASN)
			for i := 0; i < n; i++ {
				prependArgs = append(prependArgs, asn)
			}
		}
		if len(prependArgs) > 0 {
			sets = append(sets, "set as-path prepend "+strings.Join(prependArgs, " "))
		}
	}

	// export-next-hop.
	if nh := derefStr(p.ExportNextHop); nh != "" {
		sets = append(sets, setNextHop(fam, nh))
	}

	// blackhole-out is handled as a terminal accept in buildExportRM
	// (BIRD's set_blackhole accepts immediately).

	return sets
}

// ---------- header / assembly ----------

// RouterBgp renders the "router bgp" block: global commands, neighbor
// statements and address-family sections.
func RouterBgp(c *config.Config, neighbors, af4, af6 string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "router bgp %d\n", c.ASN)
	fmt.Fprintf(&b, " bgp router-id %s\n", c.RouterID)
	// Originated prefixes must not require a RIB entry (BIRD static reject
	// routes always "exist").
	b.WriteString(" no bgp network import-check\n")
	if neighbors != "" {
		b.WriteString(neighbors)
	}
	b.WriteString(" address-family ipv4 unicast\n")
	b.WriteString(networkStatements(originatedPrefixes(c.Prefixes4, c.DefaultRoute, "0.0.0.0/0")))
	if af4 != "" {
		b.WriteString(af4)
	}
	b.WriteString(" exit-address-family\n")
	b.WriteString(" address-family ipv6 unicast\n")
	b.WriteString(networkStatements(originatedPrefixes(c.Prefixes6, c.DefaultRoute, "::/0")))
	if af6 != "" {
		b.WriteString(af6)
	}
	b.WriteString(" exit-address-family\n")
	b.WriteString("!\n")
	return b.String()
}

// originatedPrefixes returns the prefixes BIRD would originate via its
// static reject-route protocols: the configured prefixes plus a default
// route when default-route is enabled. defaultPrefix is the family's
// default route ("0.0.0.0/0" or "::/0").
func originatedPrefixes(prefixes []string, defaultRoute bool, defaultPrefix string) []string {
	out := append([]string{}, prefixes...)
	if defaultRoute {
		out = append(out, defaultPrefix)
	}
	return out
}

// networkStatements renders `network` statements for originated prefixes.
func networkStatements(prefixes []string) string {
	if len(prefixes) == 0 {
		return ""
	}
	var b strings.Builder
	sort.Strings(prefixes)
	for _, p := range prefixes {
		fmt.Fprintf(&b, "  network %s\n", p)
	}
	return b.String()
}

// Statics renders kernel static routes and blackhole helper routes.
func Statics(c *config.Config, needBH bool) string {
	var b strings.Builder
	if c.Kernel != nil {
		var v4, v6 []string
		for p, nh := range c.Kernel.Statics4 {
			v4 = append(v4, fmt.Sprintf("ip route %s %s", p, nh))
		}
		for p, nh := range c.Kernel.Statics6 {
			v6 = append(v6, fmt.Sprintf("ipv6 route %s %s", p, nh))
		}
		sort.Strings(v4)
		sort.Strings(v6)
		for _, r := range append(v4, v6...) {
			b.WriteString(r + "\n")
		}
	}
	if needBH {
		b.WriteString("ip route 192.0.2.1/32 blackhole\n")
		b.WriteString("ipv6 route 100::1/128 blackhole\n")
	}
	return b.String()
}

// KernelExport renders the zebra route-maps controlling which BGP routes
// are installed in the kernel FIB. BIRD's kernel export filter accepts
// statics early, then (when srd-communities is set) only routes carrying
// those communities; originated prefixes (static reject routes in BIRD)
// never reach the kernel.
//
// FRR's `ip protocol bgp route-map` only sees BGP-learned routes — kernel
// statics, connected routes and other protocols are handled by zebra
// natively and cannot be filtered this way.
func KernelExport(c *config.Config) string {
	var b strings.Builder

	srdStd := c.Kernel.SRDStandardCommunities
	srdLarge := c.Kernel.SRDLargeCommunities
	srdSet := len(srdStd)+len(srdLarge) > 0

	// Emit the SRD community lists once, shared by both family route-maps.
	if srdSet {
		writeCommunityLists(&b, "PV_KRT_SRD_C", "PV_KRT_SRD_L", srdStd, srdLarge)
		b.WriteString("!\n")
	}

	for _, fam := range []string{"v4", "v6"} {
		var localName string
		if fam == "v4" {
			localName = "PV_LOCAL_V4"
		} else {
			localName = "PV_LOCAL_V6"
		}
		// Only emit a map for families that have originated prefixes or
		// need SRD/export filtering.
		needLocal := (fam == "v4" && len(c.Prefixes4) > 0) || (fam == "v6" && len(c.Prefixes6) > 0)
		if !needLocal && !srdSet && c.Kernel.Export {
			continue
		}

		rmName := "PV_KRT4"
		protoCmd := "ip protocol bgp route-map"
		if fam == "v6" {
			rmName = "PV_KRT6"
			protoCmd = "ipv6 protocol bgp route-map"
		}

		seq := 5
		if needLocal {
			fmt.Fprintf(&b, "route-map %s deny %d\n", rmName, seq)
			fmt.Fprintf(&b, " match %s address prefix-list %s\n", addrFamilyMatch(fam), localName)
			seq += 10
		}
		if !c.Kernel.Export {
			// export: false → reject everything BGP-originated.
			fmt.Fprintf(&b, "route-map %s deny %d\n", rmName, seq)
			seq += 10
		} else if srdSet {
			// srd-communities → permit routes carrying any of them.
			// Standard and large matches go in separate sequences because
			// multiple match clauses in one sequence are ANDed, while BIRD
			// accepts on either. The community lists are emitted once above.
			stdName := "PV_KRT_SRD_C"
			largeName := "PV_KRT_SRD_L"
			if len(srdStd) > 0 {
				fmt.Fprintf(&b, "route-map %s permit %d\n", rmName, seq)
				fmt.Fprintf(&b, " match community %s\n", stdName)
				seq += 10
			}
			if len(srdLarge) > 0 {
				fmt.Fprintf(&b, "route-map %s permit %d\n", rmName, seq)
				fmt.Fprintf(&b, " match large-community %s\n", largeName)
				seq += 10
			}
			fmt.Fprintf(&b, "route-map %s deny %d\n", rmName, seq)
			seq += 10
		} else {
			fmt.Fprintf(&b, "route-map %s permit %d\n", rmName, seq)
		}
		fmt.Fprintf(&b, "%s %s\n", protoCmd, rmName)
		b.WriteString("!\n")
	}
	return b.String()
}

// addrFamilyMatch returns "ip" or "ipv6" for match clauses.
func addrFamilyMatch(fam string) string {
	if fam == "v6" {
		return "ipv6"
	}
	return "ip"
}

// Config assembles the complete frr.conf. peerBlocks is keyed by peer
// name; peers are emitted in sorted order for deterministic output.
func Config(c *config.Config, peerBlocks map[string]PeerBlocks) string {
	var nb, a4, a6 strings.Builder
	names := make([]string, 0, len(peerBlocks))
	for n := range peerBlocks {
		names = append(names, n)
	}
	sort.Strings(names)
	var objs strings.Builder
	for _, n := range names {
		pb := peerBlocks[n]
		nb.WriteString(pb.Neighbor)
		a4.WriteString(pb.AF4)
		a6.WriteString(pb.AF6)
		objs.WriteString(pb.Objects)
	}

	var b strings.Builder
	b.WriteString("# Generated by Pathvector NG — do not edit\n")
	b.WriteString("# Backend: frr\n\n")
	// No `frr version` line: it records which FRR release wrote the config,
	// but Pathvector does not know the daemon's version at generate time and
	// vtysh validates against its own. `frr defaults traditional` selects the
	// integrated single-file config style on FRR 8+.
	b.WriteString("frr defaults traditional\n")
	b.WriteString("!\n")
	b.WriteString(GlobalObjects(c))
	// Peer policy objects (prefix-lists, community-lists, as-path access-lists)
	// and their route-maps must be defined BEFORE `router bgp`: neighbors
	// attach route-maps by name, and bgpd warns "route-map does not exist"
	// (and does not apply it) when the reference precedes the definition.
	// Within each peer's Objects block the matchable objects already precede
	// the route-maps that reference them, and route-maps never reference
	// statics or kernel-export objects, so this ordering is safe.
	if objs.Len() > 0 {
		b.WriteString("!\n")
		b.WriteString(objs.String())
	}
	b.WriteString("!\n")
	b.WriteString(RouterBgp(c, nb.String(), a4.String(), a6.String()))
	if s := Statics(c, needBlackhole(c)); s != "" {
		b.WriteString(s)
	}
	if s := KernelExport(c); s != "" {
		b.WriteString("!\n")
		b.WriteString(s)
	}
	b.WriteString("!\n")
	return b.String()
}

// needBlackhole reports whether any peer/global option requires the
// blackhole static helper routes.
func needBlackhole(c *config.Config) bool {
	if c.BlackholeBogonASNs {
		return true
	}
	for _, p := range c.Peers {
		if derefBool(p.BlackholeIn) || derefBool(p.BlackholeOut) || derefBool(p.AllowBlackholeCommunity) {
			return true
		}
	}
	return false
}

// ---------- control operations ----------

// Check validates a rendered frr.conf with `vtysh -C -f` (dry-run parse;
// does not require running daemons).
func Check(binary, configPath string) error {
	cmd := exec.Command(binary, "-C", "-f", configPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("vtysh -C -f: %s: %s", err, strings.TrimSpace(string(out)))
	}
	log.Infof("%s", strings.TrimSpace(string(out)))
	return nil
}

// Deploy copies rendered files into the FRR config directory. Applying the
// configuration to a running FRR is done with `vtysh -b` (integrated boot)
// when the daemons are up; a plain file copy is always safe.
func Deploy(c *config.Config, cacheDir string, noConfigure bool) error {
	if err := os.MkdirAll(c.FRRDirectory, os.FileMode(0755)); err != nil {
		return fmt.Errorf("create frr directory: %w", err)
	}
	files := []string{"frr.conf", "pathvector.yml"}
	for _, f := range files {
		src := filepath.Join(cacheDir, f)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := util.CopyFile(src, filepath.Join(c.FRRDirectory, f)); err != nil {
			return fmt.Errorf("deploy %s: %w", f, err)
		}
	}
	if noConfigure {
		return nil
	}
	// Best-effort live apply: vtysh -b re-reads the boot config and pushes
	// per-daemon configs to running daemons. Failure just means FRR picks
	// the file up on next restart.
	cmd := exec.Command(c.VtyshBinary, "-b")
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Warnf("vtysh -b failed (is FRR running?): %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Show runs a vtysh command and returns stdout (for status --json etc.).
func Show(c *config.Config, args ...string) (string, error) {
	cmd := exec.Command(c.VtyshBinary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("vtysh %s: %s: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// NeighborInfo is a single BGP neighbor parsed from `show bgp summary json`,
// across all address families that have neighbors.
type NeighborInfo struct {
	Name     string // configured peer name when the address is known, else the neighbor address
	Neighbor string // neighbor address
	ASN      int    // remote AS number
	State    string // session state, e.g. "Established"
	AFI      string // address-family name, e.g. "ipv4-unicast"
	Imported int    // prefixes received (pfxRcd)
	Exported int    // prefixes sent (pfxSnt)
	Uptime   string // session uptime string
}

// neighborJSON is the per-neighbor subset of `show bgp summary json` output.
type neighborJSON struct {
	RemoteAs int    `json:"remoteAs"`
	State    string `json:"state"`
	PfxRcd   int    `json:"pfxRcd"`
	PfxSnt   int    `json:"pfxSnt"`
	Uptime   string `json:"peerUptime"`
}

// Neighbors returns BGP neighbor state for all address families, mapping
// neighbor addresses back to configured pathvector peer names where possible.
func Neighbors(c *config.Config) ([]NeighborInfo, error) {
	nameByAddr := map[string]string{}
	for name, p := range c.Peers {
		for _, addr := range neighborAddrs(p) {
			nameByAddr[addr] = name
		}
	}

	out, err := Show(c, "-c", "show bgp summary json")
	if err != nil {
		return nil, err
	}
	return parseNeighbors(out, nameByAddr)
}

// parseNeighbors parses `show bgp summary json` output into NeighborInfo
// rows, one per neighbor per address family.
func parseNeighbors(out string, nameByAddr map[string]string) ([]NeighborInfo, error) {
	// `show bgp summary json` returns one object per AFI/SAFI that has
	// neighbors, e.g. {"ipv4-unicast": {"peers": {...}}, "ipv6-unicast": {...}}.
	var byAF map[string]struct {
		Peers map[string]neighborJSON `json:"peers"`
	}
	if err := json.Unmarshal([]byte(out), &byAF); err != nil {
		return nil, fmt.Errorf("parsing vtysh JSON: %w", err)
	}

	var neighbors []NeighborInfo
	for afi, sec := range byAF {
		for addr, pj := range sec.Peers {
			name := nameByAddr[addr]
			if name == "" {
				name = addr
			}
			neighbors = append(neighbors, NeighborInfo{
				Name:     name,
				Neighbor: addr,
				ASN:      pj.RemoteAs,
				State:    pj.State,
				AFI:      afi,
				Imported: pj.PfxRcd,
				Exported: pj.PfxSnt,
				Uptime:   pj.Uptime,
			})
		}
	}
	sort.Slice(neighbors, func(i, j int) bool { return neighbors[i].Name < neighbors[j].Name })
	return neighbors, nil
}
