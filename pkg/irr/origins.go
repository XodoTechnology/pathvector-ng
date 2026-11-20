package irr

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// whoisQuery sends a raw whois query to server and returns the response.
// server may be "host" or "host:port" (port 43 is assumed).
func whoisQuery(server, query string, queryTimeout uint, bgpqArgs string) (string, error) {
	if _, _, err := net.SplitHostPort(server); err != nil {
		server = net.JoinHostPort(server, "43")
	}
	timeout := time.Duration(queryTimeout) * time.Second //nolint:gosec // configured timeout in seconds, far below the int64 range
	conn, err := net.DialTimeout("tcp", server, timeout)
	if err != nil {
		return "", fmt.Errorf("connecting to whois server %s: %w", server, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return "", err
	}

	if sources := sourcesFlagValue(strings.Fields(bgpqArgs)); sources != "" {
		query = "-s " + sources + " " + query
	}
	log.Debugf("Querying whois server %s for %s", server, query)
	if _, err := fmt.Fprintf(conn, "%s\r\n", query); err != nil {
		return "", fmt.Errorf("whois query to %s: %w", server, err)
	}
	response, err := io.ReadAll(bufio.NewReader(conn))
	if err != nil {
		return "", fmt.Errorf("reading whois response from %s: %w", server, err)
	}
	return string(response), nil
}

// originPrefixes parses an IRRd `!g`/`!6` response: an optional `A<size>`
// header, space-separated prefixes, and a trailing `C`/`D` marker. Only
// tokens that look like prefixes are kept, so markers and noise are dropped.
func originPrefixes(response string) []string {
	var out []string
	for _, tok := range strings.Fields(response) {
		if strings.Contains(tok, "/") {
			out = append(out, tok)
		}
	}
	return out
}

// OriginPrefixes queries the IRR for the prefixes each origin ASN originates
// (`!g<asn>` for IPv4, `!6AS<asn>` for IPv6 on IRRd-compatible whois servers)
// and returns a map of prefix -> set of origin ASNs. IRR servers are tried in
// order; the first server answering all queries is used so results reflect a
// single IRR database. An error is returned only if every server fails.
func OriginPrefixes(servers []string, asns []uint32, queryTimeout uint, bgpqArgs string) (map[string]map[uint32]struct{}, error) {
	if len(asns) == 0 {
		return nil, nil
	}

	var lastErr error
	for _, srv := range servers {
		result := make(map[string]map[uint32]struct{})
		failed := false
		for _, asn := range asns {
			for _, q := range []struct{ query string }{
				{fmt.Sprintf("!g%d", asn)},
				{fmt.Sprintf("!6AS%d", asn)},
			} {
				resp, err := whoisQuery(srv, q.query, queryTimeout, bgpqArgs)
				if err != nil {
					log.Debugf("origin query %s on %s failed: %s", q.query, srv, err)
					failed = true
					lastErr = err
					break
				}
				for _, pfx := range originPrefixes(resp) {
					set := result[pfx]
					if set == nil {
						set = make(map[uint32]struct{})
						result[pfx] = set
					}
					set[asn] = struct{}{}
				}
			}
			if failed {
				break
			}
		}
		if !failed {
			return result, nil
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no IRR servers configured")
	}
	return nil, fmt.Errorf("all IRR servers failed: %w", lastErr)
}
