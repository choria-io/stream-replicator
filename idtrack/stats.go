// Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package idtrack

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	trackedItems = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "tracker", "total_items"),
		Help: "Number of entries being tracked",
	}, []string{"stream", "replicator", "worker"})

	seenByGossip = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "tracker", "seen_by_gossip"),
		Help: "Number of entries that we learned about via gossip synchronization",
	}, []string{"stream", "replicator", "worker"})
)

func init() {
	prometheus.MustRegister(trackedItems)
	prometheus.MustRegister(seenByGossip)
}
