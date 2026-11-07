package metrics

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/XodoTechnology/pathvector-ng/pkg/config"
	"github.com/XodoTechnology/pathvector-ng/pkg/frr"
)

// FRRHandler returns an http.Handler that renders BGP neighbor state from FRR
// (queried through vtysh on each scrape) in Prometheus text exposition format.
// It emits the same pathvector_protocol_* and pathvector_bgp_session_info
// series as the BIRD exporter, plus pathvector_frr_up as the daemon health
// gauge (the BIRD equivalent is pathvector_bird_up).
func FRRHandler(c *config.Config) http.Handler {
	return frrMetricsHandler(func() ([]frr.NeighborInfo, error) {
		return frr.Neighbors(c)
	})
}

// frrMetricsHandler renders the Prometheus exposition for a slice of FRR
// neighbors fetched by fetch. fetch is called on each scrape.
func frrMetricsHandler(fetch func() ([]frr.NeighborInfo, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")

		neighbors, err := fetch()
		if err != nil {
			fmt.Fprintf(w, "# HELP pathvector_frr_up Whether vtysh can reach a running FRR.\n")
			fmt.Fprintf(w, "# TYPE pathvector_frr_up gauge\n")
			fmt.Fprintf(w, "pathvector_frr_up 0\n")
			log.Warnf("[metrics] FRR query failed: %s", err)
			return
		}

		sort.Slice(neighbors, func(i, j int) bool {
			if neighbors[i].Name != neighbors[j].Name {
				return neighbors[i].Name < neighbors[j].Name
			}
			return neighbors[i].AFI < neighbors[j].AFI
		})

		var b strings.Builder
		b.WriteString("# HELP pathvector_frr_up Whether vtysh can reach a running FRR.\n")
		b.WriteString("# TYPE pathvector_frr_up gauge\n")
		b.WriteString("pathvector_frr_up 1\n")

		b.WriteString("# HELP pathvector_protocol_up Whether the BGP session is Established (1) or not (0).\n")
		b.WriteString("# TYPE pathvector_protocol_up gauge\n")
		b.WriteString("# HELP pathvector_protocol_routes Number of prefixes received/sent on the session.\n")
		b.WriteString("# TYPE pathvector_protocol_routes gauge\n")

		for _, n := range neighbors {
			up := 0
			if strings.EqualFold(n.State, "Established") {
				up = 1
			}
			// The afi label keeps dual-stack peers unique: unlike BIRD, where
			// each address family is a distinct protocol name, FRR reports one
			// neighbor row per AF under the same peer name. (BIRD's `table`
			// label is its routing-table name, a different concept, so afi is
			// used here rather than reusing `table`.)
			fmt.Fprintf(&b, "pathvector_protocol_up{name=%q,proto=%q,afi=%q} %d\n", n.Name, "bgp", n.AFI, up)
			fmt.Fprintf(&b, "pathvector_protocol_routes{name=%q,proto=%q,afi=%q,direction=%q} %d\n", n.Name, "bgp", n.AFI, "imported", n.Imported)
			fmt.Fprintf(&b, "pathvector_protocol_routes{name=%q,proto=%q,afi=%q,direction=%q} %d\n", n.Name, "bgp", n.AFI, "exported", n.Exported)
			fmt.Fprintf(&b, "pathvector_bgp_session_info{name=%q,neighbor=%q,remote_as=%q} 1\n",
				n.Name, n.Neighbor, strconv.Itoa(n.ASN))
		}

		fmt.Fprint(w, b.String())
	})
}
