// Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package memory

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	limiterMessagesWithoutTrackingFieldCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "limiter", "messages_without_limit_field_count"),
		Help: "The number of messages that did not have the data field or header used for limiting",
	}, []string{"limiter", "stream", "replicator"})
)

func init() {
	prometheus.MustRegister(limiterMessagesWithoutTrackingFieldCount)
}
