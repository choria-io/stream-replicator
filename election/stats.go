// Copyright (c) 2017-2021, R.I. Pienaar and the Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package election

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	prometheusNamespace = "choria_stream_replicator"

	campaignsCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(prometheusNamespace, "election", "campaigns"),
		Help: "The number of campaigns a specific candidate voted in",
	}, []string{"election", "identity", "state", "replicator"})

	leaderGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prometheus.BuildFQName(prometheusNamespace, "election", "leader"),
		Help: "Indicates if a specific instance is the current leader",
	}, []string{"election", "identity", "replicator"})

	campaignIntervalGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prometheus.BuildFQName(prometheusNamespace, "election", "interval_seconds"),
		Help: "The number of seconds between campaigns",
	}, []string{"election", "identity", "replicator"})
)

func init() {
	prometheus.MustRegister(campaignsCounter)
	prometheus.MustRegister(leaderGauge)
	prometheus.MustRegister(campaignIntervalGauge)
}
