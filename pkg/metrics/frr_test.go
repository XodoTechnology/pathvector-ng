package metrics

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/XodoTechnology/pathvector-ng/pkg/frr"
)

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	return rec.Body.String()
}

func TestFRRMetricsHandler(t *testing.T) {
	h := frrMetricsHandler(func() ([]frr.NeighborInfo, error) {
		return []frr.NeighborInfo{
			{Name: "Peer", Neighbor: "10.0.0.2", ASN: 65002, State: "Established", AFI: "ipv4-unicast", Imported: 3, Exported: 5},
			{Name: "Peer", Neighbor: "fd00::2", ASN: 65002, State: "Active", AFI: "ipv6-unicast", Imported: 0, Exported: 1},
		}, nil
	})

	body := scrape(t, h)

	for _, want := range []string{
		"pathvector_frr_up 1",
		`pathvector_protocol_up{name="Peer",proto="bgp",afi="ipv4-unicast"} 1`,
		`pathvector_protocol_up{name="Peer",proto="bgp",afi="ipv6-unicast"} 0`,
		`pathvector_protocol_routes{name="Peer",proto="bgp",afi="ipv4-unicast",direction="imported"} 3`,
		`pathvector_protocol_routes{name="Peer",proto="bgp",afi="ipv4-unicast",direction="exported"} 5`,
		`pathvector_bgp_session_info{name="Peer",neighbor="10.0.0.2",remote_as="65002"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q\n---\n%s", want, body)
		}
	}
}

func TestFRRMetricsHandlerDown(t *testing.T) {
	h := frrMetricsHandler(func() ([]frr.NeighborInfo, error) {
		return nil, errors.New("vtysh: connection refused")
	})

	body := scrape(t, h)
	if !strings.Contains(body, "pathvector_frr_up 0") {
		t.Errorf("expected pathvector_frr_up 0, got:\n%s", body)
	}
	if strings.Contains(body, "pathvector_protocol_up") {
		t.Errorf("expected no protocol metrics when FRR is down, got:\n%s", body)
	}
}
