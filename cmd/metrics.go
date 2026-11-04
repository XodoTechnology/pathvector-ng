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
		birdSocket, _ := birdPaths(c)
		if err := metrics.Serve(listen, birdSocket); err != nil {
			log.Fatal(err)
		}
	},
}
