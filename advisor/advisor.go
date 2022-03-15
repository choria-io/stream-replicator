// Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package advisor

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/choria-io/stream-replicator/backoff"
	"github.com/choria-io/stream-replicator/config"
	"github.com/choria-io/stream-replicator/idtrack"
	"github.com/nats-io/jsm.go"
	"github.com/nats-io/jsm.go/api"
	"github.com/nats-io/nats.go"
	"github.com/segmentio/ksuid"
	"github.com/sirupsen/logrus"
)

// AgeAdvisoryV2 defines a message published when a node has not been seen within configured deadlines and when it recovers
type AgeAdvisoryV2 struct {
	Protocol     string    `json:"protocol"`
	EventID      string    `json:"event_id"`
	InspectField string    `json:"inspect_field"`
	Age          float64   `json:"age"`
	Seen         int64     `json:"seen"`
	Replicator   string    `json:"replicator"`
	Timestamp    int64     `json:"timestamp"`
	Event        EventType `json:"event"`
	Value        string    `json:"value"`
}

// EventType is the kind of event that triggered the advisory
type EventType string

type Tracker interface {
	NotifyFirstSeen(cb func(string, idtrack.Item)) error
	NotifyRecover(cb func(string, idtrack.Item)) error
	NotifyAgeWarning(cb func(map[string]idtrack.Item)) error
	NotifyExpired(cb func(map[string]idtrack.Item)) error
	RecordAdvised(v string)
}

var (
	TimeoutEvent     EventType = "timeout"
	RecoverEvent     EventType = "recover"
	ExpireEvent      EventType = "expire"
	FirstSeenEvent   EventType = "new"
	AdvisoryProtocol           = "io.choria.sr.v2.age_advisory"
)

const (
	_EMPTY_ = ""
)

type Advisor struct {
	cfg          *config.Advisory
	nc           *nats.Conn
	tracker      Tracker
	out          chan *AgeAdvisoryV2
	inspectField string
	replicator   string
	log          *logrus.Entry
	stream       string
}

func New(ctx context.Context, wg *sync.WaitGroup, cfg *config.Advisory, nc *nats.Conn, tracker Tracker, inspectField string, stream string, replicator string, log *logrus.Entry) (*Advisor, error) {
	a := &Advisor{
		cfg:          cfg,
		replicator:   replicator,
		stream:       stream,
		nc:           nc,
		tracker:      tracker,
		inspectField: inspectField,
		log:          log.WithFields(logrus.Fields{"advisor": replicator, "stream": stream, "reliable": cfg.Reliable}),
		out:          make(chan *AgeAdvisoryV2, 1000),
	}

	err := tracker.NotifyFirstSeen(a.firstSeenCB)
	if err != nil {
		return nil, err
	}
	err = tracker.NotifyAgeWarning(a.warnCB)
	if err != nil {
		return nil, err
	}
	err = tracker.NotifyRecover(a.recoverCB)
	if err != nil {
		return nil, err
	}
	err = tracker.NotifyExpired(a.expireCB)
	if err != nil {
		return nil, err
	}

	wg.Add(1)
	go a.publisher(ctx, wg)

	a.log.Infof("Advisory starting publishing to %s", a.cfg.Subject)
	return a, nil
}

func (a *Advisor) publisher(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	publisher := func(advisory *AgeAdvisoryV2) error {
		advisoryCount.WithLabelValues(string(advisory.Event), a.stream, a.replicator).Inc()

		d, err := json.Marshal(advisory)
		if err != nil {
			return err
		}

		tries := 1
		if a.cfg.Reliable {
			tries = 10
		}

		return backoff.FiveSec.For(ctx, func(try int) error {
			if try > tries {
				a.log.Warnf("Giving up on advisory after %d tries", try-1)
				return nil
			}

			if !a.cfg.Reliable {
				return a.nc.Publish(a.cfg.Subject, d)
			}

			msg := nats.NewMsg(a.cfg.Subject)
			msg.Data = d
			if advisory.EventID != _EMPTY_ {
				msg.Header.Add(api.JSMsgId, advisory.EventID)
			}

			timeout, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()

			res, err := a.nc.RequestMsgWithContext(timeout, msg)
			if err != nil {
				advisoryPublishErrors.WithLabelValues(string(advisory.Event), a.stream, a.replicator).Inc()
				return err
			}

			ack, err := jsm.ParsePubAck(res)
			if err != nil {
				advisoryPublishErrors.WithLabelValues(string(advisory.Event), a.stream, a.replicator).Inc()
				return err
			}

			a.log.Debugf("Published %s advisory for %v to %s with sequence %d", advisory.Event, advisory.Value, ack.Stream, ack.Sequence)
			return nil
		})
	}

	for {
		select {
		case advisory := <-a.out:
			err := publisher(advisory)
			if err != nil {
				a.log.Errorf("Could not publish advisory: %v", err)
			}

		case <-ctx.Done():
			a.log.Warnf("Advisory shutting down on context interrupt")
			return
		}
	}
}

func (a *Advisor) newAdvisory(v string, i idtrack.Item, kind EventType) *AgeAdvisoryV2 {
	id, _ := ksuid.NewRandom()

	advisory := &AgeAdvisoryV2{
		Protocol:     AdvisoryProtocol,
		EventID:      id.String(),
		InspectField: a.inspectField,
		Value:        v,
		Seen:         i.Seen.Unix(),
		Replicator:   a.replicator,
		Timestamp:    time.Now().Unix(),
		Event:        kind,
	}

	if i.Seen.IsZero() {
		advisory.Age = -1
	} else {
		advisory.Age = time.Since(i.Seen).Round(time.Second).Seconds()
	}

	select {
	case a.out <- advisory:
		if kind == TimeoutEvent {
			a.tracker.RecordAdvised(v)
		}

	default:
		a.log.Warnf("Could not enqueue %v advisory for %s, channel has %d entries", kind, v, len(a.out))
	}

	return nil
}

func (a *Advisor) expireCB(expired map[string]idtrack.Item) {
	a.log.Debugf("Received %d expire notifications", len(expired))

	// mainly to make testing function, cos maps have no order, ugh
	keys := make([]string, len(expired))
	idx := 0
	for v := range expired {
		keys[idx] = v
		idx++
	}
	sort.Strings(keys)

	for _, k := range keys {
		a.newAdvisory(k, expired[k], ExpireEvent)
	}
}

func (a *Advisor) recoverCB(v string, i idtrack.Item) {
	a.newAdvisory(v, i, RecoverEvent)
}

func (a *Advisor) firstSeenCB(v string, i idtrack.Item) {
	a.newAdvisory(v, i, FirstSeenEvent)
}

func (a *Advisor) warnCB(warnings map[string]idtrack.Item) {
	a.log.Debugf("Received %d warning notifications", len(warnings))

	// mainly to make testing function, cos maps have no order, ugh
	keys := make([]string, len(warnings))
	idx := 0
	for v := range warnings {
		keys[idx] = v
		idx++
	}
	sort.Strings(keys)

	for _, k := range keys {
		a.newAdvisory(k, warnings[k], TimeoutEvent)
	}
}
