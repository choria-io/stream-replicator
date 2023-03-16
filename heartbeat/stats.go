// Copyright (c) 2022-2023, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package heartbeat

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// gauge
	hbSubjects = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "heartbeat", "subjects_count"),
		Help: "Number of subjects that heartbeats are being published for",
	}, []string{"replicator", "subject"})
	hbPublishedCtr = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "heartbeat", "published_count"),
		Help: "Number of published messages",
	}, []string{"replicator", "subject"})
	hbPublishedCtrErr = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "heartbeat", "published_error_count"),
		Help: "Number of published message errors",
	}, []string{"replicator", "subject"})
	hbPublishTime = prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "heartbeat", "publish_time"),
		Help: "Time taken to publish a message",
	}, []string{"replicator", "subject"})
	hbPaused = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "heartbeat", "paused"),
		Help: "Paused under leader election",
	}, []string{"replicator"})
)

func init() {
	prometheus.MustRegister(hbSubjects)
	prometheus.MustRegister(hbPublishedCtr)
	prometheus.MustRegister(hbPublishedCtrErr)
	prometheus.MustRegister(hbPublishTime)
	prometheus.MustRegister(hbPaused)
}
