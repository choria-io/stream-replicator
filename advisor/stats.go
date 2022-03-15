// Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package advisor

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	advisoryCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "advisor", "total_messages"),
		Help: "The total number of advisories sent",
	}, []string{"advisory", "stream", "replicator"})

	advisoryPublishErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "advisor", "publish_errors"),
		Help: "The number of times publishing advisories failed",
	}, []string{"advisory", "stream", "replicator"})
)

func init() {
	prometheus.MustRegister(advisoryCount)
	prometheus.MustRegister(advisoryPublishErrors)
}
