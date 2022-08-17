// Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package replicator

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/choria-io/stream-replicator/backoff"
	"github.com/choria-io/stream-replicator/config"
	"github.com/nats-io/jsm.go"
	"github.com/nats-io/jsm.go/api"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/segmentio/ksuid"
	"github.com/sirupsen/logrus"
)

type targetInitiatedCopier struct {
	mu       sync.Mutex
	health   *time.Ticker
	lastCSeq uint64
	msgs     chan *nats.Msg
	reset    chan uint64
	s        *Stream
	sr       *config.Config
	source   *Target
	dest     *Target
	cfg      *config.Stream
	log      *logrus.Entry
}

func newTargetInitiatedCopier(s *Stream, log *logrus.Entry) *targetInitiatedCopier {
	return &targetInitiatedCopier{
		mu:     sync.Mutex{},
		s:      s,
		source: s.source,
		dest:   s.dest,
		cfg:    s.cfg,
		sr:     s.sr,
		log:    log.WithField("copier", "target_initiated"),
		health: time.NewTicker(time.Millisecond),
		msgs:   make(chan *nats.Msg, 10000),
		reset:  make(chan uint64, 1),
	}
}

func (c *targetInitiatedCopier) setSourceResumeSeq(seq uint64) {
	c.source.mu.Lock()
	c.source.resumeSeq = seq
	c.source.mu.Unlock()
}

func (c *targetInitiatedCopier) getSourceResumeSeq() uint64 {
	c.source.mu.Lock()
	defer c.source.mu.Unlock()

	return c.source.resumeSeq
}

func (c *targetInitiatedCopier) setLastConsumerSeq(seq uint64) {
	c.mu.Lock()
	c.lastCSeq = seq
	c.mu.Unlock()
}

func (c *targetInitiatedCopier) getLastConsumerSeq() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastCSeq
}

func (c *targetInitiatedCopier) copyMessages(ctx context.Context) error {
	if c.cfg.FilterSubject == "" {
		return fmt.Errorf("a filter subject is required")
	}

	c.log.Infof("Starting Target-initiated data copier for %s", c.cfg.Stream)

	for {
		select {
		case msg := <-c.msgs:
			if c.s.isPaused() {
				continue
			}

			// move health check forward we know we're ok
			c.health.Reset(c.s.hcInterval)

			_, err := c.handler(ctx, msg)
			if err != nil {
				handlerErrorCount.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name).Inc()
				c.log.Errorf("Handling message failed: %v", err)
				continue
			}

		case <-c.reset:
			if c.s.isPaused() {
				continue
			}

			c.source.mu.Lock()
			err := c.source.sub.Unsubscribe()
			if err != nil {
				c.log.Warnf("Could not unsubscribe from last inbox: %v", err)
			}

			// drop all in flight messages
			close(c.msgs)
			c.msgs = make(chan *nats.Msg, 100000)
			c.source.mu.Unlock()

			_, err = c.recreateEphemeral()
			if err != nil {
				c.log.Errorf("Recreating consumer after reset failed: %v", err)
				continue
			}

			consumerRepairCount.WithLabelValues(c.source.stream.Name(), c.sr.ReplicatorName, c.cfg.Name).Inc()
			c.setLastConsumerSeq(0)
			c.health.Reset(c.s.hcInterval)

		case <-c.health.C:
			if c.s.isPaused() {
				continue
			}

			c.log.Debugf("Performing health checks")
			repaired, err := c.healthCheckSource()
			if err != nil {
				c.log.Errorf("Health check failed: %v", err)
			}
			if repaired {
				consumerRepairCount.WithLabelValues(c.source.stream.Name(), c.sr.ReplicatorName, c.cfg.Name).Inc()
				c.setLastConsumerSeq(0)
			}

			c.health.Reset(c.s.hcInterval)

		case <-ctx.Done():
			c.health.Stop()

			c.log.Warnf("Copier shutting down after context interrupt")
			return nil
		}
	}
}

func (c *targetInitiatedCopier) handler(ctx context.Context, msg *nats.Msg) (*jsm.MsgInfo, error) {
	// heartbeats and fc
	if len(msg.Data) == 0 && msg.Header != nil {
		if msg.Header.Get("Status") == "100" {
			if msg.Reply == "" {
				if stalled := msg.Header.Get("Nats-Consumer-Stalled"); stalled != "" {
					msg.Reply = stalled
					c.log.Infof("Resuming stalled consumer")
				}
			} else {
				c.log.Debugf("Responding to Flow Control message")
			}

			if msg.Reply != "" {
				err := msg.Respond(nil)
				if err != nil {
					c.log.Warnf("Responding to status messages failed: %v", err)
				}
			}
		}

		return nil, nil
	}

	receivedMessageCount.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name).Inc()
	receivedMessageSize.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name).Add(float64(len(msg.Data)))
	obs := prometheus.NewTimer(processTime.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name))
	defer obs.ObserveDuration()

	meta, err := jsm.ParseJSMsgMetadata(msg)
	if err != nil {
		metaParsingFailedCount.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name).Inc()
		return nil, fmt.Errorf("message data parse failed: %v", err)
	}

	lagMessageCount.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name).Set(float64(meta.Pending()))
	streamSequence.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name).Set(float64(meta.StreamSequence()))

	rseq := c.getSourceResumeSeq()

	lastCSeq := c.getLastConsumerSeq()
	if lastCSeq != 0 && meta.ConsumerSequence() != lastCSeq+1 {
		if meta.ConsumerSequence() == 1 && meta.StreamSequence() == rseq {
			c.log.Warnf("Consumer got reset but with correct stream sequence %d, repairing internal state", meta.StreamSequence())
			c.setLastConsumerSeq(1)
		} else {
			c.log.Warnf("Gap detected %d -> %d on stream sequence %d resuming on sequence %d", lastCSeq, meta.ConsumerSequence(), meta.StreamSequence(), rseq)

			select {
			case c.reset <- rseq:
			default:
			}

			return nil, fmt.Errorf("gap detected")
		}
	}

	msg.Header = nats.Header{}
	msg.Header.Add(srcHeader, fmt.Sprintf(srcHeaderPattern, c.cfg.Stream, meta.StreamSequence(), c.sr.ReplicatorName, c.cfg.Name, meta.TimeStamp().UnixMilli()))
	msg.Subject = c.s.targetForSubject(msg.Subject)

	// we are about to try 5 times, if there isnt a msgid lets add one to avoid dupes
	if msg.Header.Get(api.JSMsgId) == "" {
		msg.Header.Add(api.JSMsgId, ksuid.New().String())
	}

	err = backoff.Default.For(ctx, func(try int) error {
		if try == 6 {
			return fmt.Errorf("maximum attempts reached")
		}

		resp, err := c.dest.nc.RequestMsg(msg, 2*time.Second)
		if err != nil {
			c.log.Errorf("Could not store message to target stream: %v", err)
			return err
		}

		err = jsm.ParseErrorResponse(resp)
		if err != nil {
			c.log.Errorf("Could not store message to target stream: %v", err)
			return err
		}

		return nil
	})
	if err != nil {
		c.log.Warnf("Handling stream sequence %d failed rewinding: %v", meta.StreamSequence(), err)

		c.setSourceResumeSeq(meta.StreamSequence())

		select {
		case c.reset <- meta.StreamSequence():
		default:
		}

		return nil, fmt.Errorf("storing message failed: %v", err)
	}

	c.log.Debugf("Copied message seq %d, %d message(s) behind", meta.StreamSequence(), meta.Pending())

	c.setSourceResumeSeq(meta.StreamSequence() + 1)
	c.setLastConsumerSeq(meta.ConsumerSequence())

	copiedMessageCount.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name).Inc()
	copiedMessageSize.WithLabelValues(c.cfg.Stream, c.sr.ReplicatorName, c.cfg.Name).Add(float64(len(msg.Data)))

	return meta, nil
}

func (c *targetInitiatedCopier) getStartSequence() (uint64, error) {
	msg, err := c.dest.stream.ReadLastMessageForSubject(c.s.targetForSubject(c.cfg.FilterSubject))
	if err != nil {
		// no message found means we start fresh, need to think about expiry on target and how to resume that
		if jsm.IsNatsError(err, 10037) {
			return 0, nil
		}

		return 0, err
	}

	hdrs, err := decodeHeadersMsg(msg.Header)
	if err != nil {
		return 0, err
	}

	src := hdrs.Get("Choria-SR-Source")
	if src == "" {
		return 0, fmt.Errorf("last message is not a stream replicator message")
	}
	parts := strings.Split(src, " ")
	if len(parts) != 5 {
		return 0, fmt.Errorf("last message has an invalid header: %v", src)
	}

	if parts[0] != c.cfg.Stream {
		return 0, fmt.Errorf("last message is from a different stream %s", c.cfg.Name)
	}

	seq, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, err
	}

	c.log.Debugf("Detected last message from stream %s sequence %d", parts[0], seq)

	return uint64(seq) + 1, nil
}

func (c *targetInitiatedCopier) healthCheckSource() (bool, error) {
	c.source.mu.Lock()
	defer c.source.mu.Unlock()

	var err error

	// no ephemeral ever created so we get the start sequence from the target stream
	if c.source.consumer == nil {
		rSeq, err := c.getStartSequence()
		if err != nil {
			return false, err
		}
		c.source.resumeSeq = rSeq
		return c.recreateEphemeraLocked()
	}

	c.source.consumer, err = c.source.stream.LoadConsumer(c.source.consumer.Name())
	if err != nil {
		if jsm.IsNatsError(err, 10014) {
			c.log.Warnf("Consumer was not found, recreating")
			return c.recreateEphemeraLocked()
		}
	}

	return false, err
}

func (c *targetInitiatedCopier) recreateEphemeral() (bool, error) {
	c.source.mu.Lock()
	defer c.source.mu.Unlock()

	return c.recreateEphemeraLocked()
}

func (c *targetInitiatedCopier) recreateEphemeraLocked() (bool, error) {
	var err error
	if c.source.sub != nil && c.source.sub.IsValid() {
		err = c.source.sub.Unsubscribe()
		if err != nil {
			return false, fmt.Errorf("unsubscribe failed: %v", err)
		}
	}

	c.source.sub, err = c.source.nc.ChanSubscribe(c.source.nc.NewRespInbox(), c.msgs)
	if err != nil {
		return false, err
	}

	opts := []jsm.ConsumerOption{
		jsm.ConsumerDescription(fmt.Sprintf("Choria Stream Replicator %s", c.cfg.Name)),
		jsm.AcknowledgeNone(),
		jsm.DeliverySubject(c.source.sub.Subject),
		jsm.PushFlowControl(),
		jsm.IdleHeartbeat(20 * time.Second),
	}

	if c.cfg.FilterSubject != _EMPTY_ {
		opts = append(opts, jsm.FilterStreamBySubject(c.cfg.FilterSubject))
	}

	if c.source.resumeSeq > 0 {
		c.log.Infof("Resuming consumer from stream sequence %d", c.source.resumeSeq)
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

	c.source.consumer, err = c.source.stream.NewConsumer(opts...)

	return true, err
}

// copied from nats.go
const (
	hdrLine   = "NATS/1.0\r\n"
	crlf      = "\r\n"
	hdrPreEnd = len(hdrLine) - len(crlf)
	statusLen = 3
	statusHdr = "Status"
	descrHdr  = "Description"
)

func decodeHeadersMsg(data []byte) (nats.Header, error) {
	tp := textproto.NewReader(bufio.NewReader(bytes.NewReader(data)))
	l, err := tp.ReadLine()
	if err != nil || len(l) < hdrPreEnd || l[:hdrPreEnd] != hdrLine[:hdrPreEnd] {
		return nil, nats.ErrBadHeaderMsg
	}

	mh, err := readMIMEHeader(tp)
	if err != nil {
		return nil, err
	}

	// Check if we have an inlined status.
	if len(l) > hdrPreEnd {
		var description string
		status := strings.TrimSpace(l[hdrPreEnd:])
		if len(status) != statusLen {
			description = strings.TrimSpace(status[statusLen:])
			status = status[:statusLen]
		}
		mh.Add(statusHdr, status)
		if len(description) > 0 {
			mh.Add(descrHdr, description)
		}
	}
	return nats.Header(mh), nil
}

// copied from nats.go
func readMIMEHeader(tp *textproto.Reader) (textproto.MIMEHeader, error) {
	m := make(textproto.MIMEHeader)
	for {
		kv, err := tp.ReadLine()
		if len(kv) == 0 {
			return m, err
		}

		// Process key fetching original case.
		i := bytes.IndexByte([]byte(kv), ':')
		if i < 0 {
			return nil, nats.ErrBadHeaderMsg
		}
		key := kv[:i]
		if key == "" {
			// Skip empty keys.
			continue
		}
		i++
		for i < len(kv) && (kv[i] == ' ' || kv[i] == '\t') {
			i++
		}
		value := string(kv[i:])
		m[key] = append(m[key], value)
		if err != nil {
			return m, err
		}
	}
}
