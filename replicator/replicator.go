// Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package replicator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/choria-io/stream-replicator/advisor"
	"github.com/choria-io/stream-replicator/backoff"
	"github.com/choria-io/stream-replicator/config"
	"github.com/choria-io/stream-replicator/idtrack"
	"github.com/choria-io/stream-replicator/internal/util"
	"github.com/choria-io/stream-replicator/limiter/memory"
	"github.com/nats-io/jsm.go"
	"github.com/nats-io/jsm.go/api"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

type Limiter interface {
	ProcessAndRecord(msg *nats.Msg, f func(msg *nats.Msg, process bool) error) error
	Tracker() *idtrack.Tracker
}

type Stream struct {
	sr         *config.Config
	cfg        *config.Stream
	cname      string
	log        *logrus.Entry
	source     *Target
	dest       *Target
	limiter    Limiter
	copied     int64
	skipped    int64
	hcInterval time.Duration
	mu         *sync.Mutex
}

type Target struct {
	mgr       *jsm.Manager
	stream    *jsm.Stream
	consumer  *jsm.Consumer
	cfg       api.StreamConfig
	nc        *nats.Conn
	mu        *sync.Mutex
	sub       *nats.Subscription
	resumeSeq uint64
}

const (
	pollFrequency = 10 * time.Second
	srcHeader     = "Choria-SR-Source"
	_EMPTY_       = ""
)

func (t *Target) Close() error {
	if t.nc != nil {
		err := t.nc.Drain()
		if err != nil {
			return err
		}

		t.nc.Close()
	}

	return nil
}

func NewStream(stream *config.Stream, sr *config.Config, log *logrus.Entry) (*Stream, error) {
	if stream.Stream == _EMPTY_ {
		return nil, fmt.Errorf("stream name is required")
	}
	if stream.SourceURL == _EMPTY_ {
		return nil, fmt.Errorf("source_url is required")
	}
	if stream.TargetURL == _EMPTY_ {
		return nil, fmt.Errorf("target_url is required")
	}
	if stream.TargetStream == _EMPTY_ {
		stream.TargetStream = stream.Stream
	}

	name := "stream_replicator"
	if stream.Name != _EMPTY_ {
		if strings.HasPrefix("SR_", stream.Name) {
			name = stream.Name
		} else {
			name = fmt.Sprintf("SR_%s", stream.Name)
		}
	}

	return &Stream{
		sr:         sr,
		cfg:        stream,
		cname:      name,
		mu:         &sync.Mutex{},
		hcInterval: time.Minute,
		log: log.WithFields(logrus.Fields{
			"source":   stream.Stream,
			"target":   stream.TargetStream,
			"consumer": name}),
	}, nil
}

func (s *Stream) Run(ctx context.Context, wg *sync.WaitGroup) error {
	defer wg.Done()

	var err error

	if s.cfg.InspectJSONField != _EMPTY_ && s.cfg.InspectDuration > 0 {
		s.limiter, err = memory.New(ctx, wg, s.cfg.InspectJSONField, s.cfg.InspectDuration, s.cfg.WarnDuration, s.cfg.PayloadSizeTrigger, s.cname, s.cfg.StateFile, s.cfg.Stream, s.sr.ReplicatorName, s.log)
		if err != nil {
			return err
		}

		if s.cfg.AdvisoryConf != nil {
			nc, err := s.connectAdvisories(ctx)
			if err != nil {
				return err
			}

			_, err = advisor.New(ctx, wg, s.cfg.AdvisoryConf, nc, s.limiter.Tracker(), s.cfg.InspectJSONField, s.cfg.Stream, s.sr.ReplicatorName, s.log)
			if err != nil {
				return err
			}
		}
	}

	err = s.connect(ctx)
	if err != nil {
		s.log.Errorf("Could not setup connection: %v", err)
		return err
	}

	err = s.copier(ctx)
	if err != nil {
		s.log.Errorf("Copier failed: %v", err)
		return err
	}

	<-ctx.Done()
	s.log.Infof("exiting on context interrupt")

	s.mu.Lock()
	defer s.mu.Unlock()

	s.source.Close()
	s.dest.Close()

	return nil
}

func (s *Stream) limitedProcess(msg *nats.Msg, cb func(msg *nats.Msg, process bool) error) error {
	if s.limiter == nil {
		return cb(msg, true)
	}

	return s.limiter.ProcessAndRecord(msg, cb)
}

func (s *Stream) handler(msg *nats.Msg) (*jsm.MsgInfo, error) {
	receivedMessageCount.WithLabelValues(s.cfg.Stream, s.sr.ReplicatorName).Inc()
	receivedMessageSize.WithLabelValues(s.cfg.Stream, s.sr.ReplicatorName).Add(float64(len(msg.Data)))
	obs := prometheus.NewTimer(processTime.WithLabelValues(s.cfg.Stream, s.sr.ReplicatorName))
	defer obs.ObserveDuration()

	if msg.Header == nil {
		msg.Header = nats.Header{}
	}

	meta, err := jsm.ParseJSMsgMetadata(msg)
	if err == nil {
		lagCount.WithLabelValues(s.cfg.Stream, s.sr.ReplicatorName).Set(float64(meta.Pending()))

		msg.Header.Add(srcHeader, fmt.Sprintf("%s %d %s", s.cfg.Stream, meta.StreamSequence(), s.cfg.Name))
	} else {
		s.log.Warnf("Could not parse message metadata from %v: %v", msg.Reply, err)
		metaParsingFailedCount.WithLabelValues(s.cfg.Stream, s.sr.ReplicatorName).Inc()
		msg.Header.Add(srcHeader, fmt.Sprintf("%s -1 %s", s.cfg.Stream, s.cfg.Name))
	}

	return meta, s.limitedProcess(msg, func(msg *nats.Msg, process bool) error {
		if meta != nil && meta.StreamSequence()%1000 == 0 {
			copied := atomic.LoadInt64(&s.copied)
			skipped := atomic.LoadInt64(&s.skipped)
			s.log.Infof("Handling message %d, %d message(s) behind, copied %d skipped %d", meta.StreamSequence(), meta.Pending(), copied, skipped)
		}

		if !process {
			atomic.AddInt64(&s.skipped, 1)
			skippedMessageCount.WithLabelValues(s.cfg.Stream, s.sr.ReplicatorName).Inc()
			skippedMessageSize.WithLabelValues(s.cfg.Stream, s.sr.ReplicatorName).Add(float64(len(msg.Data)))
			return nil
		}

		target := msg.Subject
		if s.cfg.TargetPrefix != _EMPTY_ {
			target = fmt.Sprintf("%s.%s", s.cfg.TargetPrefix, target)
			msg.Subject = target
		}

		resp, err := s.dest.nc.RequestMsg(msg, 2*time.Second)
		if err != nil {
			return err
		}

		err = jsm.ParseErrorResponse(resp)
		if err != nil {
			return err
		}

		atomic.AddInt64(&s.copied, 1)
		if meta != nil {
			s.log.Debugf("Copied message seq %d, %d message(s) behind", meta.StreamSequence(), meta.Pending())
		}

		return nil
	})
}

func (s *Stream) nakMsg(msg *nats.Msg, meta *jsm.MsgInfo) (time.Duration, error) {
	r := nats.NewMsg(msg.Reply)
	next := backoff.TwentySec.Duration(20)
	if meta != nil {
		next = backoff.TwentySec.Duration(meta.Delivered())
	}
	r.Data = []byte(fmt.Sprintf(`%s {"delay": %d}`, api.AckNak, next))

	err := msg.RespondMsg(r)
	if err != nil {
		ackFailedCount.WithLabelValues(s.cfg.Stream, s.sr.ReplicatorName).Inc()
		return next, err
	}

	return next, nil
}

func (s *Stream) copier(ctx context.Context) (err error) {
	msgs := make(chan *nats.Msg, 11)

	s.source.mu.Lock()
	nextSubj := s.source.consumer.NextSubject()
	nc := s.source.nc
	ib := nc.NewRespInbox()
	s.source.sub, err = nc.ChanQueueSubscribe(ib, _EMPTY_, msgs)
	s.source.mu.Unlock()
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
	polls := time.NewTicker(time.Millisecond)
	health := time.NewTicker(s.hcInterval)

	for {
		select {
		case <-polls.C:
			if time.Since(polled) < pollFrequency {
				polls.Reset(pollFrequency)
				continue
			}

			s.log.Infof("Performing poll for messages last poll %v", time.Since(polled))
			err = nc.PublishMsg(pollMsg)
			if err != nil {
				s.log.Errorf("Could not request next messages: %v", err)
				continue
			}

			polled = time.Now()
			polls.Reset(pollFrequency)

		case <-health.C:
			s.log.Debugf("Performing health checks")

			fixed, err := s.healthCheckSource()
			if err != nil {
				s.log.Errorf("Source health check failed: %v", err)
			}

			if fixed {
				s.log.Warnf("Source consumer %s recreated", s.cname)
				consumerRepairCount.WithLabelValues(s.source.stream.Name(), s.sr.ReplicatorName).Inc()
				polled = time.Time{}
				polls.Reset(time.Microsecond)
			}

		case msg := <-msgs:
			if len(msg.Data) == 0 && msg.Header != nil {
				status := msg.Header.Get("Status")
				if status == "404" || status == "408" || status == "409" {
					// poll expired basically so poll again
					// could also be a NxT that expired though so the poll has
					// a protection against polling too often so this is safe
					polls.Reset(time.Microsecond)
					continue
				}
			}

			meta, err := s.handler(msg)
			if err != nil {
				next, nerr := s.nakMsg(msg, meta)
				if nerr != nil {
					s.log.Errorf("Could not NaK message %v", err)
				}

				if meta != nil {
					s.log.Errorf("Handling msg %d failed on try %d, backing off for %v: %v", meta.StreamSequence(), meta.Delivered(), next, err)
				} else {
					s.log.Errorf("Handling msg failed, backing off for %v: %v", next, err)
				}

				polls.Reset(next)

				handlerErrorCount.WithLabelValues(s.cfg.Stream, s.sr.ReplicatorName).Inc()

				continue
			}

			res := nextMsg
			res.Subject = msg.Reply
			err = msg.RespondMsg(res)
			if err != nil {
				ackFailedCount.WithLabelValues(s.cfg.Stream, s.sr.ReplicatorName).Inc()
				s.log.Errorf("ACK failed: %v", err)
				continue
			}

			if meta != nil {
				s.source.mu.Lock()
				s.source.resumeSeq = meta.StreamSequence()
				s.source.mu.Unlock()
			}

			polls.Reset(pollFrequency)

		case <-ctx.Done():
			s.source.nc.Close()
			s.dest.nc.Close()

			s.log.Warnf("Copier shutting down after context interrupt")
			return
		}
	}
}

func (s *Stream) connect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.source != nil || s.dest != nil {
		return fmt.Errorf("already have connections")
	}

	err := s.connectSource(ctx)
	if err != nil {
		return err
	}

	err = s.connectDestination(ctx)
	if err != nil {
		return err
	}

	if s.source == nil || s.dest == nil {
		return fmt.Errorf("connection setup failed")
	}

	return nil
}

func (s *Stream) healthCheckSource() (fixed bool, err error) {
	s.source.mu.Lock()
	defer s.source.mu.Unlock()

	opts := []jsm.ConsumerOption{
		jsm.DurableName(s.cname),
		jsm.ConsumerDescription(fmt.Sprintf("Choria Stream Replicator %s", s.cfg.Name)),
		jsm.AcknowledgeExplicit(),
		jsm.MaxAckPending(1),
		jsm.AckWait(30 * time.Second),
	}

	if s.source.resumeSeq > 0 {
		opts = append(opts, jsm.StartAtSequence(s.source.resumeSeq))
	} else {
		switch {
		case s.cfg.StartAtEnd:
			opts = append(opts, jsm.StartWithNextReceived())
		case s.cfg.StartSequence > 0:
			opts = append(opts, jsm.StartAtSequence(s.cfg.StartSequence))
		case s.cfg.StartDelta > 0:
			opts = append(opts, jsm.StartAtTime(time.Now().UTC().Add(-1*s.cfg.StartDelta)))
		case !s.cfg.StartTime.IsZero():
			opts = append(opts, jsm.StartAtTime(s.cfg.StartTime.UTC()))
		default:
			opts = append(opts, jsm.DeliverAllAvailable())
		}
	}

	stream := s.source.stream
	s.source.consumer, err = stream.LoadConsumer(s.cname)
	if jsm.IsNatsError(err, 10014) {
		s.log.Errorf("Consumer %s was not found, attempting to recreate", s.cname)
		s.source.consumer, err = stream.NewConsumerFromDefault(jsm.DefaultConsumer, opts...)
		fixed = err == nil
	}

	return fixed, err
}

func (s *Stream) connectAdvisories(ctx context.Context) (nc *nats.Conn, err error) {
	tls := s.cfg.SourceTLS

	return util.ConnectNats(ctx, "stream-replicator-advisories", s.cfg.SourceURL, tls, false, s.log.WithField("connection", "advisories"))
}

func (s *Stream) connectSource(ctx context.Context) (err error) {
	tls := s.cfg.SourceTLS

	log := s.log.WithField("connection", "source")

	s.source, err = s.setupConnection(ctx, s.cfg.SourceURL, tls, log)
	if err != nil {
		return fmt.Errorf("source connection failed: %v", err)
	}

	err = backoff.TwentySec.For(ctx, func(try int) error {
		s.source.stream, err = s.source.mgr.LoadStream(s.cfg.Stream)
		if err != nil {
			log.Infof("Loading stream failed on try %d: %v", try, err)
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	s.source.cfg = s.source.stream.Configuration()

	_, err = s.healthCheckSource()
	return err
}

func (s *Stream) connectDestination(ctx context.Context) (err error) {
	tls := s.cfg.TargetTLS

	s.source.mu.Lock()
	scfg := s.source.cfg
	s.source.mu.Unlock()

	if s.cfg.TargetPrefix != _EMPTY_ {
		var subjects []string
		for _, sub := range scfg.Subjects {
			subjects = append(subjects, fmt.Sprintf("%s.%s", s.cfg.TargetPrefix, sub))
		}
		scfg.Subjects = subjects
	}

	log := s.log.WithField("connection", "target")

	s.dest, err = s.setupConnection(ctx, s.cfg.TargetURL, tls, log)
	if err != nil {
		return fmt.Errorf("source connection failed: %v", err)
	}

	return backoff.TwentySec.For(ctx, func(try int) error {
		s.dest.stream, err = s.source.mgr.LoadOrNewStreamFromDefault(s.cfg.TargetStream, scfg)
		if err != nil {
			log.Infof("Loading stream failed on try %d: %v", try, err)
			return err
		}

		return nil
	})
}

func (s *Stream) setupConnection(ctx context.Context, url string, tls *config.TLS, log *logrus.Entry) (*Target, error) {
	t := &Target{mu: &sync.Mutex{}}
	var err error

	t.nc, err = util.ConnectNats(ctx, s.cfg.Stream, url, tls, true, log)
	if err != nil {
		return nil, err
	}

	t.mgr, err = jsm.New(t.nc)
	if err != nil {
		return nil, err
	}

	return t, nil
}
