package cmd

import (
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/XodoTechnology/pathvector-ng/pkg/metrics"
)

var metricsListen string

func init() {
	metricsCmd.Flags().StringVarP(&metricsListen, "listen", "l", "", "listen address (default metrics-listen config or 127.0.0.1:9517)")
	rootCmd.AddCommand(metricsCmd)
}

var metricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "Serve BIRD protocol state and route counts in Prometheus format on /metrics",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadConfig()
		if err != nil {
			log.Fatal(err)
		}
		listen := metricsListen
		if listen == "" {
			listen = c.MetricsListen
		}
		if c.Backend == "cairn" {
			// cairnd serves Prometheus metrics natively on its [metrics]
			// listener — pathvector does not need to proxy them.
			log.Infof("backend is cairn: cairnd serves metrics natively on %s/metrics", listen)
			return
		}
		if isFRRBackend(c) {
			// FRR does not expose a pathvector-compatible metrics feed yet;
			// fail loudly rather than silently falling back to birdc.
			log.Fatal("metrics are not yet supported on the frr backend; use FRR's own monitoring or the bird backend")
		}
		birdSocket, _ := birdPaths(c)
		if err := metrics.Serve(listen, birdSocket); err != nil {
			log.Fatal(err)
		}
	},
}
