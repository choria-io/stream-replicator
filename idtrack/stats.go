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
	}, []string{"stream", "replicator"})
)

func init() {
	prometheus.MustRegister(trackedItems)
}
