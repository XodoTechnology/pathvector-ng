// Package metrics implements a small Prometheus exporter that polls BIRD's
// control socket on each /metrics scrape. No external dependencies — the
// exposition format is written out by hand.
package metrics

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/XodoTechnology/pathvector-ng/pkg/bird"
)

// Handler returns an http.Handler that renders protocol state and route counts
// from the BIRD socket at socketPath in Prometheus text exposition format.
func Handler(socketPath string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")

		out, _, err := bird.RunCommand("show protocols all", socketPath)
		if err != nil {
			fmt.Fprintf(w, "# HELP pathvector_bird_up Whether the BIRD control socket is reachable.\n")
			fmt.Fprintf(w, "# TYPE pathvector_bird_up gauge\n")
			fmt.Fprintf(w, "pathvector_bird_up 0\n")
			log.Warnf("[metrics] BIRD socket query failed: %s", err)
			return
		}

		states, err := bird.ParseProtocols(out)
		if err != nil {
			http.Error(w, fmt.Sprintf("parsing protocols: %s", err), http.StatusInternalServerError)
			return
		}

		sort.Slice(states, func(i, j int) bool { return states[i].Name < states[j].Name })

		var b strings.Builder
		b.WriteString("# HELP pathvector_bird_up Whether the BIRD control socket is reachable.\n")
		b.WriteString("# TYPE pathvector_bird_up gauge\n")
		b.WriteString("pathvector_bird_up 1\n")

		b.WriteString("# HELP pathvector_protocol_up Whether the protocol is up (1) or down (0).\n")
		b.WriteString("# TYPE pathvector_protocol_up gauge\n")
		b.WriteString("# HELP pathvector_protocol_routes Number of routes in the protocol's table.\n")
		b.WriteString("# TYPE pathvector_protocol_routes gauge\n")

		for _, st := range states {
			up := 0
			if strings.EqualFold(st.State, "up") {
				up = 1
			}
			fmt.Fprintf(&b, "pathvector_protocol_up{name=%q,proto=%q,table=%q} %d\n", st.Name, st.Proto, st.Table, up)
			if st.Routes != nil {
				for _, rc := range [][2]interface{}{{"imported", st.Routes.Imported}, {"filtered", st.Routes.Filtered}, {"exported", st.Routes.Exported}, {"preferred", st.Routes.Preferred}} {
					if n, ok := rc[1].(int); ok && n >= 0 {
						fmt.Fprintf(&b, "pathvector_protocol_routes{name=%q,proto=%q,direction=%q} %d\n", st.Name, st.Proto, rc[0], n)
					}
				}
			}
			if st.BGP != nil {
				fmt.Fprintf(&b, "pathvector_bgp_session_info{name=%q,neighbor=%q,remote_as=%q} 1\n",
					st.Name, st.BGP.NeighborAddress, strconv.Itoa(st.BGP.NeighborAS))
			}
		}

		fmt.Fprint(w, b.String())
	})
}

// Serve starts the metrics HTTP listener. It blocks until the server stops.
func Serve(listen string, socketPath string) error {
	if listen == "" {
		listen = "127.0.0.1:9517"
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", Handler(socketPath))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/metrics", http.StatusFound)
	})
	log.Infof("[metrics] serving /metrics on %s (BIRD socket %s)", listen, socketPath)
	//nolint:gosec // read/write timeouts are not critical for a local metrics listener
	return http.ListenAndServe(listen, mux)
}
