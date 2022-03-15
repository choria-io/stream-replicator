// Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package replicator

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	receivedMessageCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "total_messages"),
		Help: "The total number of messages processed including ones that would be ignored",
	}, []string{"stream", "replicator"})

	receivedMessageSize = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "total_bytes"),
		Help: "The size of messages processed including ones that would be ignored",
	}, []string{"stream", "replicator"})

	handlerErrorCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "handler_error_count"),
		Help: "The number of times the handler failed to process a message",
	}, []string{"stream", "replicator"})

	processTime = prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "processing_time_seconds"),
		Help: "How long it took to process messages",
	}, []string{"stream", "replicator"})

	lagCount = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "stream_lag_messages"),
		Help: "How many messages from the end of the stream the current processing point is",
	}, []string{"stream", "replicator"})

	skippedCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "skipped_messages"),
		Help: "How many messages were skipped due to limiter configuration",
	}, []string{"stream", "replicator"})

	metaParsingFailedCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "meta_parse_failed_count"),
		Help: "How many times a message metadata could not be parsed",
	}, []string{"stream", "replicator"})

	ackFailedCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "ack_failed_count"),
		Help: "How many times an ack or nack failed",
	}, []string{"stream", "replicator"})

	consumerRepairCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "consumer_recreated"),
		Help: "How many times the source consumer had to be recreated",
	}, []string{"stream", "replicator"})
)

func init() {
	prometheus.MustRegister(receivedMessageCount)
	prometheus.MustRegister(receivedMessageSize)
	prometheus.MustRegister(handlerErrorCount)
	prometheus.MustRegister(processTime)
	prometheus.MustRegister(lagCount)
	prometheus.MustRegister(skippedCount)
	prometheus.MustRegister(metaParsingFailedCount)
	prometheus.MustRegister(ackFailedCount)
	prometheus.MustRegister(consumerRepairCount)
}
