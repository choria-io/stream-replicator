// Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package replicator

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/choria-io/stream-replicator/backoff"
	"github.com/choria-io/stream-replicator/config"
	"github.com/nats-io/jsm.go"
	"github.com/nats-io/jsm.go/api"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

type sourceInitiatedCopier struct {
	mu      sync.Mutex
	health  *time.Ticker
	msgs    chan *nats.Msg
	s       *Stream
	sr      *config.Config
	source  *Target
	dest    *Target
	copied  int64
	skipped int64
	cname   string
	cfg     *config.Stream
	log     *logrus.Entry
}

func newSourceInitiatedCopier(s *Stream, log *logrus.Entry) *sourceInitiatedCopier {
	return &sourceInitiatedCopier{
		mu:     sync.Mutex{},
		health: time.NewTicker(s.hcInterval),
		msgs:   make(chan *nats.Msg, 10),
		s:      s,
		sr:     s.sr,
		source: s.source,
		dest:   s.dest,
		cname:  s.cname,
		cfg:    s.cfg,
		log: log.WithFields(logrus.Fields{
			"copier":   "source_initiated",
			"consumer": s.cname,
		}),
	}
}

func (c *sourceInitiatedCopier) copyMessages(ctx context.Context) error {
	c.log.Infof("Starting Source-initiated data copier for %s", c.cfg.Stream)

	var err error
	var nextSubj string

	c.source.mu.Lock()
	nextSubj, err = c.source.mgr.NextSubject(c.source.stream.Name(), c.cname)
	if err != nil {
		return err
	}

	nc := c.source.nc
	ib := nc.NewRespInbox()
	c.source.sub, err = nc.ChanQueueSubscribe(ib, _EMPTY_, c.msgs)
	c.source.mu.Unlock()
	if err != nil {
		return err
	}

	req := api.JSApiConsumerGetNextRequest{
		Expires: pollFrequency,
		Batch:   1,
	}

	pollRequest, err := json.Marshal(&req)
	if err != nil {
		return err
	}
	pollMsg := nats.NewMsg(nextSubj)
	pollMsg.Reply = ib
	pollMsg.Data = pollRequest

	if err != nil {
		return err
	}
	nextMsg := nats.NewMsg(nextSubj)
	nextMsg.Reply = ib
	nextMsg.Data = []byte(fmt.Sprintf("%s %s", string(api.AckNext), string(pollRequest)))

	polled := time.Time{}
	polls := time.NewTicker(pollFrequency)
	health := time.NewTicker(time.Millisecond)

	for {
		select {
		case <-polls.C:
			if c.s.isPaused() {
				c.log.Debugf("Not polling while paused")
				polls.Reset(pollFrequency)
				continue
			}

			if time.Since(polled) < pollFrequency {
				polls.Reset(pollFrequency)
				continue
			}

			if polled.IsZero() {
				c.log.Debugf("Performing poll for messages last poll: never")
			} else {
				c.log.Debugf("Performing poll for messages last poll: %s", time.Since(polled))
			}

			err = nc.PublishMsg(pollMsg)
			if err != nil {
				c.log.Errorf("Could not request next messages: %v", err)
				continue
			}

			polled = time.Now()
			polls.Reset(pollFrequency)

		case <-health.C:
			if c.s.isPaused() {
				c.log.Debugf("Not health checking while paused")
				health.Reset(c.s.hcInterval)
				continue
			}

			c.log.Debugf("Performing health checks")

			fixed, err := c.healthCheckSource()
			if err != nil {
				c.log.Errorf("Source health check failed: %v", err)
			}

			if fixed {
				c.log.Warnf("Source consumer %s recreated", c.cname)
				consumerRepairCount.WithLabelValues(c.source.stream.Name(), c.sr.ReplicatorName, c.cfg.Name).Inc()
				polled = time.Time{}
				polls.Reset(50 * time.Millisecond)
			}

			health.Reset(c.s.hcInterval)

		case msg := <-c.msgs:
			if len(msg.Data) == 0 && msg.Header != nil {
				status := msg.Header.Get("Status")
				if status == "404" || status == "408" || status == "409" {
					continue
				}
			}

			// we got a message - we know it's healthy, lets postpone health checks
			health.Reset(c.s.hcInterval)

			meta, err := c.handler(msg)
			if err != nil {
				next, nerr := c.nakMsg(msg, meta)
				if nerr != nil {
					c.log.Errorf("Could not NaK message %v", err)
				}

				if meta != nil {
					c.log.Errorf("Handling msg %d failed on try %d, backing off for %v: %v", meta.StreamSequence(), meta.Delivered(), next, err)
				} else {
					c.log.Errorf("Handling msg failed, backing off for %v: %v", next, err)
				}

				if !c.s.isPaused() {
					polls.Reset(next)
				}

				handlerErrorCount.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name).Inc()

				continue
			}

			if c.s.isPaused() {
				err = msg.AckSync()
			} else {
				res := nextMsg
				res.Subject = msg.Reply
				err = msg.RespondMsg(res)
			}
			if err != nil {
				ackFailedCount.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name).Inc()
				c.log.Errorf("ACK failed: %v", err)
				continue
			}

			if meta != nil {
				c.source.mu.Lock()
				c.source.resumeSeq = meta.StreamSequence()
				c.source.mu.Unlock()
			}

			polls.Reset(pollFrequency)

		case <-ctx.Done():
			health.Stop()
			polls.Stop()

			c.log.Warnf("Copier shutting down after context interrupt")
			return nil
		}
	}
}

func (c *sourceInitiatedCopier) healthCheckSource() (fixed bool, err error) {
	c.source.mu.Lock()
	defer c.source.mu.Unlock()

	opts := []jsm.ConsumerOption{
		jsm.DurableName(c.cname),
		jsm.ConsumerDescription(fmt.Sprintf("Choria Stream Replicator %s", c.cfg.Name)),
		jsm.AcknowledgeExplicit(),
		jsm.MaxAckPending(1),
		jsm.AckWait(30 * time.Second),
	}

	if c.cfg.Ephemeral {
		// we fake an ephemeral consumer using a durable so that the name is static, simplifying the code significantly
		opts = append(opts,
			jsm.ConsumerOverrideReplicas(1),
			jsm.ConsumerOverrideMemoryStorage(),
			jsm.InactiveThreshold(5*pollFrequency))
	}

	if c.cfg.FilterSubject != _EMPTY_ {
		opts = append(opts, jsm.FilterStreamBySubject(c.cfg.FilterSubject))
	}

	if c.source.resumeSeq > 0 {
		opts = append(opts, jsm.StartAtSequence(c.source.resumeSeq))
	} else {
		switch {
		case c.cfg.StartAtEnd:
			opts = append(opts, jsm.StartWithNextReceived())
		case c.cfg.StartSequence > 0:
			opts = append(opts, jsm.StartAtSequence(c.cfg.StartSequence))
		case c.cfg.StartDelta > 0:
			opts = append(opts, jsm.StartAtTime(time.Now().UTC().Add(-1*c.cfg.StartDelta)))
		case !c.cfg.StartTime.IsZero():
			opts = append(opts, jsm.StartAtTime(c.cfg.StartTime.UTC()))
		default:
			opts = append(opts, jsm.DeliverAllAvailable())
		}
	}

	stream := c.source.stream
	if stream == nil {
		return false, fmt.Errorf("stream %s does not exist, cannot recover consumer", c.cfg.Stream)
	}

	c.source.consumer, err = stream.LoadConsumer(c.cname)
	if jsm.IsNatsError(err, 10014) {
		c.log.Errorf("Consumer %s was not found, attempting to recreate", c.cname)
		c.source.consumer, err = stream.NewConsumerFromDefault(jsm.DefaultConsumer, opts...)
		fixed = err == nil
	}

	return fixed, err
}

func (c *sourceInitiatedCopier) handler(msg *nats.Msg) (*jsm.MsgInfo, error) {
	receivedMessageCount.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name).Inc()
	receivedMessageSize.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name).Add(float64(len(msg.Data)))
	obs := prometheus.NewTimer(processTime.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name))
	defer obs.ObserveDuration()

	if msg.Header == nil {
		msg.Header = nats.Header{}
	}

	meta, err := jsm.ParseJSMsgMetadata(msg)
	if err == nil {
		streamSequence.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name).Set(float64(meta.StreamSequence()))

		if c.cfg.MaxAgeDuration > 0 && time.Since(meta.TimeStamp()) > c.cfg.MaxAgeDuration {
			ageSkippedCount.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name).Inc()
			return meta, nil
		}

		msg.Header.Add(srcHeader, fmt.Sprintf(srcHeaderPattern, c.cfg.Stream, meta.StreamSequence(), c.sr.ReplicatorName, c.cfg.Name, meta.TimeStamp().UnixMilli()))
	} else {
		c.log.Warnf("Could not parse message metadata from %v: %v", msg.Reply, err)
		metaParsingFailedCount.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name).Inc()
		msg.Header.Add(srcHeader, fmt.Sprintf(srcHeaderPattern, c.cfg.Stream, -1, c.sr.ReplicatorName, c.cfg.Name, -1))
	}

	return meta, c.s.limitedProcess(msg, func(msg *nats.Msg, process bool) error {
		if meta != nil && meta.StreamSequence()%1000 == 0 {
			copied := atomic.LoadInt64(&c.copied)
			skipped := atomic.LoadInt64(&c.skipped)
			c.log.Infof("Handling message %d, %d message(s) behind, copied %d skipped %d", meta.StreamSequence(), meta.Pending(), copied, skipped)
		}

		if !process {
			atomic.AddInt64(&c.skipped, 1)
			skippedMessageCount.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name).Inc()
			skippedMessageSize.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name).Add(float64(len(msg.Data)))
			return nil
		}

		msg.Subject = c.s.targetForSubject(msg.Subject)

		resp, err := c.dest.nc.RequestMsg(msg, 2*time.Second)
		if err != nil {
			return err
		}

		err = jsm.ParseErrorResponse(resp)
		if err != nil {
			return err
		}

		atomic.AddInt64(&c.copied, 1)
		if meta != nil {
			c.log.Debugf("Copied message seq %d, %d message(s) behind", meta.StreamSequence(), meta.Pending())
		}

		copiedMessageCount.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name).Inc()
		copiedMessageSize.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name).Add(float64(len(msg.Data)))

		return nil
	})
}

func (c *sourceInitiatedCopier) nakMsg(msg *nats.Msg, meta *jsm.MsgInfo) (time.Duration, error) {
	r := nats.NewMsg(msg.Reply)
	next := backoff.TwentySec.Duration(20)
	if meta != nil {
		next = backoff.TwentySec.Duration(meta.Delivered())
	}
	r.Data = []byte(fmt.Sprintf(`%s {"delay": %d}`, api.AckNak, next))

	err := msg.RespondMsg(r)
	if err != nil {
		ackFailedCount.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name).Inc()
		return next, err
	}

	return next, nil
}
