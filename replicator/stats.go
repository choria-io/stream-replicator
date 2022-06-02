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
	}, []string{"stream", "replicator", "worker"})

	receivedMessageSize = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "total_bytes"),
		Help: "The size of messages processed including ones that would be ignored",
	}, []string{"stream", "replicator", "worker"})

	handlerErrorCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "handler_error_count"),
		Help: "The number of times the handler failed to process a message",
	}, []string{"stream", "replicator", "worker"})

	processTime = prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "processing_time_seconds"),
		Help: "How long it took to process messages",
	}, []string{"stream", "replicator", "worker"})

	lagMessageCount = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "stream_lag_messages"),
		Help: "How many messages from the end of the stream the current processing point is",
	}, []string{"stream", "replicator", "worker"})

	streamSequence = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "stream_sequence"),
		Help: "The stream sequence of the last message received from the consumer",
	}, []string{"stream", "replicator", "worker"})

	ageSkippedCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "too_old_messages"),
		Help: "How many messages were discarded for being too old",
	}, []string{"stream", "replicator", "worker"})

	copiedMessageCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "copied_messages"),
		Help: "How many messages were copied",
	}, []string{"stream", "replicator", "worker"})

	copiedMessageSize = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "copied_bytes"),
		Help: "The size of messages that were copied",
	}, []string{"stream", "replicator", "worker"})

	skippedMessageCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "skipped_messages"),
		Help: "How many messages were skipped due to limiter configuration",
	}, []string{"stream", "replicator", "worker"})

	skippedMessageSize = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "skipped_bytes"),
		Help: "The size of messages that were skipped due to limited configuration",
	}, []string{"stream", "replicator", "worker"})

	metaParsingFailedCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "meta_parse_failed_count"),
		Help: "How many times a message metadata could not be parsed",
	}, []string{"stream", "replicator", "worker"})

	ackFailedCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "ack_failed_count"),
		Help: "How many times an ack or nack failed",
	}, []string{"stream", "replicator", "worker"})

	consumerRepairCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("choria_stream_replicator", "replicator", "consumer_recreated"),
		Help: "How many times the source consumer had to be recreated",
	}, []string{"stream", "replicator", "worker"})
)

func init() {
	prometheus.MustRegister(receivedMessageCount)
	prometheus.MustRegister(receivedMessageSize)
	prometheus.MustRegister(handlerErrorCount)
	prometheus.MustRegister(processTime)
	prometheus.MustRegister(lagMessageCount)
	prometheus.MustRegister(copiedMessageCount)
	prometheus.MustRegister(copiedMessageSize)
	prometheus.MustRegister(skippedMessageCount)
	prometheus.MustRegister(skippedMessageSize)
	prometheus.MustRegister(metaParsingFailedCount)
	prometheus.MustRegister(ackFailedCount)
	prometheus.MustRegister(consumerRepairCount)
	prometheus.MustRegister(streamSequence)
	prometheus.MustRegister(ageSkippedCount)
}
